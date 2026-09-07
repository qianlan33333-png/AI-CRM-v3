import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_GROUPOPS_TEST_URL;
const username = process.env.AICRM_GROUPOPS_TEST_USERNAME;
const password = process.env.AICRM_GROUPOPS_TEST_PASSWORD;
const planID = process.env.AICRM_GROUPOPS_TEST_PLAN_ID;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(planID || "")) throw new Error("Group Ops Chromium journey requires HTTPS URL, credentials, and plan ID");
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const browserBinary = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    if (candidate.includes("/")) { try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {} }
    else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
  }
  throw new Error("Chromium binary is unavailable");
};
class CDP {
  constructor(socket) { this.socket = socket; this.nextID = 0; this.pending = new Map(); socket.addEventListener("message", (event) => { const message = JSON.parse(String(event.data)); const pending = this.pending.get(message.id); if (!pending) return; this.pending.delete(message.id); message.error ? pending.reject(new Error("CDP request failed")) : pending.resolve(message.result || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { for (const pending of this.pending.values()) pending.reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}
const waitForPort = async (profile) => { for (let i = 0; i < 160; i += 1) { try { const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch (_) {} await delay(50); } throw new Error("Chromium remote debugging did not become ready"); };
const evaluate = async (cdp, expression) => { const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; };
const waitFor = async (cdp, expression, message) => { for (let i = 0; i < 180; i += 1) { if (await evaluate(cdp, expression)) return; await delay(50); } throw new Error(message); };
const waitForExit = (child, ms) => new Promise((resolve) => { if (child.exitCode !== null || child.signalCode !== null) return resolve(true); const timer = setTimeout(() => resolve(false), ms); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-groupops-chromium-"));
let browser; let cdp; let failed = false;
class DevToolsUnavailable extends Error {}
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  let address;
  try { address = await waitForPort(profile); } catch (error) { if (process.platform === "darwin") throw new DevToolsUnavailable(); throw error; }
  const target = await (await fetch(`${address}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  const planPath = `/admin/automation-conversion/group-ops/plans/${planID}`;
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=${encodeURIComponent(planPath)}` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, `location.pathname === ${JSON.stringify(planPath)}`, "login did not reach Group Ops detail route");
  await waitFor(cdp, "Boolean(document.querySelector('#group-ops-app .group-ops__detail-shell')) && document.body.textContent.includes('Chromium 群运营计划')", "standard Group Ops Host did not load real plan");
  const mounted = await evaluate(cdp, "(() => ({host:document.querySelector('#group-ops-app')?.dataset.groupOpsStandardHost, css:Array.from(document.styleSheets).some((sheet)=>String(sheet.href||'').includes('/groupops-assets/assets/')), csrf:['aicrm_admin_csrf','aicrm_csrf'].every((name)=>document.cookie.split(';').some((item)=>item.trim().startsWith(name+'='))) }))()");
  if (mounted?.host !== "true" || !mounted?.css || !mounted?.csrf) throw new Error("Group Ops Host asset or CSRF bridge is absent");
  const shellLayout = await evaluate(cdp, "(() => { const box=node=>{if(!node)return null;const rect=node.getBoundingClientRect(),style=getComputedStyle(node);return {left:rect.left,top:rect.top,bottom:rect.bottom,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop};}; const visible=node=>{if(!node)return false;const rect=node.getBoundingClientRect(),style=getComputedStyle(node);return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>1&&rect.height>1;}; const stage=document.querySelector('#stage.admin-page[data-group-ops-standard-stage]'),topbar=document.querySelector('.admin-topbar'),title=topbar?.querySelector('.admin-page-title'),root=stage?.querySelector('#group-ops-app[data-group-ops-standard-host=\\\"true\\\"]'),detail=root?.querySelector('.group-ops__detail-shell'); return {stage:box(stage),topbar:box(topbar),titleText:String(title?.textContent||'').trim(),headers:document.querySelectorAll('header.admin-topbar').length,pageH1Count:Array.from(document.querySelectorAll('h1')).filter(visible).length,root:box(root),detail:box(detail),syntheticWorkspaceHeadings:root?.querySelectorAll('.group-ops__page-heading').length??-1}; })()");
  if (!shellLayout?.stage || !shellLayout?.topbar || shellLayout.headers !== 1 || shellLayout.titleText !== "群运营计划" || shellLayout.pageH1Count !== 1 || !shellLayout.root || !shellLayout.detail || shellLayout.syntheticWorkspaceHeadings !== 0 || shellLayout.stage.paddingLeft !== "20px" || shellLayout.stage.paddingTop !== "16px" || shellLayout.root.top + 1 < shellLayout.topbar.bottom || Math.abs(shellLayout.root.left - shellLayout.stage.left - 20) > 1 || Math.abs(shellLayout.root.top - shellLayout.stage.top - 16) > 1 || Math.abs(shellLayout.detail.left - shellLayout.root.left) > 1 || Math.abs(shellLayout.detail.top - shellLayout.root.top) > 1) throw new Error("Group Ops native shell title/content geometry is invalid");
  await evaluate(cdp, "document.querySelector('[data-action=\"switch-detail-panel\"][data-panel=\"nodes\"]').click(); document.querySelector('[data-action=\"open-node-modal\"]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[name=\"node_day_index\"]'))", "standard node editor did not open");
  await evaluate(cdp, "(() => { const set=(name,value)=>{const input=document.querySelector(`[name=\"${name}\"]`); input.value=value; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new Event('change',{bubbles:true}));}; set('node_day_index','2'); set('node_scheduled_time','09:30'); set('node_action_title','Chromium 日程动作'); set('node_content_package_json',JSON.stringify({content_text:'浏览器真实后端节点',image_library_ids:[],miniprogram_library_ids:[],attachment_library_ids:[],group_invite_library_ids:[]})); document.querySelector('[data-action=\"save-node\"]').click(); return true; })()");
  await waitFor(cdp, "document.body.textContent.includes('Chromium 日程动作') && document.body.textContent.includes('第 2 天') && document.body.textContent.includes('09:30')", "browser node save did not return persisted schedule");
  const persisted = await evaluate(cdp, `fetch('/api/admin/automation-conversion/group-ops/plans/${planID}/nodes',{credentials:'same-origin'}).then((response)=>response.json()).then((body)=>body.items?.some((node)=>node.day_index===2&&node.scheduled_time==='09:30'&&node.trigger_time_label==='09:30'&&node.action_title==='Chromium 日程动作'&&node.status==='active'))`);
  if (!persisted) throw new Error("Group Ops node API did not return browser-persisted schedule");
  await evaluate(cdp, "document.querySelector('[data-action=\"switch-detail-panel\"][data-panel=\"basic\"]').click(); document.querySelector('[data-action=\"pick-plan-owner\"]').click(); true");
  await waitFor(cdp, "document.querySelectorAll('[data-operation-member-picker]:not([hidden]) [data-operation-member-row]').length >= 2", "standard owner picker did not load local employees");
  const ownerChanged = await evaluate(cdp, "(() => { const owner=document.querySelector('[name=\"owner_userid\"]').value; const row=Array.from(document.querySelectorAll('[data-operation-member-picker] [data-operation-member-row]')).find((item)=>item.dataset.userId!==owner); if(!row)return false; row.querySelector('[data-operation-member-row-select]').click(); document.querySelector('[data-operation-member-picker] [data-operation-member-confirm]').click(); return true; })()");
  if (!ownerChanged) throw new Error("standard owner picker had no replacement employee");
  await waitFor(cdp, "document.querySelector('[name=\"owner_userid\"]').value !== ''", "standard owner picker did not set an owner");
  await evaluate(cdp, "document.querySelector('[data-action=\"save-plan\"]').click(); true");
  await waitFor(cdp, "document.body.textContent.includes('saved') || document.body.textContent.includes('已保存')", "browser owner save did not return persisted detail");
  const ownerPersisted = await evaluate(cdp, `fetch("/api/admin/automation-conversion/group-ops/plans/${planID}",{credentials:"same-origin"}).then((response)=>response.json()).then((body)=>body.members?.length===1)`);
  if (!ownerPersisted) throw new Error("Group Ops plan API did not return browser-persisted owner replacement");
  console.log("group_ops_chromium: PASS");
} catch (error) {
  if (error instanceof DevToolsUnavailable) console.log("group_ops_chromium: SKIP_DEVTOOLS");
  else { failed = true; throw error; }
} finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); if (!await waitForExit(browser, 3000)) { browser.kill("SIGKILL"); await waitForExit(browser, 1000); } }
  await fs.rm(profile, { recursive: true, force: true }).catch(() => { if (!failed) throw new Error("Chromium test profile cleanup failed"); });
}
