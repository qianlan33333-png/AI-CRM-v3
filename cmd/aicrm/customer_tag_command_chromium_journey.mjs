import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_CUSTOMER_TAG_TEST_URL;
const username = process.env.AICRM_CUSTOMER_TAG_TEST_USERNAME;
const password = process.env.AICRM_CUSTOMER_TAG_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) throw new Error("customer tag Chromium journey requires HTTPS URL and test credentials");
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const candidates = () => {
  const explicit = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") explicit.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  return [...explicit, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];
};
function browserBinary() {
  for (const candidate of candidates()) {
    if (candidate.includes("/")) { try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {} }
    else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
  }
  throw new Error("Chromium binary is unavailable");
}
class CDP {
  constructor(socket) { this.socket = socket; this.nextID = 0; this.pending = new Map(); this.events = new Map(); socket.addEventListener("message", (event) => { const message = JSON.parse(String(event.data)); if (message.id && this.pending.has(message.id)) { const pending = this.pending.get(message.id); this.pending.delete(message.id); message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {}); return; } for (const listener of this.events.get(message.method) || []) listener(message.params || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  on(method, listener) { const list = this.events.get(method) || []; list.push(listener); this.events.set(method, list); }
  close() { for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed")); this.pending.clear(); this.socket.close(); }
}
async function port(profile) { for (let i = 0; i < 160; i += 1) { try { const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`; } catch (_) {} await delay(50); } throw new Error("Chromium remote debugging did not become ready"); }
async function evaluate(cdp, expression) { const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; }
async function waitFor(cdp, expression, message) { for (let i = 0; i < 180; i += 1) { if (await evaluate(cdp, expression)) return; await delay(50); } throw new Error(message); }
async function browserExit(child) { if (!child || child.exitCode !== null || child.signalCode !== null) return; await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]); if (child.exitCode === null && child.signalCode === null) { child.kill("SIGKILL"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(1000)]); } }
async function removeProfile(profile) { for (let i = 0; i < 40; i += 1) { try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; } catch (error) { if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false; await delay(100); } } return false; }
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-customer-tags-chromium-"));
let browser; let cdp; let failed = false;
const screenshotDir = process.env.AICRM_CUSTOMER_TAG_SCREENSHOT_DIR;
const captureSectionError = process.env.AICRM_CUSTOMER_TAG_CAPTURE_SECTION_ERROR === "1";
async function captureCustomerScreenshot(name, width) {
  if (!screenshotDir || !cdp) return;
  await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 1100, deviceScaleFactor: 1, mobile: false });
  await delay(100);
  const shot = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  await fs.mkdir(screenshotDir, { recursive: true });
  await fs.writeFile(path.join(screenshotDir, name), Buffer.from(shot.data, "base64"));
}
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const created = await (await fetch(`${await port(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const exceptions = []; const resources = new Map(); let customerListResponses = 0;
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  const requiredResources = ["/static/admin_console/admin_customers.js", "/assets/standard-components/standard_components_host.js", "/assets/standard-components/wecom_tag_picker.js", "/api/admin/customers", "/api/admin/wecom/tags"];
  cdp.on("Network.responseReceived", (params) => { try { const pathname = new URL(String(params.response?.url || "")).pathname; if (pathname === "/api/admin/customers") customerListResponses += 1; if ([...requiredResources, "/api/v1/customer-tag-commands/preview", "/api/v1/customer-tag-commands"].includes(pathname)) resources.set(pathname, Number(params.response?.status) || 0); } catch (_) {} });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin%2Fcustomers` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/customers' && Boolean(document.querySelector('[data-customer-directory-root]'))", "login did not load the customer Host route");
  const diagnostic = async () => JSON.stringify({ path: await evaluate(cdp, "location.pathname"), rows: await evaluate(cdp, "document.querySelectorAll('#customer-list-body input[type=checkbox]').length"), resources: Object.fromEntries(resources), exceptions });
  try { await waitFor(cdp, "document.querySelectorAll('#customer-list-body input[type=checkbox]').length >= 2 && document.querySelectorAll('#customer-tag-batch option').length >= 2 && typeof window.AICRMWeComTagPicker?.open === 'function' && typeof window.AICRMTagPicker?.open === 'function'", "customer list, tag catalog, or V3 picker did not load"); } catch (_) { throw new Error(`customer Host did not load: ${await diagnostic()}`); }
  if (requiredResources.some((pathname) => resources.get(pathname) !== 200)) throw new Error(`customer Host release assets did not load: ${await diagnostic()}`);
  // The customer query remains this Host's native form-submit interaction.
  // A Chinese IME candidate Enter must commit composition only; the next plain
  // Enter is the explicit query and may replace the list.
  const requestsBeforeComposition = customerListResponses;
  await evaluate(cdp, "document.querySelector('#customer-list-filters [name=keyword]').focus(); true");
  await cdp.call("Input.imeSetComposition", { text: "候选客户", selectionStart: 4, selectionEnd: 4, replacementStart: 0, replacementEnd: 0 });
  await cdp.call("Input.dispatchKeyEvent", { type: "rawKeyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await delay(150);
  if (customerListResponses !== requestsBeforeComposition) throw new Error(`IME candidate Enter submitted the customer search: ${await diagnostic()}`);
  await cdp.call("Input.insertText", { text: "候选客户" });
  await evaluate(cdp, "document.querySelector('#customer-list-filters [name=keyword]').focus(); true");
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", text: "\r", unmodifiedText: "\r", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  for (let attempt = 0; attempt < 20 && customerListResponses <= requestsBeforeComposition; attempt += 1) await delay(100);
  if (customerListResponses <= requestsBeforeComposition) throw new Error(`plain Enter did not request the customer search: ${await diagnostic()}`);
  await evaluate(cdp, "document.querySelector('#customer-list-clear').click(); true");
  await waitFor(cdp, "document.querySelectorAll('#customer-list-body input[type=checkbox]').length >= 2", "clearing the IME search did not restore the customer selection fixture");
  // Exercise the same V3 dialog that the rendered customer form exposes, then
  // submit through the original preview-and-confirm command. The picker edits
  // only the two form drafts; it must not replace the durable command path.
  await evaluate(cdp, `(() => { const form=document.querySelector('#customer-tag-batch'); const add=form.querySelector('[name="add_tag_ids"]'); const button=add.parentElement.querySelector('button'); button.click(); return true; })()`);
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]').length >= 2", "V3 add-tag picker did not render the real catalog");
  await evaluate(cdp, "document.querySelectorAll('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]')[0].click(); document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-confirm]').click(); true");
  await waitFor(cdp, "(() => { const select=document.querySelector('#customer-tag-batch [name=\"add_tag_ids\"]'); return select?.selectedOptions[0]?.value === select?.options[0]?.value; })()", "V3 add-tag picker did not update the original form draft");
  await evaluate(cdp, `(() => { const form=document.querySelector('#customer-tag-batch'); const remove=form.querySelector('[name="remove_tag_ids"]'); const button=remove.parentElement.querySelector('button'); button.click(); return true; })()`);
  await waitFor(cdp, "document.querySelectorAll('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]').length >= 2", "V3 remove-tag picker did not render the real catalog");
  await evaluate(cdp, "document.querySelectorAll('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]')[1].click(); document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-confirm]').click(); true");
  await waitFor(cdp, "(() => { const select=document.querySelector('#customer-tag-batch [name=\"remove_tag_ids\"]'); return select?.selectedOptions[0]?.value === select?.options[1]?.value; })()", "V3 remove-tag picker did not update the original form draft");
  await evaluate(cdp, `(() => { const checks=[...document.querySelectorAll('#customer-list-body input[type=checkbox]')].slice(0,2); checks.forEach((box) => { box.checked=true; box.dispatchEvent(new Event('change', {bubbles:true})); }); window.confirm=()=>true; document.querySelector('#customer-tag-batch').requestSubmit(); return true; })()`);
  try { await waitFor(cdp, "document.querySelector('#customer-tag-batch-result')?.textContent.includes('已刷新执行结果')", "browser did not render the accepted command result"); } catch (_) { throw new Error(`customer command interaction did not render: ${await diagnostic()}`); }
  await waitFor(cdp, "Boolean(document.querySelector('#customer-tag-batch-refresh:not([hidden])'))", "accepted tag command did not expose the explicit result refresh action");
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await evaluate(cdp, "document.querySelector('#customer-tag-batch-refresh')?.click(); true");
    await delay(250);
    const rendered = await evaluate(cdp, "document.querySelector('#customer-tag-batch-result')?.textContent || ''");
    if (
      rendered.includes('客户 #1：已执行；观察标签：fixture group / fixture add（已生效）') &&
      rendered.includes('客户 #2：结果待核实；观察标签：暂无已观察标签') &&
      !rendered.includes('：executed') &&
      !rendered.includes('：outcome_unknown') &&
      !rendered.includes('（active）')
    ) break;
    if (attempt === 19) throw new Error(`explicit result refresh did not render durable outcomes: ${rendered}`);
  }
  if (screenshotDir) {
    await cdp.call("Page.navigate", { url: `${baseURL}/admin/customers/1` });
    await waitFor(cdp, "location.pathname === '/admin/customers/1' && !document.querySelector('#customer-detail-content')?.hidden", "customer detail route did not render");
    await waitFor(cdp, "document.querySelectorAll('#customer-360-main .customer-record-card').length >= 2", "customer detail sections did not render");
    if (await evaluate(cdp, "document.querySelector('#customer-profile-meta')?.textContent.includes('客户类型 0')")) throw new Error("unknown customer contact type was rendered as a raw numeric code");
    await captureCustomerScreenshot("customer-profile-1440.png", 1440);
    await captureCustomerScreenshot("customer-profile-1280.png", 1280);
    if (captureSectionError) {
      // This browser-only response fixture proves that a degraded /360 section
      // does not erase ready sibling sections. It is intentionally kept out of
      // the normal server readback evidence.
      const recordBody = Buffer.from(JSON.stringify({
        canonical_customer_id: 1,
        profile: { status: "ready", data: { customer_id: 1, display_name: "fixture one", oneid: "customer #1", status: "active", contact_type: 1, source: "chromium_fixture" } },
        identity_summary: { status: "ready", data: { identities: [{ summary: "企业身份已验证" }], phones: [] } },
        order_summary: { status: "ready", data: { total: 2, paid: 1, refunded: 0, failed: 1, recent: [{ id: 1001, merchant_order_no: "ORD-1001", status: "paid", created_at: "2026-09-15T00:00:00Z" }, { id: 1002, merchant_order_no: "ORD-1002", status: "failed", created_at: "2026-09-14T00:00:00Z" }] } },
        questionnaire_summary: { status: "ready", data: { total: 2, recent: [{ id: 81, title: "初次需求问卷", assessment_label: "已完成", submitted_at: "2026-09-15T00:00:00Z" }, { id: 82, title: "跟进问卷", score: 0, submitted_at: "2026-09-14T00:00:00Z" }] } },
        risk: { status: "ready", data: { level: "medium", reasons: ["payment_failures_present"] } },
        recent_touchpoints: { status: "ready", data: [{ id: 91, title: "客户资料已同步", source_domain: "wecom", occurred_at: "2026-09-15T00:00:00Z" }, { id: 92, title: "订单支付失败", source_domain: "order", occurred_at: "2026-09-14T00:00:00Z" }] },
      })).toString("base64");
      const degradedBody = Buffer.from(JSON.stringify({
        canonical_customer_id: 1,
        profile: { status: "ready", data: { customer_id: 1, display_name: "fixture one", oneid: "customer #1", status: "active", contact_type: 1, source: "chromium_fixture" } },
        identity_summary: { status: "ready", data: { identities: [{ summary: "企业身份已验证" }], phones: [] } },
        order_summary: { status: "degraded", data: {} },
        questionnaire_summary: { status: "ready", data: { total: 2, recent: [{ id: 81, title: "初次需求问卷", assessment_label: "已完成", submitted_at: "2026-09-15T00:00:00Z" }] } },
        risk: { status: "degraded", data: { level: "unknown", reasons: ["order_section_unavailable"] } },
        recent_touchpoints: { status: "ready", data: [{ id: 91, title: "客户资料已同步", source_domain: "wecom", occurred_at: "2026-09-15T00:00:00Z" }] },
      })).toString("base64");
      let customer360Body = recordBody;
      cdp.on("Fetch.requestPaused", (params) => { void cdp.call("Fetch.fulfillRequest", { requestId: params.requestId, responseCode: 200, responseHeaders: [{ name: "Content-Type", value: "application/json" }], body: customer360Body }); });
      await cdp.call("Fetch.enable", { patterns: [{ urlPattern: "*/api/admin/customers/1/360*", requestStage: "Response" }] });
      await cdp.call("Page.navigate", { url: `${baseURL}/admin/customers/1` });
      await waitFor(cdp, "document.querySelector('#customer-360-main')?.textContent.includes('ORD-1001') && document.querySelector('#customer-360-sidebar')?.textContent.includes('客户资料已同步')", "controlled customer records did not render");
      await captureCustomerScreenshot("customer-profile-records-fixture-1280.png", 1280);
      customer360Body = degradedBody;
      await cdp.call("Page.navigate", { url: `${baseURL}/admin/customers/1` });
      await waitFor(cdp, "document.querySelector('#customer-360-main .admin-state--error')?.textContent.includes('该分区暂时不可用')", "controlled degraded customer section did not render");
      await captureCustomerScreenshot("customer-profile-degraded-1280.png", 1280);
      await cdp.call("Fetch.disable");
    }
    console.log(`customer screenshots: ${screenshotDir}`);
  }
  console.log("customer_tag_command_chromium: PASS");
} catch (error) { failed = true; throw error; } finally { if (cdp) cdp.close(); if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); } const removed = await removeProfile(profile); if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete"); }
