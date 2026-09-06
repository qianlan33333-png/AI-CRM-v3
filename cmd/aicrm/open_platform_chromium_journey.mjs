import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_OPEN_PLATFORM_TEST_URL;
const username = process.env.AICRM_OPEN_PLATFORM_TEST_USERNAME;
const password = process.env.AICRM_OPEN_PLATFORM_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) {
  throw new Error("Open Platform Chromium journey requires HTTPS URL and test credentials");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const browserBinary = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/")) { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; }
      else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
    } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket; this.nextID = 0; this.pending = new Map(); this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id); this.pending.delete(message.id);
        message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  on(method, listener) { const listeners = this.events.get(method) || []; listeners.push(listener); this.events.set(method, listeners); return () => this.events.set(method, (this.events.get(method) || []).filter((item) => item !== listener)); }
  nextEvent(method, predicate, timeout, message) { return new Promise((resolve, reject) => { let unsubscribe = () => {}; const timer = setTimeout(() => { unsubscribe(); reject(new Error(message)); }, timeout); unsubscribe = this.on(method, (params) => { if (!predicate(params)) return; clearTimeout(timer); unsubscribe(); resolve(params); }); }); }
  close() { for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed")); this.pending.clear(); this.events.clear(); this.socket.close(); }
}

async function debuggingAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try { const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`; } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
}
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
}
async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
}
async function waitForResource(resources, pathname, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (resources.has(pathname)) return resources.get(pathname);
    await delay(50);
  }
  throw new Error(message);
}
const navigation = (cdp, message) => cdp.nextEvent("Page.frameNavigated", (params) => Boolean(params.frame && !params.frame.parentId), 8000, message);
async function browserExit(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) { child.kill("SIGKILL"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(1000)]); }
}
async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; }
    catch (error) { if (!error || !["ENOTEMPTY", "EBUSY", "EPERM"].includes(error.code)) return false; await delay(100); }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-open-platform-chromium-"));
let browser; let cdp; let failed = false;
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const created = await (await fetch(`${await debuggingAddress(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const resources = new Map(); const exceptions = [];
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  cdp.on("Network.responseReceived", (params) => { try {
    const pathname = new URL(String(params.response?.url || "")).pathname;
    const status = Number(params.response?.status) || 0;
    if (pathname.startsWith("/assets/")) resources.set("/assets/", status);
    if (pathname === "/api/admin/open-platform/clients" || pathname === "/api/admin/open-platform/routes") resources.set(pathname, status);
    if (pathname.startsWith("/api/admin/open-platform/clients/browser-open-agent/")) resources.set(pathname, status);
  } catch (_) {} });

  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin%2Fapidocs.html` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  const login = navigation(cdp, "login form did not complete top-level navigation");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  const frame = await login;
  if (new URL(frame.frame.url).pathname !== "/admin/apidocs.html") throw new Error("login did not redirect to the V1 caller page");
  try { await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-host=\"v1\"] [data-open-platform-create=\"client_id\"]'))", "authenticated V1 caller Host did not render"); }
  catch (_) {
    const state = await evaluate(cdp, "(() => ({path:location.pathname,stage:Boolean(document.querySelector('#stage')),host:Boolean(document.querySelector('[data-open-platform-host=\\\"v1\\\"]'))}))()");
    throw new Error(`authenticated V1 caller Host did not render: path=${state?.path || "unavailable"} stage=${Boolean(state?.stage)} host=${Boolean(state?.host)} assets=${resources.get("/assets/") || 0} clients=${resources.get("/api/admin/open-platform/clients") || 0} catalog=${resources.get("/api/admin/open-platform/routes") || 0} exceptions=${exceptions.length ? exceptions.join(",") : "none"}`);
  }

  const click = async (label) => evaluate(cdp, `(() => { const button=[...document.querySelectorAll('[data-open-platform-action]')].find((node)=>node.dataset.openPlatformAction===${JSON.stringify(label)}); if (!button) return false; button.click(); return true; })()`);
  await evaluate(cdp, `(() => {
    document.querySelector('[data-open-platform-create="client_id"]').value='browser-open-agent';
    document.querySelector('[data-open-platform-create="display_name"]').value='Browser Open Agent';
    document.querySelector('[data-open-platform-create="token_ttl_seconds"]').value='1800';
    document.querySelector('input[name="create-scope"][value="read"]').checked=true;
    document.querySelector('input[name="create-capability"][value="platform.capabilities.read"]').checked=true;
    return true;
  })()`);
  if (!await click("创建并显示一次密钥")) throw new Error("create action was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret'))", "create did not display a one-time credential");
  const firstSecret = await evaluate(cdp, "document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret')?.textContent || ''");
  if (!firstSecret) throw new Error("one-time credential was empty");
  const oauth = (secret, scope = "read") => evaluate(cdp, `fetch('/oauth/token',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/x-www-form-urlencoded','Authorization':'Basic '+btoa('browser-open-agent:'+${JSON.stringify(secret)})},body:new URLSearchParams({grant_type:'client_credentials',audience:'external_integration',scope:${JSON.stringify(scope)}})}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))`);
  const restCatalog = (token) => evaluate(cdp, `fetch('/open/v1/capabilities',{headers:{Authorization:'Bearer '+${JSON.stringify(token)}}}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))`);
  const mcpCatalog = (token) => evaluate(cdp, `fetch('/mcp',{method:'POST',headers:{Authorization:'Bearer '+${JSON.stringify(token)},'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:'browser-catalog',method:'tools/list',params:{}})}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))`);

  const firstActivationPath = "/api/admin/open-platform/clients/browser-open-agent/activate";
  resources.delete(firstActivationPath);
  if (!await click("我已手动复制并确认启用")) throw new Error("manual credential confirmation was unavailable");
  const firstActivationStatus = await waitForResource(resources, firstActivationPath, "manual confirmation did not issue an activation request");
  if (firstActivationStatus !== 200) throw new Error(`manual confirmation activation status=${firstActivationStatus}`);
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"]')?.textContent.includes('已启用')", "activation succeeded but the caller Host did not refresh as enabled");
  const firstOAuth = await oauth(firstSecret); const firstToken = firstOAuth?.body?.access_token;
  if (firstOAuth?.status !== 200 || typeof firstToken !== "string" || !firstToken) throw new Error("OAuth did not issue an activated token");
  const firstCatalog = await restCatalog(firstToken); const firstOperations = firstCatalog?.body?.data?.operations;
  if (firstCatalog?.status !== 200 || !Array.isArray(firstOperations) || firstOperations.length !== 6 || !firstOperations.some((item) => item?.operation_id === "platform.capabilities.list")) throw new Error("REST V1 catalog did not expose the six current operations");
  const firstMCP = await mcpCatalog(firstToken);
  if (firstMCP?.status !== 200 || !Array.isArray(firstMCP?.body?.result?.tools) || !firstMCP.body.result.tools.some((tool) => tool?.name === "list_capabilities")) throw new Error("MCP catalog was not available to the activated caller");

  await evaluate(cdp, "document.querySelector('input[name=\"edit-capability\"][value=\"customer.resolve\"]')?.click(); true");
  if (!await click("保存授权")) throw new Error("grant save action was unavailable");
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"] input[name=\"edit-capability\"][value=\"customer.resolve\"]')?.checked === true", "grant edit did not reload the caller");
  if ((await restCatalog(firstToken))?.status !== 401) throw new Error("grant update did not revoke the prior OAuth token");
  const grantedOAuth = await oauth(firstSecret); const grantedToken = grantedOAuth?.body?.access_token;
  if (grantedOAuth?.status !== 200 || typeof grantedToken !== "string") throw new Error("credential did not issue a replacement token after grant update");
  if (!(await restCatalog(grantedToken))?.body?.data?.operations?.some((item) => item?.operation_id === "customer.resolve")) throw new Error("changed grant did not appear in the REST catalog");

  if (!await click("轮换密钥")) throw new Error("rotation action was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret'))", "rotation did not display its one-time credential");
  const secondSecret = await evaluate(cdp, "document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret')?.textContent || ''");
  if (!secondSecret || secondSecret === firstSecret) throw new Error("rotation did not issue a distinct one-time credential");
  if ((await oauth(firstSecret))?.status === 200) throw new Error("rotation left the old credential usable");
  const rotated = await evaluate(cdp, "fetch('/api/admin/open-platform/clients/browser-open-agent',{credentials:'same-origin'}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))");
  if (rotated?.status !== 200 || rotated?.body?.client?.enabled !== false) throw new Error("rotation did not return the caller to disabled handoff state");
  const secondActivationPath = "/api/admin/open-platform/clients/browser-open-agent/activate";
  resources.delete(secondActivationPath);
  if (!await click("我已手动复制并确认启用")) throw new Error("rotated credential confirmation was unavailable");
  const secondActivationStatus = await waitForResource(resources, secondActivationPath, "rotated credential confirmation did not issue an activation request");
  if (secondActivationStatus !== 200) throw new Error(`rotated credential confirmation activation status=${secondActivationStatus}`);
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"]')?.textContent.includes('已启用')", "rotated activation succeeded but the caller Host did not refresh as enabled");
  const secondOAuth = await oauth(secondSecret); const secondToken = secondOAuth?.body?.access_token;
  if (secondOAuth?.status !== 200 || typeof secondToken !== "string") throw new Error("rotated credential did not issue an OAuth token");

  if (!await click("停用调用方")) throw new Error("disable action was unavailable");
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"]')?.textContent.includes('待启用或已停用')", "disable did not update the caller detail");
  if ((await restCatalog(secondToken))?.status !== 401) throw new Error("disable did not revoke the current OAuth token");
  const audit = await evaluate(cdp, "fetch('/api/admin/open-platform/clients/browser-open-agent/audit?limit=20',{credentials:'same-origin'}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))");
  if (audit?.status !== 200 || !Array.isArray(audit?.body?.items) || !audit.body.items.some((item) => item?.action === "machine_client_disabled")) throw new Error("caller audit did not record final disable");
  console.log("open_platform_chromium: PASS");
} catch (error) { failed = true; throw error; }
finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); }
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete");
}
