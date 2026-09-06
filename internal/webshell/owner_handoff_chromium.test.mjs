import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "./chromium_launch.mjs";
import { strFromU8, unzipSync } from "fflate";

const baseURL = process.env.AICRM_OWNER_HANDOFF_TEST_URL;
const username = process.env.AICRM_OWNER_HANDOFF_TEST_USERNAME;
const password = process.env.AICRM_OWNER_HANDOFF_TEST_PASSWORD;
const source = process.env.AICRM_OWNER_HANDOFF_TEST_SOURCE;
const target = process.env.AICRM_OWNER_HANDOFF_TEST_TARGET;
const sourceUserID = process.env.AICRM_OWNER_HANDOFF_TEST_SOURCE_USERID;
const targetUserID = process.env.AICRM_OWNER_HANDOFF_TEST_TARGET_USERID;
const requestedMode = process.env.AICRM_OWNER_HANDOFF_TEST_MODE || "both";
const requestedScope = process.env.AICRM_OWNER_HANDOFF_TEST_SCOPE || "all";
const parseOptionalBoolean = name => {
  const value = process.env[name];
  if (value === undefined || value === "") return false;
  if (value === "true") return true;
  if (value === "false") return false;
  throw new Error(`${name} must be true or false`);
};
const readTransfer = parseOptionalBoolean("AICRM_OWNER_HANDOFF_TEST_READ_TRANSFER");
const legacyOwnerFileCSV = [
  "external_userid,是否迁移,当前负责人userid,客户备注名,备注",
  "browser-external,是,browser-source,已知客户,可迁移",
  "browser-external,是,browser-source,重复客户,不可迁移",
  ",是,browser-source,缺少 external_userid,不可迁移",
  "browser-invalid,maybe,browser-source,非法标记,不可迁移",
  "browser-skipped,否,browser-source,文件跳过,保留",
  "browser-mismatch,是,not-browser-source,负责人不符,保留",
].join("\n");
const uploadLegacyFileExpression = () => `(() => { const root=document.querySelector("[data-owner-handoff-host] [data-owner-migration-page]"); const input=root.querySelector("[data-import-file]"); const csv=${JSON.stringify(legacyOwnerFileCSV)}; const files=new DataTransfer(); files.items.add(new File([csv], "legacy-owner-list.xls", {type:"text/csv"})); Object.defineProperty(input,"files",{configurable:true,value:files.files}); root.querySelector("[data-upload-file]").click(); return true; })()`;
const selectExcelScopeExpression = () => `(() => { document.querySelector('[data-owner-handoff-host] [data-scope-segment="excel_include"]').click(); return true; })()`;
const downloadBlockedRowsExpression = () => `document.querySelector("[data-owner-handoff-host] [data-download-errors]").click(); true`;
const downloadResultRowsExpression = () => `document.querySelector("[data-owner-handoff-host] [data-download-result]").click(); true`;
const operationMemberSelector = userID => `[data-operation-member-row][data-user-id=${JSON.stringify(userID)}]`;
const operationMemberPresentExpression = userID => `Boolean(document.querySelector(${JSON.stringify(operationMemberSelector(userID))}))`;
const operationMemberChooseExpression = userID => `document.querySelector(${JSON.stringify(`${operationMemberSelector(userID)} [data-operation-member-row-select]`)}).click(); true`;
const ownerHandoffBatchStateExpression = () => String.raw`(async () => {
  const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]');
  const log=root?.querySelector('[data-execution-log]')?.textContent || '';
  const match=/\bbatch_id=([^\s]+)/.exec(log);
  if (!match) return { category:'missing_batch' };
  const response=await fetch('/api/admin/customers/owner-handoffs/batches/'+encodeURIComponent(match[1]),{credentials:'same-origin'});
  const payload=await response.json().catch(()=>({}));
  return {
    category: response.ok ? 'ok' : 'http_'+response.status,
    lines: response.ok && Array.isArray(payload.Lines) ? payload.Lines.map(line=>({state:String(line.State||''),transfer_status:Number(line.TransferStatus||0)})) : [],
    transfer_read: document.querySelector('[data-owner-handoff-host]')?.dataset.ownerHandoffTransferResultStatus || 'idle',
    notice_error: getComputedStyle(root?.querySelector('[data-workbench-notice]') || document.body).color === 'rgb(153, 27, 27)',
  };
})()`;
const compileRuntimeExpression = (expression, step) => {
  try { new Function(expression); } catch (error) {
    const category = String(error?.name || "SyntaxError").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96);
    throw new Error(`${step} expression is invalid (${category})`);
  }
  return expression;
};
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !source || !target || !sourceUserID || !targetUserID) throw new Error("owner handoff Chromium journey requires HTTPS URL, credentials, and fixture IDs");
const preflightRuntimeExpressions = () => [
  `Boolean(document.querySelector('form[action="/login"] input[name="login_csrf_token"]'))`,
  `(() => { const stage=document.querySelector('[data-owner-handoff-host]'); return ['ready','donor_error','context_error','host_error'].includes(stage?.dataset.ownerHandoffInit || ''); })()`,
  `(() => { const stage=document.querySelector('[data-owner-handoff-host]'); const root=stage?.querySelector('[data-owner-migration-page]'); return { init: stage?.dataset.ownerHandoffInit || 'missing', http_status: stage?.dataset.ownerHandoffInitStatus || '', page: Boolean(root), has_curly_marker: Boolean(root?.innerHTML.includes('{{')), has_block_marker: Boolean(root?.innerHTML.includes('{%')), operator_ready: Boolean(root?.querySelector('#operator')?.value.startsWith('管理员 #')), welcome_ready: root?.querySelector('[data-transfer-welcome-msg]')?.value === '您好，后续将由新的服务同事继续为您服务。', wecom_checked: Boolean(root?.querySelector('[data-include-wecom-transfer]')?.checked) }; })()`,
  `(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const header=root?.querySelector('.owner-migration-header'); return { page_max_width: root ? getComputedStyle(root).maxWidth : '', header_display: header ? getComputedStyle(header).display : '' }; })()`,
  `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`,
  `(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); root.querySelector('[data-owner-picker="source"]').click(); return true; })()`,
  operationMemberPresentExpression(sourceUserID),
  `(() => { const picker=document.querySelector('[data-operation-member-picker]'); return { hidden: Boolean(picker?.hidden), display: picker ? getComputedStyle(picker).display : '', visibility: picker ? getComputedStyle(picker).visibility : '' }; })()`,
  operationMemberChooseExpression(sourceUserID),
  `document.querySelector('[data-operation-member-confirm]').click(); true`,
  `document.querySelector('[data-owner-handoff-host] [data-owner-userid="source"]').value === ${JSON.stringify(source)}`,
  `document.querySelector('[data-owner-handoff-host] [data-owner-picker="target"]').click(); true`,
  operationMemberPresentExpression(targetUserID),
  operationMemberChooseExpression(targetUserID),
  `document.querySelector('[data-operation-member-confirm]').click(); true`,
  `document.querySelector('[data-owner-handoff-host] [data-owner-userid="target"]').value === ${JSON.stringify(target)}`,
  selectExcelScopeExpression(),
  uploadLegacyFileExpression(),
  `!document.querySelector('[data-owner-handoff-host] [data-import-summary]').hidden`,
  `(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const wecom=root.querySelector('[data-include-wecom-transfer]'); wecom.checked=true; wecom.dispatchEvent(new Event('change',{bubbles:true})); root.querySelector('[data-preview]').click(); return true; })()`,
  `Boolean(document.querySelector('[data-owner-handoff-host] [data-preview-content]:not([hidden])'))`,
  `(() => { const text=document.querySelector('[data-owner-handoff-host] [data-preview-rows]').textContent; return ["browser-external","duplicate","missing_external_userid","invalid_move_flag","skipped_by_file","not_under_source_owner"].every(value => text.includes(value)); })()`,
  downloadBlockedRowsExpression(),
  `document.querySelector('[data-owner-handoff-host] [data-confirm-phrase-display]').textContent`,
  `(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const input=root.querySelector('[data-confirm-phrase-input]'); input.value=${JSON.stringify("确认迁移")}; input.dispatchEvent(new Event('input',{bubbles:true})); root.querySelector('[data-execute]').click(); return true; })()`,
  `document.querySelector('[data-owner-handoff-host] [data-execution-log]').textContent.includes('batch_id=')`,
  ownerHandoffBatchStateExpression(),
  `document.querySelector('[data-owner-handoff-host] [data-read-transfer-result]').click(); true`,
  `document.querySelector('[data-owner-handoff-host] [data-execution-log]').textContent.includes('transfer_status=1')`,
  downloadResultRowsExpression(),
  `Boolean(document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'))`,
];
if (process.env.AICRM_OWNER_HANDOFF_TEST_COMPILE_ONLY === "1") {
  preflightRuntimeExpressions().forEach((expression, index) => compileRuntimeExpression(expression, `expression_${index + 1}`));
  console.log("owner_handoff_chromium_expression_preflight: PASS");
  process.exit(0);
}
const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));
const chrome = () => {
  for (const value of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"].filter(Boolean)) {
    try { if (value.includes("/") ? spawnSync(value, ["--version"], {stdio:"ignore"}).status === 0 : spawnSync("which", [value], {stdio:"ignore"}).status === 0) return value; } catch (_) {}
  } throw new Error("Chromium binary is unavailable");
};
class CDP {
  constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); this.events=new Map(); socket.addEventListener("message", event => { const message=JSON.parse(String(event.data)); if (message.id && this.pending.has(message.id)) { const {resolve,reject}=this.pending.get(message.id); this.pending.delete(message.id); message.error ? reject(new Error(`CDP ${message.error.code}`)) : resolve(message.result || {}); return; } for (const listener of this.events.get(message.method) || []) listener(message.params || {}); }); }
  call(method, params={}) { return new Promise((resolve,reject) => { const id=++this.id; this.pending.set(id,{resolve,reject}); this.socket.send(JSON.stringify({id,method,params})); }); }
  on(method, listener) { this.events.set(method,[...(this.events.get(method)||[]),listener]); return () => this.events.set(method,(this.events.get(method)||[]).filter(item => item !== listener)); }
  next(method, predicate, message) { return new Promise((resolve,reject) => { let off=()=>{}; const timer=setTimeout(() => { off(); reject(new Error(message)); }, 8000); const listener=params => { if (!predicate(params)) return; clearTimeout(timer); off(); resolve(params); }; off=this.on(method,listener); }); }
  close() { for (const {reject} of this.pending.values()) reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}
