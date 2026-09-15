import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/productForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const frozenPicker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = check();
    if (value) return value;
    await wait(10);
  }
  throw new Error(label + ` diagnostics=${JSON.stringify(diagnostics)}`);
}

const projection = { schema_version: 1, status: 'active', enabled: false, buy_button_text: '立即购买', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, purchase_action_enabled: false, purchase_action_mode: '', wecom_tagging: {}, slices: [] };
const absolute39 = 'https://test.invalid/api/admin/image-library/39/variants/original';
const opaqueUpload = 'https://uploads.example.invalid/products/original-current.png';
const initial = { id: 101, product_code: 'order-preserving-product', name: '顺序保留商品', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [absolute39, opaqueUpload, '/api/admin/image-library/38/variants/original'], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' };
let saved;
const diagnostics = [];
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/productForm.html?id=101', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: (() => { const console = new VirtualConsole(); console.on('jsdomError', (error) => diagnostics.push(String(error.stack || error.message))); return console; })(),
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.fetch = async (input, init = {}) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      diagnostics.push(`${method} ${url.pathname}${url.search}`);
      const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/products/101') {
        if (method === 'PUT') { saved = JSON.parse(String(init.body)); return json({ ...initial, ...saved, version: 2 }); }
        return json(initial);
      }
      if (url.pathname === '/api/v1/products/101/local-entitlements') return json({ items: [] });
      if (url.pathname === '/api/v1/products' && method === 'GET') return json({ items: [initial], next_cursor: '' });
      if (url.pathname === '/api/admin/wechat-pay/products/101/external-push') return json({ product_id: 101, product_kind: 'wechat_pay', enabled: false, configuration_reference: '', updated_at: '' });
      if (url.pathname === '/api/admin/image-library/38') return json({ item: { id: 38, name: '待移除素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true } });
      if (url.pathname === '/api/admin/image-library/39') return json({ item: { id: 39, name: '保留字面 URL 素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true } });
      if (url.pathname === '/api/admin/image-library/40') return json({ item: { id: 40, name: '后续新增素材', original_url: '/api/admin/image-library/40/variants/original', thumb_320_url: '/api/admin/image-library/40/variants/thumb_320', enabled: true } });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return json({ items: [{ id: 38, name: '待移除素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }], has_more: true, next_offset: 1 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return json({ items: [{ id: 39, name: '保留字面 URL 素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: true, next_offset: 2 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '2') return json({ items: [{ id: 40, name: '后续新增素材', original_url: '/api/admin/image-library/40/variants/original', thumb_320_url: '/api/admin/image-library/40/variants/thumb_320', enabled: true }], has_more: false });
      if (url.pathname === '/api/admin/image-library') return json({ items: [{ id: 38, name: '待移除素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }, { id: 39, name: '保留字面 URL 素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: false });
      if (url.pathname === '/api/admin/channels' || url.pathname === '/api/admin/wecom/tags' || url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return json({ items: [], groups: [], total: 0, has_more: false, tag_limit: 1000, read_model_status: 'ready' });
      if (url.pathname === '/api/admin/config') return json({ categories: [] });
      if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return json({});
      return json({ code: 'unexpected_product_material_order_request', path: url.pathname }, 500);
    };
  },
});

dom.window.eval(frozenPicker);
dom.window.eval(host);
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
const document = dom.window.document;
await waitFor(() => document.getElementById('pfName'), 'the real frozen product editor must load the existing owner draft');
document.querySelector('a[href="#product-media"]').click();
const open = await waitFor(() => [...document.querySelectorAll('#product-media button')].find((button) => button.textContent.trim() === '从素材库选择'), 'product material caller did not mount');
open.click();
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('保留字面 URL 素材') && document.querySelector('[data-v3-picker-selected]')?.textContent.includes('待移除素材'), 'reopening must resolve both recognized current library URLs without treating the external URL as a library item');
assert.equal(document.querySelector('[data-v3-picker-selected]').textContent.includes('products/original-current'), false, 'the opaque uploaded URL must stay in the original owner draft, not become a fake library record');
document.querySelector('[data-v3-material-remove$=":38"]').click();
document.querySelector('[data-v3-picker-more]').click();
await waitFor(() => document.querySelector('[data-v3-material-key$=":39"]'), 'the second page must remain available after a temporary removal');
document.querySelector('[data-v3-picker-more]').click();
const appended = await waitFor(() => document.querySelector('[data-v3-material-key$=":40"]'), 'the authorized later page must load for a new product material');
appended.click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => !document.querySelector('[data-v3-selection-session="material"]'), 'the owner draft must accept the full selection before V3 closes');
const save = [...document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
save.click();
await waitFor(() => Array.isArray(saved?.images), 'the original product save must serialize the caller-owned draft');
assert.deepEqual(saved.images, [absolute39, opaqueUpload, '/api/admin/image-library/40/variants/original'], 'canonical matching may decide membership but must preserve surviving literal URL order and append only the new authorized material');
open.click();
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('保留字面 URL 素材') && document.querySelector('[data-v3-picker-selected]')?.textContent.includes('后续新增素材'), 'reopen must read the owner draft after save rather than a stale dialog cache');
document.querySelector('[data-v3-picker-cancel]').click();
dom.window.close();
console.log('product material owner URL ordering and opaque-draft preservation: PASS');
