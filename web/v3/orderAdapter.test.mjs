import assert from 'node:assert/strict';
import { build } from 'esbuild';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = await build({ stdin: { contents: "import './web/v3/orderAdapter'; import {AdminController} from './web/src/admin/controller'; window.OrderControllerFixture = AdminController;", resolveDir: root, loader: 'ts' }, bundle: true, format: 'iife', write: false, platform: 'browser', logLevel: 'silent' });
const host = bundle.outputFiles[0].text;
const pause = () => new Promise((resolve) => setTimeout(resolve, 15));

const calls = [];
const dom = new JSDOM(`<!doctype html><body>
  <input id="orderTransactionId"><input id="orderProductCode"><input id="orderMobile"><button type="button">导出微信支付 CSV</button>
  <table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th></tr></thead><tbody><tr><td>2026-09-08T00:00:00Z</td><td><div>merchant-1</div></td><td><div>付款人姓名</div><div>customer:123</div></td></tr></tbody></table>
</body>`, {
  url: 'https://test.invalid/admin/orders', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      calls.push(url);
      return new Response(JSON.stringify({ items: [{ created_at: '2026-09-08T00:00:00Z', payer_name: '付款人姓名', payer_id: 'customer:123', provider_label: '微信支付', currency: 'CNY' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});

try {
  dom.window.eval(host);
  await pause();
  const document = dom.window.document;
  assert.equal(document.querySelector('thead th:nth-child(3)').textContent, '付款人', 'the list must not label an internal identity as customer-facing data');
  assert.equal(document.querySelector('tbody td:first-child').textContent, '2026-09-08 08:00:00', 'created time must display Beijing seconds without a zone suffix');
  assert.equal(document.querySelector('tbody td:nth-child(3) div:nth-child(2)').hidden, true, 'internal customer references must not render under the payer name');
  // A mutation after formatting must preserve Beijing text; it may not treat
  // completed display text as a new UTC instant under a non-Shanghai browser.
  document.querySelector('tbody tr').append(document.createElement('span')); await pause();
  assert.equal(document.querySelector('tbody td:first-child').textContent, '2026-09-08 08:00:00', 'repeated DOM presentation must not shift Beijing time');

  const controller = new dom.window.OrderControllerFixture({ mode: 'http' }, 'orders');
  controller.state.orderFilters = { transactionId: 'server-platform-reference', payer: '13800138000', product: 'server-product-code', status: '', createdFrom: '', createdTo: '' };
  controller.db.rows.orders = [{ no: 'merchant-returned', payer: '实际客户姓名', uid: 'customer:7', product: '实际商品名', time: '2026-09-08', tone: 'ok' }];
  controller.db.orderList = { total: 51, hasMore: true };
  controller.state.orderOffset = 50;
  const values = controller.renderVals();
  assert.equal(values.rows.orders.length, 1, 'server-resolved phone/contact matches must not be discarded by donor local name filtering');
  assert.equal(values.orderPage.filters.payer, '13800138000', 'rendering must retain the user query');
  assert.match(values.orderPage.summary, /51/, 'server paging must retain the returned total and offset');

  document.getElementById('orderMobile').value = '138 0013 8000';
  document.querySelector('button').click(); await pause();
  assert.match(document.body.textContent, /筛选暂不支持导出/, 'identity-filtered result sets must not silently export all orders');
  document.getElementById('orderMobile').value = '138 0013 8000';
  await dom.window.fetch('/api/admin/orders?limit=50&offset=0');
  assert.equal(calls.at(-1).searchParams.get('phone'), '13800138000', 'phone searches must stay server-side and preserve paging');
  assert.equal(calls.at(-1).searchParams.get('external_userid'), null, 'phone and external-contact filters are mutually exclusive');

  document.getElementById('orderMobile').value = 'external-contact-fixture';
  await dom.window.fetch('/api/admin/orders?limit=50&offset=50');
  assert.equal(calls.at(-1).searchParams.get('external_userid'), 'external-contact-fixture', 'external-contact searches must use the server identity filter');
  assert.equal(calls.at(-1).searchParams.get('phone'), null, 'external-contact filters must not add a phone dimension');
} finally {
  dom.window.close();
}

console.log('order Host identity query and presentation journey: PASS');