const openCDP = async webSocketDebuggerUrl => {
  const socket = new WebSocket(webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",()=>reject(new Error("CDP connection failed")),{once:true});});
  return new CDP(socket);
};
const waitForPort = async (profile, processState) => {
  const deadline=Date.now()+chromiumStartupTimeoutMS;
  while (Date.now()<deadline) {
    try {
      const [port]=String(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n");
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    const state=processState();
    if (state.launchError || state.exitCode !== null || state.signalCode) throw new Error(chromiumStartupDiagnostic({...state,profile}));
    await sleep(50);
  }
  throw new Error(chromiumStartupDiagnostic({...processState(),profile}));
};
const waitForExit = async (child, ms) => !child || child.exitCode !== null || child.signalCode !== null || new Promise(resolve => { const timer=setTimeout(()=>resolve(false),ms); child.once("exit",()=>{clearTimeout(timer);resolve(true);}); });
const removeProfile = async profile => { for (let attempt=0;attempt<40;attempt++) { try { await fs.rm(profile,{recursive:true,force:true,maxRetries:0}); return true; } catch (error) { if (!["ENOTEMPTY","EBUSY","EPERM"].includes(error?.code)) return false; await sleep(100); } } return false; };
const xmlText = value => String(value).replace(/[&<>'"]/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&apos;", '"': "&quot;" }[char]));

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-owner-handoff-chromium-"));
const downloads = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-owner-handoff-downloads-"));
let child; let cdp; let browserCDP; let failed=false; let chromeLaunchError; let chromeStderr="";
try {
  child=spawn(chrome(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--no-first-run","--no-default-browser-check","--disable-background-networking","--disable-component-update","--disable-sync","--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:["ignore","ignore","pipe"]});
  child.once("error", error => { chromeLaunchError=error; });
  child.stderr?.on("data", chunk => { chromeStderr=(chromeStderr+String(chunk)).slice(-1024); });
  const address=await waitForPort(profile, () => ({ exitCode:child?.exitCode ?? null, signalCode:child?.signalCode ?? null, launchError:chromeLaunchError, stderr:chromeStderr }));
  const browserInfo=await (await fetch(`${address}/json/version`)).json();
  if (!browserInfo.webSocketDebuggerUrl) throw new Error("Chromium browser debugging endpoint is unavailable");
  browserCDP=await openCDP(browserInfo.webSocketDebuggerUrl);
  await browserCDP.call("Browser.setDownloadBehavior",{behavior:"allow",downloadPath:downloads,eventsEnabled:true});
  const page=await (await fetch(`${address}/json/new?about:blank`,{method:"PUT"})).json();
  cdp=await openCDP(page.webSocketDebuggerUrl); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
  const evaluate=async (expression, step="page_evaluation")=>{ compileRuntimeExpression(expression, step); const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails) { const category=String(result.exceptionDetails.exception?.className || result.exceptionDetails.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g,"_").slice(0,96); throw new Error(`${step} page evaluation failed (${category})`); } return result.result?.value; };
  const waitFor=async(expression,message)=>{for(let attempt=0;attempt<160;attempt++){if(await evaluate(expression))return;await sleep(50);}throw new Error(message);};
  const awaitDownloadCompletion=(filename)=>new Promise((resolve,reject) => {
    let guid=""; let offBegin=()=>{}; let offProgress=()=>{};
    const finish=(error)=>{ clearTimeout(timer); offBegin(); offProgress(); error ? reject(error) : resolve(); };
    const timer=setTimeout(() => finish(new Error(`download ${filename} did not complete`)), 8000);
    offBegin=browserCDP.on("Browser.downloadWillBegin", event => { if (event.suggestedFilename === filename) guid=String(event.guid || ""); });
    offProgress=browserCDP.on("Browser.downloadProgress", event => {
      if (!guid || String(event.guid || "") !== guid || (event.state !== "completed" && event.state !== "canceled")) return;
      finish(event.state === "completed" ? undefined : new Error(`download ${filename} ended ${event.state}`));
    });
  });
  const readDownloadedWorkbook=async (filename, expectedValues, trigger) => {
    const completed=awaitDownloadCompletion(filename);
    await trigger();
    await completed;
    const destination=path.join(downloads,filename);
    let bytes;
    try { bytes=await fs.readFile(destination); } catch (_) { throw new Error(`completed download ${filename} was not written`); }
    if (bytes.length < 4 || bytes[0] !== 0x50 || bytes[1] !== 0x4b) throw new Error(`completed download ${filename} was not a readable XLSX file (bytes=${bytes.length},prefix=${Buffer.from(bytes.subarray(0,8)).toString("hex") || "none"})`);
    let workbook;
    try { workbook=unzipSync(bytes); } catch (_) { throw new Error(`downloaded ${filename} could not be opened as XLSX`); }
    const sheet=workbook["xl/worksheets/sheet1.xml"];
    if (!sheet) throw new Error(`downloaded ${filename} did not contain the result worksheet`);
    const cells=strFromU8(sheet);
    for (const value of expectedValues) if (!cells.includes(xmlText(value))) throw new Error(`downloaded ${filename} omitted expected result field`);
    await fs.rm(destination,{force:true});
  };
  await cdp.call("Page.navigate",{url:`${baseURL}/login?next=%2Fadmin%2Fowner-migration`});
  await waitFor("Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))","login shell did not render");
  const loginNav=cdp.next("Page.frameNavigated",params=>Boolean(params.frame&&!params.frame.parentId),"login form did not navigate");
  await evaluate(`(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  const loginFrame=await loginNav; if(new URL(loginFrame.frame.url).pathname!=="/admin/owner-migration") throw new Error("login did not reach owner migration");
  await waitFor("(() => { const stage=document.querySelector('[data-owner-handoff-host]'); return ['ready','donor_error','context_error','host_error'].includes(stage?.dataset.ownerHandoffInit || ''); })()", "owner handoff Host did not complete initialization");
  const hostDiagnostic = await evaluate(`(() => {
    const stage=document.querySelector('[data-owner-handoff-host]');
    const root=stage?.querySelector('[data-owner-migration-page]');
    return {
      init: stage?.dataset.ownerHandoffInit || 'missing',
      http_status: stage?.dataset.ownerHandoffInitStatus || '',
      page: Boolean(root),
      has_curly_marker: Boolean(root?.innerHTML.includes('{{')),
      has_block_marker: Boolean(root?.innerHTML.includes('{%')),
      operator_ready: Boolean(root?.querySelector('#operator')?.value.startsWith('管理员 #')),
      welcome_ready: root?.querySelector('[data-transfer-welcome-msg]')?.value === '您好，后续将由新的服务同事继续为您服务。',
      wecom_checked: Boolean(root?.querySelector('[data-include-wecom-transfer]')?.checked),
    };
  })()`);
  if (hostDiagnostic.init !== 'ready' || !hostDiagnostic.page || hostDiagnostic.has_curly_marker || hostDiagnostic.has_block_marker || !hostDiagnostic.operator_ready || !hostDiagnostic.welcome_ready || !hostDiagnostic.wecom_checked) throw new Error(`owner handoff Host initialization mismatch ${JSON.stringify(hostDiagnostic)}`);
  const pageStyle = await evaluate(`(() => {
    const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]');
    const header=root?.querySelector('.owner-migration-header');
    return { page_max_width: root ? getComputedStyle(root).maxWidth : '', header_display: header ? getComputedStyle(header).display : '' };
  })()`);
  if (pageStyle.page_max_width !== '1440px' || pageStyle.header_display !== 'flex') throw new Error(`owner handoff frozen-page styles were blocked ${JSON.stringify(pageStyle)}`);
  const run=async (mode, scope=requestedScope)=>{
    await evaluate(`(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); root.querySelector('[data-owner-picker="source"]').click(); return true; })()`);
    await waitFor(operationMemberPresentExpression(sourceUserID),"source picker did not include inactive source");
    const pickerStyle = await evaluate(`(() => { const picker=document.querySelector('[data-operation-member-picker]'); return { hidden: Boolean(picker?.hidden), display: picker ? getComputedStyle(picker).display : '', visibility: picker ? getComputedStyle(picker).visibility : '' }; })()`);
    if (pickerStyle.hidden || pickerStyle.display !== 'flex' || pickerStyle.visibility !== 'visible') throw new Error(`owner handoff shared picker styles were blocked ${JSON.stringify(pickerStyle)}`);
    await evaluate(operationMemberChooseExpression(sourceUserID));
    await evaluate("document.querySelector('[data-operation-member-confirm]').click(); true");
    await waitFor(`document.querySelector('[data-owner-handoff-host] [data-owner-userid="source"]').value === ${JSON.stringify(source)}`, "source picker did not persist the selected Access staff");
    await evaluate(`document.querySelector('[data-owner-handoff-host] [data-owner-picker="target"]').click(); true`);
    await waitFor(operationMemberPresentExpression(targetUserID),"target picker did not include active target");
    await evaluate(operationMemberChooseExpression(targetUserID));
    await evaluate("document.querySelector('[data-operation-member-confirm]').click(); true");
    await waitFor(`document.querySelector('[data-owner-handoff-host] [data-owner-userid="target"]').value === ${JSON.stringify(target)}`, "target picker did not persist the selected Access staff");
    if (scope === "excel_include") {
      await evaluate(selectExcelScopeExpression(), "excel_scope_select");
      await evaluate(uploadLegacyFileExpression(), "excel_fixture_upload");
      await waitFor(`!document.querySelector("[data-owner-handoff-host] [data-import-summary]").hidden`, "old .xls import did not parse");
    }
    await evaluate(`(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const wecom=root.querySelector('[data-include-wecom-transfer]'); wecom.checked=${mode === "wecom_then_crm"}; wecom.dispatchEvent(new Event('change',{bubbles:true})); root.querySelector('[data-preview]').click(); return true; })()`);
    await waitFor("Boolean(document.querySelector('[data-owner-handoff-host] [data-preview-content]:not([hidden])'))",`${mode} preview was not persisted through actual HTTP API`);
    if (scope === "excel_include") {
      await waitFor(`(() => { const text=document.querySelector("[data-owner-handoff-host] [data-preview-rows]").textContent; return ["browser-external","duplicate","missing_external_userid","invalid_move_flag","skipped_by_file","not_under_source_owner"].every(value => text.includes(value)); })()`, "Excel preview lost donor row states or fields");
      await readDownloadedWorkbook("owner_migration_blocked_rows.xlsx", ["行号", "external_userid", "状态", "原因", "duplicate", "missing_external_userid", "invalid_move_flag", "not_under_source_owner"], () => evaluate(downloadBlockedRowsExpression(), "blocked_rows_download"));
    }
    const phrase=await evaluate("document.querySelector('[data-owner-handoff-host] [data-confirm-phrase-display]').textContent");
    await evaluate(`(() => { const root=document.querySelector('[data-owner-handoff-host] [data-owner-migration-page]'); const input=root.querySelector('[data-confirm-phrase-input]'); input.value=${JSON.stringify(phrase)}; input.dispatchEvent(new Event('input',{bubbles:true})); root.querySelector('[data-execute]').click(); return true; })()`);
    await waitFor("document.querySelector('[data-owner-handoff-host] [data-execution-log]').textContent.includes('batch_id=')",`${mode} confirmation was not persisted through actual HTTP API`);
    if (mode === "wecom_then_crm" && readTransfer) {
      let batchState = { category: "missing_batch", lines: [] };
      let accepted = false;
      for (let attempt = 0; attempt < 160; attempt += 1) {
        batchState = await evaluate(ownerHandoffBatchStateExpression(), "provider_batch_read");
        if (batchState.category === "ok" && batchState.lines.some(line => line.state === "provider_accepted" || (line.state === "observed" && line.transfer_status > 0))) { accepted = true; break; }
        await sleep(50);
      }
      if (!accepted) throw new Error(`provider transfer did not reach an accepted line ${JSON.stringify(batchState)}`);
      let read = false;
      for (let attempt = 0; attempt < 80; attempt += 1) {
        await evaluate("document.querySelector('[data-owner-handoff-host] [data-read-transfer-result]').click(); true", "transfer_result_click");
        await sleep(100);
        if (await evaluate("document.querySelector('[data-owner-handoff-host] [data-execution-log]').textContent.includes('transfer_status=1')", "transfer_result_render")) { read = true; break; }
      }
      if (!read) {
        const diagnostic = await evaluate(ownerHandoffBatchStateExpression(), "transfer_result_diagnostic");
        throw new Error(`transfer-result readback did not render final status ${JSON.stringify(diagnostic)}`);
      }
    }
    await readDownloadedWorkbook("owner_migration_result.xlsx", ["行号", "external_userid", "迁移状态", "企微转接状态", mode === "wecom_then_crm" ? "browser-external" : "本地迁移", mode === "wecom_then_crm" ? "企微转接已完成" : "本地迁移"], () =>
      evaluate(downloadResultRowsExpression(), "result_rows_download"));
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
  if(browserCDP) browserCDP.close();
  if(child&&child.exitCode===null&&child.signalCode===null){child.kill("SIGTERM");if(!await waitForExit(child,3000)&&child.exitCode===null&&child.signalCode===null){child.kill("SIGKILL");await waitForExit(child,1000);}}
  const [removedProfile,removedDownloads]=await Promise.all([removeProfile(profile),removeProfile(downloads)]); if((!removedProfile||!removedDownloads)&&!failed) throw new Error("Chromium test temporary-directory cleanup did not complete");
}
