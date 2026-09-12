import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import crypto from 'node:crypto';
import jsdom from 'jsdom';
import { build } from 'esbuild';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole, requestInterceptor } = jsdom;

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/couponAdapter.ts'));
const feedback = (await build({ stdin: { contents: 'import {initFeedback} from "./web/src/shared/ui/feedback.ts"; initFeedback();', resolveDir: root }, bundle: true, format: 'iife', write: false })).outputFiles[0].text;
const publishAction = (await build({ stdin: { contents: `import {walkChildren} from './web/src/shared/ui/runtime.ts'; import {confirmBox} from './web/src/shared/ui/feedback.ts'; import {publishLegacyCoupon} from './web/src/api/generated/p4-coupon-compat/p4-coupon-compat.ts'; const stage=document.createElement('div');stage.innerHTML='<button id="publishActual" onclick="{{ publish }}">发布</button>';document.body.append(stage);walkChildren(stage,{publish:()=>confirmBox('发布优惠券','确认发布？','确认发布',true,()=>publishLegacyCoupon(21).then(r=>window.publishReceipt=r))});`, resolveDir: root }, bundle: true, format: 'iife', write: false })).outputFiles[0].text;
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
  dom.window.eval(feedback);
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
  assert.equal(document.querySelector('#fb-toast').textContent, '', 'the real global capture handler must not report an unavailable backend for the actual donor save');
  assert.equal(document.querySelector('#saveCoupon').dataset.capabilityState, 'real');
  dom.window.eval(publishAction); document.querySelector('#publishActual').click();
  assert.equal(document.querySelector('#fb-mask').hidden, false, 'the real bound publish action opens confirmation');
  document.querySelector('#fb-ok').click();
  await waitFor(() => dom.window.publishReceipt, 'confirmed publish must use the actual generated HTTP client');
  assert.equal(dom.window.publishReceipt.status, 200); assert.equal(document.querySelector('#fb-toast').textContent, '', 'publish confirmation must not emit the unbound-action error');
  await dom.window.fetch('/api/admin/coupons/21/publish', { method: 'POST', body: '' }); await dom.window.fetch('/api/admin/coupons/21/stop', { method: 'POST', body: '' }); await dom.window.fetch('/api/admin/coupons/21/publish', { method: 'POST', body: '' }); await dom.window.fetch('/api/admin/coupons/21', { method: 'DELETE' });
  const publish = calls.filter((call) => call.url.pathname.endsWith('/publish')); const stop = calls.find((call) => call.url.pathname.endsWith('/stop')); assert.match(publish[0].headers.get('Idempotency-Key'), /^coupon-/, 'publish must include the server-required idempotency receipt'); assert.match(publish[2].headers.get('Idempotency-Key'), /^coupon-/, 'republishing after a confirmed stop must include a fresh receipt'); assert.notEqual(publish[1].headers.get('Idempotency-Key'), publish[2].headers.get('Idempotency-Key'), 'a confirmed publish -> stop -> publish is a new lifecycle intent'); assert.match(stop.headers.get('Idempotency-Key'), /^coupon-/, 'stop has its own lifecycle key'); assert.match(calls.find((call) => call.method === 'DELETE').headers.get('Idempotency-Key'), /^coupon-/, 'delete must have a lifecycle idempotency key');
  assert.equal(createAttempts, 1, 'one completed submit must create exactly once');
  const unknownBody = '{"name":"unknown-create"}';
  await assert.rejects(() => dom.window.fetch('/api/admin/coupons', { method: 'POST', body: unknownBody }), /response lost/);
  const altered = await dom.window.fetch('/api/admin/coupons', { method: 'POST', body: '{"name":"changed-after-unknown"}' }); assert.equal(altered.status, 409, 'changed content cannot create another coupon while the first create was unknown');
  const recovered = await dom.window.fetch('/api/admin/coupons', { method: 'POST', body: unknownBody }); assert.equal(recovered.status, 201, 'same payload retries the original server receipt after a lost response');
  const unknownCalls = calls.filter((call) => call.method === 'POST' && call.url.pathname === '/api/admin/coupons' && String(call.body).includes('unknown-create')); assert.equal(unknownCalls.length, 2, 'only the original logical create is retried'); assert.equal(unknownCalls[0].headers.get('Idempotency-Key'), unknownCalls[1].headers.get('Idempotency-Key'), 'unknown retry retains its original key'); assert.equal(unknownCreateAttempts, 2);
  const malformed = await dom.window.fetch('/api/admin/coupons', { method: 'POST', body: '{"name":"empty-receipt"}' }); assert.equal(malformed.status, 503, '200 without a coupon ID remains create outcome unknown and cannot show a false saved state');
} finally { dom.window.document.body.dataset.page = 'closed'; dom.window.close(); }

