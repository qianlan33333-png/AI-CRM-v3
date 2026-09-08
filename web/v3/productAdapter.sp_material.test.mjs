import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/spProductForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const admin = await buildTestBrowserBundle(path.join(root, 'web/src/admin/main.ts'));
const materialPicker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, label) { for (let i = 0; i < 80; i += 1) { if (check()) return; await wait(20); } throw new Error(label); }
const calls = [];
const projection = { schema_version: 1, status: 'draft', enabled: false, buy_button_text: '', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, wecom_tagging: {}, slices: [] };
const dom = new JSDOM(page, { url: 'https://test.invalid/admin/spProductForm.html', runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole: new VirtualConsole(), beforeParse(window) {
  window.__AICRM_TEST_MOCK__ = false; window.Request = Request; window.Response = Response; window.Headers = Headers;
  window.fetch = async (input, init = {}) => {
    const url = new URL(input instanceof Request ? input.url : String(input), window.location.href); const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
    calls.push({ path: url.pathname, method, body: typeof init.body === 'string' ? init.body : '' }); const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
    if (url.pathname === '/api/admin/service-period-products' && method === 'POST') return json({ product: { service_product_id: 201, product_code: 'sp-media', name: '周期素材', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: ['/api/admin/image-library/39/variants/original'], admin_projection: projection, version: 1 } }, 201);
    if (url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/v1/products') return json({ items: [], total: 0, has_more: false });
    if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return json({ items: [{ id: 38, name: '首页素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }], has_more: true, next_offset: 1 });
    if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return json({ items: [{ id: 39, name: '周期后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: false });
    if (url.pathname === '/api/admin/image-library') return json({ items: [{ id: 39, name: '周期后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: false });
    if (['/api/admin/channels','/api/admin/wecom/tags','/api/admin/attachment-library','/api/admin/mini-program-library','/api/admin/wecom/tag-groups','/api/admin/questionnaires','/api/admin/customers','/api/admin/orders','/api/admin/coupons'].includes(url.pathname)) return json({ items: [], groups: [], total: 0, has_more: false });
    if (['/api/admin/config','/api/admin/app-settings','/api/admin/push-capabilities','/api/admin/releases'].includes(url.pathname)) return json({ categories: [] });
    return json({ code: 'unexpected' }, 500);
  };
} });
const fixtureFetch = dom.window.fetch;
dom.window.eval(materialPicker); dom.window.eval(host); dom.window.eval(admin); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
const document = dom.window.document;
await waitFor(() => document.getElementById('spfName'), 'frozen periodic form did not mount');
await waitFor(() => document.querySelector('#sp-wecom [data-product-tag-open]') && document.querySelector('#sp-action [data-product-purchase-enabled]'), 'periodic action and tag controls must mount');
document.getElementById('spfName').value = '切换保留';
for (const id of ['sp-sale', 'sp-media', 'sp-action', 'sp-wecom', 'sp-push']) {
  document.querySelector(`a[href="#${id}"]`).click();
  const visible = ['sp-sale', 'sp-media', 'sp-action', 'sp-wecom', 'sp-push'].filter(key => dom.window.getComputedStyle(document.getElementById(key)).display !== 'none');
  assert.deepEqual(visible, [id], 'only the selected periodic dimension is visible');
  assert.equal(document.getElementById('spfName').value, '切换保留', 'dimension switch preserves unsaved input');
}
document.querySelector('a[href="#sp-action"]').click();
const actionEnabled = document.querySelector('[data-product-purchase-enabled]');
actionEnabled.checked = true; actionEnabled.dispatchEvent(new dom.window.Event('change', {bubbles:true}));
const qrMode = document.querySelector('input[name="spfPurchaseActionMode"][value="qr"]');
qrMode.checked = true; qrMode.dispatchEvent(new dom.window.Event('change', {bubbles:true}));
assert.notEqual(dom.window.getComputedStyle(document.getElementById('spfLeadQrTitle').parentElement).display, 'none');
assert.equal(dom.window.getComputedStyle(document.getElementById('spfCompletionRedirectUrl').parentElement).display, 'none');
actionEnabled.checked = false; actionEnabled.dispatchEvent(new dom.window.Event('change', {bubbles:true}));
document.querySelector('a[href="#sp-media"]').click();

const open = [...document.querySelectorAll('button')].find((button) => button.textContent.trim() === '从素材库选择'); open.click();
await waitFor(() => document.querySelector('[data-picker-id="39"]'), 'original picker did not include later periodic catalog page');
assert.equal(document.querySelector('.pk-mask').style.getPropertyPriority('display'), 'important', 'frozen periodic picker was not suppressed');
document.querySelector('[data-picker-close]').click();
await waitFor(() => document.querySelector('.pk-mask') === null, 'periodic cancel left frozen picker pending');
assert.equal(document.querySelector('#sp-media img'), null, 'periodic cancel changed the draft');
open.click(); await waitFor(() => document.querySelector('[data-picker-id="39"]'), 'original picker did not reopen after cancel'); document.querySelector('[data-picker-id="39"]').click();
await waitFor(() => [...document.querySelectorAll('#sp-media img')].some((image) => image.src.includes('/39/variants/thumb_320')), 'periodic frozen form did not receive original material');
for (const [id, value] of [['spfName', '周期素材'], ['spfCode', 'sp-media'], ['spfPrice', '0.02'], ['spfStock', '1']]) document.getElementById(id).value = value;
const save = [...document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度'); save.click();
await waitFor(() => calls.some((call) => call.path === '/api/admin/service-period-products' && call.method === 'POST'), 'periodic frozen save did not write');
const body = JSON.parse(calls.find((call) => call.path === '/api/admin/service-period-products' && call.method === 'POST').body);
assert.deepEqual(body.images, ['/api/admin/image-library/39/variants/original'], 'periodic save lost original selected URL');
await wait(300);
const savedProjection = { ...projection, purchase_action_enabled: true, purchase_action_mode: 'redirect', completion_redirect_enabled: true, completion_redirect_url: '/complete', wecom_tagging: { enabled: false, tag_ids: [37] } };
const reopened = new JSDOM(page, { url: 'https://test.invalid/admin/spProductForm.html?id=201', runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole: new VirtualConsole(), beforeParse(window) {
  window.__AICRM_TEST_MOCK__ = false; window.Request = Request; window.Response = Response; window.Headers = Headers;
  window.fetch = async (input, init = {}) => {
    const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
    if (url.pathname === '/api/admin/service-period-products/201') return new Response(JSON.stringify({product:{service_product_id:201,product_code:'sp-media',name:'已保存周期',price_minor:2,currency:'CNY',stock_quantity:1,images:[],admin_projection:savedProjection,version:2}}), {headers:{'Content-Type':'application/json'}});
    if (url.pathname.startsWith('/api/admin/service-period-products/201/')) return new Response(JSON.stringify({items:[],enabled:false,configuration_reference:'',service_product_id:201}), {headers:{'Content-Type':'application/json'}});
    return fixtureFetch(input, init);
  };
} });
reopened.window.eval(host); reopened.window.eval(admin); reopened.window.document.dispatchEvent(new reopened.window.Event('DOMContentLoaded'));
await waitFor(() => reopened.window.document.querySelector('[data-product-purchase-enabled]'), 'saved periodic action controls did not reopen');
assert.equal(reopened.window.document.querySelector('[data-product-purchase-enabled]').checked, true, 'reopening must retain the saved action switch');
assert.equal(reopened.window.document.querySelector('input[name="spfPurchaseActionMode"][value="redirect"]').checked, true, 'reopening must retain redirect mode');
assert.equal(reopened.window.document.querySelector('[data-product-tag-enabled]').checked, false, 'saved disabled tags must not be silently enabled from nonempty selections');
reopened.window.close(); dom.window.close(); console.log('periodic product dimensions, action reload, material selection, cancel, and save: PASS');
