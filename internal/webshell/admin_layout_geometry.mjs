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
const radarID = process.env.AICRM_ADMIN_LAYOUT_TEST_RADAR_ID;
const screenshotDirectory = process.env.AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "") || !/^[1-9][0-9]*$/.test(radarID || "") || !/^[A-Za-z0-9._:-]{1,200}$/.test(historicalOrderReference || "") || !screenshotDirectory) {
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
let currentStep = "bootstrap";
const requests = new Map();
// Keep only same-origin admin requests and responses. Static assets can be
// numerous across the route matrix and must not evict a later business action
// such as the HXC refresh from the diagnostic window.
const requestEvents = [];
const responses = [];
const runtimeExceptions = [];
const appendBounded = (items, value, limit = 240) => {
  items.push(value);
  if (items.length > limit) items.splice(0, items.length - limit);
};
const waitForRecorded = async (items, predicate, message) => {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (items.some(predicate)) return;
    await delay(50);
  }
  throw new Error(message);
};
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
  // The production regression was observed in the desktop admin shell.  The
  // explicit viewport prevents Chrome's narrow responsive layout from hiding
  // the sidebar and turning a desktop geometry assertion into a false failure.
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  cdp.on("Runtime.exceptionThrown", params => {
    const value = String(params.exceptionDetails?.exception?.className || params.exceptionDetails?.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (runtimeExceptions.length < 8) runtimeExceptions.push(value);
  });
  cdp.on("Network.requestWillBeSent", params => {
    try {
      const url = new URL(String(params.request?.url || ""));
      if (url.origin !== baseURL) return;
      const request = { method: String(params.request?.method || "GET"), path: url.pathname };
      requests.set(params.requestId, request);
      if (/^\/(?:admin|api\/admin)\//.test(request.path)) appendBounded(requestEvents, `${request.method} ${request.path}`);
    } catch (_) {}
  });
  cdp.on("Network.responseReceived", params => {
    const request = requests.get(params.requestId);
    if (request && /^\/(?:admin|api\/admin)\//.test(request.path)) appendBounded(responses, `${request.method} ${request.path}:${Number(params.response?.status) || 0}`);
  });

  const capture = async name => {
    const result = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(result.data, "base64"), { mode: 0o600 });
  };
  const captureFailureEvidence = async label => {
    const safeLabel = label.replace(/[^A-Za-z0-9_.-]/g, "_");
    let screenshotCaptured = false;
    try {
      const shot = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
      await fs.writeFile(path.join(screenshotDirectory, "failure-" + safeLabel + ".png"), Buffer.from(shot.data, "base64"), { mode: 0o600 });
      screenshotCaptured = true;
    } catch (_) {}
    // Geometry and safe route/status diagnostics remain available even if a
    // browser screenshot command itself fails while handling an earlier page
    // error. No response body, credential, or customer data is persisted.
    let geometry = { path: "unavailable", screenshot_captured: screenshotCaptured, responses: responses.slice(-12), runtime_exceptions: runtimeExceptions.slice(-8) };
    try {
      const measured = await evaluate(cdp, `(() => {
        const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop,display:style.display}; };
        const visible = node => { if (!node) return false; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
        const token = value => String(value || '').replace(/[^A-Za-z0-9_-]/g, '_').slice(0, 96);
        const dom = [document.body, ...document.querySelectorAll('.admin-main-wrap,.admin-sidebar,.admin-topbar,#stage,.order-host-layout,[data-runtime-release-host],[data-open-platform-host],.sec-funnel')].filter((node, index, all) => node instanceof Element && all.indexOf(node) === index).slice(0, 20).map(node => ({tag:node.tagName.toLowerCase(),id:token(node.id),classes:Array.from(node.classList).map(token).filter(Boolean).slice(0, 12),visible:visible(node)}));
        return {path:location.pathname,ready:document.readyState,sidebar:box('.admin-sidebar'),main:box('.admin-main-wrap'),topbar:box('.admin-topbar'),content:box('#stage') || box('.admin-main-wrap > .admin-page'),stage:box('#stage'),viewport:{width:innerWidth,height:innerHeight},overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,dom};
      })()`);
      geometry = { ...measured, screenshot_captured: screenshotCaptured, responses: responses.slice(-12), runtime_exceptions: runtimeExceptions.slice(-8) };
    } catch (_) {}
    await fs.writeFile(path.join(screenshotDirectory, "failure-" + safeLabel + "-geometry.json"), JSON.stringify(geometry), { mode: 0o600 });
  };
  const waitForFonts = async label => {
    const settled = await evaluate(cdp, "document.fonts ? document.fonts.ready.then(() => document.fonts.status === 'loaded') : true");
    if (!settled) throw new Error(label + " document fonts did not settle");
  };
  const geometryFailures = [];
  const interactionFailures = [];
  const recordGeometry = async (label, assertion, screenshot) => {
    try {
      await assertion();
      if (screenshot) await capture(label);
    } catch (error) {
      try { await captureFailureEvidence(label); } catch (_) {}
      const message = String(error instanceof Error ? error.message : "geometry assertion failed").replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 160);
      geometryFailures.push(label + ":" + message);
    }
  };
  const currentLayout = titleSelector => evaluate(cdp, String.raw`(() => {
    const isVisible = node => { if (!node) return false; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
    const box = node => { if (!node) return null; const value=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:value.left,top:value.top,right:value.right,bottom:value.bottom,width:value.width,height:value.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop,display:style.display}; };
    const renderedChildren = root => {
      const result=[];
      const visit=node => {
        if (!(node instanceof Element) || ['STYLE','SCRIPT','TEMPLATE'].includes(node.tagName)) return;
        if (getComputedStyle(node).display === 'contents') { for (const child of Array.from(node.children)) visit(child); return; }
        result.push(node);
      };
      for (const child of Array.from(root?.children || [])) visit(child);
      return result;
    };
    const stage=document.querySelector('#stage');
    const main=document.querySelector('.admin-main-wrap');
    const stageBox=stage?.getBoundingClientRect();
    const mainBox=main?.getBoundingClientRect();
    const roots=renderedChildren(stage).filter(isVisible);
    const selector=${JSON.stringify(titleSelector || 'h1,h2,[role="heading"],[class*="toolbar"],[class*="header"],[class*="head"],[class*="title"]')};
    const title=stage ? Array.from(stage.querySelectorAll(selector)).find(node => isVisible(node) && String(node.textContent || '').trim().length > 0) || null : null;
    const titleLineage=[]; for (let current=title; current && current !== stage; current=current.parentElement) titleLineage.push(current);
    const isHeaderEdge = node => {
      if (!stageBox || !mainBox || node === stage || getComputedStyle(node).display === 'contents') return false;
      const value=node.getBoundingClientRect();
      return Math.abs(value.left-stageBox.left) <= 1 && Math.abs(value.top-stageBox.top) <= 1 && Math.abs(value.right-mainBox.right) <= 1 && value.width >= Math.min(220, stageBox.width * 0.4);
    };
    const className = node => typeof node.className === 'string' ? node.className : '';
    const explicitHeader = node => node.tagName === 'HEADER' || /(?:^|[-_\s])(header|toolbar|topbar|page-head|page-header|title-bar)(?:$|[-_\s])/.test(className(node));
    const compactTopLevelHeader = node => {
      if (!roots.includes(node)) return false;
      const value=node.getBoundingClientRect();
      return value.height >= 40 && value.height <= 120 && String(node.textContent || '').trim().length > 0;
    };
    // A donor page can use a plain, inline-styled top-level div instead of a
    // semantic header.  That narrow case is accepted only for a compact
    // rendered root; the full workspace/root is never allowed as the bar.
    const headerCandidates=[];
    for (const node of [...roots, ...titleLineage]) {
      if (headerCandidates.includes(node) || !isVisible(node) || !isHeaderEdge(node)) continue;
      if (explicitHeader(node) || compactTopLevelHeader(node)) headerCandidates.push(node);
    }
    const innerBar=headerCandidates[0] || null;
    if (innerBar) globalThis.__aicrmAdminLayoutInnerBar = innerBar;
    const titleCount=stage ? Array.from(stage.querySelectorAll('h1')).filter(isVisible).filter(node => node.getBoundingClientRect().top < stage.getBoundingClientRect().top + 180).length : 0;
    const content=stage || document.querySelector('.admin-main-wrap > .admin-page');
    return {sidebar:box(document.querySelector('.admin-sidebar')),main:box(main),topbar:box(document.querySelector('.admin-topbar')),content:box(content),stage:box(stage),renderedRootCount:roots.length,innerBar:box(innerBar),innerBarText:String(innerBar?.textContent || '').trim(),headerCandidateCount:headerCandidates.length,title:box(title),titleCount,headers:document.querySelectorAll('header.admin-topbar').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,ready:document.readyState};
  })()`);
  const assertLayout = async (kind, label, titleSelector) => {
    const layout = await currentLayout(titleSelector);
    if (!layout.sidebar || !layout.main || layout.overflow || Math.abs(layout.sidebar.right - layout.main.left) > 1) throw new Error(`${label} shell geometry invalid`);
    if (kind === "standard") {
      if (!layout.topbar || layout.headers !== 1 || Math.abs(layout.topbar.left - layout.main.left) > 1 || Math.abs(layout.topbar.right - layout.main.right) > 1 || Math.abs(layout.topbar.top) > 1 || layout.topbar.height < 48 || !layout.content || layout.content.top + 1 < layout.topbar.bottom) throw new Error(`${label} standard topbar geometry invalid`);
      return;
    }
    if (layout.headers !== 0 || !layout.stage || layout.renderedRootCount < 1 || !layout.innerBar || !layout.innerBarText || !layout.title || layout.titleCount > 1 || Math.abs(layout.stage.left - layout.main.left) > 1 || Math.abs(layout.stage.top - layout.main.top) > 1 || Math.abs(layout.innerBar.left - layout.stage.left) > 1 || Math.abs(layout.innerBar.top - layout.stage.top) > 1 || Math.abs(layout.innerBar.right - layout.main.right) > 1 || layout.stage.paddingLeft !== "0px" || layout.stage.paddingTop !== "0px") throw new Error(`${label} embedded workspace/header geometry invalid`);
  };
  const assertHXCLayout = async label => {
    await assertLayout("standard", label, ".sec-funnel .page-head");
    const hxc = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage.labs.sec-funnel');
      const crumb=stage?.querySelector(':scope > .crumb');
      const title=stage?.querySelector(':scope > .page-head > :first-child');
      const refresh=stage?.querySelector('#hxcRefresh');
      const style=stage ? getComputedStyle(stage) : null;
      return {stage: Boolean(stage), paddingLeft: style?.paddingLeft || '', paddingTop: style?.paddingTop || '', crumbHidden: Boolean(crumb) && getComputedStyle(crumb).display === 'none', titleHidden: Boolean(title) && getComputedStyle(title).display === 'none', refreshVisible: Boolean(refresh) && getComputedStyle(refresh).display !== 'none'};
    })()`);
    if (!hxc?.stage || hxc.paddingLeft !== "20px" || hxc.paddingTop !== "16px" || !hxc.crumbHidden || !hxc.titleHidden || !hxc.refreshVisible) throw new Error(label + " HXC title/padding/action layout invalid");
  };
  const assertRadarLayout = async (label, actionSelector, contentSelector = ".sec-radar .page-head") => {
    await assertLayout("standard", label, contentSelector);
    const radar = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage.labs.sec-radar');
      const crumb=stage?.querySelector(':scope > .crumb');
      const title=stage?.querySelector(':scope > .page-head > :first-child');
      const action=stage?.querySelector(${JSON.stringify(actionSelector)});
      const style=stage ? getComputedStyle(stage) : null;
      const page=String(document.body?.dataset.page || '');
      const pageHead=stage?.querySelector(':scope > .page-head');
      const hidden=node => { const rect=node?.getBoundingClientRect(); return !node || getComputedStyle(node).display === 'none' || !rect || rect.width < 1 || rect.height < 1; };
      return {stage:Boolean(stage),paddingLeft:style?.paddingLeft || '',paddingTop:style?.paddingTop || '',crumbHidden:Boolean(crumb) && hidden(crumb),titleHidden:Boolean(title) && hidden(title),emptyPageHeadHidden:!(page === 'radarDetail' || page === 'radarForm') || hidden(pageHead),actionVisible:Boolean(action) && getComputedStyle(action).display !== 'none'};
    })()`);
    if (!radar?.stage || radar.paddingLeft !== "20px" || radar.paddingTop !== "16px" || !radar.crumbHidden || !radar.titleHidden || !radar.emptyPageHeadHidden || !radar.actionVisible) throw new Error(label + " V3 title/action layout invalid");
  };
  const assertOwnerHandoffLayout = async label => {
    await assertLayout("standard", label, "[data-owner-picker=\"source\"]");
    const owner = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage[data-owner-handoff-host]');
      const page=stage?.querySelector('[data-owner-migration-page]');
      const donorHeader=page?.querySelector(':scope > .owner-migration-header');
      const donorTitle=donorHeader?.querySelector(':scope > :first-child');
      const status=donorHeader?.querySelector('.owner-migration-status-bar');
      const style=stage ? getComputedStyle(stage) : null;
      return {stage:Boolean(stage),paddingLeft:style?.paddingLeft || '',paddingTop:style?.paddingTop || '',donorTitleHidden:Boolean(donorTitle) && getComputedStyle(donorTitle).display === 'none',statusVisible:Boolean(status) && getComputedStyle(status).display !== 'none',migrationActionVisible:Boolean(page?.querySelector('[data-owner-picker="source"]')) && getComputedStyle(page.querySelector('[data-owner-picker="source"]')).display !== 'none'};
    })()`);
    if (!owner?.stage || owner.paddingLeft !== "20px" || owner.paddingTop !== "16px" || !owner.donorTitleHidden || !owner.statusVisible || !owner.migrationActionVisible) throw new Error(label + " V3 title/status/action layout invalid");
  };
  const assertExternalEffectsLayout = async label => {
    await assertLayout("standard", label, "#stage h2");
    const effects = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage');
      const shell=stage?.querySelector(':scope > div');
      const localHeader=shell?.querySelector(':scope > :first-child');
      const localCrumb=localHeader?.querySelector(':scope > :first-child');
      const localTitle=localHeader?.querySelector(':scope > h1');
      const history=shell?.querySelector('a[href*="history=1"]');
      const refresh=stage?.querySelector('#effects-refresh');
      return {stage:Boolean(stage),localCrumbHidden:Boolean(localCrumb) && getComputedStyle(localCrumb).display === 'none',localTitleHidden:Boolean(localTitle) && getComputedStyle(localTitle).display === 'none',historyVisible:Boolean(history) && getComputedStyle(history).display !== 'none',refreshVisible:Boolean(refresh) && getComputedStyle(refresh).display !== 'none'};
    })()`);
    if (!effects?.stage || !effects.localCrumbHidden || !effects.localTitleHidden || !effects.historyVisible || !effects.refreshVisible) throw new Error(label + " V3 title/history/action layout invalid");
  };
  const assertInsetRegressionRejected = async (label, titleSelector) => {
    const prepared = await evaluate(cdp, `(() => {
      const target=globalThis.__aicrmAdminLayoutInnerBar;
      if (!(target instanceof Element) || !target.isConnected) return false;
      globalThis.__aicrmAdminLayoutInnerBarStyle=target.getAttribute('style');
      target.style.setProperty('position','relative','important');
      target.style.setProperty('left','20px','important');
      return true;
    })()`);
    if (!prepared) throw new Error(label + " did not expose a concrete inner title bar for the padding regression control");
    let rejected = false;
    try {
      await assertLayout("embedded", label, titleSelector);
    } catch (_) {
      rejected = true;
    } finally {
      await evaluate(cdp, `(() => {
        const target=globalThis.__aicrmAdminLayoutInnerBar;
        const original=globalThis.__aicrmAdminLayoutInnerBarStyle;
        if (!(target instanceof Element)) return false;
        if (original === null || original === undefined) target.removeAttribute('style');
        else target.setAttribute('style', original);
        return true;
      })()`);
    }
    if (!rejected) throw new Error(label + " accepted a 20px inset title bar regression");
    await assertLayout("embedded", label, titleSelector);
  };
  const recordRouteFailure = async (label, error) => {
    try { await captureFailureEvidence(label); } catch (_) {}
    const message = String(error instanceof Error ? error.message : "route assertion failed").replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 160);
    geometryFailures.push(label + ":" + message);
  };
  const clickNavigation = async (pathname, label) => {
    const destination = new URL(pathname, baseURL);
    const found = await evaluate(cdp, `(() => [...document.querySelectorAll('.admin-nav-link[href]')].some(node => { const target=new URL(node.href, location.href); return target.pathname === ${JSON.stringify(destination.pathname)} && target.search === ${JSON.stringify(destination.search)}; }))()`);
    if (!found) throw new Error(label + " menu link is absent or points to a fallback route");
    await evaluate(cdp, `(() => { const node=[...document.querySelectorAll('.admin-nav-link[href]')].find(value => { const target=new URL(value.href, location.href); return target.pathname === ${JSON.stringify(destination.pathname)} && target.search === ${JSON.stringify(destination.search)}; }); node.click(); return true; })()`);
  };
  const navigate = async (pathname, ready, label, kind, titleSelector, screenshot = false, fromMenu = false, finalPath = pathname) => {
    currentStep = label;
    try {
      if (fromMenu) await clickNavigation(pathname, label);
      else await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(finalPath.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready + " && Boolean((() => { const stage=document.querySelector('#stage'); if (!stage) return false; const visible=node => { const rect=node.getBoundingClientRect(), style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; }; return Array.from(stage.querySelectorAll(" + JSON.stringify(titleSelector) + ")).some(node => visible(node) && String(node.textContent || '').trim().length > 0); })())", label + " Host did not render a visible workspace title");
      await waitForFonts(label);
      await recordGeometry(label, () => assertLayout(kind, label, titleSelector), screenshot);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };

  const embeddedTitle = 'h1,h2,[role="heading"],[class*="toolbar"],[class*="header"],[class*="head"],[class*="title"]';
  // The frozen questionnaire list uses an inline-styled, classless title. This
  // exact selector records its source-backed visible title contract without
  // admitting arbitrary container text as a page header.
  const questionnaireTitle = '#stage div[style*="font-size:16px"][style*="font-weight:600"][style*="line-height:22px"]';
  // The frozen product and media list templates use the same source-backed
  // 52px toolbar but no semantic title class. Keep this narrow shape instead
  // of accepting arbitrary body text as a workspace heading.
  const frozenListToolbarTitle = '#stage > div[style*="display: contents"] > div[style*="height:52px"] div[style*="font-size:16px"][style*="font-weight:600"][style*="line-height:22px"]';
  const navigateStandard = async (pathname, ready, label, screenshot = false, fromMenu = false) => {
    currentStep = label;
    try {
      if (fromMenu) await clickNavigation(pathname, label);
      else await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready, label + " Host did not become ready");
      await waitForFonts(label);
      await recordGeometry(label, () => assertLayout("standard", label, embeddedTitle), screenshot);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };
  const assertStaticOpenLayout = async label => {
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const title=document.querySelector('.open-platform-header h1');
      return {side:box('.side'),stage:box('#stage'),root:box('[data-open-platform-host="v1"]'),header:box('.open-platform-header'),title:box('.open-platform-header h1'),titleText:String(title?.textContent || '').trim(),headers:document.querySelectorAll('.open-platform-header').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
    })()`);
    if (!layout.side || !layout.stage || !layout.root || !layout.header || !layout.title || !layout.titleText || layout.headers !== 1 || layout.overflow || Math.abs(layout.side.right-layout.stage.left) > 1 || Math.abs(layout.stage.top) > 1 || Math.abs(layout.root.left-layout.stage.left) > 1 || Math.abs(layout.root.top-layout.stage.top) > 1 || Math.abs(layout.header.left-layout.stage.left) > 1 || Math.abs(layout.header.top-layout.stage.top) > 1 || layout.stage.paddingLeft !== "0px" || layout.stage.paddingTop !== "0px" || layout.root.paddingLeft !== "0px" || layout.root.paddingTop !== "0px" || Math.abs(layout.header.right-layout.stage.right) > 1 || layout.header.height < 48) throw new Error(label + " static topbar/sidebar geometry invalid");
  };
  const navigateStaticHost = async (pathname, ready, label) => {
    currentStep = label;
    try {
      await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready, label + " V3 Host did not become ready");
      await assertStaticOpenLayout(label);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };

  const assertRuntimeConfigLayout = async label => {
    await assertLayout("standard", label, embeddedTitle);
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const root=document.querySelector('[data-runtime-release-host]');
      const card=root?.querySelector('.admin-card');
      const title=card ? [...card.querySelectorAll('h2')].find(node => String(node.textContent || '').trim().length > 0) : null;
      return {root:box('[data-runtime-release-host]'),card:box('[data-runtime-release-host] .admin-card'),title:box('[data-runtime-release-host] .admin-card h2'),topbar:box('.admin-topbar'),headers:document.querySelectorAll('header.admin-topbar').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,titleText:String(title?.textContent || '').trim()};
    })()`);
    if (!layout.root || !layout.card || !layout.title || !layout.titleText || !layout.topbar || layout.headers !== 1 || layout.overflow || layout.root.top + 1 < layout.topbar.bottom || layout.card.top + 1 < layout.root.top || layout.card.left + 1 < layout.root.left) throw new Error(label + " V3 topbar/release-card geometry invalid");
  };
  const navigateRuntimeConfig = async () => {
    currentStep = "runtime-config";
    try {
      await cdp.call("Page.navigate", { url: baseURL + "/admin/config/releases" });
      await waitFor(cdp, "location.pathname === '/admin/config/releases' && document.readyState !== 'loading'", "runtime config did not navigate");
      await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-host] .admin-card h2')) && document.body?.textContent?.includes('当前运行时配置')", "runtime config Host did not become ready");
      await waitForFonts("runtime-config");
      await recordGeometry("runtime-config", () => assertRuntimeConfigLayout("runtime-config"), true);
      return true;
    } catch (error) {
      await recordRouteFailure("runtime-config", error);
      return false;
    }
  };

  const initial = "/admin/automation-conversion";
  currentStep = "automation";
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=" + encodeURIComponent(initial) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/automation-conversion'", "login did not establish the Access session");
  await waitFor(cdp, "Boolean(document.querySelector('.admin-topbar')) && Boolean(document.querySelector('.aud-layout'))", "automation shell did not load");
  await waitForFonts("automation");
  await recordGeometry("automation", () => assertLayout("standard", "automation", embeddedTitle), true);

  // The matrix follows every actual item in ADMIN_NAV_GROUPS.  The embedded
  // rows require a live workspace root and a visible donor/V3 page title in
  // addition to the shell geometry; an empty Host cannot satisfy this check.
  await navigate("/admin/operation-cycles", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "cycles", "embedded", embeddedTitle, true, true);
  await recordGeometry("cycles-padding-regression-control", () => assertInsetRegressionRejected("cycles", embeddedTitle), false);
  await navigate("/admin/automation-conversion/group-ops/ui", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "groupops", "embedded", embeddedTitle, true, true, "/admin/groupops.html");
  await navigate("/admin/channels", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "channels", "embedded", embeddedTitle, true, true);
  await navigate("/admin/cloud-orchestrator/plans", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "ai", "embedded", embeddedTitle, true, true);
  await navigateStandard("/admin/customers", "Boolean(document.querySelector('[data-customer-directory-root]'))", "customers", true, true);
  const hxcMounted = await navigate("/admin/hxc-dashboard", "Boolean(document.querySelector('#hxcRefresh')) && Boolean(document.querySelector('.sec-funnel'))", "hxc", "standard", ".sec-funnel .page-head", false, true);
  if (hxcMounted) await recordGeometry("hxc", () => assertHXCLayout("hxc"), true);
  await navigate("/admin/questionnaires", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "questionnaires", "embedded", questionnaireTitle, true, true);
  const radarMounted = await navigate("/admin/radar-links", "Boolean(document.querySelector('#stage.labs.sec-radar')) && Boolean(document.querySelector('#btnNew'))", "radar", "standard", ".sec-radar .page-head", false, true);
  if (radarMounted) await recordGeometry("radar", () => assertRadarLayout("radar", "#btnNew"), true);
  const radarNumericID = Number(radarID);
  const radarDetailMounted = await navigate("/admin/radarDetail.html?id=" + encodeURIComponent(String(radarNumericID)), "Boolean(document.querySelector('#stage.labs.sec-radar')) && Boolean(document.querySelector('#dEdit'))", "radar-detail", "standard", "#dEdit", false);
  if (radarDetailMounted) await recordGeometry("radar-detail", () => assertRadarLayout("radar-detail", "#dEdit", "#dEdit"), true);
  const radarFormMounted = await navigate("/admin/radarForm.html", "Boolean(document.querySelector('#stage.labs.sec-radar')) && Boolean(document.querySelector('#fSave'))", "radar-form", "standard", "#fSave", false);
  if (radarFormMounted) await recordGeometry("radar-form", () => assertRadarLayout("radar-form", "#fSave", "#fSave"), true);
  await navigate("/admin/wecom-tags", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "tags", "embedded", embeddedTitle, true, true);

  await navigate("/admin/orders", "Boolean(document.querySelector('.order-host-layout')) && Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "orders", "embedded", embeddedTitle, true, true);
  await navigate("/admin/wechat-pay/products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "products", "embedded", frozenListToolbarTitle, true, true);
  await navigate("/admin/service-period-products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "service-period-products", "embedded", frozenListToolbarTitle, true, true);
  await navigate("/admin/productForm.html?id=" + productID, "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('#pfExternalPushEnabled'))", "product", "embedded", embeddedTitle, true);
  await navigate("/admin/spProductForm.html?id=" + serviceProductID, "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('#spfExternalPushEnabled'))", "service-period-product", "embedded", embeddedTitle, true);
  await navigate("/admin/coupons", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "coupons", "embedded", embeddedTitle, true, true);

  await navigate("/admin/image-library", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "image-library", "embedded", frozenListToolbarTitle, true, true);
  await navigate("/admin/miniprogram-library", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "miniprogram-library", "embedded", frozenListToolbarTitle, true, true);
  await navigate("/admin/attachment-library", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "attachment-library", "embedded", embeddedTitle, true, true);

  await navigate("/admin/automation-agents", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "automation-agents", "embedded", embeddedTitle, true, true);
  const ownerMounted = await navigate("/admin/owner-migration", "Boolean(document.querySelector('[data-owner-handoff-host][data-owner-handoff-init=\"ready\"]')) && Boolean(document.querySelector('[data-owner-migration-page] .owner-migration-status-bar')) && Boolean(document.querySelector('[data-owner-migration-page] [data-owner-picker=\"source\"]'))", "owner-migration", "standard", "[data-owner-picker=\"source\"]", false, true);
  if (ownerMounted) await recordGeometry("owner-migration", () => assertOwnerHandoffLayout("owner-migration"), true);
  await navigate("/admin/config", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "config", "embedded", embeddedTitle, true, true);
  await navigateRuntimeConfig();
  await navigateStandard("/admin/oneid", "Boolean(document.querySelector('[data-admin-oneid-root]'))", "oneid", true, true);
  currentStep = "api-docs";
  await clickNavigation("/admin/api-docs", "api-docs");
  await waitFor(cdp, "location.pathname === '/admin/apidocs.html' && document.readyState !== 'loading'", "api-docs did not canonicalize to its V3 Host document");
  await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-host]') || document.querySelector('[class*=openPlatformHost]'))", "api-docs V3 Host did not become ready");
  await waitForFonts("api-docs");
  await recordGeometry("api-docs", () => assertStaticOpenLayout("api-docs"), true);

  // Detail and frozen aliases remain on their business Host, including the
  // order history panel whose source mapping is independently seeded below.
  await navigate("/admin/orderDetail.html?id=" + encodeURIComponent(historicalOrderReference), "Boolean(document.querySelector('.order-host-layout')) && Boolean(document.body?.textContent?.includes('外推回执'))", "order-detail-history", "embedded", embeddedTitle, true);
  const effectsMounted = await navigate("/admin/campaigns.html?view=external-effects", "Boolean(document.querySelector('#stage')) && Boolean(document.querySelector('#effects-refresh')) && Boolean(document.querySelector('#stage h2'))", "external-effects", "standard", "#stage h2", false);
  if (effectsMounted) await recordGeometry("external-effects", () => assertExternalEffectsLayout("external-effects"), true);

  currentStep = "hxc-refresh";
  try {
    await cdp.call("Page.navigate", { url: baseURL + "/admin/hxc-dashboard" });
    await waitFor(cdp, "location.pathname === '/admin/hxc-dashboard' && Boolean(document.querySelector('#hxcRefresh'))", "HXC did not return for refresh");
    const hxcContent = await evaluate(cdp, `(() => { const crumb=document.querySelector('.sec-funnel > .crumb'); const heading=document.querySelector('.sec-funnel > .page-head > :first-child'); const refresh=document.querySelector('#hxcRefresh'); const grid=document.querySelector('.sec-funnel .grid-scroll'); return {crumbHidden: Boolean(crumb) && getComputedStyle(crumb).display === 'none', headingHidden: Boolean(heading) && getComputedStyle(heading).display === 'none', refreshVisible: Boolean(refresh) && getComputedStyle(refresh).display !== 'none', scrollable: Boolean(grid) && grid.scrollHeight > grid.clientHeight}; })()`);
    if (!hxcContent?.crumbHidden || !hxcContent?.headingHidden || !hxcContent?.refreshVisible || !hxcContent?.scrollable) throw new Error("HXC duplicate title/action/scroll layout invalid");
    await evaluate(cdp, "(() => { const grid=document.querySelector('.sec-funnel .grid-scroll'); grid.scrollTop=grid.scrollHeight; return grid.scrollTop > 0; })()");
    if (!await evaluate(cdp, "document.querySelector('.sec-funnel .grid-scroll')?.scrollTop > 0")) throw new Error("HXC grid did not retain a user scroll");
    // These arrays are bounded diagnostics for the full route matrix. Reset
    // them immediately before this one interaction so their window cannot be
    // invalidated by a later ring-buffer eviction.
    requestEvents.length = 0;
    responses.length = 0;
    await evaluate(cdp, "(() => { document.querySelector('#hxcRefresh').click(); return true; })()");
    await waitForRecorded(requestEvents, value => value === "POST /api/admin/hxc-dashboard/refreshes", "HXC refresh did not issue its configured POST");
    await waitForRecorded(responses, value => value === "POST /api/admin/hxc-dashboard/refreshes:503", "HXC refresh did not reach the disabled runtime contract");
    await waitFor(cdp, "document.querySelector('#hxcRefresh')?.disabled === false && document.querySelector('#hxcRefresh')?.textContent === '立即刷新'", "HXC refresh action did not settle");
  } catch (error) {
    await recordRouteFailure("hxc-refresh", error);
    interactionFailures.push("hxc-refresh:" + String(error instanceof Error ? error.message : "refresh assertion failed").replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 160));
  }
  if (runtimeExceptions.length) interactionFailures.push("runtime-exception:" + runtimeExceptions.join(","));
  if (geometryFailures.length || interactionFailures.length) throw new Error("admin layout failures=" + [...geometryFailures, ...interactionFailures].join(","));
  console.log("admin_shell_layout_chromium: PASS routes=" + responses.filter(value => value.includes("/admin/") || value.includes("/api/admin/hxc-dashboard")).length);
} catch (error) {
  failed = true;
  if (cdp) {
    try { await captureFailureEvidence(currentStep); } catch (_) {}
  }
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
