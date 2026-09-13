import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(new URL('./distributionCenter.ts', import.meta.url).pathname);
const delay = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, message) { for (let attempt = 0; attempt < 100; attempt++) { if (check()) return; await delay(); } throw new Error(message); }
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const calls = [];
const me = { distributor: { public_no: 'D-0001', enabled: true, agreement_version: '2026-09', registered_at: '2026-09-14T00:00:00Z' }, receiver: { ready: true, reason: '', app_id: 'wx-app' }, registration_required: false, current_agreement_version: '2026-09' };
const dom = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL; window.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new window.Event('close')); };
  window.fetch = async (input, init = {}) => { const url = new URL(String(input), window.location.href); calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body || '', idempotencyKey: new Headers(init.headers).get('Idempotency-Key') });
    if (url.pathname === '/api/v1/distribution/me') return json(me);
    if (url.pathname === '/api/v1/distribution/agreement') return json({ version: '2026-09', content: '分销协议正文' });
    if (url.pathname === '/api/v1/distribution/products') return json({ items: [{ product_id: 7, product_type: 'standard_product', cover_url: '', purchase_url: '/p/growth-course', name: '增长课', price_minor: 19900, currency: 'CNY', commission_rate_basis_points: 333, estimated_commission_minor: 662, wait_days: 7, promotion_ready: true, promotion_block_reason: '' }], next_cursor: '' });
    if (url.pathname === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 19900, successful_refunds_minor: 0, initial_commission_minor: 662, commission_adjustments_minor: 0, unsettled_payable_minor: 662, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' });
    if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [{ commission_id: 'c1', order_reference: 'O-1', product_name: '增长课', initial_minor: 662, current_payable_minor: 662, paid_minor: 0, status: 'pending', hold_reason: '', cancel_reason: '', exception_reason: '', paid_confirmed_at: '2026-09-14T00:00:00Z', due_at: '2026-09-21T00:00:00Z', paid_at: '', created_at: '2026-09-14T00:00:00Z', currency: 'CNY' }], next_cursor: '' });
    if (url.pathname === '/api/v1/distribution/products/7/promotion-credentials') return json({ promotion_url: 'https://crm.example/d/dpc_12345678901234567890', credential_expires_at: '2026-09-15T00:00:00Z' });
    return json({ error: 'not_found' }, 404);
  };
} });
dom.window.eval(bundle); await waitFor(() => dom.window.document.body.textContent.includes('增长课'), 'distribution products did not render');
assert.match(dom.window.document.body.textContent, /预计 ¥6\.62/, 'estimated commission must render server minor amount');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '生成推广入口').click();
await waitFor(() => calls.some((call) => call.path.endsWith('/promotion-credentials')), 'promotion credential did not use the real API');
await waitFor(() => dom.window.document.querySelector('dialog'), 'promotion dialog did not render');
const credential = calls.find((call) => call.path.endsWith('/promotion-credentials'));
assert.deepEqual(JSON.parse(credential.body), { product_type: 'standard_product' }, 'credential request must not carry distributor, amount, receiver, or policy');
assert.match(credential.idempotencyKey || '', /^[0-9a-f-]{16,}$/i, 'credential request must carry a stable server replay key');
assert.equal(dom.window.document.querySelector('a[href="/p/growth-course"]')?.textContent, '查看商品并购买', 'purchase entry must use server-provided URL');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '关闭')?.click();
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '我的收益').click();
await waitFor(() => dom.window.document.body.textContent.includes('累计推广成交额'), 'earnings tab did not render');
assert.match(dom.window.document.body.textContent, /未结算佣金/, 'earnings must expose unsettled definition');
dom.window.close();

let registrationBody; let registrationIdempotencyKey;
const registration = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL; window.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new window.Event('close')); };
  window.fetch = async (input, init = {}) => { const path = new URL(String(input), window.location.href).pathname; if (path === '/api/v1/distribution/me') return json(registrationBody ? me : { distributor: null, receiver: { ready: false, reason: '待验证', app_id: '' }, registration_required: true, current_agreement_version: '2026-09' }); if (path === '/api/v1/distribution/agreement') return json({ version: '2026-09', content: '分销协议正文' }); if (path === '/api/v1/distribution/registration') { registrationBody = JSON.parse(String(init.body)); registrationIdempotencyKey = new Headers(init.headers).get('Idempotency-Key'); return json(me); } if (path === '/api/v1/distribution/products') return json({ items: [], next_cursor: '' }); if (path === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 0, successful_refunds_minor: 0, initial_commission_minor: 0, commission_adjustments_minor: 0, unsettled_payable_minor: 0, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' }); if (path === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' }); return json({ error: 'not_found' }, 404); };
} });
registration.window.eval(bundle); await waitFor(() => registration.window.document.body.textContent.includes('申请成为分销员'), 'registration view did not render');
const check = registration.window.document.querySelector('input[type="checkbox"]'); check.checked = true; check.dispatchEvent(new registration.window.Event('change', { bubbles: true }));
[...registration.window.document.querySelectorAll('button')].find((button) => button.textContent === '同意并注册').click();
await waitFor(() => registrationBody, 'registration did not call the real API');
assert.deepEqual(registrationBody, { agreement_version: '2026-09' }, 'registration may submit only server-advertised agreement version');
assert.match(registrationIdempotencyKey || '', /^[0-9a-f-]{16,}$/i, 'registration must carry a replay key');
await waitFor(() => registration.window.document.body.textContent.includes('分销员编号'), 'registration reload did not settle');
registration.window.close();
console.log('distribution center contract: PASS');

let preparationRequest;
const preparation = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL; window.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new window.Event('close')); };
  window.fetch = async (input, init = {}) => { const path = new URL(String(input), window.location.href).pathname; if (path === '/api/v1/distribution/me') return json({ ...me, receiver: { ready: false, reason: '尚未完成收款准备', app_id: 'wx-app' } }); if (path === '/api/v1/distribution/agreement') return json({ version: '2026-09', content: '分销协议正文' }); if (path === '/api/v1/distribution/products') return json({ items: [], next_cursor: '' }); if (path === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 0, successful_refunds_minor: 0, initial_commission_minor: 0, commission_adjustments_minor: 0, unsettled_payable_minor: 0, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' }); if (path === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' }); if (path === '/api/v1/distribution/receiver-preparation') { preparationRequest = { method: init.method, body: init.body || '', idempotencyKey: new Headers(init.headers).get('Idempotency-Key') }; return json({ receiver: { ready: false, reason: '正在处理', reference: 'r-1', app_id: 'wx-app', checked_at: '2026-09-14T00:00:00Z' }, setup: { state: 'processing', action_url: '/api/v1/distribution/receiver-preparation', retry_after_seconds: 30 } }); } return json({ error: 'not_found' }, 404); };
} });
preparation.window.eval(bundle); await waitFor(() => preparation.window.document.body.textContent.includes('完成收款准备'), 'receiver preparation action did not render');
[...preparation.window.document.querySelectorAll('button')].find((button) => button.textContent === '完成收款准备').click();
await waitFor(() => preparationRequest, 'receiver preparation did not use the real API');
assert.deepEqual({ method: preparationRequest.method, body: preparationRequest.body }, { method: 'POST', body: '' }, 'receiver preparation must not submit identity, receiver, or app identifiers');
assert.match(preparationRequest.idempotencyKey || '', /^[0-9a-f-]{16,}$/i, 'receiver preparation must carry a replay key');
await waitFor(() => preparation.window.document.body.textContent.includes('30 秒后'), 'receiver preparation processing state did not render');
preparation.window.close();
console.log('distribution receiver preparation contract: PASS');
