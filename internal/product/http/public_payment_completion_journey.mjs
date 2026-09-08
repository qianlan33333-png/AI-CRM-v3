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

function response(body) {
  return {ok: true, status: 202, async json() { return body; }};
}

async function settle() {
  for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve));
}

function boot(store, completion, redirectFailure = false) {
  const calls = [], elements = new Map();
  const setGlobal = (name, value) => Object.defineProperty(globalThis, name, {value, configurable: true, writable: true});
  const element = () => ({hidden: false, disabled: false, dataset: {}, value: '0', checked: true, textContent: '', children: [], addEventListener(type, listener) { this.listener ??= {}; this.listener[type] = listener; }, appendChild(child) { this.children.push(child); }});
  for (const id of ['price', 'buy', 'restart', 'status', 'coupon', 'wechatNotice', 'beneficiarySelf', 'mobile']) elements.set(id, element());
  setGlobal('document', {getElementById(id) { return elements.get(id); }, addEventListener() {}, createElement() { return element(); }});
  setGlobal('navigator', {userAgent: 'MicroMessenger'});
  setGlobal('localStorage', {getItem(key) { return store.get(key) ?? null; }, setItem(key, value) { store.set(key, String(value)); }, removeItem(key) { store.delete(key); }});
  setGlobal('location', {href: '', assign(url) { calls.push({redirect: url}); if (redirectFailure) throw new Error('redirect blocked'); }});
  setGlobal('crypto', {randomUUID() { return 'fresh-checkout-key'; }});
  setGlobal('WeixinJSBridge', {invoke() { throw new Error('paid reload must not invoke payment'); }});
  setGlobal('fetch', async (url, options = {}) => {
    calls.push({url: String(url), method: options.method ?? 'GET'});
    if (String(url).startsWith('/api/h5/coupons/available')) return response({items: []});
    assert.equal(String(url), '/api/v1/wechat-pay/checkouts/M-paid-7');
    return response(completion);
  });
  Function(script)();
  return {calls, elements};
}

// A QR action is re-read after a reload from the persisted paid checkpoint.
// The bootstrap contains no code path that starts a new checkout or invokes
// WeixinJSBridge for this already-paid order.
{
  const store = new Map([[storageKey, paidCheckpoint()]]);
  const run = boot(store, {status: 'paid', completion_action: {state: 'available', mode: 'qr', lead_qr: {url: 'https://work.weixin.qq.com/q/test', title: '添加客服', subtitle: '领取资料'}}});
  await settle();
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
