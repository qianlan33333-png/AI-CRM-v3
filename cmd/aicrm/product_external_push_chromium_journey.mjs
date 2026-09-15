import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_PRODUCT_PUSH_TEST_URL;
const username = process.env.AICRM_PRODUCT_PUSH_TEST_USERNAME;
const password = process.env.AICRM_PRODUCT_PUSH_TEST_PASSWORD;
const productID = process.env.AICRM_PRODUCT_PUSH_TEST_PRODUCT_ID;
const serviceProductID = process.env.AICRM_PRODUCT_PUSH_TEST_SERVICE_PRODUCT_ID;
const historicalOrderReference = process.env.AICRM_PRODUCT_PUSH_TEST_HISTORICAL_ORDER;
const exactParams = process.env.AICRM_PRODUCT_PUSH_TEST_PARAMS;
// The fixture input intentionally has a different key order. Go persists an
// object and returns its canonical map text. Keep this as literal JSON rather
// than parsing it in JavaScript: JSON.parse would round the 64-bit integer.
const canonicalParams = '{"count":9007199254740993,"flag":false,"nested":[{"inner":9007199254740993}]}';
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "") || !/^[A-Za-z0-9._:-]{1,200}$/.test(historicalOrderReference || "") || !exactParams) {
  throw new Error("product external push Chromium journey requires HTTPS URL, credentials, ordinary and service-period product ids, historical order, and JSON");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
// Do not embed a regular expression in a Runtime.evaluate template string:
// JavaScript string escaping would turn `\s` into a literal `s`. Cookie order
// is arbitrary, so split exact names instead of relying on a position-specific
// substring.
const cookieNamePresent = (cookie, name) => String(cookie || '').split(';').some((part) => part.trim().startsWith(name + '='));
if (!cookieNamePresent('aicrm_admin_csrf=test; aicrm_csrf=test', 'aicrm_admin_csrf') ||
  !cookieNamePresent('aicrm_admin_csrf=test; aicrm_csrf=test', 'aicrm_csrf') ||
  cookieNamePresent('aicrm_admin_csrf=test; aicrm_csrf=test', 'csrf_token')) {
  throw new Error('cookie name fixture is invalid');
}
const browserBinary = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    if (candidate.includes("/")) {
      try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {}
    } else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) {
      return candidate;
    }
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
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error("CDP request failed"));
        else pending.resolve(message.result || {});
        return;
      }
      if (!message.method) return;
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
    return () => this.events.set(method, (this.events.get(method) || []).filter((item) => item !== listener));
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    this.events.clear();
    this.socket.close();
  }
}

const waitForPort = async (profile) => {
  const activePort = path.join(profile, "DevToolsActivePort");
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(activePort, "utf8")).split("\n")[0];
      if (/^\d+$/.test(port)) return "http://127.0.0.1:" + port;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
};

const evaluate = async (cdp, expression) => {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
};
const waitFor = async (cdp, expression, message) => {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
};

