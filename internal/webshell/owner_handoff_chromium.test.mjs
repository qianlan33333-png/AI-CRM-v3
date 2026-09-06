import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_OWNER_HANDOFF_TEST_URL;
const username = process.env.AICRM_OWNER_HANDOFF_TEST_USERNAME;
const password = process.env.AICRM_OWNER_HANDOFF_TEST_PASSWORD;
const source = process.env.AICRM_OWNER_HANDOFF_TEST_SOURCE;
const target = process.env.AICRM_OWNER_HANDOFF_TEST_TARGET;
const sourceUserID = process.env.AICRM_OWNER_HANDOFF_TEST_SOURCE_USERID;
const targetUserID = process.env.AICRM_OWNER_HANDOFF_TEST_TARGET_USERID;
const requestedMode = process.env.AICRM_OWNER_HANDOFF_TEST_MODE || "both";
const requestedScope = process.env.AICRM_OWNER_HANDOFF_TEST_SCOPE || "all";
const readTransfer = process.env.AICRM_OWNER_HANDOFF_TEST_READ_TRANSFER === "1";
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !source || !target || !sourceUserID || !targetUserID) throw new Error("owner handoff Chromium journey requires HTTPS URL, credentials, and fixture IDs");
const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));
const chrome = () => {
  for (const value of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"].filter(Boolean)) {
    try { if (value.includes("/") ? spawnSync(value, ["--version"], {stdio:"ignore"}).status === 0 : spawnSync("which", [value], {stdio:"ignore"}).status === 0) return value; } catch (_) {}
  } throw new Error("Chromium binary is unavailable");
};
class CDP {
  constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); this.events=new Map(); socket.addEventListener("message", event => { const message=JSON.parse(String(event.data)); if (message.id && this.pending.has(message.id)) { const {resolve,reject}=this.pending.get(message.id); this.pending.delete(message.id); message.error ? reject(new Error(`CDP ${message.error.code}`)) : resolve(message.result || {}); return; } for (const listener of this.events.get(message.method) || []) listener(message.params || {}); }); }
  call(method, params={}) { return new Promise((resolve,reject) => { const id=++this.id; this.pending.set(id,{resolve,reject}); this.socket.send(JSON.stringify({id,method,params})); }); }
  next(method, predicate, message) { return new Promise((resolve,reject) => { let off=()=>{}; const timer=setTimeout(() => { off(); reject(new Error(message)); }, 8000); const listener=params => { if (!predicate(params)) return; clearTimeout(timer); off(); resolve(params); }; off=() => this.events.set(method,(this.events.get(method)||[]).filter(item => item !== listener)); this.events.set(method,[...(this.events.get(method)||[]),listener]); }); }
  close() { for (const {reject} of this.pending.values()) reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}
