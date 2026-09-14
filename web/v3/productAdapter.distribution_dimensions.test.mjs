import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/productForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = check();
    if (value) return value;
    await wait(20);
  }
  throw new Error(message);
}

const policy = { enabled: false, commission_rate_basis_points: 1234, wait_days: 8, version: 2 };
let persistedPolicy = { ...policy };
let productVersion = 7;
const writes = [];
const requests = [];
const editorConsole = new VirtualConsole();
editorConsole.on('jsdomError', (error) => process.stderr.write(`JSDOM: ${error.message}\n`));
const projection = {
  schema_version: 1, status: 'draft', enabled: false, buy_button_text: '', require_mobile: false,
  lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '',
  completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null,
  purchase_action_enabled: false, purchase_action_mode: '', wecom_tagging: {}, slices: [],
};
const product = () => ({
  id: 101, product_code: 'dimension-policy', name: '维度分销商品', description: '', price_minor: 990,
  currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft',
  enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: productVersion,
  created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', distribution_policy: persistedPolicy,
});
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/wechat-pay/products/101/edit', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: editorConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      requests.push({ path: url.pathname, method });
      const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/products/101') {
        if (method === 'PUT') {
          const body = JSON.parse(init.body);
          writes.push(body);
          if (Object.hasOwn(body, 'distribution_policy')) persistedPolicy = body.distribution_policy;
          productVersion += 1;
        }
        return reply(product());
      }
      if (url.pathname === '/api/v1/products') return reply({ items: [product()], next_cursor: '' });
      if (url.pathname === '/api/admin/wechat-pay/products/101/external-push') {
        return reply({ product_id: 101, product_kind: 'wechat_pay', enabled: false, configuration_reference: '', updated_at: '2026-09-15T00:00:00Z' });
      }
      if (url.pathname === '/api/admin/channels' || url.pathname === '/api/admin/image-library' || url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return reply({ items: [], total: 0, has_more: false });
      if (url.pathname === '/api/admin/wecom/tags') return reply({ groups: [], items: [] });
      if (url.pathname === '/api/admin/config') return reply({ categories: [] });
      if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return reply({});
      return reply({ code: 'unexpected_product_request', path: url.pathname }, 500);
    };
  },
});

dom.window.eval(host);
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
const document = dom.window.document;
const sale = await waitFor(() => document.getElementById('product-sale'), 'ordinary product editor did not mount');
const distribution = await waitFor(() => document.querySelector('[data-distribution-policy]'), 'sale dimension did not mount its distribution controls');
assert.equal(distribution.parentElement, sale, 'distribution controls must be owned by the sale dimension');
assert.equal(distribution.querySelectorAll('input').length, 3, 'policy exposes only its switch, commission rate, and refund wait days');
for (const selector of ['[data-distribution-application-entry]', '[data-distribution-application-link]', '[data-distribution-application-qr]', '[data-distribution-application-pending]']) {
  assert.equal(document.querySelector(selector), null, `${selector} must not exist in the product editor`);
}

const rate = distribution.querySelector('[data-distribution-policy-rate]');
rate.value = '23.45';
rate.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
for (const id of ['product-media', 'product-action', 'product-wecom', 'product-push']) {
  document.querySelector(`a[href="#${id}"]`).click();
  assert.equal(document.querySelector(`#${id} [data-distribution-policy]`), null, `${id} must not render distribution controls`);
  assert.equal(rate.value, '23.45', 'switching dimensions must retain the sale draft');
  if (id === 'product-push') continue;
  const save = [...document.querySelectorAll(`#${id} button`)].find((button) => button.textContent.trim() === '保存当前维度');
  assert.ok(save, `${id} must retain its own save button`);
  const expectedWrites = writes.length + 1;
  save.click();
  await waitFor(() => writes.length === expectedWrites, `${id} save did not write its current dimension; requests=${JSON.stringify(requests)}`);
  assert.equal(Object.hasOwn(writes.at(-1), 'distribution_policy'), false, `${id} save must leave the stored distribution policy untouched`);
}

const afterOtherDimensions = await dom.window.fetch('/api/v1/products/101').then((response) => response.json());
assert.deepEqual(afterOtherDimensions.distribution_policy, policy, 'other-dimension saves must leave the server-read policy unchanged');

document.querySelector('a[href="#product-sale"]').click();
assert.equal(rate.value, '23.45', 'returning to sale information must retain its draft');
const saleSave = [...sale.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
assert.ok(saleSave, 'sale information must retain its save button');
const expectedWrites = writes.length + 1;
saleSave.click();
await waitFor(() => writes.length === expectedWrites, 'sale save did not write the product');
assert.deepEqual(writes.at(-1).distribution_policy, { enabled: false, commission_rate_basis_points: 2345, wait_days: 8, version: 2 }, 'sale save must persist the disabled policy exactly as read and drafted');
const afterSaleSave = await dom.window.fetch('/api/v1/products/101').then((response) => response.json());
assert.deepEqual(afterSaleSave.distribution_policy, { enabled: false, commission_rate_basis_points: 2345, wait_days: 8, version: 2 }, 'sale save must persist the drafted policy for the next server read');

dom.window.close();
console.log('product distribution policy dimensions, drafts, and save boundaries: PASS');
