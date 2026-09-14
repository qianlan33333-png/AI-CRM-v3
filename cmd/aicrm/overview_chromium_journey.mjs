import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_OVERVIEW_BROWSER_URL;
const username = process.env.AICRM_OVERVIEW_BROWSER_USERNAME;
const password = process.env.AICRM_OVERVIEW_BROWSER_PASSWORD;
const screenshotDirectory = process.env.AICRM_OVERVIEW_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) throw new Error("admin overview Chromium journey requires HTTPS URL and credentials");

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
function browserBinary() {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/") && spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate;
      if (!candidate.includes("/") && spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
    } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
}

class CDP {
  constructor(socket) {
    this.socket = socket; this.nextID = 0; this.pending = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data)); const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { for (const pending of this.pending.values()) pending.reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
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
  for (let attempt = 0; attempt < 180; attempt += 1) { if (await evaluate(cdp, expression)) return; await delay(50); }
  throw new Error(message);
}
async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
}
async function captureOverview(cdp, name, width) {
  if (!screenshotDirectory) return;
  await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
  await delay(80);
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  await fs.mkdir(screenshotDirectory, { recursive: true });
  await fs.writeFile(path.join(screenshotDirectory, name), Buffer.from(image.data, "base64"));
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-admin-overview-chromium-"));
let browser; let cdp; let failed = false;
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const target = await (await fetch(`${await port(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin' && !document.querySelector('form[action=\"/login\"]')", "login did not reach the authenticated admin Host");
  try {
    await waitFor(cdp, "Boolean(document.querySelector('#overview-admin-root .overview-metrics--primary')) && document.body.textContent.includes('已确认支付')", "overview Host did not render the primary metrics");
  } catch (error) {
    const diagnostics = await evaluate(cdp, "JSON.stringify({root:document.querySelector('#overview-admin-root')?.outerHTML||'',scripts:[...document.scripts].map((script)=>script.src),text:document.body.textContent.slice(0,1200)})");
    throw new Error(`${error.message}: ${diagnostics}`);
  }
  const overviewDOM = await evaluate(cdp, "JSON.stringify({primary:[...document.querySelectorAll('.overview-metrics--primary .overview-metric')].map((node)=>node.textContent),secondary:[...document.querySelectorAll('.overview-metrics--secondary .overview-metric')].map((node)=>node.textContent),today:performance.getEntriesByType('resource').some((entry)=>String(entry.name).includes('/api/admin/overview?period=today')),nav:[...document.querySelectorAll('.admin-nav-section-title')].map((node)=>node.textContent)})");
  const rendered = JSON.parse(overviewDOM || "{}");
  if (rendered.primary?.length !== 4 || !rendered.primary?.[0]?.includes("已确认支付") || !rendered.primary?.[1]?.includes("支付订单") || !rendered.primary?.[2]?.includes("支付客户") || !rendered.primary?.[3]?.includes("新增客户") || rendered.secondary?.length !== 2 || !rendered.secondary?.[0]?.includes("完成退款") || !rendered.secondary?.[1]?.includes("净收款") || !rendered.today || rendered.nav?.join("|") !== "总览|客户|运营|交易|分销|内容素材|系统设置") throw new Error("overview Host did not preserve its V3 metric and navigation contract");
  await captureOverview(cdp, "overview-1280.png", 1280);
  await captureOverview(cdp, "overview-1440.png", 1440);
  const response = await evaluate(cdp, "fetch('/api/admin/overview?period=7d',{credentials:'same-origin'}).then(async (value)=>({status:value.status,body:await value.json()}))");
  const overview = response?.body;
  if (response?.status !== 200 || !overview || overview.range?.timezone !== "Asia/Shanghai" || overview.paid?.status !== "ready" || overview.paid?.order_count !== 1 || overview.paid?.gross?.[0]?.amount_minor !== 1200 || overview.customers?.new_canonical_customers !== 1 || overview.refunds?.completed_count !== 1 || overview.distribution?.current_unsettled_minor !== 120 || overview.todos?.items?.[0]?.href !== "/admin/distribution") throw new Error("browser overview response did not preserve owner facts");
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/wechat-pay/products` });
  await waitFor(cdp, "Boolean(document.querySelector('.admin-nav a[href=\"/admin/wechat-pay/products\"].is-active'))", "server-rendered product shell did not retain the V3 navigation state");
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/distribution` });
  await waitFor(cdp, "Boolean(document.querySelector('#distribution-admin-root')) && Boolean(document.querySelector('.admin-nav a[href=\"/admin/distribution\"].is-active'))", "server-rendered distribution shell did not retain the V3 navigation state");
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/customerDetail.html?id=1` });
  await waitFor(cdp, "Boolean(document.querySelector('.side-nav[data-v3-navigation-host=\"ready\"]'))", "built customer document did not receive the shared V3 navigation Host");
  const customerNavigation = await evaluate(cdp, "JSON.stringify({groups:[...document.querySelectorAll('.side-nav .side-grp')].map((node)=>node.textContent),active:[...document.querySelectorAll('.side-nav .nav-item.on')].map((node)=>node.textContent?.trim()),access:document.body.textContent.includes('登录与权限')})");
  const customer = JSON.parse(customerNavigation || "{}");
  if (customer.groups?.join("|") !== "总览|客户|运营|交易|分销|内容素材|系统设置" || customer.active?.join("|") !== "客户激活 / 客户列表" || customer.access) throw new Error("built customer document did not apply the shared navigation safely");
  console.log("admin_overview_chromium: PASS");
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  await fs.rm(profile, { recursive: true, force: true, maxRetries: 2 }).catch((error) => { if (!failed) throw error; });
}