// Existing draft: real donor submit plus real global feedback, with a durable
// receipt-shaped response. A failed or malformed save must never show success.
let savedDraft = { id: 18, name: '验收草稿', status: 'draft', discount_amount_total: 1, total_issue_limit: 3, per_user_issue_limit: 1, claim_starts_at: '2026-09-08T00:00:00.123456Z', claim_ends_at: '2026-09-15T00:00:00Z', validity_mode: 'fixed_range', use_starts_at: '2026-09-08T02:00:00.123456Z', use_ends_at: '2026-09-15T02:00:00Z', relative_validity_days: null, target_refs: ['standard_product:32'], target_products: [{ target_ref: 'standard_product:32', name: '已选中文商品', state: 'available' }], instructions: '' };
let editOutcome = 'success'; const edits = [];
const editDom = new JSDOM('<body data-page="couponForm"><main id="stage">正在加载页面…</main><template id="tpl"><div data-coupon-form-mode="edit"></div></template><button id="unowned">发布未接入功能</button></body>', {
  url: 'https://test.invalid/admin/couponForm.html?id=18', runScripts: 'dangerously', virtualConsole: new VirtualConsole(),
  resources: { interceptors: [requestInterceptor(async (request) => request.url.endsWith('/coupon_form_runtime.js') ? new Response(donorRuntime, { headers: { 'Content-Type': 'application/javascript' } }) : undefined)] },
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname.endsWith('/coupon_form.html')) return new Response(donorForm);
      if (url.pathname.endsWith('/coupon_styles.html')) return new Response(donorStyle);
      if (url.pathname.endsWith('/product-options')) return Response.json({ total: 1, items: [{ target_ref: 'standard_product:32', name: '适用商品', price_minor: 100 }] });
      if (init.method === 'PUT') {
        edits.push({ body: JSON.parse(init.body), headers: new Headers(init.headers) });
        if (editOutcome === 'rejected') return Response.json({ error: 'coupon_invalid', code: 'coupon_invalid', message: 'untrusted machine detail' }, { status: 400 });
        if (editOutcome === 'malformed') return Response.json({});
        savedDraft = { ...savedDraft, ...JSON.parse(init.body) };
      }
      return Response.json({ coupon: savedDraft });
    };
  },
});
try {
  editDom.window.eval(feedback); editDom.window.eval(host);
  const d = editDom.window.document;
  await waitFor(() => d.querySelector('#saveCoupon')?.__dcBound, 'existing draft runtime must own the save action');
  assert.match(d.querySelector('#selectedProductList').textContent, /已选中文商品/, 'the Product-owned detail projection supplies the selected product name');
  assert.match(d.querySelector('#selectedProductList').textContent, /价格待核验/, 'a narrow name-only detail projection must not invent a current price');
  assert.doesNotMatch(d.querySelector('#selectedProductList').textContent, /¥0\.00/, 'a missing current price must not appear as zero');
  assert.doesNotMatch(d.querySelector('#selectedProductList').textContent, /standard_product:32/, 'the editor must not display a technical target reference as a product name');
  assert.doesNotMatch(d.querySelector('#stage').textContent, /北京时间/, 'the Coupon Host removes time-zone labels while retaining fixed time input semantics');
  for (const outcome of ['rejected', 'malformed']) {
    editOutcome = outcome; d.querySelector('#saveCoupon').click();
    await waitFor(() => !d.querySelector('#saveCoupon').disabled && d.querySelector('#couponFormToast').textContent !== '正在保存…', 'failed save must restore the submit button');
    assert.doesNotMatch(d.querySelector('#couponFormToast').textContent, /已保存/);
    assert.match(d.querySelector('#couponFormToast').textContent, outcome === 'malformed' ? /无法确认/ : /请检查优惠券内容后重新保存/);
    assert.doesNotMatch(d.querySelector('#couponFormToast').textContent, /untrusted machine detail/);
  }
  editOutcome = 'success';
  d.querySelector('#couponName').value = '已修改草稿';
  d.querySelector('#saveCoupon').click();
  await waitFor(() => d.querySelector('#couponFormToast').textContent.includes('优惠券已保存'), 'draft PUT must receive a confirmed saved receipt');
  assert.equal(savedDraft.name, '已修改草稿'); assert.equal(savedDraft.discount_amount_total, 1); assert.deepEqual(savedDraft.target_refs, ['standard_product:32']);
  assert.equal(edits[2].body.claim_starts_at, '2026-09-08T00:00:00.123456Z', 'an unchanged edit preserves the original timestamp precision and instant');
  assert.equal(edits[2].body.claim_ends_at, '2026-09-15T00:00:00Z');
  assert.equal(edits[2].body.use_starts_at, '2026-09-08T02:00:00.123456Z', 'an unchanged fixed-range edit preserves its original use-window precision and instant');
  assert.equal(edits[2].body.use_ends_at, '2026-09-15T02:00:00Z');
  assert.equal(edits.length, 3); assert.match(edits[2].headers.get('Idempotency-Key'), /^coupon-/);
  assert.equal(d.querySelector('#fb-toast').textContent, '', 'confirmed save must not have a simultaneous unavailable-backend toast');
  const relative = d.querySelector('input[name="couponValidityMode"][value="relative_days"]');
  relative.checked = true; relative.dispatchEvent(new editDom.window.Event('change', { bubbles: true }));
  d.querySelector('#couponRelativeDays').value = '7';
  d.querySelector('#saveCoupon').click();
  await waitFor(() => edits.length === 4, 'switching to relative validity must submit one normalized update');
  assert.equal(edits[3].body.validity_mode, 'relative_days');
  assert.equal(edits[3].body.use_starts_at, null, 'switching fixed-range to relative-days clears the hidden use start instead of restoring it');
  assert.equal(edits[3].body.use_ends_at, null, 'switching fixed-range to relative-days clears the hidden use end instead of restoring it');
  assert.equal(edits[3].body.relative_validity_days, 7);
  d.querySelector('#unowned').click(); assert.match(d.querySelector('#fb-toast').textContent, /后端能力未就绪/, 'unrelated unbound actions must remain guarded');
  const api = editDom.window.AdminApi;
  for (const [status, expected] of [[401, '登录状态已失效'], [403, '当前账号没有操作优惠券的权限'], [409, '优惠券内容已变化'], [503, '商品或优惠券信息暂不可用'], [500, '请求未完成']] ) {
    editDom.window.fetch = async () => Response.json({ code: 'unrecognized', message: 'untrusted machine detail' }, { status });
    await assert.rejects(api.requestJson('/api/admin/coupons/18', { method: 'PUT', body: {} }), new RegExp(expected));
  }
  editDom.window.fetch = async () => Response.json({ code: 'CREATE_OUTCOME_UNKNOWN', message: 'untrusted machine detail' }, { status: 503 });
  await assert.rejects(api.requestJson('/api/admin/coupons/18', { method: 'PUT', body: {} }), /保存结果未知/);
} finally { editDom.window.document.body.dataset.page = 'closed'; editDom.window.close(); }