const waitForPort = async profile => { for (let attempt=0; attempt<160; attempt++) { try { const [port]=String(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n"); if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch (_) {} await sleep(50); } throw new Error("Chromium remote debugging did not become ready"); };
const waitForExit = async (child, ms) => !child || child.exitCode !== null || child.signalCode !== null || new Promise(resolve => { const timer=setTimeout(()=>resolve(false),ms); child.once("exit",()=>{clearTimeout(timer);resolve(true);}); });
const removeProfile = async profile => { for (let attempt=0;attempt<40;attempt++) { try { await fs.rm(profile,{recursive:true,force:true,maxRetries:0}); return true; } catch (error) { if (!["ENOTEMPTY","EBUSY","EPERM"].includes(error?.code)) return false; await sleep(100); } } return false; };

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-owner-handoff-chromium-"));
let child; let cdp; let failed=false;
try {
  child=spawn(chrome(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--no-first-run","--no-default-browser-check","--disable-background-networking","--disable-component-update","--disable-sync","--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:"ignore"});
  const address=await waitForPort(profile);
  const page=await (await fetch(`${address}/json/new?about:blank`,{method:"PUT"})).json();
  const socket=new WebSocket(page.webSocketDebuggerUrl); await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",()=>reject(new Error("CDP connection failed")),{once:true});});
  cdp=new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
  const evaluate=async expression=>{ const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; };
  const waitFor=async(expression,message)=>{for(let attempt=0;attempt<160;attempt++){if(await evaluate(expression))return;await sleep(50);}throw new Error(message);};
  await cdp.call("Page.navigate",{url:`${baseURL}/login?next=%2Fadmin%2Fowner-migration`});
  await waitFor("Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))","login shell did not render");
  const loginNav=cdp.next("Page.frameNavigated",params=>Boolean(params.frame&&!params.frame.parentId),"login form did not navigate");
  await evaluate(`(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  const loginFrame=await loginNav; if(new URL(loginFrame.frame.url).pathname!=="/admin/owner-migration") throw new Error("login did not reach owner migration");
  await waitFor("Boolean(document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'))","frozen owner-migration page did not mount after login");
  await waitFor("(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); return root && !root.innerHTML.includes('{{') && !root.innerHTML.includes('{%') && root.querySelector('#operator').value.startsWith('管理员 #') && root.querySelector('[data-transfer-welcome-msg]').value === '您好，后续将由新的服务同事继续为您服务。' && root.querySelector('[data-include-wecom-transfer]').checked; })()","Host left server-template markers or did not initialize V3 context");
  const run=async (mode, scope=requestedScope)=>{
    await evaluate(`(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); root.querySelector('[data-owner-picker="source"]').click(); return true; })()`);
    await waitFor(`Boolean(document.querySelector('[data-operation-member-row][data-user-id="${sourceUserID}"]'))`,"source picker did not include inactive source");
    await evaluate(`document.querySelector('[data-operation-member-row][data-user-id="${sourceUserID}"] [data-operation-member-row-select]').click(); true`);
    await evaluate("document.querySelector('[data-operation-member-confirm]').click(); true");
    await waitFor(`document.querySelector('[data-owner-handoff-host] [data-owner-userid="source"]').value === ${JSON.stringify(source)}`, "source picker did not persist the selected Access staff");
    await evaluate(`document.querySelector('[data-owner-handoff-host] [data-owner-picker="target"]').click(); true`);
    await waitFor(`Boolean(document.querySelector('[data-operation-member-row][data-user-id="${targetUserID}"]'))`,"target picker did not include active target");
    await evaluate(`document.querySelector('[data-operation-member-row][data-user-id="${targetUserID}"] [data-operation-member-row-select]').click(); true`);
    await evaluate("document.querySelector('[data-operation-member-confirm]').click(); true");
    await waitFor(`document.querySelector('[data-owner-handoff-host] [data-owner-userid="target"]').value === ${JSON.stringify(target)}`, "target picker did not persist the selected Access staff");
    if (scope === "excel_include") {
      await evaluate(`(() => { const root=document.querySelector("[data-owner-handoff-host] [data-owner-migration-page]"); root.querySelector("[data-scope-segment=\"excel_include\"]").click(); const input=root.querySelector("[data-import-file]"); const csv=["external_userid,是否迁移,当前负责人userid,客户备注名,备注","browser-external,是,browser-source,已知客户,可迁移","browser-external,是,browser-source,重复客户,不可迁移",",是,browser-source,缺少 external_userid,不可迁移","browser-invalid,maybe,browser-source,非法标记,不可迁移","browser-skipped,否,browser-source,文件跳过,保留","browser-mismatch,是,not-browser-source,负责人不符,保留"].join("\n"); const files=new DataTransfer(); files.items.add(new File([csv], "legacy-owner-list.xls", {type:"text/csv"})); Object.defineProperty(input,"files",{configurable:true,value:files.files}); root.querySelector("[data-upload-file]").click(); return true; })()`);
      await waitFor(`!document.querySelector("[data-owner-handoff-host] [data-import-summary]").hidden`, "old .xls import did not parse");
    }
    await evaluate(`(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const wecom=root.querySelector('[data-include-wecom-transfer]'); wecom.checked=${mode === "wecom_then_crm"}; wecom.dispatchEvent(new Event('change',{bubbles:true})); root.querySelector('[data-preview]').click(); return true; })()`);
    await waitFor("Boolean(document.querySelector('[data-owner-handoff-host] [data-preview-content]:not([hidden])'))",`${mode} preview was not persisted through actual HTTP API`);
    if (scope === "excel_include") {
      await waitFor(`(() => { const text=document.querySelector("[data-owner-handoff-host] [data-preview-rows]").textContent; return ["browser-external","duplicate","missing_external_userid","invalid_move_flag","skipped_by_file","not_under_source_owner"].every(value => text.includes(value)); })()`, "Excel preview lost donor row states or fields");
      await evaluate(`document.querySelector("[data-owner-handoff-host] [data-download-errors]").click(); true`);
      await waitFor(`window.__ownerHandoffDownloads.includes("owner_migration_blocked_rows.xlsx")`, "blocked-row Excel export was not created");
    }
    const phrase=await evaluate("document.querySelector('[data-owner-handoff-host] [data-confirm-phrase-display]').textContent");
    await evaluate(`(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const input=root.querySelector('[data-confirm-phrase-input]'); input.value=${JSON.stringify(phrase)}; input.dispatchEvent(new Event('input',{bubbles:true})); root.querySelector('[data-execute]').click(); return true; })()`);
    await waitFor("document.querySelector('[data-owner-handoff-host] [data-execution-log]').textContent.includes('batch_id=')",`${mode} confirmation was not persisted through actual HTTP API`);
    if (mode === "wecom_then_crm" && readTransfer) { let read = false; for (let attempt = 0; attempt < 80; attempt += 1) { await sleep(100); await evaluate("document.querySelector('[data-owner-handoff-host] [data-read-transfer-result]').click(); true"); if (await evaluate("document.querySelector('[data-owner-handoff-host] [data-execution-log]').textContent.includes('transfer_status=1')")) { read = true; break; } } if (!read) throw new Error("transfer-result readback did not render final status"); }
    await evaluate(`document.querySelector("[data-owner-handoff-host] [data-download-result]").click(); true`);
    await waitFor(`window.__ownerHandoffDownloads.includes("owner_migration_result.xlsx")`, ` result Excel export was not created`);
  };
  if (requestedMode === "local_only") {
    await run("local_only");
  } else if (requestedMode === "wecom_then_crm") {
    await run("wecom_then_crm");
  } else {
    await run("local_only");
    const secondNav=cdp.next("Page.frameNavigated",params=>Boolean(params.frame&&!params.frame.parentId),"second owner migration navigation did not complete");
    await cdp.call("Page.navigate",{url:`/admin/owner-migration`}); await secondNav;
    await waitFor("Boolean(document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'))","owner handoff Host did not mount after second navigation");
    await run("wecom_then_crm");
  }
  console.log("owner_handoff_chromium: PASS");
} catch (error) { failed=true; throw error; } finally {
  if(cdp) cdp.close();
  if(child&&child.exitCode===null&&child.signalCode===null){child.kill("SIGTERM");if(!await waitForExit(child,3000)&&child.exitCode===null&&child.signalCode===null){child.kill("SIGKILL");await waitForExit(child,1000);}}
  const removed=await removeProfile(profile); if(!removed&&!failed) throw new Error("Chromium test profile cleanup did not complete");
}
