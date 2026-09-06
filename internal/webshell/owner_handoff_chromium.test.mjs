import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_OWNER_HANDOFF_TEST_URL;
const username = process.env.AICRM_OWNER_HANDOFF_TEST_USERNAME;
const password = process.env.AICRM_OWNER_HANDOFF_TEST_PASSWORD;
const source = process.env.AICRM_OWNER_HANDOFF_TEST_SOURCE;
const target = process.env.AICRM_OWNER_HANDOFF_TEST_TARGET;
const localCustomer = process.env.AICRM_OWNER_HANDOFF_TEST_LOCAL_CUSTOMER;
const wecomCustomer = process.env.AICRM_OWNER_HANDOFF_TEST_WECOM_CUSTOMER;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !source || !target || !localCustomer || !wecomCustomer) throw new Error("owner handoff Chromium journey requires HTTPS URL, credentials, and fixture IDs");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const chrome = () => {
  for (const value of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) {
    try { if (value.includes("/") ? spawnSync(value, ["--version"], {stdio:"ignore"}).status === 0 : spawnSync("which", [value], {stdio:"ignore"}).status === 0) return value; } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
};
class CDP {
  constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); socket.addEventListener("message", event => { const message=JSON.parse(String(event.data)); if (!message.id || !this.pending.has(message.id)) return; const {resolve,reject}=this.pending.get(message.id); this.pending.delete(message.id); message.error ? reject(new Error(`CDP ${message.error.code}`)) : resolve(message.result || {}); }); }
  call(method, params={}) { return new Promise((resolve,reject) => { const id=++this.id; this.pending.set(id,{resolve,reject}); this.socket.send(JSON.stringify({id,method,params})); }); }
  close() { for (const {reject} of this.pending.values()) reject(new Error("CDP closed")); this.socket.close(); }
}
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-owner-handoff-chromium-"));
let child; let cdp;
try {
  child = spawn(chrome(), ["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--no-first-run","--no-default-browser-check","--disable-background-networking","--disable-component-update","--disable-sync","--ignore-certificate-errors","--allow-insecure-localhost","about:blank"], {stdio:"ignore"});
  let address;
  for (let attempt=0; attempt<160; attempt++) { try { const [port] = String(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n"); if (/^\d+$/.test(port)) { address=`http://127.0.0.1:${port}`; break; } } catch (_) {} await sleep(50); }
  if (!address) throw new Error("Chromium remote debugging did not become ready");
  const page = await (await fetch(`${address}/json/new?about:blank`, {method:"PUT"})).json();
  const socket = new WebSocket(page.webSocketDebuggerUrl); await new Promise((resolve,reject) => { socket.addEventListener("open",resolve,{once:true}); socket.addEventListener("error",() => reject(new Error("CDP connection failed")),{once:true}); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
  const evaluate = async (expression) => { const result=await cdp.call("Runtime.evaluate", {expression,returnByValue:true,awaitPromise:true}); if (result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; };
  const waitFor = async (expression, message) => { for (let attempt=0; attempt<160; attempt++) { if (await evaluate(expression)) return; await sleep(50); } throw new Error(message); };
  await cdp.call("Page.navigate", {url:`${baseURL}/login?next=%2Fadmin%2Fowner-migration`});
  await waitFor("Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(`(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor("location.pathname === '/admin/owner-migration' && Boolean(document.querySelector('[data-owner-handoff-host] [data-preview]'))", "owner handoff Host did not render after login");
  const run = async (mode, customerID) => {
    await evaluate(`(() => { const stage=document.querySelector('[data-owner-handoff-host]'); stage.querySelector('[data-mode]').value=${JSON.stringify(mode)}; stage.querySelector('[data-scope]').value='wecom-corp:browser'; stage.querySelector('[data-source]').value=${JSON.stringify(source)}; stage.querySelector('[data-target]').value=${JSON.stringify(target)}; stage.querySelector('[data-customers]').value=${JSON.stringify(customerID)}; stage.querySelector('[data-preview]').click(); return true; })()`);
    await waitFor("Boolean(document.querySelector('[data-preview-result] [data-confirm]'))", `${mode} preview was not persisted`);
    await evaluate("document.querySelector('[data-preview-result] [data-confirm]').click(); true");
    await waitFor("Boolean(document.querySelector('[data-batch-result]')) && document.querySelector('[data-batch-result]').textContent.includes('accepted')", `${mode} confirmation was not persisted`);
  };
  await run("local_only", localCustomer);
  await cdp.call("Page.navigate", {url:`${baseURL}/admin/owner-migration`});
  await waitFor("Boolean(document.querySelector('[data-owner-handoff-host] [data-preview]'))", "owner handoff Host did not rerender");
  await run("wecom_then_crm", wecomCustomer);
  console.log("owner_handoff_chromium: PASS");
} finally {
  if (cdp) cdp.close();
  if (child && child.exitCode === null) child.kill("SIGTERM");
  await sleep(100); await fs.rm(profile,{recursive:true,force:true}).catch(() => {});
}
