#!/usr/bin/env node
import assert from 'node:assert/strict';
import { JSDOM, VirtualConsole } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = await buildTestBrowserBundle(path.join(repository, 'web', 'v3', 'orderAdapter.ts'));
const requests = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', () => undefined);
const dom = new JSDOM(`<!doctype html><body data-page="orders"><div><input id="orderTransactionId" value="WX-1"><input id="orderProductCode" value="sku-a"><input id="orderMobile" value="wm-order-fixture"><button>查询</button></div><table><tbody><tr><td>1</td><td><div>merchant-ref-9</div></td><td></td><td></td><td></td><td></td><td></td><td><a data-capability-state="real">查看详情</a></td></tr></tbody></table><script>${bundle}</script></body>`, {
  url: 'https://test.invalid/admin/orders.html', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      requests.push(url);
      return new Response(JSON.stringify({ items: [{ currency: 'CNY', provider_label: '微信支付' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  await new Promise((resolve) => setTimeout(resolve, 20));
  const response = await dom.window.fetch('/api/admin/orders?limit=20');
  assert.deepEqual(await response.json(), { items: [{ currency: '微信支付', provider_label: '微信支付' }] }, 'provider channel must occupy donor payment-channel display field');
  assert.equal(requests[0].searchParams.get('order_ref'), 'WX-1');
  assert.equal(requests[0].searchParams.get('product'), 'sku-a');
  assert.equal(requests[0].searchParams.get('external_userid'), 'wm-order-fixture');
  assert.equal(requests[0].searchParams.has('customer_id'), false, 'the browser never interprets a search value as an internal customer identity');
  const link = dom.window.document.querySelector('a');
  link.click();
  assert.match(link.href, /orderDetail\.html\?id=merchant-ref-9/, 'actual detail action must target the server-backed detail route');
  assert.equal(link.textContent, '正在打开…');
  console.log('order host query and detail journey: PASS');
} finally { dom.window.close(); }
