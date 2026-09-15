import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { chromiumStartupDiagnostic } from "../../internal/webshell/chromium_launch.mjs";

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

const baseURL = process.env.AICRM_ACCESS_UI_TEST_URL;
const screenshots = process.env.AICRM_ACCESS_UI_SCREENSHOT_DIR;
const credentials = {
  super: [process.env.AICRM_ACCESS_UI_SUPER_USERNAME, process.env.AICRM_ACCESS_UI_SUPER_PASSWORD],
  admin: [process.env.AICRM_ACCESS_UI_ADMIN_USERNAME, process.env.AICRM_ACCESS_UI_ADMIN_PASSWORD],
  viewer: [process.env.AICRM_ACCESS_UI_VIEWER_USERNAME, process.env.AICRM_ACCESS_UI_VIEWER_PASSWORD],
};
if (!/^https:\/\//.test(baseURL || "") || !screenshots || Object.values(credentials).flat().some((value) => !value)) throw new Error("Access UI Chromium journey environment is incomplete");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const accessChromiumStartupTimeoutMS = 8_000;
const browserBinary = () => {
  const choices = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean);
  for (const candidate of choices) { try { if (candidate.includes("/") ? spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0 : spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {} }
  throw new Error("Chromium binary is unavailable");
};
class CDP {
  constructor(socket) { this.socket = socket; this.id = 0; this.pending = new Map(); socket.addEventListener("message", (event) => { const message = JSON.parse(String(event.data)); const pending = this.pending.get(message.id); if (!pending) return; this.pending.delete(message.id); message.error ? pending.reject(new Error(`CDP ${message.error.code}`)) : pending.resolve(message.result || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP ${method} timed out`)); }, 8000); this.pending.set(id, { resolve: (value) => { clearTimeout(timer); resolve(value); }, reject: (error) => { clearTimeout(timer); reject(error); } }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { this.socket.close(); }
}
async function address(profile, processState) {
  const deadline = Date.now() + accessChromiumStartupTimeoutMS;
  while (Date.now() < deadline) {
    try {
      const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    const state = processState();
    if (state.launchError || state.exitCode !== null || state.signalCode) {
      throw new Error(chromiumStartupDiagnostic({ ...state, profile, timeoutMS: accessChromiumStartupTimeoutMS }));
    }
    await sleep(50);
  }
  throw new Error(chromiumStartupDiagnostic({ ...processState(), profile, timeoutMS: accessChromiumStartupTimeoutMS }));
}
async function evaluate(cdp, expression) { const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; }
async function waitFor(cdp, expression, message) { for (let index = 0; index < 180; index += 1) { if (await evaluate(cdp, expression)) return; await sleep(50); } throw new Error(message); }
const cookiesFrom = (response) => typeof response.headers.getSetCookie === "function" ? response.headers.getSetCookie() : [];
async function login(cdp, user) {
  const [username, password] = credentials[user];
  const page = await fetch(`${baseURL}/login`, { redirect: "manual" });
  const html = await page.text(); const match = /name="login_csrf_token" value="([^"]+)"/.exec(html);
  if (!match) throw new Error(`${user} login CSRF was unavailable`);
  const initialCookies = cookiesFrom(page); const form = new URLSearchParams({ username, password, login_csrf_token: match[1] });
  const response = await fetch(`${baseURL}/login`, { method: "POST", redirect: "manual", headers: { "Content-Type": "application/x-www-form-urlencoded", Cookie: initialCookies.map((item) => item.split(";", 1)[0]).join("; ") }, body: form });
  if (response.status !== 303) throw new Error(`${user} login status=${response.status}`);
  for (const raw of cookiesFrom(response)) { const [pair] = raw.split(";", 1); const separator = pair.indexOf("="); if (separator < 1) continue; await cdp.call("Network.setCookie", { url: baseURL, name: pair.slice(0, separator), value: pair.slice(separator + 1), secure: true }); }
}
async function waitForAccessUI(cdp, user) {
  await waitFor(cdp, `location.pathname === '/admin/config/login-access' && Boolean(document.querySelector('[data-admin-access-root]'))`, `${user} did not reach Access UI`);
  await waitFor(cdp, "!document.querySelector('#admin-access-loading') || document.querySelector('#admin-access-loading').hidden", `${user} Access UI did not finish loading`);
}
async function openAccessFromConfig(cdp, user) {
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/config` });
  await waitFor(cdp, "location.pathname === '/admin/config' && Boolean(document.querySelector('[data-runtime-release-host]')) && Boolean(document.querySelector('[data-category-row=\"admin_access\"] a.cc-btn'))", `${user} Config center did not render the backend access link`);
  const target = await evaluate(cdp, "document.querySelector('[data-category-row=\"admin_access\"] a.cc-btn')?.getAttribute('href')");
  if (target !== "/admin/config/login-access") throw new Error(`${user} Config center access link=${target}`);
  await evaluate(cdp, "document.querySelector('[data-category-row=\"admin_access\"] a.cc-btn').click(); true");
  await waitForAccessUI(cdp, user);
}
async function openLegacyAccess(cdp, user) {
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/admin-access?journey_as=${encodeURIComponent(user)}` });
  await waitFor(cdp, `location.pathname === '/admin/config/login-access' && location.search.includes('journey_as=${user}')`, `${user} legacy access URL did not canonicalize`);
  await waitForAccessUI(cdp, user);
}
async function screenshot(cdp, width, filename) {
  await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
  await evaluate(cdp, "(() => { const root=document.querySelector('[data-admin-access-root]'); if (root) window.scrollTo({top: Math.max(0, root.getBoundingClientRect().top + window.scrollY - 80)}); })()");
  await sleep(150);
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  await fs.writeFile(path.join(screenshots, filename), Buffer.from(image.data, "base64"));
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-access-ui-chromium-"));
let child; let cdp; let chromeLaunchError; let chromeStderr = "";
try {
  child = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
  child.once("error", (error) => { chromeLaunchError = error; });
  child.stderr?.on("data", (chunk) => { chromeStderr = (chromeStderr + String(chunk)).slice(-1024); });
  const page = await (await fetch(`${await address(profile, () => ({ exitCode: child?.exitCode ?? null, signalCode: child?.signalCode ?? null, launchError: chromeLaunchError, stderr: chromeStderr }))}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("CDP page connection failed")), { once: true }); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
  await login(cdp, "super");
  await openAccessFromConfig(cdp, "super");
  await openLegacyAccess(cdp, "super");
  await waitFor(cdp, "document.querySelector('#admin-access-provision')?.hidden === false && document.querySelector('#admin-access-super')?.hidden === false && document.querySelector('#admin-access-super-title')?.textContent.includes('超级管理员甲') && document.querySelector('#admin-access-super-detail')?.textContent.includes('SuperFixtureID')", "super controls or bound enterprise identity did not render");
  if (!await evaluate(cdp, "(() => { const row=[...document.querySelectorAll('#admin-access-users-body tr')].find((item) => item.textContent.includes('SuperFixtureID')); const value=row?.querySelector('td[data-label=\"最近登录\"]')?.textContent.trim() || ''; return /^\\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}:\\d{2}$/.test(value); })()")) throw new Error("last login was not rendered as a Shanghai business time");
  await screenshot(cdp, 1440, "access-governance-1440.png");
  await screenshot(cdp, 1280, "access-governance-1280.png");
  await screenshot(cdp, 780, "access-governance-780.png");
  await evaluate(cdp, "document.querySelector('#admin-access-provision').click(); true");
  await waitFor(cdp, "document.querySelectorAll('#admin-access-employee-results button[data-wecom-userid]').length > 0", "initial authorized employee directory did not render");
  const employeeCandidate = await evaluate(cdp, "(() => { const input=document.querySelector('#admin-access-employee-search'); input.value='UnavailableID'; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true})); input.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'}); Object.defineProperty(event,'keyCode',{value:229}); input.dispatchEvent(event); return event.defaultPrevented; })()");
  if (employeeCandidate) throw new Error('employee directory IME candidate Enter was consumed as a search');
  await sleep(350);
  if (await evaluate(cdp, "document.querySelector('#admin-access-employee-search-status')?.textContent.includes('暂时不可用')")) throw new Error('employee directory IME candidate Enter issued the unavailable directory read');
  await evaluate(cdp, "(() => { const input=document.querySelector('#admin-access-employee-search'); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'}); input.dispatchEvent(event); return event.defaultPrevented; })()");
  await waitFor(cdp, "document.querySelector('#admin-access-employee-search-status')?.textContent.includes('暂时不可用') && document.querySelector('#admin-access-employee-results')?.textContent.includes('CandidateCaseID')", "directory 503 did not preserve the previous authorized employee result");
  await evaluate(cdp, "(() => { const input=document.querySelector('#admin-access-employee-search'); input.value='CandidateCaseID'; input.dispatchEvent(new Event('input', {bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'})); })()");
  await waitFor(cdp, "document.querySelectorAll('#admin-access-employee-results button[data-wecom-userid]').length === 1", "enterprise employee candidate did not render");
  await evaluate(cdp, "document.querySelector('#admin-access-employee-results button[data-wecom-userid]').click(); document.querySelector('#admin-access-provision-next').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-provision-role-step').hidden === false", "role step did not render");
  await evaluate(cdp, "document.querySelector('input[name=\"provision-role\"][value=\"viewer\"]').click(); document.querySelector('#admin-access-provision-next').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-provision-confirm-step').hidden === false && document.querySelector('#admin-access-provision-confirm-step').textContent.includes('CandidateCaseID')", "provision confirmation did not retain exact enterprise account casing");
  await screenshot(cdp, 780, "access-governance-provision-confirm.png");
  await screenshot(cdp, 420, "access-governance-provision-420.png");
  await evaluate(cdp, "document.querySelector('#admin-access-provision-submit').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-alert')?.textContent.includes('员工已开通') && document.querySelector('#admin-access-users-body')?.textContent.includes('CandidateCaseID')", "provision was not persisted and rendered through the real page");
  await screenshot(cdp, 390, "access-governance-390.png");
  const narrow = await evaluate(cdp, "(() => { const row=document.querySelector('#admin-access-users-body tr'); return row && getComputedStyle(row).display === 'grid' && document.querySelector('#admin-access-users-body td[data-label=\"员工\"]') !== null && document.documentElement.scrollWidth <= 390; })()");
  if (!narrow) throw new Error("390px Access UI did not use the narrow employee row layout");
  await evaluate(cdp, "document.querySelector('#admin-access-provision').click(); true");
  await evaluate(cdp, "(() => { const input=document.querySelector('#admin-access-employee-search'); input.value='CandidateCaseID'; input.dispatchEvent(new Event('input', {bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'})); })()");
  await waitFor(cdp, "(() => { const choice=document.querySelector('#admin-access-employee-results button[data-wecom-userid]'); return choice?.disabled === true && choice.textContent.includes('已开通'); })()", "already authorized enterprise employee was available for duplicate provisioning");
  await evaluate(cdp, "document.querySelector('#admin-access-provision-close').click(); true");
  await evaluate(cdp, "document.querySelector('#admin-access-provision').click(); true");
  await evaluate(cdp, "(() => { const input=document.querySelector('#admin-access-employee-search'); input.value='CandidateCaseID'; input.dispatchEvent(new Event('input', {bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'})); input.value='SecondCandidateID'; input.dispatchEvent(new Event('input', {bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'})); })()");
  await waitFor(cdp, "document.querySelector('#admin-access-employee-search-status')?.textContent.includes('已显示 1') && document.querySelector('#admin-access-employee-results')?.textContent.includes('SecondCandidateID') && !document.querySelector('#admin-access-employee-results')?.textContent.includes('CandidateCaseID') && document.querySelectorAll('#admin-access-employee-results button[data-wecom-userid]:not(:disabled)').length === 1", "rapid employee search did not retain the final query result");
  await evaluate(cdp, "document.querySelector('#admin-access-employee-results button[data-wecom-userid]:not(:disabled)').click(); document.querySelector('#admin-access-provision-next').click(); true");
  await sleep(100);
  const secondSelection = await evaluate(cdp, "(() => ({nextDisabled:document.querySelector('#admin-access-provision-next')?.disabled, employeeHidden:document.querySelector('#admin-access-employee-step')?.hidden, roleHidden:document.querySelector('#admin-access-provision-role-step')?.hidden, results:document.querySelector('#admin-access-employee-results')?.textContent || ''}))()");
  await waitFor(cdp, "document.querySelector('#admin-access-provision-role-step').hidden === false", `second provision role step did not render ${JSON.stringify(secondSelection)}`);
  await evaluate(cdp, "document.querySelector('input[name=\"provision-role\"][value=\"viewer\"]').click(); document.querySelector('#admin-access-provision-next').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-provision-confirm-step').hidden === false && document.querySelector('#admin-access-provision-confirm-step').textContent.includes('SecondCandidateID')", "second provision confirmation did not retain exact enterprise account casing");
  await evaluate(cdp, "document.querySelector('#admin-access-provision-submit').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-alert')?.textContent.includes('员工已开通') && document.querySelector('#admin-access-users-body')?.textContent.includes('SecondCandidateID') && document.querySelector('#admin-access-provision-submit')?.disabled === false", "second continuous provisioning did not restore its submit control");
  await login(cdp, "admin");
  await openAccessFromConfig(cdp, "admin");
  await waitFor(cdp, "document.querySelector('#admin-access-provision')?.hidden === false", "admin viewer-provision action was not rendered from server capability");
  const opened = await evaluate(cdp, "(() => { const button=document.querySelector('#admin-access-users-body button[data-access-action=\"manage\"]'); if (!button) return false; button.click(); return true; })()");
  if (!opened) { const state = await evaluate(cdp, "({rows:document.querySelector('#admin-access-users-body')?.textContent || '',notice:document.querySelector('#admin-access-no-permission')?.hidden})"); throw new Error(`admin did not receive a server-authorized management row ${JSON.stringify(state)}`); }
  await sleep(100);
  const drawerState = await evaluate(cdp, "(() => { const drawer=document.querySelector('#admin-access-drawer'); const button=document.querySelector('#admin-access-users-body button[data-access-action=\"manage\"]'); return {drawerHidden:drawer?.hidden, drawerHTML:drawer?.outerHTML.slice(0,180), button:button?.outerHTML, rows:document.querySelector('#admin-access-users-body')?.textContent || ''}; })()");
  await waitFor(cdp, "document.querySelector('#admin-access-drawer')?.hidden === false", `admin management drawer did not open ${JSON.stringify(drawerState)}`);
  if (await evaluate(cdp, "document.querySelector('#admin-access-advanced-panel')?.hidden === false || document.querySelector('#admin-access-password-form')?.hidden === false")) throw new Error("admin received super-only advanced actions");
  await screenshot(cdp, 780, "access-governance-admin-drawer.png");
  await evaluate(cdp, "document.querySelector('#admin-access-drawer-actions button[data-access-action=\"toggle-login\"]').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-alert')?.textContent.includes('已停用') && document.querySelector('#admin-access-drawer-actions button[data-access-action=\"toggle-login\"]')?.disabled === false", "first login-state action did not restore its control");
  await evaluate(cdp, "document.querySelector('#admin-access-drawer-actions button[data-access-action=\"toggle-login\"]').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-alert')?.textContent.includes('已启用') && document.querySelector('#admin-access-drawer-actions button[data-access-action=\"toggle-login\"]')?.disabled === false", "second login-state action did not restore its control");
  await login(cdp, "super");
  await openAccessFromConfig(cdp, "super");
  await evaluate(cdp, "document.querySelector('#admin-access-transfer').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-transfer-dialog')?.hidden === false && document.querySelector('#admin-access-transfer-target')?.options.length > 0", "super transfer confirmation did not render");
  if (!await evaluate(cdp, "document.querySelector('#admin-access-transfer-target')?.selectedOptions[0]?.textContent.includes('AdminFixtureID')")) throw new Error("super transfer did not offer the active administrator as its exact target");
  await screenshot(cdp, 780, "access-governance-transfer-confirm.png");
  await evaluate(cdp, "document.querySelector('#admin-access-transfer-submit').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-alert')?.textContent.includes('超级管理员已转移') && document.querySelector('#admin-access-super')?.hidden === true && document.querySelector('#admin-access-users-body')?.children.length === 0 && document.querySelector('#admin-access-list-status')?.textContent.includes('重新登录')", "super transfer was not submitted through the real page or did not clear the fenced old session view");
  await screenshot(cdp, 780, "access-governance-transfer-complete.png");

  // The former owner must read back as a normal administrator after a new
  // login. Its DOM may offer only viewer provisioning and viewer login state;
  // it must never recover super-only controls from the prior page session.
  await login(cdp, "super");
  await openAccessFromConfig(cdp, "super");
  await waitFor(cdp, "document.querySelector('#admin-access-super-title')?.textContent.includes('管理员甲') && document.querySelector('#admin-access-super-detail')?.textContent.includes('AdminFixtureID') && document.querySelector('#admin-access-transfer')?.hidden === true && document.querySelector('#admin-access-provision')?.hidden === false", "former super did not read back as administrator after a fresh login");
  await evaluate(cdp, "document.querySelector('#admin-access-provision').click(); true");
  await waitFor(cdp, "document.querySelector('#admin-access-provision-dialog')?.hidden === false && document.querySelector('#admin-access-employee-results')?.textContent.length > 0", "administrator provisioning dialog did not render");
  if (!await evaluate(cdp, "document.querySelector('[data-role-option=\"admin\"]')?.hidden === true && document.querySelector('[data-role-option=\"viewer\"]')?.hidden === false")) throw new Error("administrator received an admin-provision role option");
  await evaluate(cdp, "document.querySelector('#admin-access-provision-close').click(); true");
  const formerOwnerManage = await evaluate(cdp, "(() => { const row=[...document.querySelectorAll('#admin-access-users-body tr')].find((item) => item.textContent.includes('ViewerFixtureID')); const button=row?.querySelector('button[data-access-action=\"manage\"]'); if (!button) return false; button.click(); return true; })()");
  if (!formerOwnerManage) throw new Error("administrator did not retain viewer-only management after the transfer");
  await waitFor(cdp, "document.querySelector('#admin-access-drawer')?.hidden === false && document.querySelector('#admin-access-role-panel')?.hidden === true && document.querySelector('#admin-access-advanced-panel')?.hidden === true && document.querySelector('#admin-access-drawer-actions button[data-access-action=\"toggle-login\"]') !== null", "administrator drawer exposed role or super-only controls after the transfer");

  // The transfer target receives super controls only after it starts a new
  // session. Check the unique top card and the privileged drawer separately.
  await login(cdp, "admin");
  await openAccessFromConfig(cdp, "admin");
  await waitFor(cdp, "document.querySelector('#admin-access-super-title')?.textContent.includes('管理员甲') && document.querySelector('#admin-access-super-detail')?.textContent.includes('AdminFixtureID') && document.querySelector('#admin-access-transfer')?.hidden === false && document.querySelector('#admin-access-provision')?.hidden === false", "transfer target did not read back as the unique super administrator after a fresh login");
  const newOwnerManage = await evaluate(cdp, "(() => { const row=[...document.querySelectorAll('#admin-access-users-body tr')].find((item) => item.textContent.includes('SuperFixtureID')); const button=row?.querySelector('button[data-access-action=\"manage\"]'); if (!button) return false; button.click(); return true; })()");
  if (!newOwnerManage) throw new Error("new super administrator could not manage the former owner");
  await waitFor(cdp, "document.querySelector('#admin-access-drawer')?.hidden === false && document.querySelector('#admin-access-role-panel')?.hidden === false && document.querySelector('#admin-access-advanced-panel')?.hidden === false && document.querySelector('#admin-access-binding-form')?.hidden === false && document.querySelector('#admin-access-password-form')?.hidden === false", "new super administrator did not receive the expected privileged DOM controls");
  await login(cdp, "viewer");
  await openAccessFromConfig(cdp, "viewer");
  await sleep(100);
  const viewerState = await evaluate(cdp, "(() => ({noPermissionHidden:document.querySelector('#admin-access-no-permission')?.hidden, listErrorHidden:document.querySelector('#admin-access-list-error')?.hidden, listError:document.querySelector('#admin-access-list-error-message')?.textContent || '', provisionHidden:document.querySelector('#admin-access-provision')?.hidden, rows:document.querySelector('#admin-access-users-body')?.textContent || '', actions:[...document.querySelectorAll('#admin-access-users-body button[data-access-action]')].map((button) => ({action:button.dataset.accessAction,id:button.dataset.userId}))}))()");
  await waitFor(cdp, "document.querySelector('#admin-access-list-error')?.hidden === false && document.querySelector('#admin-access-list-error-message')?.textContent.includes('权限')", `viewer permission-denied state was not rendered ${JSON.stringify(viewerState)}`);
  if (await evaluate(cdp, "document.querySelector('#admin-access-provision')?.hidden === false || document.querySelector('#admin-access-users-body button[data-access-action=\"manage\"]') !== null")) throw new Error("viewer received a management action");
  console.log("access_governance_ui_chromium: PASS");
} finally {
  if (cdp) cdp.close();
  if (child && child.exitCode === null) { child.kill("SIGTERM"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), sleep(3000)]); if (child.exitCode === null) child.kill("SIGKILL"); }
  await fs.rm(profile, { recursive: true, force: true, maxRetries: 2 }).catch(() => {});
}
