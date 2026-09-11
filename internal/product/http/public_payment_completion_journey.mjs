import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

const source = await readFile(new URL('./public.go', import.meta.url), 'utf8');
const start = source.indexOf('</main><script>');
const end = source.indexOf('</script></body></html>`))', start);
assert.ok(start >= 0 && end > start, 'public payment template script was not found');
const script = source.slice(start + '</main><script>'.length, end)
  .replaceAll('{{if .Payment}}', '')
  .replaceAll('{{end}}', '')
  .replaceAll('{{.Product.PriceMinor}}', '990')
  .replaceAll('{{.Product.ID}}', '7')
  .replaceAll('{{.Product.ProductKind}}', 'standard')
  .replaceAll('{{.Product.CouponTargetRef}}', 'standard_product:7');

const storageKey = 'aicrm.checkout.v1:7:standard';
const paidCheckpoint = () => JSON.stringify({
  key: 'checkout-key-0000001', merchant_order_no: 'M-paid-7',
  payload: {product_id: 7, product_kind: 'standard', beneficiary_selection: 'payer_self', coupon_claim_id: 0},
  session_binding: 'a'.repeat(43), terminal_status: 'paid',
});

function response(body, status = 202) {
  return {ok: status >= 200 && status < 300, status, async json() { return body; }};
}

async function settle() {
  for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve));
}

function boot(store, completion, redirectFailure = false, sessionAuthorized = true, setup = '') {
  const calls = [], elements = new Map();
  const setGlobal = (name, value) => Object.defineProperty(globalThis, name, {value, configurable: true, writable: true});
  const element = () => ({hidden: false, disabled: false, dataset: {}, value: '0', checked: true, textContent: '', href: '', children: [], attributes: new Map(), addEventListener(type, listener) { this.listener ??= {}; this.listener[type] = listener; }, appendChild(child) { this.children.push(child); }, setAttribute(name, value) { this.attributes.set(name, String(value)); }, removeAttribute(name) { this.attributes.delete(name); }});
  for (const id of ['price', 'buy', 'restart', 'status', 'coupon', 'wechatNotice', 'mobile', 'grossAmount', 'payableAmount', 'footerAmount', 'discountAmount', 'identityGate', 'identityMessage', 'authContinue', 'checkoutContent']) elements.set(id, element());
  elements.get('authContinue').hidden = true;
  elements.get('checkoutContent').hidden = true;
  setGlobal('document', {getElementById(id) { return elements.get(id); }, addEventListener() {}, createElement() { return element(); }});
  setGlobal('navigator', {userAgent: 'MicroMessenger'});
  setGlobal('localStorage', {getItem(key) { return store.get(key) ?? null; }, setItem(key, value) { store.set(key, String(value)); }, removeItem(key) { store.delete(key); }});
  setGlobal('location', {href: '', pathname: '/pay/course-7', assign(url) { calls.push({redirect: url}); if (redirectFailure) throw new Error('redirect blocked'); }});
  setGlobal('crypto', {randomUUID() { return 'fresh-checkout-key'; }});
  setGlobal('WeixinJSBridge', {invoke() { throw new Error('paid reload must not invoke payment'); }});
  setGlobal('fetch', async (url, options = {}) => {
    calls.push({url: String(url), method: options.method ?? 'GET'});
    if (String(url) === '/api/v1/wechat-pay/checkout-session') return (typeof sessionAuthorized === 'function' ? sessionAuthorized() : sessionAuthorized) ? response({checkout_session_binding: 'a'.repeat(43)}) : response({code: 'payment_session_required'}, 401);
    if (String(url).startsWith('/api/h5/coupons/available')) return response({items: []});
    assert.equal(String(url), '/api/v1/wechat-pay/checkouts/M-paid-7');
    return response(completion);
  });
  Function(script + '\n' + setup)();
  return {calls, elements};
}

// An unauthenticated visitor remains on an explicit consent gate. Merely
// opening the product URL cannot redirect, create a customer, or create an
// order; the only next step is the user-clicked snsapi_userinfo start link.
{
  const store = new Map();
  const run = boot(store, {}, false, false);
  await settle();
  assert.equal(run.elements.get('identityGate').hidden, false);
  assert.equal(run.elements.get('checkoutContent').hidden, true);
  assert.equal(run.elements.get('authContinue').hidden, false);
  assert.equal(run.elements.get('authContinue').href, '/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7');
  assert.match(run.elements.get('identityMessage').textContent, /授权后才能/);
  assert.equal(run.calls.length, 1);
  assert.equal(run.calls[0].url, '/api/v1/wechat-pay/checkout-session');
  assert.equal(run.calls.some(call => call.method === 'POST' || call.redirect), false);
}

// A QR action is re-read after a reload from the persisted paid checkpoint.
// The bootstrap contains no code path that starts a new checkout or invokes
// WeixinJSBridge for this already-paid order.
{
  const store = new Map([[storageKey, paidCheckpoint()]]);
  const run = boot(store, {status: 'paid', completion_action: {state: 'available', mode: 'qr', lead_qr: {url: 'https://work.weixin.qq.com/q/test', title: '添加客服', subtitle: '领取资料'}}});
  await settle();
  assert.equal(run.elements.get('identityGate').hidden, true);
  assert.equal(run.elements.get('checkoutContent').hidden, false);
  assert.equal(run.elements.get('buy').disabled, true);
  assert.equal(run.elements.get('restart').hidden, false);
  assert.equal(run.elements.get('status').textContent, '支付成功');
  assert.equal(JSON.parse(store.get(storageKey)).terminal_status, 'paid');
  assert.equal(run.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 1);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
}

