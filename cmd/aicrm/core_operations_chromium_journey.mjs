import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_URL;
const username = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME;
const password = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD;
const packageID = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID;
const screenshotDirectory = process.env.AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(packageID || "") || !path.isAbsolute(screenshotDirectory || "")) {
  throw new Error("audience confirmation Chromium journey requires HTTPS URL, credentials, package ID, and absolute screenshots");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const browserBinary = () => {
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
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const current = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) current.reject(new Error("CDP " + message.error.code));
        else current.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  on(method, listener) {
    const listeners = this.events.get(method) || [];
    listeners.push(listener);
    this.events.set(method, listeners);
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    this.socket.close();
  }
}

async function debuggingAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^[0-9]+$/.test(port)) return "http://127.0.0.1:" + port;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
}

async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed: " + String(result.exceptionDetails.text || "unknown"));
  return result.result?.value;
}

async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message + " page=" + JSON.stringify(await evaluate(cdp,"({route:location.pathname,core:document.querySelector('#coreOperationsRoot')?.textContent,scripts:[...document.scripts].map(s=>s.src)})")));
}

async function waitForBrowserExit(browser, timeoutMilliseconds) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    browser.once("exit", () => { clearTimeout(timer); resolve(true); });
  });
}

async function stopBrowser(browser) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return true;
  browser.kill("SIGTERM");
  if (await waitForBrowserExit(browser, 3000)) return true;
  if (browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGKILL");
    return waitForBrowserExit(browser, 1000);
  }
  return true;
}

async function removeProfile(profile) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!['ENOTEMPTY', 'EBUSY', 'EPERM'].includes(error?.code)) return false;
      await delay(100);
    }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-audience-confirmation-chromium-"));
let browser;
let cdp;
let failed = false;
try {
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const target = await (await fetch((await debuggingAddress(profile)) + "/json/new?about:blank", { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  const requests = [];
  const exceptions = [];
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const request = new URL(String(params.request?.url || ""));
      if (request.origin === new URL(baseURL).origin && request.pathname.startsWith("/api/")) requests.push({ path: request.pathname, method: params.request?.method || "GET" });
    } catch (_) {}
  });
  cdp.on("Runtime.exceptionThrown", (params) => {
    if (exceptions.length < 8) exceptions.push(String(params.exceptionDetails?.text || "runtime_exception"));
  });
  const resize = (width, height = 900) => cdp.call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: height });
  const capture = async (name) => {
    const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(image.data, "base64"), { mode: 0o600 });
  };
  const click = (selector) => evaluate(cdp, "(() => { const node=document.querySelector(" + JSON.stringify(selector) + "); if (!(node instanceof HTMLElement)) return false; node.focus({preventScroll:true}); node.click(); return true; })()");

  await resize(1440);
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=%2Fadmin%2Fautomation-conversion" });
  await waitFor(cdp, 'Boolean(document.querySelector(\'form[action="/login"] input[name="login_csrf_token"]\'))', "login shell did not render");
  await evaluate(cdp, "(() => { document.querySelector('input[name=\"username\"]').value=" + JSON.stringify(username) + "; document.querySelector('input[name=\"password\"]').value=" + JSON.stringify(password) + "; document.querySelector('form[action=\"/login\"]').requestSubmit(); return true; })()");
  await waitFor(cdp, "document.querySelectorAll('#coreOperationsRoot form').length===5", "core operations did not load");
  await evaluate(cdp,"document.querySelector('#coreOperationsRoot').closest('details').open=true");
  await evaluate(cdp, `(() => {
    const form=document.querySelector('#coreOperationsRoot form');const fields=form.querySelectorAll('input,textarea');
    fields[0].value='测试核心产品';fields[1].value='适合创业初期客户';fields[2].value='结合问卷推荐';
    form.querySelector('select').value=${JSON.stringify(packageID)};
    form.querySelector('button').click();
  })()`);
  await waitFor(cdp,"document.querySelector('#coreOperationsRoot form select')?.disabled===true", "core product was not saved/read back");
  await evaluate(cdp,`(() => {const areas=document.querySelectorAll('#coreOperationsRoot textarea');areas[areas.length-1].value='只能从启用产品中选择一个，信息不足暂不分配';[...document.querySelectorAll('#coreOperationsRoot button')].find(x=>x.textContent==='发布提示词').click()})()`);
  await waitFor(cdp,"document.querySelector('#coreOperationsRoot').textContent.includes('当前发布版本 1')", "prompt did not publish");
  for (const width of [1440,390]) {
    await resize(width);
    await evaluate(cdp,"document.querySelector('#coreOperationsRoot').closest('details').open=true");
    const layout=await evaluate(cdp,"(() => {const n=document.querySelector('#coreOperationsRoot input');const s=getComputedStyle(n);return {height:n.getBoundingClientRect().height,border:s.borderTopStyle}})()");
    if(layout.height<30 || layout.border==='none')throw new Error('core form style missing');
    await capture('core-operations-'+width);
  }
  await cdp.call("Page.navigate",{url:baseURL+'/admin/automation-conversion/packages/'+packageID});
  await waitFor(cdp,"document.querySelector('#templateVersionBadge')?.textContent==='AI 推荐'", "bound package did not show AI ownership");
  if(exceptions.length)throw new Error('runtime exceptions: '+JSON.stringify(exceptions));
  console.log("core_operations_chromium: PASS screenshots="+screenshotDirectory);
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  const browserExited = await stopBrowser(browser);
  if (!browserExited && !failed) throw new Error('Chromium did not exit before audience-confirmation profile cleanup');
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error('Chromium profile cleanup did not complete');
}
