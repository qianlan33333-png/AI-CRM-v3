import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_URL;
const username = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_USERNAME;
const password = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) throw new Error("sidebar thumbnail Chromium journey requires HTTPS URL and test credentials");

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function browserBinary() {
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
}
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
  on(method, listener) { const listeners = this.events.get(method) || []; listeners.push(listener); this.events.set(method, listeners); }
  close() { for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed")); this.pending.clear(); this.socket.close(); }
}
async function port(profile) {
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
async function browserExit(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) { child.kill("SIGKILL"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(1000)]); }
}
async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; }
    catch (error) { if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false; await delay(100); }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-sidebar-thumbnail-chromium-"));
let browser; let cdp; let failed = false;
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const created = await (await fetch(`${await port(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const resources = new Map(); const exceptions = []; const loginResponses = new Map(); let sidebarCSP = "";
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  cdp.on("Network.responseReceived", (params) => {
    try {
      const pathname = new URL(String(params.response?.url || "")).pathname;
      const status = Number(params.response?.status) || 0;
      if (pathname === "/sidebar/bind-mobile") sidebarCSP = String(params.response?.headers?.["content-security-policy"] || params.response?.headers?.["Content-Security-Policy"] || "");
      if (pathname === "/login" || pathname === "/admin" || pathname === "/admin/customers.html") loginResponses.set(pathname, status);
      if (pathname === "/api/sidebar/v2/bootstrap" || pathname === "/api/sidebar/v2/materials" || /^\/api\/sidebar\/v2\/materials\/\d+\/variants\/thumb_320$/.test(pathname) || /^\/sidebar-assets\/sidebarHost-[A-Za-z0-9_-]+\.js$/.test(pathname)) resources.set(pathname, status);
    } catch (_) {}
  });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  // The new shell canonicalizes the post-login /admin target to its customer
  // document. Require that actual final document instead of treating its
  // intentional redirect as a failed Access session.
  try {
    await waitFor(cdp, "location.pathname === '/admin/customers.html' && !document.querySelector('form[action=\"/login\"]')", "login did not reach the authenticated shell document");
  } catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), login: loginResponses.get("/login") || 0, admin: loginResponses.get("/admin") || 0, customers: loginResponses.get("/admin/customers.html") || 0, exceptions });
    throw new Error(`login did not establish the Access session: ${diagnostic}`);
  }
  const cookies = await cdp.call("Network.getAllCookies");
  const session = (cookies.cookies || []).find((cookie) => cookie.name === "aicrm_admin_session" && cookie.value);
  if (!session) throw new Error("Access session cookie was not issued");
  await cdp.call("Network.setCookie", { name: "aicrm_sidebar_session", value: session.value, url: baseURL, path: "/", secure: true, httpOnly: true, sameSite: "Lax" });
  await cdp.call("Page.navigate", { url: `${baseURL}/sidebar/bind-mobile?external_userid=sidebar-thumbnail-external` });
  try {
    await waitFor(cdp, "location.pathname === '/sidebar/bind-mobile' && Boolean(document.querySelector('#tabs button[data-sidebar-tab=\"materials\"]'))", "sidebar Host did not render");
  } catch (_) {
    const diagnostic = JSON.stringify({
      path: await evaluate(cdp, "location.pathname"),
      document: await evaluate(cdp, "document.body ? 'ready' : 'missing'"),
      tabs: await evaluate(cdp, "Boolean(document.querySelector('#tabs'))"),
      host: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarHost-/.test(path) && status === 200),
      bootstrap: resources.get("/api/sidebar/v2/bootstrap") || 0,
      materials: resources.get("/api/sidebar/v2/materials") || 0,
      cspBlob: sidebarCSP.includes("img-src 'self' data: blob:"),
      exceptions,
    });
    throw new Error(`sidebar Host did not render: ${diagnostic}`);
  }
  await evaluate(cdp, "document.querySelector('#tabs button[data-sidebar-tab=\"materials\"]')?.click(); true");
  const ready = "(() => { const image=document.querySelector('img[data-material-preview=\"ready\"]'); return Boolean(image && image.src.startsWith('blob:') && image.complete && image.naturalWidth === 1 && image.naturalHeight === 1); })()";
  try { await waitFor(cdp, ready, "sidebar thumbnail did not load through a blob URL"); }
  catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), host: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarHost-/.test(path) && status === 200), bootstrap: resources.get("/api/sidebar/v2/bootstrap") || 0, materials: resources.get("/api/sidebar/v2/materials") || 0, thumbnail: [...resources.entries()].some(([path, status]) => /variants\/thumb_320$/.test(path) && status === 200), cspBlob: sidebarCSP.includes("img-src 'self' data: blob:"), exceptions });
    throw new Error(`sidebar thumbnail did not render: ${diagnostic}`);
  }
  if (!sidebarCSP.includes("img-src 'self' data: blob:")) throw new Error("sidebar CSP did not permit its scoped thumbnail blob URL");
  if (![...resources.entries()].some(([pathname, status]) => /^\/sidebar-assets\/sidebarHost-/.test(pathname) && status === 200) || resources.get("/api/sidebar/v2/bootstrap") !== 200 || resources.get("/api/sidebar/v2/materials") !== 200 || ![...resources.entries()].some(([pathname, status]) => /\/variants\/thumb_320$/.test(pathname) && status === 200)) throw new Error("sidebar Host/resources did not use the actual scoped thumbnail route");
  if (exceptions.length) throw new Error(`sidebar Host emitted runtime exceptions: ${exceptions.join(",")}`);
  console.log("sidebar_thumbnail_chromium: PASS");
} catch (error) { failed = true; throw error; }
finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); }
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete");
}
