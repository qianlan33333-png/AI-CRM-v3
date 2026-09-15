import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_COMPONENT_STATES_TEST_URL;
const username = process.env.AICRM_COMPONENT_STATES_TEST_USERNAME;
const password = process.env.AICRM_COMPONENT_STATES_TEST_PASSWORD;
const screenshotDirectory = process.env.AICRM_COMPONENT_STATES_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !path.isAbsolute(screenshotDirectory || "")) {
  throw new Error("component states Chromium journey requires HTTPS URL, credentials, and an absolute screenshot directory");
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
    for (const current of this.pending.values()) current.reject(new Error("CDP closed"));
    this.pending.clear();
    this.socket.close();
  }
}

async function debuggingAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^[0-9]+$/.test(value)) return "http://127.0.0.1:" + value;
    } catch (_) {}
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

async function stopBrowser(browser) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return;
  browser.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => browser.once("exit", resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGKILL");
    await Promise.race([new Promise((resolve) => browser.once("exit", resolve)), delay(1000)]);
  }
}

async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false;
      await delay(100);
    }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-component-states-chromium-"));
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
  const exceptions = [];
  const apiRequests = [];
  cdp.on("Runtime.exceptionThrown", (params) => {
    const kind = String(params.exceptionDetails?.exception?.className || params.exceptionDetails?.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (exceptions.length < 8) exceptions.push(kind);
  });
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const request = new URL(String(params.request?.url || ""));
      if (request.origin === new URL(baseURL).origin && request.pathname.startsWith("/api/")) apiRequests.push(request.pathname);
    } catch (_) {}
  });
  const capture = async (name) => {
    const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(image.data, "base64"), { mode: 0o600 });
  };
  const resize = (width, height = 900) => cdp.call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: height });
  await resize(1440);
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=%2Fadmin%2Fcomponent-states" });
  await waitFor(cdp, 'Boolean(document.querySelector(\'form[action="/login"] input[name="login_csrf_token"]\'))', "login shell did not render");
  await evaluate(cdp, "(() => { document.querySelector('input[name=\"username\"]').value=" + JSON.stringify(username) + "; document.querySelector('input[name=\"password\"]').value=" + JSON.stringify(password) + "; document.querySelector('form[action=\"/login\"]').requestSubmit(); return true; })()");
  await waitFor(cdp, 'Boolean(document.querySelector(\'[data-component-states-root][data-component-states-ready="true"]\'))', "authenticated component state Host did not mount");
  await waitFor(cdp, "document.fonts.ready", "component state fonts did not settle");
  const page = await evaluate(cdp, "(() => ({path:location.pathname,page:document.body.dataset.page,root:Boolean(document.querySelector('[data-component-states-root]')),tokens:Array.from(document.styleSheets).some(sheet=>String(sheet.href||'').includes('sharedVisualTokens')),host:Array.from(document.scripts).some(script=>String(script.src||'').includes('componentStatesHost'))}))()");
  if (page.path !== "/admin/component-states" || page.page !== "component-states" || !page.root || !page.tokens || !page.host) {
    throw new Error("authenticated component state route did not use its real resource closure");
  }

  for (const width of [1280, 1440, 360, 420]) {
    await resize(width);
    await evaluate(cdp, "new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
    const layout = await evaluate(cdp, "(() => { const root=document.querySelector('.component-states'); const grid=document.querySelector('.component-states__state-grid'); const intro=document.querySelector('.component-states__intro'); const actions=document.querySelector('.component-states__actions'); const columns=String(getComputedStyle(grid).gridTemplateColumns).trim().split(/\\s+/).filter(Boolean).length; const buttons=[...actions.querySelectorAll('button')].map(button=>button.getBoundingClientRect()); return {root:Boolean(root),cards:grid?.children.length||0,columns,overflow:document.documentElement.scrollWidth>innerWidth+1,introWidth:intro?.getBoundingClientRect().width||0,buttonWidths:buttons.map(box=>Math.round(box.width)),viewport:innerWidth}; })()");
    const expectedColumns = width <= 600 ? 1 : 4;
    if (!layout.root || layout.cards !== 7 || layout.columns !== expectedColumns || layout.overflow || layout.introWidth < 100 || layout.buttonWidths.some((value) => value < 60)) {
      throw new Error("component state responsive layout invalid at " + width + ": " + JSON.stringify(layout));
    }
    await capture("component-states-" + width);
  }

  await resize(1440);
  const click = (selector) => evaluate(cdp, "(() => { const node=document.querySelector(" + JSON.stringify(selector) + "); if (!(node instanceof HTMLElement)) return false; node.focus({preventScroll:true}); node.click(); return true; })()");
  if (!await click('[data-component-states-mode="error"]') || !await click('[data-component-states-group-open]')) throw new Error("group error controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"group\"]')?.textContent.includes('群聊目录暂时不可用')", "group error mode did not expose a real loader failure");
  await capture("component-states-error-retry");
  if (!await click('[data-v3-selection-session="group"] [data-v3-group-reload]')) throw new Error("group retry control was unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"group\"]')?.textContent.includes('北区新品体验群')", "group retry did not use the second local success result");
  if (!await click('[data-v3-selection-session="group"] [data-v3-group-key]') || !await click('[data-v3-selection-session="group"] [data-v3-group-confirm]')) throw new Error("group local commit controls were unavailable");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"group\"]')", "group dialog did not close after local confirmation");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-group-open]')")) throw new Error("group confirmation did not return focus to the mounted trigger");

  if (!await click('[data-component-states-mode="forbidden"]') || !await click('[data-component-states-material-open]')) throw new Error("material forbidden controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"material\"]')?.textContent.includes('目录权限已收回')", "403 material mode did not remain explicit");
  const forbidden = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"material\"]'); return {selected:mask?.textContent.includes('秋日活动封面'),disabled:mask?.querySelector('[data-v3-picker-confirm]')?.disabled}; })()");
  if (!forbidden.selected || !forbidden.disabled) throw new Error("403 material mode did not preserve the draft and lock confirmation");
  await capture("component-states-forbidden");
  if (!await click('[data-v3-selection-session="material"] [data-v3-picker-cancel]')) throw new Error("material cancel was unavailable");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-material-open]')")) throw new Error("material cancel did not return focus to the mounted trigger");

  if (!await click('[data-component-states-form-open]')) throw new Error("form demo trigger was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"component-states\"] [data-component-states-form-textarea]'))", "form focus demo did not open");
  const form = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"component-states\"]'); const search=mask?.querySelector('[data-component-states-ime-input]'); const candidate=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',isComposing:true}); search?.dispatchEvent(candidate); return {select:Boolean(mask?.querySelector('[data-component-states-form-select]')),textarea:Boolean(mask?.querySelector('[data-component-states-form-textarea]')),editable:Boolean(mask?.querySelector('[data-component-states-form-editable]')),candidatePrevented:candidate.defaultPrevented}; })()");
  if (!form.select || !form.textarea || !form.editable || form.candidatePrevented) throw new Error("form demo did not preserve IME or shared focus controls");
  await evaluate(cdp, "new Promise((resolve) => setTimeout(resolve, 0))");
  const normalEnter = await evaluate(cdp, "(() => { const search=document.querySelector('[data-component-states-ime-input]'); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter'}); search.dispatchEvent(event); return event.defaultPrevented; })()");
  if (!normalEnter) throw new Error("ordinary form search Enter did not use the local SelectionSession loader");
  if (!await click('[data-v3-selection-session="component-states"] [data-component-states-choice]') || !await click('[data-v3-selection-session="component-states"] [data-component-states-confirm]')) throw new Error("form local commit controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"component-states\"]')?.textContent.includes('已提交本地示例会话')", "form confirmation did not commit the local SelectionSession");
  await capture("component-states-form");
  if (!await click('[data-v3-selection-session="component-states"] [data-component-states-close]')) throw new Error("form cancel was unavailable");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-form-open]')")) throw new Error("form cancel did not return focus to the mounted trigger");

  if (apiRequests.length || exceptions.length) throw new Error("local component demo issued API requests or exceptions: " + JSON.stringify({ apiRequests, exceptions }));
  console.log("component_states_chromium: PASS screenshots=" + screenshotDirectory);
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium profile cleanup did not complete");
}
