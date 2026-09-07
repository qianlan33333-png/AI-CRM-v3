import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_ADMIN_LAYOUT_TEST_URL;
const username = process.env.AICRM_ADMIN_LAYOUT_TEST_USERNAME;
const password = process.env.AICRM_ADMIN_LAYOUT_TEST_PASSWORD;
const productID = process.env.AICRM_ADMIN_LAYOUT_TEST_PRODUCT_ID;
const serviceProductID = process.env.AICRM_ADMIN_LAYOUT_TEST_SERVICE_PRODUCT_ID;
const historicalOrderReference = process.env.AICRM_ADMIN_LAYOUT_TEST_HISTORICAL_ORDER;
const screenshotDirectory = process.env.AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "") || !/^[A-Za-z0-9._:-]{1,200}$/.test(historicalOrderReference || "") || !screenshotDirectory) {
  throw new Error("admin layout Chromium journey requires HTTPS URL, test login, product ids, and screenshot directory");
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const chromium = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/") ? spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0 : spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
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
    socket.addEventListener("message", event => {
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
    return () => this.events.set(method, (this.events.get(method) || []).filter(value => value !== listener));
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    this.events.clear();
    this.socket.close();
  }
}

const waitForPort = async profile => {
  const active = path.join(profile, "DevToolsActivePort");
  for (let attempt = 0; attempt < 180; attempt += 1) {
    try {
      const port = String(await fs.readFile(active, "utf8")).split("\n")[0];
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
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
};
const waitForExit = async (child, milliseconds) => !child || child.exitCode !== null || child.signalCode !== null || new Promise(resolve => {
  const timer = setTimeout(() => resolve(false), milliseconds);
  child.once("exit", () => { clearTimeout(timer); resolve(true); });
});
const removeDirectory = async directory => {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try { await fs.rm(directory, { recursive: true, force: true }); return; } catch (_) { await delay(50); }
  }
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-admin-layout-chromium-"));
let child;
let cdp;
let failed = false;
const requests = new Map();
const responses = [];
const runtimeExceptions = [];
try {
  child = spawn(chromium(), [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost",
    "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank",
  ], { stdio: ["ignore", "ignore", "ignore"] });
  const address = await waitForPort(profile);
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
  cdp.on("Runtime.exceptionThrown", params => {
    const value = String(params.exceptionDetails?.exception?.className || params.exceptionDetails?.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (runtimeExceptions.length < 8) runtimeExceptions.push(value);
  });
  cdp.on("Network.requestWillBeSent", params => {
    try {
      const url = new URL(String(params.request?.url || ""));
      if (url.origin === baseURL) requests.set(params.requestId, { method: String(params.request?.method || "GET"), path: url.pathname });
    } catch (_) {}
  });
  cdp.on("Network.responseReceived", params => {
    const request = requests.get(params.requestId);
    if (request && responses.length < 80) responses.push(`${request.method} ${request.path}:${Number(params.response?.status) || 0}`);
  });

  const capture = async name => {
    const result = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(result.data, "base64"), { mode: 0o600 });
  };
  const currentLayout = titleSelector => evaluate(cdp, `(() => {
    const isVisible = node => { if (!node) return false; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
    const box = node => { if (!node) return null; const value=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:value.left,top:value.top,right:value.right,bottom:value.bottom,width:value.width,height:value.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop,display:style.display}; };
    const stage=document.querySelector('#stage');
    const workspace=stage ? Array.from(stage.children).find(node => !['STYLE','SCRIPT','TEMPLATE'].includes(node.tagName) && isVisible(node)) || null : null;
    const selector=${JSON.stringify(titleSelector || 'h1,h2,[role="heading"],[class*="toolbar"],[class*="header"],[class*="head"],[class*="title"]')};
    const title=workspace ? Array.from(workspace.querySelectorAll(selector)).find(node => isVisible(node) && String(node.textContent || '').trim().length > 0) || null : null;
    const titleLineage=[]; for (let current=title; current && current !== stage; current=current.parentElement) titleLineage.push(current);
    const alignedTitleAncestors=stage ? titleLineage.filter(node => { const value=node.getBoundingClientRect(); const stageBox=stage.getBoundingClientRect(); return Math.abs(value.left-stageBox.left) <= 1 && Math.abs(value.top-stageBox.top) <= 1 && value.width >= Math.min(220, stageBox.width * 0.4); }) : [];
    const innerBar=alignedTitleAncestors.find(node => node !== workspace) || alignedTitleAncestors[0] || null;
    const titleCount=stage ? Array.from(stage.querySelectorAll('h1')).filter(isVisible).filter(node => node.getBoundingClientRect().top < stage.getBoundingClientRect().top + 180).length : 0;
    return {sidebar:box(document.querySelector('.admin-sidebar')),main:box(document.querySelector('.admin-main-wrap')),topbar:box(document.querySelector('.admin-topbar')),stage:box(stage),workspace:box(workspace),innerBar:box(innerBar),title:box(title),titleCount,headers:document.querySelectorAll('header.admin-topbar').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,ready:document.readyState};
  })()`);
  const assertLayout = async (kind, label, titleSelector) => {
    const layout = await currentLayout(titleSelector);
    if (!layout.sidebar || !layout.main || layout.overflow || Math.abs(layout.sidebar.right - layout.main.left) > 1) throw new Error(`${label} shell geometry invalid`);
    if (kind === "standard") {
      if (!layout.topbar || layout.headers !== 1 || Math.abs(layout.topbar.left - layout.main.left) > 1 || Math.abs(layout.topbar.top) > 1 || layout.topbar.height < 48 || !layout.stage || layout.stage.top + 1 < layout.topbar.bottom) throw new Error(`${label} standard topbar geometry invalid`);
      return;
    }
    if (layout.headers !== 0 || !layout.stage || !layout.workspace || !layout.innerBar || !layout.title || layout.titleCount > 1 || Math.abs(layout.stage.left - layout.main.left) > 1 || Math.abs(layout.stage.top - layout.main.top) > 1 || Math.abs(layout.workspace.left - layout.stage.left) > 1 || Math.abs(layout.workspace.top - layout.stage.top) > 1 || Math.abs(layout.innerBar.left - layout.stage.left) > 1 || Math.abs(layout.innerBar.top - layout.stage.top) > 1 || Math.abs(layout.innerBar.right - layout.main.right) > 1 || layout.stage.paddingLeft !== "0px" || layout.stage.paddingTop !== "0px") throw new Error(`${label} embedded workspace/header geometry invalid`);
  };
  const clickNavigation = async (pathname, label) => {
    const destination = new URL(pathname, baseURL);
    const found = await evaluate(cdp, `(() => [...document.querySelectorAll('.admin-nav-link[href]')].some(node => { const target=new URL(node.href, location.href); return target.pathname === ${JSON.stringify(destination.pathname)} && target.search === ${JSON.stringify(destination.search)}; }))()`);
    if (!found) throw new Error(label + " menu link is absent or points to a fallback route");
    await evaluate(cdp, `(() => { const node=[...document.querySelectorAll('.admin-nav-link[href]')].find(value => { const target=new URL(value.href, location.href); return target.pathname === ${JSON.stringify(destination.pathname)} && target.search === ${JSON.stringify(destination.search)}; }); node.click(); return true; })()`);
  };
  const navigate = async (pathname, ready, label, kind, titleSelector, screenshot = false, fromMenu = false, finalPath = pathname) => {
    if (fromMenu) await clickNavigation(pathname, label);
    else await cdp.call("Page.navigate", { url: baseURL + pathname });
    await waitFor(cdp, `location.pathname === ${JSON.stringify(finalPath.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
    await waitFor(cdp, ready + " && Boolean((() => { const stage=document.querySelector('#stage'); if (!stage) return false; const visible=node => { const rect=node.getBoundingClientRect(), style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; }; const root=Array.from(stage.children).find(node => !['STYLE','SCRIPT','TEMPLATE'].includes(node.tagName) && visible(node)); return Boolean(root && Array.from(root.querySelectorAll(" + JSON.stringify(titleSelector) + ")).some(node => visible(node) && String(node.textContent || '').trim().length > 0)); })())", label + " Host did not render a visible workspace title");
    await assertLayout(kind, label, titleSelector);
    if (screenshot) await capture(label);
  };

  const embeddedTitle = 'h1,h2,[role="heading"],[class*="toolbar"],[class*="header"],[class*="head"],[class*="title"]';
  const navigateStandard = async (pathname, ready, label, screenshot = false, fromMenu = false) => {
    if (fromMenu) await clickNavigation(pathname, label);
    else await cdp.call("Page.navigate", { url: baseURL + pathname });
    await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
    await waitFor(cdp, ready, label + " Host did not become ready");
    await assertLayout("standard", label, embeddedTitle);
    if (screenshot) await capture(label);
  };
  const assertStaticOpenLayout = async label => {
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height}; };
      return {side:box('.side'),stage:box('#stage'),root:box('[data-open-platform-host="v1"]'),header:box('.open-platform-header'),headers:document.querySelectorAll('.open-platform-header').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
    })()`);
    if (!layout.side || !layout.stage || !layout.root || !layout.header || layout.headers !== 1 || layout.overflow || Math.abs(layout.side.right-layout.stage.left) > 1 || Math.abs(layout.stage.top) > 1 || Math.abs(layout.root.left-layout.stage.left) > 1 || Math.abs(layout.root.top-layout.stage.top) > 1 || Math.abs(layout.header.left-layout.stage.left) > 1 || Math.abs(layout.header.top-layout.stage.top) > 1 || Math.abs(layout.header.right-layout.stage.right) > 1 || layout.header.height < 48) throw new Error(label + " static topbar/sidebar geometry invalid");
  };
  const navigateStaticHost = async (pathname, ready, label) => {
    await cdp.call("Page.navigate", { url: baseURL + pathname });
    await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
    await waitFor(cdp, ready, label + " V3 Host did not become ready");
    await assertStaticOpenLayout(label);
  };

  const assertRuntimeConfigLayout = async label => {
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const root=document.querySelector('[data-runtime-release-host]');
      const card=root?.querySelector('.admin-card');
      const title=card ? [...card.querySelectorAll('h2')].find(node => String(node.textContent || '').trim().length > 0) : null;
      return {sidebar:box('.admin-sidebar'),main:box('.admin-main-wrap'),root:box('[data-runtime-release-host]'),card:box('[data-runtime-release-host] .admin-card'),title:box('[data-runtime-release-host] .admin-card h2'),headers:document.querySelectorAll('header.admin-topbar').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,titleText:String(title?.textContent || '').trim()};
    })()`);
    if (!layout.sidebar || !layout.main || !layout.root || !layout.card || !layout.title || !layout.titleText || layout.headers !== 0 || layout.overflow || Math.abs(layout.sidebar.right-layout.main.left) > 1 || Math.abs(layout.root.left-layout.main.left) > 1 || Math.abs(layout.root.top-layout.main.top) > 1 || Math.abs(layout.root.right-layout.main.right) > 1 || layout.card.top + 1 < layout.root.top || layout.card.left + 1 < layout.root.left) throw new Error(label + " nested Host geometry invalid");
  };
  const navigateRuntimeConfig = async () => {
    await cdp.call("Page.navigate", { url: baseURL + "/admin/config/releases" });
    await waitFor(cdp, "location.pathname === '/admin/config/releases' && document.readyState !== 'loading'", "runtime config did not navigate");
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-host] .admin-card h2')) && document.body?.textContent?.includes('当前运行时配置')", "runtime config Host did not become ready");
    await assertRuntimeConfigLayout("runtime-config");
    await capture("runtime-config");
  };

  const initial = "/admin/automation-conversion";
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=" + encodeURIComponent(initial) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/automation-conversion'", "login did not establish the Access session");
  await waitFor(cdp, "Boolean(document.querySelector('.admin-topbar')) && Boolean(document.querySelector('.aud-layout'))", "automation shell did not load");
  await assertLayout("standard", "automation", embeddedTitle);
  await capture("automation");

  // The matrix follows every actual item in ADMIN_NAV_GROUPS.  The embedded
  // rows require a live workspace root and a visible donor/V3 page title in
  // addition to the shell geometry; an empty Host cannot satisfy this check.
  await navigate("/admin/operation-cycles", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "cycles", "embedded", embeddedTitle, true, true);
  await navigate("/admin/automation-conversion/group-ops/ui", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "groupops", "embedded", embeddedTitle, true, true, "/admin/groupops.html");
  await navigate("/admin/channels", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "channels", "embedded", embeddedTitle, true, true);
  await navigate("/admin/cloud-orchestrator/plans", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "ai", "embedded", embeddedTitle, true, true);
  await navigateStandard("/admin/customers", "Boolean(document.querySelector('[data-customer-directory-root]'))", "customers", true, true);
  await navigate("/admin/hxc-dashboard", "Boolean(document.querySelector('#hxcRefresh')) && Boolean(document.querySelector('.sec-funnel'))", "hxc", "standard", ".sec-funnel .page-head", true, true);
  await navigate("/admin/questionnaires", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "questionnaires", "embedded", embeddedTitle, true, true);
  await navigate("/admin/radar-links", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "radar", "embedded", embeddedTitle, true, true);
  await navigate("/admin/wecom-tags", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "tags", "embedded", embeddedTitle, true, true);

  await navigate("/admin/orders", "Boolean(document.querySelector('.order-host-layout')) && Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "orders", "embedded", embeddedTitle, true, true);
  await navigate("/admin/wechat-pay/products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "products", "embedded", embeddedTitle, true, true);
  await navigate("/admin/service-period-products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "service-period-products", "embedded", embeddedTitle, true, true);
  await navigate("/admin/productForm.html?id=" + productID, "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('#pfExternalPushEnabled'))", "product", "embedded", embeddedTitle, true);
  await navigate("/admin/spProductForm.html?id=" + serviceProductID, "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('#spfExternalPushEnabled'))", "service-period-product", "embedded", embeddedTitle, true);
  await navigate("/admin/coupons", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "coupons", "embedded", embeddedTitle, true, true);

  await navigate("/admin/image-library", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "image-library", "embedded", embeddedTitle, true, true);
  await navigate("/admin/miniprogram-library", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "miniprogram-library", "embedded", embeddedTitle, true, true);
  await navigate("/admin/attachment-library", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "attachment-library", "embedded", embeddedTitle, true, true);

  await navigate("/admin/automation-agents", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "automation-agents", "embedded", embeddedTitle, true, true);
  await navigate("/admin/owner-migration", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('[data-owner-handoff-host]'))", "owner-migration", "embedded", embeddedTitle, true, true);
  await navigate("/admin/config", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "config", "embedded", embeddedTitle, true, true);
  await navigateRuntimeConfig();
  await navigateStandard("/admin/oneid", "Boolean(document.querySelector('[data-admin-oneid-root]'))", "oneid", true, true);
  await clickNavigation("/admin/api-docs", "api-docs");
  await waitFor(cdp, "location.pathname === '/admin/apidocs.html' && document.readyState !== 'loading'", "api-docs did not canonicalize to its V3 Host document");
  await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-host]') || document.querySelector('[class*=openPlatformHost]'))", "api-docs V3 Host did not become ready");
  await assertStaticOpenLayout("api-docs");
  await capture("api-docs");

  // Detail and frozen aliases remain on their business Host, including the
  // order history panel whose source mapping is independently seeded below.
  await navigate("/admin/orderDetail.html?id=" + encodeURIComponent(historicalOrderReference), "Boolean(document.querySelector('.order-host-layout')) && Boolean(document.body?.textContent?.includes('外推回执'))", "order-detail-history", "embedded", embeddedTitle, true);
  await navigate("/admin/campaigns.html?view=external-effects", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "external-effects", "embedded", embeddedTitle, true);
  const refreshResponsesBefore = responses.length;
  await cdp.call("Page.navigate", { url: baseURL + "/admin/hxc-dashboard" });
  await waitFor(cdp, "location.pathname === '/admin/hxc-dashboard' && Boolean(document.querySelector('#hxcRefresh'))", "HXC did not return for refresh");
  const hxcContent = await evaluate(cdp, `(() => { const heading=document.querySelector('.sec-funnel > .page-head > :first-child'); const refresh=document.querySelector('#hxcRefresh'); const grid=document.querySelector('.sec-funnel .grid-scroll'); return {headingHidden: Boolean(heading) && getComputedStyle(heading).display === 'none', refreshVisible: Boolean(refresh) && getComputedStyle(refresh).display !== 'none', scrollable: Boolean(grid) && grid.scrollHeight > grid.clientHeight}; })()`);
  if (!hxcContent?.headingHidden || !hxcContent?.refreshVisible || !hxcContent?.scrollable) throw new Error("HXC duplicate title/action/scroll layout invalid");
  await evaluate(cdp, "(() => { const grid=document.querySelector('.sec-funnel .grid-scroll'); grid.scrollTop=grid.scrollHeight; return grid.scrollTop > 0; })()");
  if (!await evaluate(cdp, "document.querySelector('.sec-funnel .grid-scroll')?.scrollTop > 0")) throw new Error("HXC grid did not retain a user scroll");
  await evaluate(cdp, "(() => { document.querySelector('#hxcRefresh').click(); return true; })()");
  await waitFor(cdp, "document.querySelector('#hxcRefresh')?.disabled === false && document.querySelector('#hxcRefresh')?.textContent === '立即刷新'", "HXC refresh action did not settle");
  if (!responses.slice(refreshResponsesBefore).includes("POST /api/admin/hxc-dashboard/refreshes:503")) throw new Error("HXC refresh did not reach the disabled runtime contract");
  if (runtimeExceptions.length) throw new Error("admin layout runtime exception=" + runtimeExceptions.join(","));
  console.log("admin_shell_layout_chromium: PASS routes=" + responses.filter(value => value.includes("/admin/") || value.includes("/api/admin/hxc-dashboard")).length);
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  if (child && child.exitCode === null && child.signalCode === null) child.kill("SIGTERM");
  await waitForExit(child, 3000);
  if (child && child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  await waitForExit(child, 1000);
  await removeDirectory(profile);
  if (failed) process.exitCode = 1;
}
