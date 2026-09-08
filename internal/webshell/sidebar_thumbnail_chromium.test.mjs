import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_URL;
const username = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_USERNAME;
const password = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_PASSWORD;
const jssdkFixturePath = process.env.AICRM_SIDEBAR_JSSDK_FIXTURE;
const weComJSSDKURL = "https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js";
const screenshotDirectory = process.env.AICRM_SIDEBAR_SCREENSHOT_DIR || "";
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !jssdkFixturePath) throw new Error("sidebar Chromium journey requires HTTPS URL, credentials, and the official WeCom JSSDK fixture");
const jssdkFixture = await fs.readFile(jssdkFixturePath);
if (!jssdkFixture.includes(Buffer.from("agentConfig"))) throw new Error("sidebar Chromium journey JSSDK fixture lacks agentConfig");

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
function startupDiagnostic(browser, stderr) {
  const detail = String(stderr || "")
    .replace(/[\r\n]+/g, " ")
    .replace(/[^a-zA-Z0-9 .,:_+\-\/()]/g, "_")
    .slice(0, 600);
  const exit = browser?.exitCode;
  const signal = browser?.signalCode;
  return JSON.stringify({ exit_code: exit ?? null, signal: signal ?? null, stderr: detail || "none" });
}

async function port(profile, browser, stderr) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (browser?.exitCode !== null || browser?.signalCode !== null) {
      throw new Error(`Chromium exited before DevTools was ready: ${startupDiagnostic(browser, stderr())}`);
    }
    try { const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`; } catch (_) {}
    await delay(50);
  }
  throw new Error(`Chromium DevTools startup timed out after 30s: ${startupDiagnostic(browser, stderr())}`);
}
function evaluationFailure(stage, details) {
  const kind = String(details?.exception?.className || details?.text || "runtime_exception")
    .replace(/[^a-zA-Z0-9_.-]/g, "_")
    .slice(0, 96);
  return new Error(`${stage} page evaluation failed: ${kind || "runtime_exception"}`);
}
async function evaluate(cdp, expression, stage = "page") {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw evaluationFailure(stage, result.exceptionDetails);
  return result.result?.value;
}
async function waitFor(cdp, expression, message, stage = "wait") {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression, stage)) return;
    await delay(50);
  }
  throw new Error(message);
}
async function captureScreenshot(cdp, name) {
  if (!screenshotDirectory) return;
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  const shot = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  await fs.writeFile(path.join(screenshotDirectory, `${name}.png`), Buffer.from(shot.data, "base64"), { mode: 0o600 });
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
let browser; let cdp; let failed = false; let browserStderr = "";
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
  browser.stderr?.on("data", (chunk) => { if (browserStderr.length < 4096) browserStderr += String(chunk).slice(0, 4096 - browserStderr.length); });
  const created = await (await fetch(`${await port(profile, browser, () => browserStderr)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  await cdp.call("Emulation.setUserAgentOverride", { userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) wxwork/4.1.36 MicroMessenger/7.0.1", platform: "MacIntel" });

  let jssdkResourceMode = "serve";
  await cdp.call("Page.addScriptToEvaluateOnNewDocument", { source: `(() => {
    const calls = [];
    const agentAPIs = [];
    let agentCalls = 0;
    Object.defineProperty(globalThis, "__sidebarNativeBridgeCalls", { value: calls, configurable: false });
    Object.defineProperty(globalThis, "__sidebarAgentAPIs", { value: agentAPIs, configurable: false });
    Object.defineProperty(globalThis, "__sidebarClipboardWrites", { value: [], configurable: false });
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText(value) { globalThis.__sidebarClipboardWrites.push(String(value)); return Promise.resolve(); } } });
    Object.defineProperty(globalThis, "WeixinJSBridge", { configurable: false, value: {
      invoke(method, payload, callback) {
        calls.push(method);
        const scenario = new URL(globalThis.location.href).searchParams.get("sidebar_case") || "success";
        if (method === "preVerifyJSAPI" && scenario === "regular_error") return setTimeout(() => callback({ err_msg: "preVerifyJSAPI:fail" }), 0);
        if (method === "agentConfig") {
          agentCalls += 1;
          agentAPIs.splice(0, agentAPIs.length, ...(Array.isArray(payload?.jsApiList) ? payload.jsApiList : []));
          if (scenario === "agent_error" || (scenario === "agent_retry" && agentCalls === 1)) return setTimeout(() => callback({ err_msg: "agentConfig:fail" }), 0);
        }
        if (method !== "preVerifyJSAPI" && method !== "agentConfig" && !agentAPIs.includes(method)) return setTimeout(() => callback({ err_msg: method + ":no permission" }), 0);
        if (method === "getCurExternalContact") {
          if (scenario === "contact_error") return setTimeout(() => callback({ err_msg: "getCurExternalContact:fail" }), 0);
          return setTimeout(() => callback({ err_msg: "getCurExternalContact:ok", external_userid: "sidebar-thumbnail-external" }), 0);
        }
        return setTimeout(() => callback({ err_msg: method + ":ok" }), 0);
      },
      on() {}, call() {},
    }});
  })();` });
  await cdp.call("Fetch.enable", { patterns: [{ urlPattern: weComJSSDKURL }] });
  cdp.on("Fetch.requestPaused", (params) => {
    void (async () => {
      if (jssdkResourceMode === "missing") {
        await cdp.call("Fetch.failRequest", { requestId: params.requestId, errorReason: "BlockedByClient" });
        return;
      }
      await cdp.call("Fetch.fulfillRequest", {
        requestId: params.requestId,
        responseCode: 200,
        responseHeaders: [{ name: "Content-Type", value: "application/javascript; charset=utf-8" }],
        body: jssdkFixture.toString("base64"),
      });
    })().catch(() => undefined);
  });

  const resources = new Map(); const requestURLs = []; const requestRecords = []; const exceptions = []; const loginResponses = new Map(); let sidebarCSP = "";
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  cdp.on("Network.requestWillBeSent", (params) => {
    requestURLs.push(String(params.request?.url || ""));
    requestRecords.push({ url: String(params.request?.url || ""), postData: String(params.request?.postData || "") });
  });
  cdp.on("Network.responseReceived", (params) => {
    try {
      const pathname = new URL(String(params.response?.url || "")).pathname;
      const status = Number(params.response?.status) || 0;
      if (pathname === "/sidebar/bind-mobile") sidebarCSP = String(params.response?.headers?.["content-security-policy"] || params.response?.headers?.["Content-Security-Policy"] || "");
      if (pathname === "/login" || pathname === "/admin" || pathname === "/admin/customers.html") loginResponses.set(pathname, status);
      if (pathname === "/api/sidebar/v2/bootstrap" || pathname === "/api/sidebar/v2/materials" || /^\/api\/sidebar\/v2\/materials\/\d+\/variants\/thumb_320$/.test(pathname) || /^\/sidebar-assets\/(sidebarHost|sidebarStandardOverlay|sidebarImageResourceLoader)-[A-Za-z0-9_-]+\.js$/.test(pathname)) resources.set(pathname, status);
    } catch (_) {}
  });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
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

  const sidebarDocumentReadyExpression = (scenario) => `(() => { const body = document.body; return location.pathname === "/sidebar/bind-mobile" && new URL(location.href).searchParams.get("sidebar_case") === ${JSON.stringify(scenario)} && document.readyState !== "loading" && Boolean(body); })()`;
  async function openSidebar(scenario, resourceMode = "serve") {
    jssdkResourceMode = resourceMode;
    const requestStart = requestURLs.length;
    await cdp.call("Page.navigate", { url: `${baseURL}/sidebar/bind-mobile?sidebar_case=${encodeURIComponent(scenario)}` });
    await waitFor(cdp, sidebarDocumentReadyExpression(scenario), `${scenario} navigation did not reach its sidebar document`, `${scenario}_navigation`);
    return requestStart;
  }
  const bootstrapCountSince = (start) => requestURLs.slice(start).filter((url) => new URL(url).pathname === "/api/sidebar/v2/bootstrap").length;
  const jssdkCountSince = (start) => requestURLs.slice(start).filter((url) => new URL(url).pathname === "/api/sidebar/jssdk-config").length;
  const bridgeCalls = () => evaluate(cdp, "JSON.stringify(globalThis.__sidebarNativeBridgeCalls || [])").then((value) => JSON.parse(value || "[]"));

  for (const [scenario, message, resourceMode] of [
    ["sdk_missing", "企微 SDK 未载入", "missing"],
    ["regular_error", "企微 config 失败：config:fail", "serve"],
    ["agent_error", "agentConfig:fail", "serve"],
    ["contact_error", "getCurExternalContact:fail", "serve"],
  ]) {
    const start = await openSidebar(scenario, resourceMode);
    await waitFor(cdp, `(${sidebarDocumentReadyExpression(scenario)}) && Boolean(document.body?.textContent?.includes(${JSON.stringify(message)}) && document.querySelector('[data-retry-boot]'))`, `${scenario} did not render the standard overlay recovery action`, `${scenario}_recovery`);
    if (bootstrapCountSince(start) !== 0) throw new Error(`${scenario} requested sidebar bootstrap before a trusted contact`);
  }

  const retryStart = await openSidebar("agent_retry");
  await waitFor(cdp, "Boolean(document.querySelector('[data-retry-boot]'))", "agent failure did not render standard overlay retry action");
  await evaluate(cdp, "document.querySelector('[data-retry-boot]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('#tabs button[data-tab=\"materials\"]:not([disabled])'))", "agent retry did not establish the trusted sidebar context");
  const retryCalls = await bridgeCalls();
  if (retryCalls.filter((call) => call === "preVerifyJSAPI").length !== 1 || retryCalls.filter((call) => call === "agentConfig").length !== 2 || jssdkCountSince(retryStart) !== 2 || bootstrapCountSince(retryStart) !== 1) throw new Error("agent retry did not reuse regular config or re-read the agent signature exactly once");

  const successStart = await openSidebar("success");
  try {
    await waitFor(cdp, `(${sidebarDocumentReadyExpression("success")}) && Boolean(document.querySelector('#tabs button[data-tab=\"materials\"]:not([disabled])'))`, "sidebar standard overlay did not complete the official JSSDK handshake", "success_handshake");
  } catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), document: await evaluate(cdp, "document.body ? 'ready' : 'missing'"), tabs: await evaluate(cdp, "Boolean(document.querySelector('#tabs'))"), host: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarHost-/.test(path) && status === 200), overlay: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarStandardOverlay-/.test(path) && status === 200), bootstrap: bootstrapCountSince(successStart), jssdk: jssdkCountSince(successStart), cspBlob: sidebarCSP.includes("img-src 'self' data: blob:"), exceptions });
    throw new Error(`sidebar standard overlay did not complete official JSSDK handshake: ${diagnostic}`);
  }
  const successCalls = await bridgeCalls();
  const successAgentAPIs = JSON.parse(await evaluate(cdp, "JSON.stringify(globalThis.__sidebarAgentAPIs || [])"));
  if (successCalls.join("|") !== "preVerifyJSAPI|agentConfig|getCurExternalContact" || JSON.stringify(successAgentAPIs) !== JSON.stringify(["getCurExternalContact", "sendChatMessage"]) || bootstrapCountSince(successStart) !== 1) throw new Error(`official JSSDK success order/API mismatch: ${JSON.stringify({ calls: successCalls, agentAPIs: successAgentAPIs })}`);
  for (const width of [320, 375, 430, 768]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
    const geometry = JSON.parse(await evaluate(cdp, 'JSON.stringify((()=>{const t=[...document.querySelectorAll("#tabs [data-tab]")],r=t.map(n=>n.getBoundingClientRect());return {v:innerWidth,c:document.documentElement.clientWidth,s:document.documentElement.scrollWidth,n:t.length,rows:new Set(r.map(x=>Math.round(x.top))).size,cols:new Set(r.slice(0,3).map(x=>Math.round(x.left))).size,in:r.every(x=>x.left>=0&&x.right<=innerWidth+.5)}})())'));
    if (geometry.v!==width || geometry.s>geometry.c || geometry.n!==6 || geometry.rows!==2 || geometry.cols!==3 || !geometry.in) throw new Error("geometry "+width+" "+JSON.stringify(geometry));
  }
  await cdp.call("Emulation.setDeviceMetricsOverride", { width:375, height:900, deviceScaleFactor:1, mobile:false });
  await captureScreenshot(cdp, "profile-375");
  await cdp.call("Emulation.setDeviceMetricsOverride", { width:430, height:900, deviceScaleFactor:1, mobile:false });
  const profileStart=requestRecords.length;
  await evaluate(cdp, '(()=>{const f=document.querySelector("[data-profile-field=source]");f.value="Chromium活动报名";f.dispatchEvent(new Event("input",{bubbles:true}));return true})()');
  for(let n=0;n<140&&requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile").length<1;n++) await delay(50);
  let writes=requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile"), body=JSON.parse(writes[0]?.postData||"{}");
  if(writes.length!==1||body.expected_profile_version!==0||body.source!=="Chromium活动报名") throw new Error("profile CAS 0 "+JSON.stringify(writes));
  await evaluate(cdp, '(()=>{const f=document.querySelector("[data-profile-field=industry]");f.value="教育";f.dispatchEvent(new Event("input",{bubbles:true}));return true})()');
  for(let n=0;n<140&&requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile").length<2;n++) await delay(50);
  writes=requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile");body=JSON.parse(writes[1]?.postData||"{}");
  if(writes.length!==2||body.expected_profile_version!==1||body.industry!=="教育") throw new Error("profile CAS 1 "+JSON.stringify(writes));
  const surveyStart=requestRecords.length;
  await evaluate(cdp, 'document.querySelector("#tabs [data-tab=questionnaires]").click();true');
  await waitFor(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length===20', "survey page 1");
  while(await evaluate(cdp, 'Boolean(document.querySelector("[data-load-more-questionnaires]"))')){
    const before=await evaluate(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length');
    await evaluate(cdp, 'document.querySelector("[data-load-more-questionnaires]").click();true');
    await waitFor(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length>'+before, "survey cursor append");
  }
  const surveyCount=await evaluate(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length');
  const surveyReads=requestRecords.slice(surveyStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/questionnaires");
  if(surveyCount!==102||surveyReads.length!==6||surveyReads.slice(1).some(r=>!new URL(r.url).searchParams.get("cursor"))||surveyReads.some(r=>new URL(r.url).searchParams.has("offset"))) throw new Error("survey 102 cursor "+surveyCount+" "+JSON.stringify(surveyReads));
  await evaluate(cdp, 'document.querySelector("#tabs [data-tab=coupons]").click();true');
  await waitFor(cdp, 'document.body.textContent.includes("Chromium无链接券")', "coupon fixtures");
  const coupons=JSON.parse(await evaluate(cdp, 'JSON.stringify({buttons:[...document.querySelectorAll("[data-copy-url]")].map(b=>({disabled:b.disabled,url:b.dataset.copyUrl,text:b.closest(".card")?.textContent||""})),text:document.body.textContent})'));
  for(const x of ["可前往领取页确认","未到领取时间","领取已截止","已领完","已达到个人领取上限"]) if(!coupons.text.includes(x)) throw new Error("coupon state "+x);
  const active=coupons.buttons.find(x=>x.text.includes("Chromium可领取券"));
  if(!active||active.disabled||!active.url.endsWith("/c/chromium-active")||coupons.buttons.length!==6||coupons.buttons.some(x=>x.disabled!==!x.url)) throw new Error("coupon buttons "+JSON.stringify(coupons));
  await evaluate(cdp, 'document.querySelector("[data-copy-url$=\"/c/chromium-active\"]").click();true');
  await waitFor(cdp, 'globalThis.__sidebarClipboardWrites.length===1', "coupon clipboard");
  if(await evaluate(cdp, 'globalThis.__sidebarClipboardWrites[0]')!==active.url) throw new Error("coupon URL");
  await evaluate(cdp, 'document.querySelector("#tabs [data-tab=orders]").click();true');
  await waitFor(cdp, 'document.body.textContent.includes("SIDEBAR-ORDER-001")', "order fixture");
  const regular=await evaluate(cdp, 'document.getElementById("content").textContent');
  if(!regular.includes("¥99.00")||!regular.includes("已退款")||regular.includes("支付时间")) throw new Error("order facts "+regular);
  await captureScreenshot(cdp, "orders-430");
  await evaluate(cdp, 'document.querySelector("[data-order-type=periodic]").click();true');
  await waitFor(cdp, 'document.body.textContent.includes("31日真实服务周期")', "periodic fixture");
  const periodic=await evaluate(cdp, 'document.getElementById("content").textContent');
  if(!periodic.includes("浏览器周期外推商品")||!periodic.includes("生效时间")||!periodic.includes("到期时间")) throw new Error("periodic facts "+periodic);
  const materialStart = requestRecords.length;
  await evaluate(cdp, "document.querySelector('#tabs button[data-tab=\"materials\"]')?.click(); true");
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===5', "material first page did not render five standard cards");
  await evaluate(cdp, '(()=>{const old=document.querySelector(".material-page-sentinel");if(!old)throw new Error("initial material pager missing");old.dataset.preSearchPager="true";const input=document.querySelector("[data-material-search-input]");input.value="Chromium";document.querySelector("[data-material-search-form]").requestSubmit();return true})()');
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===5&&Boolean(document.querySelector(".material-page-sentinel:not([data-pre-search-pager])"))', "filtered material first page did not replace the standard pager");
  await cdp.call("Input.dispatchMouseEvent", { type: "mouseWheel", x: 215, y: 820, deltaX: 0, deltaY: 700 });
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===7', "standard image loader did not append material offset 5");
  const materialReads=requestRecords.slice(materialStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/materials").map(r=>new URL(r.url));
  const filteredReads=materialReads.filter(url=>url.searchParams.get("q")==="Chromium");
  if(filteredReads.length!==2||filteredReads[0].searchParams.get("offset")!=="0"||filteredReads[1].searchParams.get("offset")!=="5"||filteredReads.some(url=>url.searchParams.get("limit")!=="5"||url.searchParams.has("type"))) throw new Error("material standard pager query "+JSON.stringify(materialReads.map(url=>url.pathname+url.search)));
  const ready = "(() => { const images=[...document.querySelectorAll('img[data-material-preview=\"ready\"]')]; return images.length===7 && images.every(image => image.src.startsWith('blob:') && image.complete && image.naturalWidth === 1 && image.naturalHeight === 1); })()";
  try { await waitFor(cdp, ready, "sidebar thumbnails did not load through blob URLs for both standard pages"); }
  catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), host: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarHost-/.test(path) && status === 200), bootstrap: resources.get("/api/sidebar/v2/bootstrap") || 0, materials: resources.get("/api/sidebar/v2/materials") || 0, thumbnail: [...resources.entries()].some(([path, status]) => /variants\/thumb_320$/.test(path) && status === 200), cspBlob: sidebarCSP.includes("img-src 'self' data: blob:"), exceptions });
    throw new Error(`sidebar thumbnail did not render: ${diagnostic}`);
  }
  await captureScreenshot(cdp, "materials-430");
  if (!sidebarCSP.includes("img-src 'self' data: blob:")) throw new Error("sidebar CSP did not permit its scoped thumbnail blob URL");
  if (![...resources.entries()].some(([pathname, status]) => /^\/sidebar-assets\/sidebarHost-/.test(pathname) && status === 200) || ![...resources.entries()].some(([pathname, status]) => /^\/sidebar-assets\/sidebarStandardOverlay-/.test(pathname) && status === 200) || ![...resources.entries()].some(([pathname, status]) => /^\/sidebar-assets\/sidebarImageResourceLoader-/.test(pathname) && status === 200) || resources.get("/api/sidebar/v2/bootstrap") !== 200 || resources.get("/api/sidebar/v2/materials") !== 200 || ![...resources.entries()].some(([pathname, status]) => /\/variants\/thumb_320$/.test(pathname) && status === 200)) throw new Error("sidebar Host/standard overlay resources did not use the actual scoped thumbnail route");
  if (requestURLs.slice(successStart).some((url) => /\/(other-staff-messages|chat-activity|chat_activity)(?:[/?]|$)/.test(new URL(url).pathname))) throw new Error("sidebar standard overlay attempted a removed chat route");
  await evaluate(cdp, '(()=>{localStorage.setItem("sidebar_tab","chat_activity");sessionStorage.setItem("sidebar_active_tab","other_staff_messages");return true})()');
  const negativeStart=requestURLs.length;
  await cdp.call("Page.navigate",{url:baseURL+"/sidebar/bind-mobile?sidebar_case=success&tab=chat_activity&view=other_staff_messages#other-staff-messages"});
  await waitFor(cdp, 'Boolean(document.querySelector("#tabs button[data-tab=materials]:not([disabled])"))', "chat deeplink ready");
  const negative=JSON.parse(await evaluate(cdp, 'JSON.stringify({tabs:[...document.querySelectorAll("#tabs [data-tab]")].map(n=>n.dataset.tab),text:document.body.textContent})'));
  if(negative.tabs.join("|")!=="profile|questionnaires|products|orders|coupons|materials"||/其他客服聊天|聊天动态/.test(negative.text)||requestURLs.slice(negativeStart).some(url=>{const pathname=new URL(url).pathname;return pathname.includes("other-staff-messages")||pathname.includes("chat-activity")||pathname.includes("chat_activity")})) throw new Error("chat deeplink "+JSON.stringify(negative));
  if (exceptions.length) throw new Error(`sidebar Host emitted runtime exceptions: ${exceptions.join(",")}`);
  console.log("sidebar_thumbnail_chromium: PASS");
} catch (error) { failed = true; throw error; }
finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); }
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete");
}
