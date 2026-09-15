import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_GROUPOPS_TEST_URL;
const username = process.env.AICRM_GROUPOPS_TEST_USERNAME;
const password = process.env.AICRM_GROUPOPS_TEST_PASSWORD;
const planID = process.env.AICRM_GROUPOPS_TEST_PLAN_ID;
const replacementStaffID = process.env.AICRM_GROUPOPS_TEST_REPLACEMENT_STAFF_ID;
const composerImageIDs = String(process.env.AICRM_GROUPOPS_TEST_COMPOSER_IMAGE_IDS || '').split(',').map((value) => Number(value));
const screenshotDir = process.env.AICRM_GROUPOPS_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(planID || "") || composerImageIDs.length !== 2 || composerImageIDs.some((id) => !Number.isSafeInteger(id) || id < 1)) throw new Error("Group Ops Chromium journey requires HTTPS URL, credentials, plan ID, and two composer Media IDs");
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
const asError = (error) => error instanceof Error ? error : new Error(String(error));
const stopBrowser = async (child) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return null;
  try {
    child.kill("SIGTERM");
    if (await waitForExit(child, 3000)) return null;
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    if (await waitForExit(child, 3000)) return null;
    return new Error("Chromium process did not exit before profile cleanup");
  } catch (error) {
    return asError(error);
  }
};
const removeProfile = async (profile) => {
  let lastError;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return null; }
    catch (error) {
      lastError = asError(error);
      if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return lastError;
      await delay(100);
    }
  }
  return new Error(`Chromium test profile cleanup did not complete after 40 attempts: ${lastError?.code || lastError?.message || "unknown error"}`);
};
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-groupops-chromium-"));
let browser; let cdp; let journeyError;
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
  // The standard source keeps its detail canvas visually framed with a local
  // -4px margin and compensating 4px padding. The host root, rather than that
  // source-owned canvas, must align to the final admin-page 20px/16px inset.
  const invalidShellLayout = !shellLayout?.stage || !shellLayout?.topbar || shellLayout.headers !== 1 || shellLayout.titleText !== "群运营计划" || shellLayout.pageH1Count !== 1 || !shellLayout.root || !shellLayout.detail || shellLayout.syntheticWorkspaceHeadings !== 0 || shellLayout.stage.paddingLeft !== "20px" || shellLayout.stage.paddingTop !== "16px" || shellLayout.root.top + 1 < shellLayout.topbar.bottom || Math.abs(shellLayout.root.left - shellLayout.stage.left - 20) > 1 || Math.abs(shellLayout.root.top - shellLayout.stage.top - 16) > 1 || shellLayout.detail.paddingLeft !== "4px" || shellLayout.detail.paddingTop !== "4px" || Math.abs(shellLayout.detail.left - shellLayout.root.left + 4) > 1 || Math.abs(shellLayout.detail.top - shellLayout.root.top + 4) > 1;
  if (invalidShellLayout) throw new Error("Group Ops native shell title/content geometry is invalid: " + JSON.stringify(shellLayout));
  await evaluate(cdp, "document.querySelector('[data-action=\"switch-detail-panel\"][data-panel=\"nodes\"]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('#panel-nodes.is-active .group-ops__table-wrap'))", "standard node table did not render");
  for (const width of [780, 390]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 1007 });
    await delay(80);
    const responsive = await evaluate(cdp, "(() => { const wrap=document.querySelector('#panel-nodes.is-active .group-ops__table-wrap'), table=wrap?.querySelector('table'), root=document.querySelector('#group-ops-app'), detail=root?.querySelector('.group-ops__detail-shell'), workspace=root?.querySelector('.group-ops__workspace'); return {viewport:window.innerWidth,documentWidth:document.documentElement.scrollWidth,rootWidth:root?.getBoundingClientRect().width,detailWidth:detail?.getBoundingClientRect().width,workspaceWidth:workspace?.getBoundingClientRect().width,wrapClientWidth:wrap?.clientWidth,wrapScrollWidth:wrap?.scrollWidth,tableWidth:table?.getBoundingClientRect().width,wrapOverflowX:wrap ? getComputedStyle(wrap).overflowX : ''}; })()");
    const invalidResponsive = !responsive || responsive.viewport !== width || responsive.documentWidth > width + 1 || !Number.isFinite(responsive.rootWidth) || responsive.rootWidth > width + 1 || !Number.isFinite(responsive.detailWidth) || responsive.detailWidth > width + 1 || !Number.isFinite(responsive.workspaceWidth) || responsive.workspaceWidth > width + 1 || responsive.wrapOverflowX !== "auto" || !Number.isFinite(responsive.wrapClientWidth) || !Number.isFinite(responsive.wrapScrollWidth) || responsive.wrapScrollWidth <= responsive.wrapClientWidth || !Number.isFinite(responsive.tableWidth) || responsive.tableWidth < 720;
    if (invalidResponsive) throw new Error(`Group Ops ${width}px detail overflow escaped its table container: ${JSON.stringify(responsive)}`);
  }
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: 1280, screenHeight: 900 });
  await evaluate(cdp, "document.querySelector('[data-action=\"switch-detail-panel\"][data-panel=\"groups\"]').click(); document.querySelector('[data-action=\"open-group-picker\"]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"group\"] [data-v3-group-key]'))", "V3 group selection session did not open");
  // The matching group lives beyond the first 50 Owner records. This proves
  // search is a server-owned q+offset read instead of a browser filter over
  // the first directory page.
  await evaluate(cdp, "(() => { const input=document.querySelector('[data-v3-picker-search-input]'); input.value='群二'; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,key:'Enter'})); return true; })()");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"group\"] [data-v3-group-key]').length===1 && document.body.textContent.includes('Chromium 群二')", "Owner query did not narrow the V3 group picker");
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"group\"] [data-v3-group-key$=\"chromium-group-2\"]').click(); true");
  await evaluate(cdp, "(() => { const input=document.querySelector('[data-v3-picker-search-input]'); input.value=''; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,key:'Enter'})); return true; })()");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"group\"] [data-v3-group-key]').length===50", "cleared Owner query did not restore the first scoped page");
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"group\"] [data-v3-group-more]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"group\"] [data-v3-group-key$=\"chromium-group-1\"]'))", "Owner paging did not reach the post-query group record");
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"group\"] [data-v3-group-key$=\"chromium-group-1\"]').click(); true");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"group\"] [data-v3-group-remove]').length===2", "multi-select group draft did not render");
  for (const width of [1280, 420, 360]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
    await delay(80);
    const picker = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"group\"]'), dialog=mask?.querySelector('.group-ops__modal--groups'), selected=mask?.querySelector('[data-v3-group-selected]'), list=mask?.querySelector('[data-v3-group-list]'), confirm=mask?.querySelector('[data-v3-group-confirm]'); const box=node=>node&&node.getBoundingClientRect(); return {documentWidth:document.documentElement.scrollWidth, dialog:box(dialog), selected:box(selected), list:box(list), confirm:box(confirm), selectedOverflow:selected&&getComputedStyle(selected).overflowY, listOverflow:list&&getComputedStyle(list).overflowY}; })()");
    if (!picker || picker.documentWidth > width + 1 || !picker.dialog || picker.dialog.width > width || picker.dialog.bottom > 900 || !picker.selected || !picker.list || picker.selectedOverflow !== 'auto' || picker.listOverflow !== 'auto' || !picker.confirm || picker.confirm.bottom > picker.dialog.bottom + 1) throw new Error(`group picker ${width}px layout is not operable: ${JSON.stringify(picker)}`);
    if (screenshotDir) { await fs.mkdir(screenshotDir, { recursive: true }); const shot = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); const target = path.join(screenshotDir, `group-picker-${width}.png`); await fs.writeFile(target, Buffer.from(shot.data, 'base64')); console.log(`group_ops_chromium: SCREENSHOT ${target}`); }
  }
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: 1280, screenHeight: 900 });
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"group\"] [data-v3-group-confirm]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"group\"]')", "browser group selection did not finish its commit");
  const selectedGroupsPersisted = await evaluate(cdp, `fetch('/api/admin/automation-conversion/group-ops/plans/${planID}',{credentials:'same-origin'}).then((response)=>response.json()).then((body)=>['chromium-group-1','chromium-group-2'].every((reference)=>body.group_assets?.some((item)=>item.asset_reference===reference)))`);
  if (!selectedGroupsPersisted) throw new Error("Group Ops API did not return browser-persisted group bindings");
  // The V3 Host asks the frozen renderer to reread its own detail projection
  // after a group commit. Wait for that renderer's real, bound controls rather
  // than merely waiting for the dialog to disappear: renderLoading temporarily
  // replaces the action button while the projection request is in flight.
  let refreshedDetail;
  for (let attempt = 0; attempt < 180; attempt += 1) {
    refreshedDetail = await evaluate(cdp, "(() => { const bound=Array.from(document.querySelectorAll('[data-action=\"remove-group\"]')).map((row)=>row.dataset.chatId); const node=document.querySelector('[data-action=\"open-node-modal\"]'); return {ready:Boolean(node) && ['chromium-group-1','chromium-group-2'].every((reference)=>bound.includes(reference)), bound, nodeAction:node?.dataset.action || '', mode:document.querySelector('#group-ops-app')?.dataset.groupOpsStandardHost || ''}; })()");
    if (refreshedDetail.ready) break;
    await delay(50);
  }
  if (!refreshedDetail?.ready) throw new Error(`Group Ops detail did not finish renderer refresh after group save: ${JSON.stringify(refreshedDetail)}`);
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  const nodeModalOpened = await evaluate(cdp, "(() => { const node=document.querySelector('[data-action=\"open-node-modal\"]'); if (!node) return false; node.click(); return true; })()");
  if (!nodeModalOpened) throw new Error(`Group Ops renderer lost the node action after its ready check: ${JSON.stringify(refreshedDetail)}`);
  await waitFor(cdp, "Boolean(document.querySelector('[name=\"node_day_index\"]'))", "standard node editor did not open");
  await evaluate(cdp, "(() => { const set=(name,value)=>{const input=document.querySelector(`[name=\"${name}\"]`); input.value=value; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new Event('change',{bubbles:true}));}; set('node_day_index','2'); set('node_scheduled_time','09:30'); set('node_action_title','Chromium 日程动作'); document.querySelector('[data-action=\"configure-node-content\"]').click(); return true; })()");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-content-composer] [data-v3-composer-text]'))", "actual Group Ops content composer did not open");
  await evaluate(cdp, `(() => { const field=document.querySelector('[data-v3-content-composer] [data-v3-composer-text]'); field.value='浏览器真实后端节点 {{历史变量}}'; field.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('[data-v3-content-composer] [data-v3-composer-add="image"]').click(); return true; })()`);
  await waitFor(cdp, `Boolean(document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":${composerImageIDs[0]}"]')) && Boolean(document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":${composerImageIDs[1]}"]'))`, "Group Ops scoped image selector did not read the authorised Media page");
  await evaluate(cdp, `document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":${composerImageIDs[0]}"]').click(); document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":${composerImageIDs[1]}"]').click(); document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-confirm]').click(); true`);
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"material\"]') && document.querySelectorAll('[data-v3-content-composer] [data-v3-composer-remove]').length===2", "two temporary Group Ops materials did not return to the content draft");
  // Remove then reopen the real selector. The restored current selection must
  // retain its real Media label; no save/send happens until the node command.
  await evaluate(cdp, "document.querySelector('[data-v3-content-composer] [data-v3-composer-remove=\"0\"]').click(); true");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-content-composer] [data-v3-composer-remove]').length===1", "composer removal did not stay local to the draft");
  await evaluate(cdp, "document.querySelector('[data-v3-content-composer] [data-v3-composer-add=\"image\"]').click(); true");
  await waitFor(cdp, `Boolean(document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":${composerImageIDs[0]}"][aria-pressed="false"]'))`, "reopened Group Ops selector did not retain the remaining selected Media record");
  await evaluate(cdp, `document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":${composerImageIDs[0]}"]').click(); document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-confirm]').click(); true`);
  await waitFor(cdp, `(() => { const names=Array.from(document.querySelectorAll('[data-v3-content-composer] .aicrm-content-composer__materials strong')).map((item)=>item.textContent||''); return names.length===2 && names[0].includes('Chromium 群运营素材二') && names[1].includes('Chromium 群运营素材一'); })()`, "reopened selector did not preserve its caller-owned material draft order");
  await evaluate(cdp, "document.querySelector('[data-v3-content-composer] [data-v3-composer-move=\"0:1\"]').click(); true");
  await waitFor(cdp, `(() => Array.from(document.querySelectorAll('[data-v3-content-composer] .aicrm-content-composer__materials strong')).map((item)=>item.textContent||'')[0]?.includes('Chromium 群运营素材一'))()`, "composer reorder did not update its local persisted-order draft");
  for (const width of [1440, 1280, 420, 360]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 1000, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 1000 });
    await delay(80);
    const layout = await evaluate(cdp, "(() => { const dialog=document.querySelector('[data-v3-content-composer] .aicrm-content-composer'), body=dialog?.querySelector('.aicrm-content-composer__body'), textarea=dialog?.querySelector('[data-v3-composer-text]'), preview=dialog?.querySelector('[data-content-presentation=\"preview\"]'), footer=dialog?.querySelector('footer'), cancel=dialog?.querySelector('header [data-v3-composer-cancel]'), thumbnail=preview?.querySelector('.aicrm-content-presentation__thumbnail'), details=preview?.querySelector('.aicrm-content-presentation__material-details'); const box=node=>{const rect=node?.getBoundingClientRect(); return rect?{left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,width:rect.width,height:rect.height}:null;}; const thumbnailStyle=thumbnail&&getComputedStyle(thumbnail); return {documentWidth:document.documentElement.scrollWidth,dialog:box(dialog),body:box(body),textarea:box(textarea),preview:box(preview),footer:box(footer),thumbnail:box(thumbnail),details:box(details),thumbnailCSS:thumbnailStyle&&{width:thumbnailStyle.width,height:thumbnailStyle.height,maxWidth:thumbnailStyle.maxWidth,maxHeight:thumbnailStyle.maxHeight,display:thumbnailStyle.display},thumbnailHTML:thumbnail?.outerHTML,bodyOverflow:body&&getComputedStyle(body).overflowY,cancelNoWrap:cancel&&getComputedStyle(cancel).whiteSpace,cancelFits:cancel&&cancel.scrollWidth<=cancel.clientWidth}; })()");
    const previewLayoutIsOrdered = width <= 640
      ? layout?.details?.top >= layout?.thumbnail?.bottom + 6
      : layout?.details?.left >= layout?.thumbnail?.right + 6;
    if (!layout || layout.documentWidth > width + 1 || !layout.dialog || layout.dialog.width > width || layout.dialog.bottom > 1000 || !layout.body || layout.bodyOverflow !== 'auto' || !layout.textarea || !layout.preview || !layout.footer || layout.footer.bottom > layout.dialog.bottom + 1 || layout.footer.top < layout.dialog.top || layout.cancelNoWrap !== 'nowrap' || !layout.cancelFits || !layout.thumbnail || layout.thumbnail.width > 52 || layout.thumbnail.height > 52 || !layout.details || !previewLayoutIsOrdered) throw new Error(`Group Ops composer ${width}px layout is not operable: ${JSON.stringify(layout)}`);
    if (screenshotDir) { await fs.mkdir(screenshotDir, { recursive: true }); const shot = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); const target = path.join(screenshotDir, `groupops-composer-${width}.png`); await fs.writeFile(target, Buffer.from(shot.data, 'base64')); console.log(`group_ops_chromium: SCREENSHOT ${target}`); }
  }
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: 1280, screenHeight: 900 });
  await evaluate(cdp, "document.querySelector('[data-v3-content-composer] [data-v3-composer-confirm]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-content-composer]')", "content composer confirmation did not return to the node-local draft");
  const localContentDraft = await evaluate(cdp, "(() => ({pkg:document.querySelector('[name=\"node_content_package_json\"]')?.value,order:document.querySelector('[name=\"node_content_material_order_json\"]')?.value}))()");
  if (!localContentDraft?.pkg?.includes('浏览器真实后端节点 {{历史变量}}') || !localContentDraft?.order?.includes('media-library')) throw new Error(`Group Ops composer did not update only the local node draft: ${JSON.stringify(localContentDraft)}`);
  await evaluate(cdp, "document.querySelector('[data-action=\"save-node\"]').click(); true");
  await waitFor(cdp, "document.body.textContent.includes('Chromium 日程动作') && document.body.textContent.includes('第 2 天') && document.body.textContent.includes('09:30')", "browser node save did not return persisted schedule");
  const persistedNodes = await evaluate(cdp, `fetch('/api/admin/automation-conversion/group-ops/plans/${planID}/nodes',{credentials:'same-origin'}).then((response)=>response.json()).then((body)=>body.items||[])`);
  const persisted = Array.isArray(persistedNodes) && persistedNodes.some((node)=>node.day_index===2&&node.scheduled_time==='09:30'&&node.trigger_time_label==='09:30'&&node.action_title==='Chromium 日程动作'&&node.status==='active'&&node.message_text==='浏览器真实后端节点 {{历史变量}}'&&node.material_plan?.references?.length===2&&node.material_plan.references[0]?.kind==='image'&&node.material_plan.references[0]?.id===composerImageIDs[0]&&node.material_plan.references[1]?.kind==='image'&&node.material_plan.references[1]?.id===composerImageIDs[1]);
  if (!persisted) throw new Error(`Group Ops node API did not return browser-persisted schedule: ${JSON.stringify(persistedNodes)}`);
  await waitFor(cdp, "Boolean(document.querySelector('[data-action=\"view-node-content\"]'))", "saved node did not expose its actual readonly content action");
  await evaluate(cdp, "document.querySelector('[data-action=\"view-node-content\"]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-content-readonly]'))", "saved node content did not resolve into the shared readonly presenter");
  const readonly = await evaluate(cdp, "(() => { const root=document.querySelector('[data-v3-content-readonly]'); const text=root?.textContent||''; const previews=Array.from(root?.querySelectorAll('img.aicrm-content-presentation__thumbnail')||[]).map((image)=>({complete:image.complete,width:image.naturalWidth,src:image.getAttribute('src')})); return {text,previews}; })()");
  if (!readonly?.text?.includes('浏览器真实后端节点 {{历史变量}}') || readonly.text.indexOf('Chromium 群运营素材一') > readonly.text.indexOf('Chromium 群运营素材二') || readonly.previews.length !== 2 || readonly.previews.some((preview)=>!preview.complete || preview.width < 1 || !preview.src)) throw new Error(`saved Group Ops content did not read back names, order, and thumbnails: ${JSON.stringify(readonly)}`);
  await evaluate(cdp, "document.querySelector('[data-v3-content-readonly-close]').click(); true");
  await evaluate(cdp, "document.querySelector('[data-action=\"switch-detail-panel\"][data-panel=\"basic\"]').click(); document.querySelector('[data-action=\"pick-plan-owner\"]').click(); true");
  await waitFor(cdp, "Array.from(document.querySelectorAll('[data-v3-selection-session=\"staff\"] [data-v3-staff-key]')).filter(row=>String(row.textContent||'').includes('chromium-replacement')).length === 1", "V3 owner picker did not load the authorised local employees");
  const ownerChanged = await evaluate(cdp, "(() => { const row=Array.from(document.querySelectorAll('[data-v3-selection-session=\"staff\"] [data-v3-staff-key]')).find((item)=>String(item.textContent||'').includes('chromium-replacement')); if(!row)return false; row.click(); const confirm=document.querySelector('[data-v3-selection-session=\"staff\"] [data-v3-staff-confirm]'); if(!confirm||confirm.disabled)return false; confirm.click(); return true; })()");
  if (!ownerChanged) throw new Error("V3 owner picker had no authorised replacement employee");
  await waitFor(cdp, `document.querySelector('[name="owner_userid"]').value === ${JSON.stringify(replacementStaffID)}`, "standard owner picker did not set an owner");
  await evaluate(cdp, "document.querySelector('[data-action=\"save-plan\"]').click(); true");
  await waitFor(cdp, "document.body.textContent.includes('saved') || document.body.textContent.includes('已保存')", "browser owner save did not return persisted detail");
  const ownerPersisted = await evaluate(cdp, `fetch("/api/admin/automation-conversion/group-ops/plans/${planID}",{credentials:"same-origin"}).then((response)=>response.json()).then((body)=>body.members?.length===1 && String(body.members[0].staff_id)===${JSON.stringify(replacementStaffID)})`);
  if (!ownerPersisted) throw new Error("Group Ops plan API did not return browser-persisted owner replacement");

  // The material dialog is installed by the actual Radar Host. Its page-scoped
  // loader supplies the selection below; a three-item temporary draft then
  // measures the shared dialog at every target width without a parallel picker.
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/radarForm.html` });
  await waitFor(cdp, "Boolean(document.querySelector('#btnPick')) && Boolean(document.querySelector('#typeCards'))", "actual Radar form did not mount");
  const materialHost = await evaluate(cdp, "(() => ({dialogCSS:Array.from(document.querySelectorAll('link[rel=stylesheet]')).some((link)=>String(link.href).includes('selectionDialogStyles-')), picker:typeof window.AICRMMaterialPicker?.open==='function'}))()");
  if (!materialHost?.dialogCSS || !materialHost?.picker) throw new Error("Radar selection dialog stylesheet or material adapter is absent");
  // Invoke the actual Radar Host's installed picker with the same
  // page-scoped loader. The frozen callback relay is covered by its composed
  // Host journey; this browser gate concentrates on real layout and input.
  await evaluate(cdp, "(() => { window.__aicrmRadarMaterial=0; window.AICRMMaterialPicker.open({type:'image',title:'Radar 素材验收',selectedIds:[],limit:1,onConfirm:item=>{window.__aicrmRadarMaterial=item.library_id;},onCancel:()=>{}}); return true; })()");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key]'))", "actual Radar page-scoped material picker did not open");
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key]').click(); document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-picker-confirm]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"material\"]') && Number(window.__aicrmRadarMaterial)>0", "actual Radar material picker did not return its selected material");
  await evaluate(cdp, "window.AICRMMaterialPicker.open({type:'image',title:'多素材布局验收',selectedIds:[],limit:3,onCommit:()=>{},onCancel:()=>{}}); true");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"material\"] [data-v3-material-key]').length>=3", "Radar scoped material page did not return its records");
  const previewState = await evaluate(cdp, "(() => Array.from(document.querySelectorAll('[data-v3-selection-session=\"material\"] [data-v3-material-preview]')).map((preview)=>({src:preview.getAttribute('src'),complete:preview.complete,naturalWidth:preview.naturalWidth,naturalHeight:preview.naturalHeight})))()");
  if (previewState.length < 3 || previewState.some((preview) => !preview.complete || preview.naturalWidth < 1 || preview.naturalHeight < 1)) throw new Error(`Radar material thumbnails did not load from their real scoped URLs: ${JSON.stringify(previewState)}`);
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key]').click(); true");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"material\"] [data-v3-material-remove]').length===1", "first material temporary selection did not render");
  await evaluate(cdp, "Array.from(document.querySelectorAll('[data-v3-selection-session=\"material\"] [data-v3-material-key]')).find((row)=>row.getAttribute('aria-pressed')==='false')?.click(); true");
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"material\"] [data-v3-material-remove]').length===2", "multi-material temporary selection did not render");
  for (const width of [1280, 420, 360]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
    await delay(80);
    const layout = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"material\"]'), dialog=mask?.querySelector('.aicrm-material-picker'), selected=mask?.querySelector('[data-v3-picker-selected]')?.closest('.aicrm-material-picker__body'), body=mask?.querySelector('[data-picker-grid]')?.closest('.aicrm-material-picker__body'), confirm=mask?.querySelector('[data-v3-picker-confirm]'); const box=node=>node&&node.getBoundingClientRect(); return {documentWidth:document.documentElement.scrollWidth,dialog:box(dialog),selected:box(selected),body:box(body),confirm:box(confirm),selectedOverflow:selected&&getComputedStyle(selected).overflowY,bodyOverflow:body&&getComputedStyle(body).overflowY}; })()");
    if (!layout || layout.documentWidth > width + 1 || !layout.dialog || layout.dialog.width > width || layout.dialog.bottom > 900 || !layout.selected || !layout.body || layout.selectedOverflow !== 'auto' || layout.bodyOverflow !== 'auto' || !layout.confirm || layout.confirm.bottom > layout.dialog.bottom + 1) throw new Error(`material picker ${width}px layout is not operable: ${JSON.stringify(layout)}`);
    if (screenshotDir) { await fs.mkdir(screenshotDir, { recursive: true }); const shot = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); const target = path.join(screenshotDir, `material-picker-${width}.png`); await fs.writeFile(target, Buffer.from(shot.data, 'base64')); console.log(`group_ops_chromium: SCREENSHOT ${target}`); }
  }
  await evaluate(cdp, "(() => { const preview=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-preview]'); if (!preview) return false; preview.src='/api/admin/image-library/999999/variants/thumb_320'; return true; })()");
  await waitFor(cdp, "(() => { const preview=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-preview]'); const fallback=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-preview-unavailable]'); const row=preview?.closest('[data-v3-material-key]'); return preview?.hidden === true && fallback?.hidden === false && !row?.disabled && fallback?.textContent?.includes('预览暂不可用'); })()", "thumbnail failure did not retain the selectable material with an explicit fallback");
  if (screenshotDir) { const shot = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); const target = path.join(screenshotDir, 'material-picker-thumbnail-fallback-360.png'); await fs.writeFile(target, Buffer.from(shot.data, 'base64')); console.log(`group_ops_chromium: SCREENSHOT ${target}`); }
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-picker-cancel]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"material\"]')", "material dialog cancel did not return to the actual Radar form");
  console.log("group_ops_chromium: PASS");
} catch (error) {
  if (error instanceof DevToolsUnavailable) console.log("group_ops_chromium: SKIP_DEVTOOLS");
  else journeyError = asError(error);
} finally {
  const cleanupErrors = [];
  if (cdp) { try { cdp.close(); } catch (error) { cleanupErrors.push(asError(error)); } }
  const browserError = await stopBrowser(browser);
  if (browserError) cleanupErrors.push(browserError);
  const profileError = await removeProfile(profile);
  if (profileError) cleanupErrors.push(profileError);
  if (journeyError && cleanupErrors.length) throw new AggregateError([journeyError, ...cleanupErrors], "Group Ops Chromium journey and cleanup failed");
  if (journeyError) throw journeyError;
  if (cleanupErrors.length) throw new AggregateError(cleanupErrors, "Group Ops Chromium cleanup failed");
}