const waitForBrowserExit = async (child, timeoutMilliseconds) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    child.once("exit", () => { clearTimeout(timer); resolve(true); });
  });
};
const removeProfile = async (profile) => {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!error || !["ENOTEMPTY", "EBUSY", "EPERM"].includes(error.code)) return false;
      await delay(100);
    }
  }
  return false;
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-product-push-chromium-"));
let browser;
let cdp;
let journeyFailed = false;
class DevToolsUnavailable extends Error {}
try {
  browser = spawn(browserBinary(), [
    "--headless=new", "--no-sandbox", "--remote-debugging-port=0", "--user-data-dir=" + profile,
    "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--ignore-certificate-errors",
    "--allow-insecure-localhost", "about:blank",
  ], { stdio: ["ignore", "ignore", "ignore"] });
  let address;
  try {
    address = await waitForPort(profile);
  } catch (error) {
    if (process.platform === "darwin") throw new DevToolsUnavailable();
    throw error;
  }
  const created = await (await fetch(address + "/json/new?about:blank", { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  const runtimeExceptions = [];
  // Keep only route, method and status. A browser journey failure needs enough
  // evidence to distinguish Host wiring, session/CSRF and HTTP rejection, but
  // never serializes request bodies, cookies, bearer values or receiver data.
  const requests = new Map();
  const responses = [];
  cdp.on("Runtime.exceptionThrown", (params) => {
    const details = params.exceptionDetails || {};
    const name = String(details.exception?.className || details.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (runtimeExceptions.length < 8) runtimeExceptions.push(name);
  });
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const pathname = new URL(String(params.request?.url || "")).pathname;
      if (pathname.includes("products") || pathname.includes("productForm") || pathname.includes("orderDetail") || pathname.includes("external-push") || pathname.includes("service-period-products") || pathname.startsWith("/assets/")) {
        requests.set(params.requestId, { pathname, method: String(params.request?.method || "GET") });
      }
    } catch (_) {}
  });
  cdp.on("Network.responseReceived", (params) => {
    const request = requests.get(params.requestId);
    if (!request || responses.length >= 32) return;
    responses.push(`${request.method} ${request.pathname}:${Number(params.response?.status) || 0}`);
  });
  const browserSaveDiagnostic = async () => {
    const page = await evaluate(cdp, `(() => {
      const panel = document.querySelector('[data-external-push-configuration]');
      const save = document.querySelector('[data-external-push-configuration-save]');
      const toast = document.querySelector('#product-v3-toast');
      const status = panel?.querySelector('[data-external-push-configuration-status]')?.textContent || '';
      const hasCookie = (name) => String(document.cookie || '').split(';').some((part) => part.trim().startsWith(name + '='));
      const knownStatus = status === '正在读取配置…' ? 'configuration_loading'
        : /^配置版本 \d+$/.test(status) ? 'configuration_loaded'
        : status === '外推配置响应不完整' ? 'configuration_response_invalid'
        : /^外推请求失败（HTTP \d+）$/.test(status) ? 'configuration_http_error'
        : status ? 'configuration_status_other' : 'configuration_status_empty';
      const toastText = String(toast?.textContent || '');
      const knownToast = toastText === '外推配置尚未读取完成' ? 'configuration_not_loaded'
        : toastText === '外推业务参数已保存；未发送外部请求。' ? 'configuration_saved'
        : toastText ? 'toast_other' : 'toast_empty';
      return {
        path: location.pathname,
        status: knownStatus,
        toast: knownToast,
        saveDisabled: Boolean(save && save.disabled),
        adminCSRF: hasCookie('aicrm_admin_csrf'),
        compatCSRF: hasCookie('aicrm_csrf'),
        anchor: Boolean(document.querySelector(location.pathname.endsWith('/admin/wechat-pay/spProductForm.html') ? '#sp-push' : '#product-push')),
        hostPanel: Boolean(document.querySelector('#product-v3-external-push-test')),
        businessBinding: Boolean(document.querySelector(location.pathname.endsWith('/admin/wechat-pay/spProductForm.html') ? '#spfExternalPushEnabled' : '#pfExternalPushEnabled')),
        productHostAsset: Array.from(document.scripts).some((script) => String(script.src || '').includes('/product-assets/')),
        frozenAdminEntry: Array.from(document.scripts).some((script) => String(script.src || '').includes('/assets/')),
      };
    })()`);
    const routes = responses.join(',') || 'none';
    return `path=${page?.path || 'unknown'} status=${page?.status || 'none'} toast=${page?.toast || 'none'} save_disabled=${page?.saveDisabled === true} csrf_admin=${page?.adminCSRF === true} csrf_compat=${page?.compatCSRF === true} anchor=${page?.anchor === true} host_panel=${page?.hostPanel === true} binding=${page?.businessBinding === true} product_host_asset=${page?.productHostAsset === true} frozen_admin_entry=${page?.frozenAdminEntry === true} exceptions=${runtimeExceptions.join(',') || 'none'} responses=${routes}`;
  };

  const assertProductEditorHeader = async (kind, title, returnLabel) => {
    for (const width of [1280, 1440]) {
      await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
      const layout = await evaluate(cdp, `(() => {
        const topbar = document.querySelector('.admin-topbar');
        const actions = Array.from(topbar?.querySelectorAll('[data-page-header-actions="product-editor"] button') || []);
        const bodyTitle = Array.from(document.querySelectorAll('#stage h2')).find((node) => node.textContent?.trim() === ${JSON.stringify(title)});
        const bodyReturn = Array.from(document.querySelectorAll('#stage button')).some((button) => button.textContent?.trim() === ${JSON.stringify(returnLabel)});
        return {
          topbars: document.querySelectorAll('.admin-topbar').length,
          shellTitles: topbar?.querySelectorAll('.admin-page-title').length || 0,
          actions: actions.map((button) => button.textContent?.trim()),
          actionsFit: actions.every((button) => button.getBoundingClientRect().right <= window.innerWidth),
          bodyTitleHidden: bodyTitle instanceof HTMLElement && bodyTitle.hidden,
          bodyReturn,
          width: window.innerWidth,
        };
      })()`);
      if (!layout || layout.topbars !== 1 || layout.shellTitles !== 1 || layout.width !== width ||
        layout.actions.join('|') !== `${returnLabel}|保存当前维度` || !layout.actionsFit || !layout.bodyTitleHidden || layout.bodyReturn) {
        throw new Error(`${kind} editor header layout invalid at ${width}: ${JSON.stringify(layout)}`);
      }
    }
  };

  const productPath = "/admin/wechat-pay/productForm.html?id=" + productID;
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=" + encodeURIComponent(productPath) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, "(() => { document.querySelector('input[name=\"username\"]').value=" + JSON.stringify(username) + "; document.querySelector('input[name=\"password\"]').value=" + JSON.stringify(password) + "; document.querySelector('form[action=\"/login\"]').requestSubmit(); return true; })()");
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html'", "login did not reach frozen product form");

  const hostReady = "Boolean(document.querySelector('[data-external-push-configuration]')) && Boolean(document.querySelector('#product-v3-external-push-custom-params'))";
  try {
    await waitFor(cdp, hostReady, "product Host did not render");
  } catch (_) {
    throw new Error("product Host did not render " + await browserSaveDiagnostic());
  }
  if (!await evaluate(cdp, `(() => { const hasCookie = (name) => String(document.cookie || '').split(';').some((part) => part.trim().startsWith(name + '=')); return hasCookie('aicrm_admin_csrf') && hasCookie('aicrm_csrf'); })()`)) {
    throw new Error("product Host did not receive CSRF session bridge " + await browserSaveDiagnostic());
  }
  await assertProductEditorHeader('ordinary', '编辑普通商品', '返回商品管理');
  // The list Host owns the lifecycle buttons. Exercise the real browser
  // session, CSRF header and CAS endpoint once in each direction before the
  // form journey, leaving the seeded fixture enabled for its remaining steps.
  const productsPath = "/admin/products.html";
  await cdp.call("Page.navigate", { url: baseURL + productsPath });
  await waitFor(cdp, "location.pathname === '/admin/products.html' && Array.from(document.querySelectorAll('tbody tr')).some((row) => row.textContent.includes('browser-push-product') && Array.from(row.querySelectorAll('button')).some((button) => button.textContent.trim() === '停用'))", "product list lifecycle Host did not render the seeded enabled row");
  await evaluate(cdp, "(() => { const row=Array.from(document.querySelectorAll('tbody tr')).find((item)=>item.textContent.includes('browser-push-product')); Array.from(row.querySelectorAll('button')).find((button)=>button.textContent.trim()==='停用').click(); return true; })()");
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('商品已停用')", "product lifecycle disable did not complete through the Host");
  await cdp.call("Page.navigate", { url: baseURL + productsPath });
  await waitFor(cdp, "Array.from(document.querySelectorAll('tbody tr')).some((row) => row.textContent.includes('browser-push-product') && Array.from(row.querySelectorAll('button')).some((button) => button.textContent.trim() === '启用'))", "product list did not read back the disabled lifecycle");
  await evaluate(cdp, "(() => { const row=Array.from(document.querySelectorAll('tbody tr')).find((item)=>item.textContent.includes('browser-push-product')); Array.from(row.querySelectorAll('button')).find((button)=>button.textContent.trim()==='启用').click(); return true; })()");
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('商品已启用')", "product lifecycle enable did not complete through the Host");
  await cdp.call("Page.navigate", { url: baseURL + productPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html'", "product lifecycle return did not reach frozen product form");
  // Host mounting creates the editor before its configuration GET resolves.
  // Wait for the first revision rather than racing the closure that owns the
  // configuration snapshot used for CAS in the save handler.
  try {
    await waitFor(cdp, "document.querySelector('[data-external-push-configuration-status]')?.textContent === '配置版本 1'", "product configuration did not load");
  } catch (_) {
    throw new Error("product configuration did not load " + await browserSaveDiagnostic());
  }
  // Field-variable filtering belongs to the mounted V3 mapping editor. It
  // filters locally only after explicit Enter; preview/save remain unchanged.
  const productPushTabOpened = await evaluate(cdp, "(()=>{const tab=document.querySelector('a[href=\"#product-push\"]');const panel=document.querySelector('#product-push');if(!(tab instanceof HTMLAnchorElement)||!(panel instanceof HTMLElement))return false;tab.click();return true})()");
  if (!productPushTabOpened) throw new Error('product external-push tab was unavailable');
  await waitFor(cdp, "(()=>{const panel=document.querySelector('#product-push');const conversion=[...(panel?.querySelectorAll('button')||[])].find(item=>item.textContent?.trim()==='转换为字段映射');return Boolean(panel&&conversion&&panel.getClientRects().length&&getComputedStyle(panel).visibility!=='hidden')})()", "product external-push tab did not become visible before field-mapping conversion");
  const conversionOpened = await evaluate(cdp, "(()=>{const panel=document.querySelector('#product-push');const button=[...(panel?.querySelectorAll('button')||[])].find(item=>item.textContent?.trim()==='转换为字段映射');if(!button)return false;button.click();return true})()");
  if (!conversionOpened) throw new Error('product field-mapping conversion entry was unavailable');
  await waitFor(cdp, "Boolean(document.querySelector('[data-mapping-conversion]'))", "product field-mapping conversion preview did not open");
  await evaluate(cdp, "[...document.querySelectorAll('[data-mapping-conversion] button')].find(item=>item.textContent?.trim()==='确认转换').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[data-fm-rows] .fm-row'))", "product field-mapping editor did not mount");
  const mappingSearchCandidate = await evaluate(cdp, `(()=>{
    const row=document.querySelector('[data-fm-rows] .fm-row');
    const source=row?.querySelectorAll('select')[0];
    if (!(source instanceof HTMLSelectElement)) return null;
    source.value='variable'; source.dispatchEvent(new Event('change',{bubbles:true}));
    const picker=row.querySelector('button.fm-variable'); picker?.click();
    const input=row.querySelector('[data-field-mapping-variable-search]');
    if (!(input instanceof HTMLInputElement)) return null;
    const choices=()=>row.querySelectorAll('.fm-choice').length;
    const before=choices(); input.focus(); const focused=document.activeElement===input; input.value='付款';
    input.dispatchEvent(new Event('input',{bubbles:true,cancelable:true}));
    input.dispatchEvent(new FocusEvent('blur',{bubbles:true}));
    input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));
    input.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));
    const candidate=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter',isComposing:true});
    Object.defineProperty(candidate,'keyCode',{value:229}); input.dispatchEvent(candidate);
    return {before,after:choices(),focused,prevented:candidate.defaultPrevented};
  })()`);
  if (!mappingSearchCandidate || !mappingSearchCandidate.focused || mappingSearchCandidate.prevented || mappingSearchCandidate.before !== 3 || mappingSearchCandidate.after !== 3) throw new Error('product field-mapping IME candidate altered variable choices or did not receive focus');
  await evaluate(cdp, "document.querySelector('[data-field-mapping-variable-search]').dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'})); true");
  await waitFor(cdp, "document.querySelectorAll('[data-fm-rows] .fm-choice').length===1 && document.querySelector('[data-fm-rows] .fm-choice')?.textContent.includes('付款人昵称')", "product field-mapping ordinary Enter did not filter variables");
  const mappingSearchFocus = await evaluate(cdp, "(()=>{const input=document.querySelector('[data-field-mapping-variable-search]');return Boolean(input&&document.activeElement===input&&input.value==='付款')})()");
  if (!mappingSearchFocus) throw new Error('product field-mapping Enter did not retain query focus');
  await cdp.call("Page.navigate", { url: baseURL + productPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html' && document.querySelector('[data-external-push-configuration-status]')?.textContent === '配置版本 1'", "product form did not reset after local mapping search proof");
  await evaluate(cdp, "(() => { document.querySelector('a[href=\"#product-push\"]')?.click(); const enabled=document.querySelector('#pfExternalPushEnabled'); const reference=document.querySelector('#pfExternalPushReference'); enabled.value='true'; enabled.dispatchEvent(new Event('change',{bubbles:true})); reference.value='browser-push-target'; reference.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('#product-v3-external-push-url').value='https://commerce-browser.invalid'; document.querySelector('#product-v3-external-push-type').value='member_open'; document.querySelector('#product-v3-external-push-day').value='30'; document.querySelector('#product-v3-external-push-frequency').value='1'; document.querySelector('#product-v3-external-push-expires-at-ts').value='2147483647'; document.querySelector('#product-v3-external-push-remark').value='browser preserves JSON'; document.querySelector('#product-v3-external-push-custom-params').value=" + JSON.stringify(exactParams) + "; (Array.from(document.querySelectorAll('button')).find(button=>button.textContent.trim()==='保存当前维度' && !button.closest('#product-push') && !button.closest('#sp-push')) || document.querySelector('[data-external-push-configuration-save]')).click(); return true; })()");
  try {
    await waitFor(cdp, "document.querySelector('[data-external-push-configuration-status]')?.dataset.configurationRevision === '2' && document.querySelector('[data-external-push-configuration-status]')?.textContent === '配置已保存'", "browser configuration save did not finish");
  } catch (_) {
    throw new Error("browser configuration save did not finish " + await browserSaveDiagnostic());
  }
  await waitFor(cdp, "document.querySelector('#product-v3-external-push-custom-params')?.value === " + JSON.stringify(exactParams), "browser save changed typed custom JSON before reload");

  await cdp.call("Page.navigate", { url: baseURL + productPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html' && " + hostReady + " && document.querySelector('#product-v3-external-push-custom-params')?.value === " + JSON.stringify(canonicalParams) + " && document.querySelector('#product-v3-external-push-expires-at-ts')?.value === '2147483647'", "reloaded product Host did not preserve exact JSON text or expiry");
  await evaluate(cdp, "document.querySelector('[data-external-push-test=\"run\"]').click(); true");
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('测试已受理，等待受控投递')", "synthetic test was not accepted through Product HTTP");
  const terminalTimeline = "document.querySelector('[data-external-push-timeline]')?.textContent.includes('结果未知，需按原投递 ID 对账')";
  for (let attempt = 0; attempt < 120; attempt += 1) {
    await evaluate(cdp, "document.querySelector('[data-external-push-test=\"refresh\"]')?.click(); true");
    if (await evaluate(cdp, terminalTimeline)) break;
    await delay(250);
  }
  if (!await evaluate(cdp, terminalTimeline)) throw new Error("manual refresh did not display the durable unknown terminal result");

  // The frozen service-period form has separate donor bindings and a separate
  // Host endpoint. Save and reload it through the outer application handler
  // too, so the ordinary form cannot mask a service-period adapter failure.
  const serviceProductPath = "/admin/wechat-pay/spProductForm.html?id=" + serviceProductID;
  await cdp.call("Page.navigate", { url: baseURL + serviceProductPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/spProductForm.html'", "navigation did not reach frozen service-period product form");
  const serviceHostReady = hostReady + " && Boolean(document.querySelector('#spfExternalPushEnabled'))";
  try {
    await waitFor(cdp, serviceHostReady, "service-period product Host did not render");
  } catch (_) {
    throw new Error("service-period product Host did not render " + await browserSaveDiagnostic());
  }
  await assertProductEditorHeader('service-period', '编辑周期商品', '返回周期商品管理');
  try {
    await waitFor(cdp, "document.querySelector('[data-external-push-configuration-status]')?.textContent === '配置版本 1'", "service-period product configuration did not load");
  } catch (_) {
    throw new Error("service-period product configuration did not load " + await browserSaveDiagnostic());
  }
  await evaluate(cdp, "(() => { document.querySelector('a[href=\"#sp-push\"]')?.click(); const enabled=document.querySelector('#spfExternalPushEnabled'); const reference=document.querySelector('#spfExternalPushReference'); enabled.value='true'; enabled.dispatchEvent(new Event('change',{bubbles:true})); reference.value='browser-push-target'; reference.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('#product-v3-external-push-url').value='https://commerce-browser.invalid'; document.querySelector('#product-v3-external-push-type').value='member_renew'; document.querySelector('#product-v3-external-push-day').value='30'; document.querySelector('#product-v3-external-push-frequency').value='1'; document.querySelector('#product-v3-external-push-expires-at-ts').value='2147483647'; document.querySelector('#product-v3-external-push-remark').value='service browser preserves JSON'; document.querySelector('#product-v3-external-push-custom-params').value=" + JSON.stringify(exactParams) + "; (Array.from(document.querySelectorAll('button')).find(button=>button.textContent.trim()==='保存当前维度' && !button.closest('#product-push') && !button.closest('#sp-push')) || document.querySelector('[data-external-push-configuration-save]')).click(); return true; })()");
  try {
    await waitFor(cdp, "document.querySelector('[data-external-push-configuration-status]')?.dataset.configurationRevision === '2' && document.querySelector('[data-external-push-configuration-status]')?.textContent === '配置已保存'", "service-period browser configuration save did not finish");
  } catch (_) {
    throw new Error("service-period browser configuration save did not finish " + await browserSaveDiagnostic());
  }
  await waitFor(cdp, "document.querySelector('#product-v3-external-push-custom-params')?.value === " + JSON.stringify(exactParams), "service-period browser save changed typed custom JSON before reload");
  await cdp.call("Page.navigate", { url: baseURL + serviceProductPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/spProductForm.html' && " + serviceHostReady + " && document.querySelector('#product-v3-external-push-custom-params')?.value === " + JSON.stringify(canonicalParams) + " && document.querySelector('#product-v3-external-push-expires-at-ts')?.value === '2147483647'", "reloaded service-period Host did not preserve exact JSON text or expiry");

  const historicalOrderPath = "/admin/orderDetail.html?id=" + encodeURIComponent(historicalOrderReference);
  await cdp.call("Page.navigate", { url: baseURL + historicalOrderPath });
  await waitFor(cdp, "location.pathname === '/admin/orderDetail.html' && document.body?.textContent?.includes('外部处理记录') && document.body?.textContent?.includes('历史记录：外推成功')", "outer order-detail route did not render its mapped historical delivery");
  const orderEffects = await evaluate(cdp, "fetch('/api/admin/wechat-pay/orders/" + historicalOrderReference + "/external-push-deliveries',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>({source:body?.items?.[0]?.source,delivery:body?.items?.[0]?.legacy_delivery_id,status:body?.items?.[0]?.status}))");
  if (orderEffects?.source !== "history" || orderEffects?.delivery !== "browser-history-delivery-1" || orderEffects?.status !== "succeeded") throw new Error("outer order delivery route did not return mapped frozen history");
  console.log("product_external_push_chromium: PASS");
} catch (error) {
  if (error instanceof DevToolsUnavailable) {
    console.log("product_external_push_chromium: SKIP_DEVTOOLS");
  } else {
    journeyFailed = true;
    throw error;
  }
} finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGTERM");
    if (!await waitForBrowserExit(browser, 3000) && browser.exitCode === null && browser.signalCode === null) {
      browser.kill("SIGKILL");
      await waitForBrowserExit(browser, 1000);
    }
  }
  const removed = await removeProfile(profile);
  if (!removed && !journeyFailed) throw new Error("Chromium test profile cleanup did not complete");
}
