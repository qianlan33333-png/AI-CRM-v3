import assert from "node:assert/strict";
import { JSDOM, VirtualConsole } from "jsdom";

const baseURL = String(process.env.AICRM_PUBLIC_CHECKOUT_JOURNEY_BASE_URL || "").replace(/\/$/, "");
const firstSession = String(process.env.AICRM_PUBLIC_CHECKOUT_JOURNEY_FIRST_COOKIE || "");
const secondSession = String(process.env.AICRM_PUBLIC_CHECKOUT_JOURNEY_SECOND_COOKIE || "");
if (!baseURL || !firstSession || !secondSession) throw new Error("public checkout journey requires base URL and two trusted cookies");

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
let keySequence = 0;

class SharedStorage {
  constructor(fails = false) {
    this.fails = fails;
    this.values = new Map();
  }

  getItem(key) {
    if (this.fails) throw new Error("storage unavailable");
    return this.values.has(key) ? this.values.get(key) : null;
  }

  setItem(key, value) {
    if (this.fails) throw new Error("storage unavailable");
    this.values.set(String(key), String(value));
  }

  removeItem(key) {
    if (this.fails) throw new Error("storage unavailable");
    this.values.delete(String(key));
  }
}

async function getPage(cookie) {
  const response = await fetch(new URL("/pay/course-7", baseURL), { headers: { Cookie: cookie } });
  assert.equal(response.status, 200, "payment page status");
  return response.text();
}

async function runPage(storage, cookie, bridge) {
  const html = await getPage(cookie);
  const errors = [];
  const console = new VirtualConsole();
  console.on("jsdomError", (error) => errors.push(error));
  const dom = new JSDOM(html, {
    url: new URL("/pay/course-7", baseURL).toString(),
    pretendToBeVisual: true,
    runScripts: "dangerously",
    virtualConsole: console,
    beforeParse(window) {
      Object.defineProperty(window.navigator, "userAgent", { configurable: true, value: "MicroMessenger test" });
      Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
      Object.defineProperty(window, "crypto", { configurable: true, value: { randomUUID: () => `checkout-journey-${++keySequence}` } });
      window.WeixinJSBridge = {
        invoke(_method, _handoff, callback) {
          callback({ err_msg: bridge.next() });
        },
      };
      window.fetch = async (input, init = {}) => {
        const requestURL = new URL(typeof input === "string" ? input : input.url, baseURL);
        const headers = new Headers(init.headers || {});
        headers.set("Cookie", cookie);
        return fetch(requestURL, { ...init, headers });
      };
      window.setTimeout = (callback) => {
        queueMicrotask(callback);
        return 1;
      };
      window.clearTimeout = () => {};
    },
  });
  await sleep(5);
  assert.equal(errors.length, 0, errors.map((error) => error.stack || error.message).join("\n"));
  Object.defineProperty(dom, "checkoutJourneyErrors", { value: errors });
  return dom;
}

function closePage(dom) {
  assert.equal(dom.checkoutJourneyErrors.length, 0, dom.checkoutJourneyErrors.map((error) => error.stack || error.message).join("\n"));
  dom.window.close();
}

function setPurchase(dom, couponID, mobile) {
  const document = dom.window.document;
  const beneficiary = document.getElementById("beneficiarySelf");
  beneficiary.checked = true;
  const coupon = document.getElementById("coupon");
  const option = document.createElement("option");
  option.value = String(couponID);
  option.textContent = `券 ${couponID}`;
  coupon.appendChild(option);
  coupon.value = String(couponID);
  const mobileField = document.getElementById("mobile");
  if (mobileField) mobileField.value = mobile;
}

async function waitFor(document, expected, label) {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (document.getElementById("status")?.textContent === expected) return;
    await sleep(5);
  }
  assert.equal(document.getElementById("status")?.textContent, expected, label);
}

const normalBridge = { next: () => "get_brand_wcpay_request:ok" };

// The payment mutation succeeds but its first HTTP response is lost. The
// reloaded page must replay the saved key with the original coupon/mobile,
// rather than use the changed form values.
const replayStorage = new SharedStorage();
const lostResponse = await runPage(replayStorage, firstSession, normalBridge);
setPurchase(lostResponse, 11, "13800138000");
lostResponse.window.document.getElementById("buy").click();
await waitFor(lostResponse.window.document, "请求失败", "lost response");
assert.equal(replayStorage.values.size, 1, "response loss must retain a recovery checkpoint");
closePage(lostResponse);