// A Product batch-read failure is an unavailable Coupon detail, not a missing
// or unnamed Product.  The editor must surface that safe Chinese state and
// must not submit any lifecycle command while the read is incomplete.
const unavailableCalls = [];
const unavailableDom = new JSDOM('<!doctype html><body data-page="couponForm"><main id="stage"><textarea id="coupon-target-refs"></textarea></main></body>', {
  url: 'https://test.invalid/admin/couponForm.html?id=18', runScripts: 'dangerously', virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      unavailableCalls.push({ url, method: String(init.method || 'GET').toUpperCase() });
      if (url.pathname === '/assets/standard-components/coupon_form.html') return new Response(donorForm);
      if (url.pathname === '/api/admin/coupons/18') return Response.json({ code: 'unavailable' }, { status: 503 });
      return Response.json({ ok: true });
    };
  },
});
try {
  unavailableDom.window.eval(host);
  await waitFor(() => unavailableDom.window.document.querySelector('[role="alert"]')?.textContent.includes('商品或优惠券信息暂不可用'), 'Product read failure must have a controlled Chinese Coupon error');
  assert.equal(unavailableCalls.some((call) => call.method !== 'GET'), false, 'unavailable detail must not write a coupon');
} finally { unavailableDom.window.document.body.dataset.page = 'closed'; unavailableDom.window.close(); }

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
} finally { failedDom.window.document.body.dataset.page = 'closed'; failedDom.window.close(); }

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
} finally { delayedDom.window.document.body.dataset.page = 'closed'; delayedDom.window.close(); }

console.log('coupon Host actual donor form, picker and lifecycle transport journey: PASS');