// If the browser cannot complete a redirect, the paid checkpoint remains.
// Reloading makes only the same authorized read; it never replaces the key,
// creates a second order, or calls the payment SDK.
{
  const store = new Map([[storageKey, paidCheckpoint()]]);
  const paidRedirect = {status: 'paid', completion_action: {state: 'available', mode: 'redirect', redirect_url: '/after-paid'}};
  const first = boot(store, paidRedirect, true);
  await settle();
  const second = boot(store, paidRedirect, true);
  await settle();
  assert.equal(JSON.parse(store.get(storageKey)).terminal_status, 'paid');
  assert.equal(first.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 1);
  assert.equal(second.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 1);
  assert.equal([...first.calls, ...second.calls].some(call => call.method === 'POST'), false);
  assert.equal([...first.calls, ...second.calls].filter(call => call.redirect === '/after-paid').length, 2);
}

// Unknown prepay must stop polling, keep the exact checkpoint, and never
// create another checkout, even when the buyer clicks again.
{
  const pending = JSON.parse(paidCheckpoint());
  delete pending.terminal_status;
  const original = JSON.stringify(pending);
  const store = new Map([[storageKey, original]]);
  const run = boot(store, {status: 'awaiting_prepay', ready: false, prepay_state: 'outcome_unknown'});
  await settle();
  await run.elements.get('buy').listener.click();
  assert.match(run.elements.get('status').textContent, /下单结果尚未确认/);
  assert.equal(run.elements.get('buy').disabled, false);
  assert.equal(store.get(storageKey), original);
  await run.elements.get('buy').listener.click();
  assert.equal(store.get(storageKey), original);
  assert.equal(run.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 3);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
}

// Bridge readiness has a deadline and removes its listener. A late bridge
// event after failure cannot unexpectedly launch a payment sheet.
{
  const timers = new Map();
  let timerID = 0, listener, removed = false, invoked = 0;
  const bridgeSource = script.slice(script.indexOf('function invokePay(handoff)'), script.indexOf('\nconst couponDiscounts'));
  const doc = {addEventListener(_, fn) { listener = fn; }, removeEventListener(_, fn) { assert.equal(fn, listener); removed = true; }};
  const invoke = Function('document', 'WeixinJSBridge', 'setTimeout', 'clearTimeout', bridgeSource + ';return invokePay;')(
    doc, undefined, (fn, ms) => { timers.set(++timerID, {fn, ms}); return timerID; }, id => timers.delete(id),
  );
  const promise = invoke({});
  assert.equal(timers.get(1).ms, 10000);
  timers.get(1).fn();
  await assert.rejects(promise, /微信支付未能打开/);
  assert.equal(removed, true);
  listener();
  assert.equal(timers.size, 0);
  assert.equal(invoked, 0);
}

// A manually abandoned legacy flow retains its original idempotency evidence.
// No replacement checkout or payment bridge invocation follows the readback.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const original = JSON.stringify(checkpoint);
  const store = new Map([[storageKey, original]]);
  const run = boot(store, {status: 'awaiting_prepay', prepay_state: 'outcome_unknown', checkout_abandoned: true});
  await settle();
  await run.elements.get('buy').listener.click();
  assert.equal(run.elements.get('buy').disabled, true);
  assert.equal(run.elements.get('restart').hidden, true);
  assert.match(run.elements.get('status').textContent, /原支付流程已停止/);
  assert.equal(store.get(storageKey), original);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  assert.equal(run.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 2);
}

// Expiry between page load and the buyer's click returns to the explicit gate.
// No checkout key or order is created and the browser never starts OAuth itself.
{
  let authorized = true;
  const store = new Map();
  const run = boot(store, {}, false, () => authorized);
  await settle();
  assert.equal(run.elements.get('checkoutContent').hidden, false);
  authorized = false;
  run.elements.get('mobile').value = '13800138000';
  await run.elements.get('buy').listener.click();
  assert.equal(run.elements.get('identityGate').hidden, false);
  assert.equal(run.elements.get('checkoutContent').hidden, true);
  assert.equal(run.elements.get('authContinue').hidden, false);
  assert.match(run.elements.get('identityMessage').textContent, /已失效/);
  assert.equal(store.size, 0);
  assert.equal(run.calls.some(call => call.method === 'POST' || call.redirect), false);
}

// A cancelled payment resumes its immutable original amount, never a newly
// selected coupon. Bootstrap reads once and cannot open the cashier or POST.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const store = new Map([[storageKey, JSON.stringify(checkpoint)]]);
  const run = boot(store, {status: 'awaiting_payment', amount_minor: 990, currency: 'CNY', ready: true, handoff: {}}, false, true, 'couponDiscounts.set(123,100)');
  await settle();
  assert.equal(run.elements.get('footerAmount').textContent, '¥9.90');
  assert.equal(run.elements.get('coupon').disabled, true);
  assert.equal(run.elements.get('mobile').disabled, true);
  assert.equal(run.calls.filter(call => call.url.includes('/checkouts/')).length, 1);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  run.elements.get('coupon').value = '123';
  run.elements.get('coupon').listener.change();
  assert.equal(run.elements.get('footerAmount').textContent, '¥9.90');
  await run.elements.get('buy').listener.click();
  assert.equal(run.elements.get('footerAmount').textContent, '¥9.90');
  assert.equal(store.get(storageKey), JSON.stringify(checkpoint));
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
}

// Missing historical amount is not replaced by today's product price.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const run = boot(new Map([[storageKey, JSON.stringify(checkpoint)]]), {status: 'awaiting_prepay'});
  await settle();
  assert.equal(run.elements.get('footerAmount').textContent, '待确认');
  assert.equal(run.elements.get('grossAmount').textContent, '以原订单为准');
  assert.equal(run.elements.get('discountAmount').hidden, true);
}
