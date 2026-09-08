import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/productForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const admin = await buildTestBrowserBundle(path.join(root, 'web/src/admin/main.ts'));
const standardTagPicker = fs.readFileSync(path.join(root, 'web/dist/assets/standard-components/wecom_tag_picker.js'), 'utf8');
const standardMaterialPicker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const value = check();
    if (value) return value;
    await wait(20);
  }
  throw new Error(message);
}

const projection = { schema_version: 1, status: 'active', enabled: true, buy_button_text: '立即购买', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, purchase_action_enabled: false, purchase_action_mode: '', wecom_tagging: {}, slices: [] };
const created = { id: 101, product_code: 'recovery-product', name: '恢复商品', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z' };
const calls = [];
let externalAttempts = 0;
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', () => undefined);
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/productForm.html', runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      calls.push({ path: url.pathname, method, key: new Headers(init.headers || (input instanceof Request ? input.headers : undefined)).get('Idempotency-Key') || '', body: typeof init.body === 'string' ? init.body : '' });
      const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/products' && method === 'GET') return reply({ items: [], next_cursor: '' });
      if (url.pathname === '/api/v1/products' && method === 'POST') return reply(created);
      if (url.pathname === '/api/admin/wechat-pay/products/101/external-push' && (method === 'POST' || method === 'PUT')) {
        externalAttempts += 1;
        if (externalAttempts === 1) return reply({ code: 'dependency_unavailable' }, 503);
        return reply({ product_id: 101, product_kind: 'wechat_pay', enabled: true, configuration_reference: 'recovery.push', updated_at: '2026-09-08T00:01:00Z' });
      }
      if (url.pathname === '/api/admin/channels') return reply({ items: [], total: 0 });
      if (url.pathname === '/api/admin/wecom/tags') return reply({ groups: [{ group_id: 4, group_name: '已同步标签' }], items: [{ tag_id: 37, tag_name: '已购买', group_id: 4, group_name: '已同步标签' }] });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return reply({ items: [{ id: 38, name: '页面素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }], total: 1, has_more: true, next_offset: 1 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return reply({ items: [{ id: 39, name: '后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], total: 2, has_more: false });
      if (url.pathname === '/api/admin/image-library') return reply({ items: [{ id: 39, name: '后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], total: 1, has_more: false });
      if (url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return reply({ items: [], total: 0, has_more: false });
      if (url.pathname === '/api/admin/config') return reply({ categories: [] });
      if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return reply({});
      return reply({ code: 'unexpected_product_request' }, 500);
    };
  },
});

dom.window.eval(standardTagPicker);
dom.window.eval(standardMaterialPicker);
dom.window.eval(host);
dom.window.eval(admin);
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
await waitFor(() => dom.window.document.getElementById('pfName'), 'frozen product form must mount through the real Admin client');
const materialOpen = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '从素材库选择');
materialOpen.click();
const materialRow = await waitFor(() => dom.window.document.querySelector('[data-picker-id="39"]'), 'the original product picker must retain later catalog pages');
const legacyProductMask = dom.window.document.querySelector('.pk-mask');
assert.equal(legacyProductMask.style.getPropertyPriority('display'), 'important', 'the delayed frozen product picker must stay hidden');
materialRow.click();
await waitFor(() => [...dom.window.document.querySelectorAll('img')].some((image) => image.src.includes('/39/variants/thumb_320')), 'the frozen form must render the original later-page material selection');
dom.window.eval(standardTagPicker);
const tagOpen = await waitFor(() => dom.window.document.querySelector('[data-product-tag-open]'), 'standard product tag control must mount');
tagOpen.click();
const tagRow = await waitFor(() => dom.window.document.querySelector('[data-tag-key="37"]'), 'real tag catalog row must render in the original picker');
tagRow.click();
dom.window.document.querySelector('[data-action="confirm"]').click();
const tagEnabled = dom.window.document.querySelector('[data-product-tag-enabled]');
tagEnabled.checked = true; tagEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => dom.window.document.getElementById('pfWecomTagging').value === '{"enabled":true,"tag_ids":[37]}', 'original tag picker must persist an enabled canonical numeric tag id');
tagEnabled.checked = false; tagEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(dom.window.document.getElementById('pfWecomTagging').value, '{"enabled":false,"tag_ids":[37]}', 'disabling tags preserves the selected draft without enabling paid tagging');
tagEnabled.checked = true; tagEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
const actionEnabled = dom.window.document.querySelector('[data-product-purchase-enabled]');
actionEnabled.checked = true; actionEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
const qr = dom.window.document.querySelector('input[name="pfPurchaseActionMode"][value="qr"]');
qr.checked = true; qr.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(dom.window.document.getElementById('pfLeadQrTitle').closest('div[style*=\"display:grid\"]')?.hidden, false, 'QR fields must appear only for QR mode');
assert.equal(dom.window.document.getElementById('pfCompletionRedirectUrl').closest('div[style*=\"display:grid\"]')?.hidden, true, 'redirect fields must stay hidden in QR mode');
for (const [id, value] of [['pfName', '恢复商品'], ['pfCode', 'recovery-product'], ['pfPrice', '0.02'], ['pfStock', '1'], ['pfExternalPushReference', 'recovery.push']]) {
  const field = dom.window.document.getElementById(id);
  field.value = value;
  field.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
}
const push = dom.window.document.getElementById('pfExternalPushEnabled');
push.value = 'true'; push.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
const save = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
assert.ok(save, 'frozen product form must retain save action');
save.click();
save.click();
await waitFor(() => dom.window.document.querySelector('#fb-toast')?.textContent.includes('商品主体已保存'), 'first external-push failure must retain created product for recovery');
const creates = calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST');
assert.equal(creates.length, 1, 'duplicate save clicks must create one product');
assert.match(creates[0].key, /^product-save-/, 'subject create must carry an idempotency key');
const createPayload = JSON.parse(creates[0].body);
assert.deepEqual(createPayload.admin_projection.wecom_tagging, { enabled: true, tag_ids: [37] }, 'product save must forward the enabled catalog-selected numeric tag IDs');
assert.deepEqual(createPayload.images, ['/api/admin/image-library/39/variants/original'], 'product save must preserve the original picker later-page URL');
assert.equal(createPayload.admin_projection.purchase_action_enabled, true, 'product save must enable the selected purchase action');
assert.equal(createPayload.admin_projection.purchase_action_mode, 'qr', 'product save must preserve the selected QR action mode');
assert.equal(new URL(dom.window.location.href).searchParams.get('id'), '101', 'failed external push must recover the created ID into the editor URL');

const recoverySave = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
assert.ok(recoverySave, 'recovery must keep a live frozen save action');
recoverySave.click();
await waitFor(() => calls.filter((call) => call.path === '/api/admin/wechat-pay/products/101/external-push').length === 2, 'recovery retry must continue only the external-push operation');
assert.equal(calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST').length, 1, 'recovery retry must never create a second product');
const external = calls.filter((call) => call.path === '/api/admin/wechat-pay/products/101/external-push');
assert.equal(external[0].key, external[1].key, 'external-push recovery must reuse its original idempotency key');

await wait(300);
dom.window.close();
console.log('product Host duplicate-save and partial-recovery journey: PASS');