const replayed = await runPage(replayStorage, firstSession, normalBridge);
setPurchase(replayed, 99, "13900139000");
replayed.window.document.getElementById("buy").click();
await waitFor(replayed.window.document, "支付成功", "replayed checkout");
assert.equal(replayStorage.values.size, 0, "terminal payment clears the recovery checkpoint");
closePage(replayed);

// A cancelled WeChat sheet leaves the same merchant order recoverable. A
// later explicit click opens that order again and does not create another one.
let bridgeCalls = 0;
const cancelThenPayBridge = { next: () => (++bridgeCalls === 1 ? "get_brand_wcpay_request:cancel" : "get_brand_wcpay_request:ok") };
const cancelStorage = new SharedStorage();
const cancelled = await runPage(cancelStorage, firstSession, cancelThenPayBridge);
setPurchase(cancelled, 12, "13800138000");
cancelled.window.document.getElementById("buy").click();
await waitFor(cancelled.window.document, "支付未完成", "cancelled checkout");
assert.equal(cancelled.window.document.getElementById("buy").disabled, false, "cancelled order remains actionable");
cancelled.window.document.getElementById("buy").click();
await waitFor(cancelled.window.document, "支付成功", "resumed cancelled checkout");
assert.equal(bridgeCalls, 2, "an explicit second click reopens the same WeChat payment");
assert.equal(cancelStorage.values.size, 0, "paid resumed order clears checkpoint");
closePage(cancelled);

// An unresolved outcome remains tied to its saved merchant order, even after
// the browser's bounded polling window ends.
const unknownStorage = new SharedStorage();
const unknown = await runPage(unknownStorage, firstSession, normalBridge);
setPurchase(unknown, 14, "13800138000");
unknown.window.document.getElementById("buy").click();
await waitFor(unknown.window.document, "支付结果确认超时，请稍后刷新查看", "unknown checkout");
assert.equal(unknownStorage.values.size, 1, "unknown result keeps checkpoint for later status recovery");
closePage(unknown);

// A saved merchant order cannot be used under another opaque payment session.
// The browser detects the trusted-session binding mismatch before it polls or
// creates anything, and retains the recovery marker to prohibit a new order.
const switchStorage = new SharedStorage();
const originalSession = await runPage(switchStorage, firstSession, normalBridge);
setPurchase(originalSession, 13, "13800138000");
originalSession.window.document.getElementById("buy").click();
await waitFor(originalSession.window.document, "支付结果确认超时，请稍后刷新查看", "original session pending checkout");
closePage(originalSession);
const switchedSession = await runPage(switchStorage, secondSession, normalBridge);
setPurchase(switchedSession, 13, "13800138000");
switchedSession.window.document.getElementById("buy").click();
await waitFor(switchedSession.window.document, "付款授权已变化，原订单标识已保留；请恢复原授权后继续", "session switch");
assert.equal(switchStorage.values.size, 1, "session switch must retain the old checkpoint and prohibit a new order");
closePage(switchedSession);

// Failing browser storage blocks the very first payment request, so an
// unknown effect is never created without a recovery identifier.
const unavailableStorage = new SharedStorage(true);
const unavailable = await runPage(unavailableStorage, firstSession, normalBridge);
setPurchase(unavailable, 15, "13800138000");
unavailable.window.document.getElementById("buy").click();
await waitFor(unavailable.window.document, "无法保存本次订单恢复信息，请检查浏览器存储后重试", "storage unavailable");
closePage(unavailable);

// A checkpoint written by the immediately preceding Host version has no
// session binding. If its create response was lost, it must remain visible but
// never be treated as permission to generate a fresh idempotency key.
const legacyStorage = new SharedStorage();
legacyStorage.setItem("aicrm.checkout.v1:7:standard", JSON.stringify({
  key: "legacy-checkpoint-0001",
  merchant_order_no: "",
  payload: { product_id: 7, product_kind: "standard", beneficiary_selection: "payer_self", coupon_claim_id: 16, mobile: "+8613800138000" },
}));
const legacy = await runPage(legacyStorage, firstSession, normalBridge);
setPurchase(legacy, 16, "13800138000");
legacy.window.document.getElementById("buy").click();
await waitFor(legacy.window.document, "旧版订单恢复标识缺少付款会话绑定，已保留原标识，请勿重新下单", "legacy response-lost checkpoint");
assert.equal(legacyStorage.values.size, 1, "legacy unbound recovery must remain and never create a new order");
closePage(legacy);
