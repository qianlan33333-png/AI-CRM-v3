import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import crypto from 'node:crypto';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole, requestInterceptor } = jsdom;

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/couponAdapter.ts'));
const donorForm = await fs.readFile(path.join(root, 'web/donors/standard-components-production/coupons/coupon_form.html'), 'utf8');
const donorStyle = await fs.readFile(path.join(root, 'web/donors/standard-components-production/coupons/coupon_styles.html'), 'utf8');
const runtimeBlock = donorForm.indexOf('{% block scripts_extra %}'); const runtimeOpen = donorForm.indexOf('<script>', runtimeBlock); const runtimeClose = donorForm.indexOf('</script>', runtimeOpen);
const donorRuntime = donorForm.slice(runtimeOpen + '<script>'.length, runtimeClose);
assert.equal(crypto.createHash('sha256').update(donorRuntime).digest('hex'), 'a3e15d50e97609d934a4edcab1adb2a2048d23e0dd7517e46ad4b9b677b6c88c', 'runtime fixture must use the exact extracted donor script bytes');
const pause = () => new Promise((resolve) => setTimeout(resolve, 15));
async function waitFor(check, message) { for (let attempt = 0; attempt < 100; attempt += 1) { const value = check(); if (value) return value; await pause(); } throw new Error(message); }
const calls = []; let createAttempts = 0; let unknownCreateAttempts = 0;
const dom = new JSDOM('<!doctype html><body data-page="couponForm"><main id="stage"><label><textarea id="coupon-target-refs"></textarea></label></main></body>', {
  url: 'https://test.invalid/admin/couponForm.html', runScripts: 'dangerously',
  resources: { interceptors: [requestInterceptor(async (request) => {
    if (request.url === 'https://test.invalid/assets/standard-components/coupon_form_runtime.js') return new Response(donorRuntime, { status: 200, headers: { 'Content-Type': 'application/javascript' } });
    return undefined;
  })] }, pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase(); const headers = new Headers(init.headers); calls.push({ url, method, headers, body: init.body || '' });
      if (url.pathname === '/assets/standard-components/coupon_form.html') return new Response(donorForm, { status: 200 });
      if (url.pathname === '/assets/standard-components/coupon_styles.html') return new Response(donorStyle, { status: 200 });
      if (url.pathname.endsWith('/product-options')) return new Response(JSON.stringify({ total: 21, items: [{ id: 9, target_ref: 'standard_product:9', name: '测试商品', price_minor: 2, currency: 'CNY' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/coupons' && method === 'POST') {
        if (String(init.body).includes('empty-receipt')) return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
        if (String(init.body).includes('unknown-create')) { unknownCreateAttempts += 1; if (unknownCreateAttempts === 1) throw new Error('response lost after commit'); return new Response(JSON.stringify({ coupon: { id: 22 } }), { status: 201, headers: { 'Content-Type': 'application/json' } }); }
        createAttempts += 1; return new Response(JSON.stringify({ coupon: { id: 21 } }), { status: 201, headers: { 'Content-Type': 'application/json' } });
      }
      return new Response(JSON.stringify({ coupon: { id: 21 } }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  dom.window.eval(host);
  const document = dom.window.document;
  await waitFor(() => document.querySelector('#couponForm'), 'actual standard coupon form must replace the frozen target_ref textarea');
  assert.equal(document.querySelector('script[data-v3-standard-coupon-runtime]')?.src, 'https://test.invalid/assets/standard-components/coupon_form_runtime.js', 'the donor runtime must load through a same-origin external script');
  assert.ok(document.querySelector('.coupon-dialog#couponProductDialog'), 'actual donor product dialog must be mounted');
  assert.ok(document.querySelector('style[data-v3-standard-coupon-style]'), 'actual donor stylesheet must be mounted');
  assert.equal(document.querySelector('#coupon-target-refs'), null, 'manual target_ref entry must not remain visible');
  document.querySelector('#openProductSelector').click(); await waitFor(() => document.querySelector('[data-product-option="standard_product:9"]'), 'actual donor picker loads the server page');
  assert.match(document.querySelector('#couponProductOptions').textContent, /普通商品/, 'target_ref prefix maps the server product type without guessing'); assert.match(document.querySelector('#couponProductOptions').textContent, /¥0\.02/, 'price_minor maps to displayed cents'); assert.match(document.querySelector('#couponProductOptions').textContent, /状态未提供/, 'missing product status must remain explicit rather than appear active');
  document.querySelector('[data-product-option="standard_product:9"]').click(); document.querySelector('#confirmProductSelection').click();
  assert.match(document.querySelector('#selectedProductList').textContent, /测试商品/, 'confirmed picker selection renders the selected standard product');
  document.querySelector('#couponName').value = '0.01 验收券'; document.querySelector('#couponAmount').value = '0.01'; document.querySelector('#couponIssueLimit').value = '1'; document.querySelector('#couponPerUserLimit').value = '1'; document.querySelector('#couponClaimStart').value = '2026-09-08T10:00'; document.querySelector('#couponClaimEnd').value = '2026-09-08T12:00'; document.querySelector('#couponUseStart').value = '2026-09-08T10:00'; document.querySelector('#couponUseEnd').value = '2026-09-08T13:00';
  document.querySelector('#saveCoupon').click(); await waitFor(() => calls.some((call) => call.method === 'POST' && call.url.pathname === '/api/admin/coupons'), 'standard form save must create through the V3 Coupon HTTP contract');
  const create = calls.find((call) => call.method === 'POST' && call.url.pathname === '/api/admin/coupons'); assert.match(create.headers.get('Idempotency-Key'), /^coupon-/); await waitFor(() => document.querySelector('#couponFormToast').textContent.includes('优惠券已保存'), 'the original donor save handler must receive the V3 create receipt');
  await dom.window.fetch('/api/admin/coupons/21/publish', { method: 'POST', body: '' }); await dom.window.fetch('/api/admin/coupons/21/stop', { method: 'POST', body: '' }); await dom.window.fetch('/api/admin/coupons/21/publish', { method: 'POST', body: '' }); await dom.window.fetch('/api/admin/coupons/21', { method: 'DELETE' });
  const publish = calls.filter((call) => call.url.pathname.endsWith('/publish')); const stop = calls.find((call) => call.url.pathname.endsWith('/stop')); assert.match(publish[0].headers.get('Idempotency-Key'), /^coupon-/, 'publish must include the server-required idempotency receipt'); assert.match(publish[1].headers.get('Idempotency-Key'), /^coupon-/, 'republishing after a confirmed stop must include a fresh receipt'); assert.notEqual(publish[0].headers.get('Idempotency-Key'), publish[1].headers.get('Idempotency-Key'), 'a confirmed publish -> stop -> publish is a new lifecycle intent'); assert.match(stop.headers.get('Idempotency-Key'), /^coupon-/, 'stop has its own lifecycle key'); assert.match(calls.find((call) => call.method === 'DELETE').headers.get('Idempotency-Key'), /^coupon-/, 'delete must have a lifecycle idempotency key');
  assert.equal(createAttempts, 1, 'one completed submit must create exactly once');
  const unknownBody = '{"name":"unknown-create"}';
  await assert.rejects(() => dom.window.fetch('/api/admin/coupons', { method: 'POST', body: unknownBody }), /response lost/);
  const altered = await dom.window.fetch('/api/admin/coupons', { method: 'POST', body: '{"name":"changed-after-unknown"}' }); assert.equal(altered.status, 409, 'changed content cannot create another coupon while the first create was unknown');
  const recovered = await dom.window.fetch('/api/admin/coupons', { method: 'POST', body: unknownBody }); assert.equal(recovered.status, 201, 'same payload retries the original server receipt after a lost response');
  const unknownCalls = calls.filter((call) => call.method === 'POST' && call.url.pathname === '/api/admin/coupons' && call.body === unknownBody); assert.equal(unknownCalls.length, 2, 'only the original logical create is retried'); assert.equal(unknownCalls[0].headers.get('Idempotency-Key'), unknownCalls[1].headers.get('Idempotency-Key'), 'unknown retry retains its original key'); assert.equal(unknownCreateAttempts, 2);
  const malformed = await dom.window.fetch('/api/admin/coupons', { method: 'POST', body: '{"name":"empty-receipt"}' }); assert.equal(malformed.status, 503, '200 without a coupon ID remains create outcome unknown and cannot show a false saved state');
} finally { dom.window.close(); }

// An external donor runtime failure occurs after the standard form has been
// mounted. Keep that form intact, make the error visible, and allow the same
// page to retry after the asset becomes available without issuing a write.
const failureCalls = []; let runtimeStatus = 503;
const failedDom = new JSDOM('<!doctype html><body data-page="couponForm"><main id="stage"><label><textarea id="coupon-target-refs"></textarea></label></main></body>', {
  url: 'https://test.invalid/admin/couponForm.html', runScripts: 'dangerously',
  resources: { interceptors: [requestInterceptor(async (request) => {
    if (request.url === 'https://test.invalid/assets/standard-components/coupon_form_runtime.js') return new Response(donorRuntime, { status: runtimeStatus, headers: { 'Content-Type': 'application/javascript' } });
    return undefined;
  })] }, pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase(); failureCalls.push({ url, method });
      if (url.pathname === '/assets/standard-components/coupon_form.html') return new Response(donorForm, { status: 200 });
      if (url.pathname === '/assets/standard-components/coupon_styles.html') return new Response(donorStyle, { status: 200 });
      return new Response(JSON.stringify({ total: 0, items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  failedDom.window.eval(host);
  const document = failedDom.window.document;
  await waitFor(() => document.querySelector('[role="alert"]')?.textContent.includes('标准优惠券交互脚本加载失败'), 'a donor runtime HTTP failure must be visible');
  assert.ok(document.querySelector('#couponForm'), 'runtime load failure must preserve the mounted standard form');
  assert.equal(failureCalls.some((call) => call.method !== 'GET'), false, 'runtime load failure must not save or mutate a coupon');
  runtimeStatus = 200;
  document.querySelector('[role="alert"] button')?.click();
  await waitFor(() => document.querySelector('#selectedProductList')?.textContent.includes('尚未选择商品'), 'retry must replay DOMContentLoaded after the external runtime loads');
  assert.equal(document.querySelectorAll('script[data-v3-standard-coupon-runtime]').length, 1, 'retry must replace the failed runtime element instead of accumulating scripts');
  assert.equal(failureCalls.some((call) => call.method !== 'GET'), false, 'retrying only the runtime must not write a coupon');
} finally { failedDom.window.close(); }

// A delayed same-origin script must not capture a DOM-ready listener registered
// by another page module while the network request is pending. Only the donor
// listener is replayed after its external bytes arrive.
const delayedCalls = []; let resolveDelayedRuntime;
const delayedDom = new JSDOM('<!doctype html><body data-page="couponForm"><main id="stage"><label><textarea id="coupon-target-refs"></textarea></label></main></body>', {
  url: 'https://test.invalid/admin/couponForm.html', runScripts: 'dangerously',
  resources: { interceptors: [requestInterceptor(async (request) => {
    if (request.url === 'https://test.invalid/assets/standard-components/coupon_form_runtime.js') {
      return new Promise((resolve) => { resolveDelayedRuntime = () => resolve(new Response(donorRuntime, { status: 200, headers: { 'Content-Type': 'application/javascript' } })); });
    }
    return undefined;
  })] }, pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase(); delayedCalls.push({ url, method });
      if (url.pathname === '/assets/standard-components/coupon_form.html') return new Response(donorForm, { status: 200 });
      if (url.pathname === '/assets/standard-components/coupon_styles.html') return new Response(donorStyle, { status: 200 });
      return new Response(JSON.stringify({ total: 0, items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  delayedDom.window.eval(host);
  await waitFor(() => typeof resolveDelayedRuntime === 'function', 'coupon runtime request must be pending before the race is exercised');
  delayedDom.window.document.addEventListener('DOMContentLoaded', () => undefined);
  resolveDelayedRuntime();
  await waitFor(() => delayedDom.window.document.querySelector('#selectedProductList')?.textContent.includes('尚未选择商品'), 'the delayed donor runtime must receive its own replayed DOM-ready handler');
  assert.equal(delayedCalls.some((call) => call.method !== 'GET'), false, 'the delayed runtime bootstrap must not write a coupon');
} finally { delayedDom.window.close(); }
console.log('coupon Host actual donor form, picker and lifecycle transport journey: PASS');
