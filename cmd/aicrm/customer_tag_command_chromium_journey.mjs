import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_CUSTOMER_TAG_TEST_URL;
const username = process.env.AICRM_CUSTOMER_TAG_TEST_USERNAME;
const password = process.env.AICRM_CUSTOMER_TAG_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) throw new Error("customer tag Chromium journey requires HTTPS URL and test credentials");
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const candidates = () => {
  const explicit = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") explicit.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  return [...explicit, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];
};
function browserBinary() {
  for (const candidate of candidates()) {
    if (candidate.includes("/")) { try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {} }
    else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
  }
  throw new Error("Chromium binary is unavailable");
}
class CDP {
  constructor(socket) { this.socket = socket; this.nextID = 0; this.pending = new Map(); this.events = new Map(); socket.addEventListener("message", (event) => { const message = JSON.parse(String(event.data)); if (message.id && this.pending.has(message.id)) { const pending = this.pending.get(message.id); this.pending.delete(message.id); message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {}); return; } for (const listener of this.events.get(message.method) || []) listener(message.params || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  on(method, listener) { const list = this.events.get(method) || []; list.push(listener); this.events.set(method, list); }
  close() { for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed")); this.pending.clear(); this.socket.close(); }
}
async function port(profile) { for (let i = 0; i < 160; i += 1) { try { const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`; } catch (_) {} await delay(50); } throw new Error("Chromium remote debugging did not become ready"); }
async function evaluate(cdp, expression) { const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; }
async function waitFor(cdp, expression, message) { for (let i = 0; i < 180; i += 1) { if (await evaluate(cdp, expression)) return; await delay(50); } throw new Error(message); }
async function browserExit(child) { if (!child || child.exitCode !== null || child.signalCode !== null) return; await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]); if (child.exitCode === null && child.signalCode === null) { child.kill("SIGKILL"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(1000)]); } }
async function removeProfile(profile) { for (let i = 0; i < 40; i += 1) { try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; } catch (error) { if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false; await delay(100); } } return false; }
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-customer-tags-chromium-"));
let browser; let cdp; let failed = false;
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const created = await (await fetch(`${await port(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const exceptions = []; const resources = new Map();
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  cdp.on("Network.responseReceived", (params) => { try { const pathname = new URL(String(params.response?.url || "")).pathname; if (["/static/admin_console/admin_customers.js", "/api/admin/customers", "/api/admin/wecom/tags", "/api/v1/customer-tag-commands/preview", "/api/v1/customer-tag-commands"].includes(pathname)) resources.set(pathname, Number(params.response?.status) || 0); } catch (_) {} });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin%2Fcustomers` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/customers' && Boolean(document.querySelector('[data-customer-directory-root]'))", "login did not load the customer Host route");
  const diagnostic = async () => JSON.stringify({ path: await evaluate(cdp, "location.pathname"), rows: await evaluate(cdp, "document.querySelectorAll('#customer-list-body input[type=checkbox]').length"), resources: Object.fromEntries(resources), exceptions });
  try { await waitFor(cdp, "document.querySelectorAll('#customer-list-body input[type=checkbox]').length >= 2 && document.querySelectorAll('#customer-tag-batch option').length >= 2", "customer list or tag catalog did not load"); } catch (_) { throw new Error(`customer Host did not load: ${await diagnostic()}`); }
  await evaluate(cdp, `(() => { const checks=[...document.querySelectorAll('#customer-list-body input[type=checkbox]')].slice(0,2); checks.forEach((box) => { box.checked=true; box.dispatchEvent(new Event('change', {bubbles:true})); }); const form=document.querySelector('#customer-tag-batch'); const add=form.querySelector('[name="add_tag_ids"]'); const remove=form.querySelector('[name="remove_tag_ids"]'); add.options[0].selected=true; remove.options[1].selected=true; window.confirm=()=>true; form.requestSubmit(); return true; })()`);
  try { await waitFor(cdp, "document.querySelector('#customer-tag-batch-result')?.textContent.includes('已刷新执行结果')", "browser did not render the accepted command result"); } catch (_) { throw new Error(`customer command interaction did not render: ${await diagnostic()}`); }
  await waitFor(cdp, "Boolean(document.querySelector('#customer-tag-batch-refresh:not([hidden])'))", "accepted tag command did not expose the explicit result refresh action");
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await evaluate(cdp, "document.querySelector('#customer-tag-batch-refresh')?.click(); true");
    await delay(250);
    const rendered = await evaluate(cdp, "document.querySelector('#customer-tag-batch-result')?.textContent || ''");
    if (rendered.includes('客户 #1：executed') && rendered.includes('客户 #2：outcome_unknown') && rendered.includes('观察标签：fixture observed（active）')) break;
    if (attempt === 19) throw new Error(`explicit result refresh did not render durable outcomes: ${rendered}`);
  }
  console.log("customer_tag_command_chromium: PASS");
} catch (error) { failed = true; throw error; } finally { if (cdp) cdp.close(); if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); } const removed = await removeProfile(profile); if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete"); }
