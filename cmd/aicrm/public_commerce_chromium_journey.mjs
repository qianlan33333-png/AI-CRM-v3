import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = String(process.env.AICRM_PUBLIC_COMMERCE_TEST_URL || "").replace(/\/$/, "");
const screenshots = process.env.AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR;
const standardCode = process.env.AICRM_PUBLIC_COMMERCE_STANDARD_CODE;
const serviceCode = process.env.AICRM_PUBLIC_COMMERCE_SERVICE_CODE;
const unavailableServiceCode = process.env.AICRM_PUBLIC_COMMERCE_UNAVAILABLE_SERVICE_CODE;
if (!/^https:\/\/127\.0\.0\.1:\d+$/.test(baseURL) || !path.isAbsolute(screenshots || "") || !standardCode || !serviceCode || !unavailableServiceCode) {
  throw new Error("public commerce Chromium journey environment is incomplete");
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const browserBinary = () => {
  for (const candidate of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) {
    if ((candidate.includes("/") ? spawnSync(candidate, ["--version"], { stdio: "ignore" }) : spawnSync("which", [candidate], { stdio: "ignore" })).status === 0) return candidate;
  }
  throw new Error("Chromium is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.next = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", event => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        message.error ? pending.reject(new Error(`CDP ${message.error.code}`)) : pending.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }

  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.next;
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 8000);
      this.pending.set(id, {
        resolve: value => { clearTimeout(timer); resolve(value); },
        reject,
      });
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
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium DevTools did not start");
}

async function evaluate(cdp, expression) {
  const response = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (response.exceptionDetails) throw new Error(`page evaluation failed: ${response.exceptionDetails.exception?.description || response.exceptionDetails.text || "unknown"}`);
  return response.result?.value;
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
  await Promise.race([new Promise(resolve => browser.once("exit", resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) browser.kill("SIGKILL");
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-public-commerce-chromium-"));
let browser;
let cdp;
try {
  await fs.mkdir(screenshots, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--ignore-certificate-errors", "--allow-insecure-localhost", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "about:blank"], { stdio: "ignore" });
  const target = await (await fetch(`${await debuggingAddress(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("CDP connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");

  const exceptions = [];
  const publicAssets = new Map();
  cdp.on("Runtime.exceptionThrown", event => {
    if (exceptions.length < 8) exceptions.push(event.exceptionDetails?.text || "runtime exception");
  });
  cdp.on("Network.responseReceived", event => {
    try {
      const resource = new URL(String(event.response?.url || ""));
      if (resource.origin === baseURL && resource.pathname.startsWith("/product-public-assets/")) publicAssets.set(resource.pathname, Number(event.response?.status || 0));
    } catch (_) {}
  });

  const resize = width => cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 860, deviceScaleFactor: 1, mobile: true, screenWidth: width, screenHeight: 860 });
  const capture = async filename => {
    const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshots, filename), Buffer.from(image.data, "base64"), { mode: 0o600 });
  };

  const visit = async ({ pagePath, kind, route, width, file, unavailable = false }) => {
    await resize(width);
    await cdp.call("Page.navigate", { url: baseURL + pagePath });
    await waitFor(cdp, "document.querySelector('main[data-v3-public-commerce]')?.dataset.publicCommerceMounted === 'true'", `presentation Host did not mount ${pagePath}`);
    await waitFor(cdp, "Array.from(document.styleSheets).some(sheet => String(sheet.href || '').includes('/product-public-assets/'))", `public stylesheet did not load ${pagePath}`);
    const state = await evaluate(cdp, "(() => { const root=document.querySelector('main[data-v3-public-commerce]'); return {kind:root?.dataset.productKind,route:root?.dataset.publicCommerceRoute,view:root?.dataset.publicCommerceView,overflow:document.documentElement.scrollWidth>innerWidth+1,host:Array.from(document.scripts).some(script=>String(script.src||'').includes('/product-public-assets/')),unavailableButton:Boolean(document.querySelector('#servicePeriodPayButton:disabled'))}; })()");
    assert.equal(state.kind, kind, `${pagePath} product kind`);
    assert.equal(state.route, route, `${pagePath} route kind`);
    assert.equal(state.host, true, `${pagePath} Host resource`);
    assert.equal(state.overflow, false, `${pagePath} ${width}px layout overflow`);
    // The frozen page owns the unavailable fact through its disabled action;
    // do not infer state from user-visible text or depend on an incidental CSS
    // class that the frozen state renderer does not preserve after load.
    if (unavailable) assert.equal(state.unavailableButton, true, `${pagePath} unavailable Owner state`);
    else assert.notEqual(state.view, undefined, `${pagePath} structural Owner state`);
    await capture(file);
  };

  await visit({ pagePath: `/p/${encodeURIComponent(standardCode)}`, kind: "standard", route: "detail", width: 375, file: "public-standard-detail-375.png" });
  await visit({ pagePath: `/pay/${encodeURIComponent(standardCode)}`, kind: "standard", route: "payment", width: 390, file: "public-standard-payment-390.png" });
  // Service-period's existing Owner selects the checkout presentation when a
  // product has no detail-media records. This fixture intentionally has no
  // synthetic media, so verify its real route output instead of treating the
  // URL alone as proof of a detail section.
  await visit({ pagePath: `/s/${encodeURIComponent(serviceCode)}`, kind: "service_period", route: "payment", width: 430, file: "public-service-available-430.png" });
  await visit({ pagePath: `/s/${encodeURIComponent(serviceCode)}/pay`, kind: "service_period", route: "payment", width: 390, file: "public-service-available-payment-390.png" });
  await visit({ pagePath: `/s/${encodeURIComponent(unavailableServiceCode)}`, kind: "service_period", route: "service-period-state", width: 375, file: "public-service-unavailable-detail-375.png", unavailable: true });
  await visit({ pagePath: `/s/${encodeURIComponent(unavailableServiceCode)}/pay`, kind: "service_period", route: "service-period-state", width: 430, file: "public-service-unavailable-payment-430.png", unavailable: true });

  const successfulAssets = [...publicAssets.entries()].filter(([, status]) => status === 200).map(([resource]) => resource);
  if (successfulAssets.length < 3 || !successfulAssets.some(resource => resource.endsWith(".css")) || !successfulAssets.some(resource => resource.endsWith(".js")) || !successfulAssets.some(resource => resource.includes("/chunks/"))) {
    throw new Error(`anonymous public asset closure did not load CSS, Host, and module chunk: ${JSON.stringify(Object.fromEntries(publicAssets))}`);
  }
  if (exceptions.length) throw new Error(`public commerce browser exceptions=${JSON.stringify(exceptions)}`);
  console.log(`public_commerce_chromium: PASS screenshots=${screenshots} assets=${successfulAssets.length}`);
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  await fs.rm(profile, { recursive: true, force: true }).catch(() => {});
}
