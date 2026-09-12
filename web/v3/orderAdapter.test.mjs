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
  <table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th><th>商品</th><th>金额</th><th>状态</th><th>支付来源</th><th>操作</th></tr></thead><tbody><tr><td>2026-09-08T00:00:00Z</td><td><div>merchant-1</div></td><td><div>付款人姓名</div><div>customer:123</div></td><td>测试商品</td><td>20.00</td><td><span>paid</span></td><td>wechat</td><td><a>查看详情</a></td></tr></tbody></table>
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
  assert.equal(document.querySelector('tbody td:nth-child(6) span').textContent, '已支付', 'the order-list status must not expose the raw protocol enum');
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

const detailCalls = [];
const detailDom = new JSDOM(`<!doctype html><body data-page="orderDetail">
  <div><span>M-ORDER-TEST-0001</span><span>paid</span></div>
  <div><div><h2>订单详情</h2></div><div></div></div>
  <div><div><h2>事件时间线</h2></div><div></div></div>
  <div><div><h2>申请退款</h2></div><div></div></div>
</body>`, {
  url: 'https://test.invalid/admin/orderDetail.html?id=M-ORDER-TEST-0001', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      detailCalls.push({ url, method: init.method || 'GET' });
      if (url.pathname === '/api/admin/orders/M-ORDER-TEST-0001') return new Response(JSON.stringify({
        record_origin: 'native', merchant_order_no: 'M-ORDER-TEST-0001', provider: 'wechat',
        transaction_id: '4200000000000000000000000000', payer_name: '测试买家', payer_id: 'customer:101', payer_phone_masked: '138****0000',
        product_name: '测试商品', amount_yuan: '20.00', refundable_amount_total: 2000, created_at: '2026-09-30T16:01:02Z', status: 'paid',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify({
        items: [{ refund_no: 'RF-TEST-1', refund_amount_total: 2000, status: 'completed', reason: '测试退款', created_at: '2026-10-01T00:01:02+08:00' }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname.endsWith('/external-push-deliveries')) return new Response(JSON.stringify({
        effects: [{ external_effect_state: 'outcome_unknown', updated_at: '2026-09-30T16:01:02Z' }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  detailDom.window.eval(host);
  await pause();
  await detailDom.window.fetch('/api/admin/refunds');
  await detailDom.window.fetch('/api/admin/wechat-pay/orders/M-ORDER-TEST-0001/external-push-deliveries');
  await new Promise((resolve) => setTimeout(resolve, 60));
  const refundFetch = detailCalls.find((call) => call.url.pathname === '/api/admin/refunds');
  assert.equal(refundFetch.url.searchParams.get('provider'), 'wechat', 'detail refund fetch must select the exact payment provider');
  assert.equal(refundFetch.url.searchParams.get('order_no'), 'M-ORDER-TEST-0001', 'detail refund fetch must select the exact merchant order');
  assert.match(detailDom.window.document.body.textContent, /订单信息/, 'detail must use the business partition');
  assert.match(detailDom.window.document.body.textContent, /支付信息/, 'payment facts belong in their own partition');
  assert.match(detailDom.window.document.body.textContent, /买家信息/, 'buyer facts belong in their own partition');
  assert.match(detailDom.window.document.body.textContent, /商品与金额/, 'item and amount facts belong in their own partition');
  assert.match(detailDom.window.document.body.textContent, /CID-101/, 'customer information must expose a business-facing canonical customer number');
  assert.ok(!detailDom.window.document.body.textContent.includes('customer:101'), 'the internal canonical key must not be shown directly');
  assert.match(detailDom.window.document.body.textContent, /4200000000000000000000000000/, 'the true provider transaction identifier remains available for confirmation');
  assert.match(detailDom.window.document.body.textContent, /退款完成/, 'refund status must be a Chinese refund-domain label');
  assert.match(detailDom.window.document.body.textContent, /¥20.00/, 'refund rows must use the scoped API refund_amount_total minor-unit field');
  assert.match(detailDom.window.document.body.textContent, /退款金额（最多 ¥20.00）/, 'the native form must default and cap to refundable_amount_total');
  assert.match(detailDom.window.document.body.textContent, /2026-10-01 00:01:02/, 'detail time must be fixed to Asia\/Shanghai seconds');
  assert.match(detailDom.window.document.body.textContent, /外部处理记录/, 'external feedback must remain visible in a separate Chinese section');
  assert.match(detailDom.window.document.body.textContent, /结果待核对/, 'external-effect state must not expose its raw enum');
  assert.ok(!detailDom.window.document.body.textContent.includes('事件时间线'), 'the donor mixed timeline must be replaced by scoped external feedback');
  assert.match(detailDom.window.document.body.textContent, /再次输入微信支付交易单号/, 'refund confirmation must ask for transaction_id, not merchant order number');
} finally {
  detailDom.window.close();
}

const historyDom = new JSDOM(`<!doctype html><body data-page="orderDetail">
  <div><div><h2>订单详情</h2></div><div></div></div>
  <div><div><h2>事件时间线</h2></div><div></div></div>
  <div><div><h2>V1 历史只读</h2></div><div></div></div>
</body>`, {
  url: 'https://test.invalid/admin/orderDetail.html?id=M-HISTORY-TEST-0001', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async () => new Response(JSON.stringify({
      record_origin: 'v1_history', merchant_order_no: 'M-HISTORY-TEST-0001', provider: 'wechat',
      payer_name: '测试买家', payer_id: 'customer:102', product_name: '历史测试商品', amount_yuan: '20.00', created_at: '2026-10-01T00:01:02Z', status: 'paid',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  },
});
try {
  historyDom.window.eval(host);
  await pause();
  assert.match(historyDom.window.document.body.textContent, /历史订单，仅供查询/, 'historical users must not see V1\/V2 implementation wording');
  assert.ok(!historyDom.window.document.body.textContent.includes('V1'), 'historical presentation must not expose a V1 technical generation label');
  assert.match(historyDom.window.document.body.textContent, /历史记录：已支付/, 'historical paid data must remain a history fact, not a current provider-confirmed assertion');
} finally {
  historyDom.window.close();
}

console.log('order detail scope, Chinese presentation, and historical read-only journey: PASS');

function nativeOrderFixture(orderNo = 'M-REFUND-TEST-0001') {
  return {
    record_origin: 'native', merchant_order_no: orderNo, provider: 'wechat',
    transaction_id: '4200000000000000000000000001', payer_name: '退款测试买家', payer_id: 'customer:201', payer_phone_masked: '139****0001',
    product_name: '退款测试商品', amount_yuan: '20.00', refundable_amount_total: 2000,
    created_at: '2026-09-30T16:01:02Z', status: 'paid',
  };
}

function refundDetailHTML(orderNo) {
  return `<!doctype html><body data-page="orderDetail">
    <div><span>${orderNo}</span><span>paid</span></div>
    <div><div><h2>订单详情</h2></div><div></div></div>
    <div><div><h2>事件时间线</h2></div><div></div></div>
    <div><div><h2>申请退款</h2></div><div></div></div>
  </body>`;
}

const unknownCalls = [];
let resolveUnknownPost;
const unknownOrderNo = 'M-REFUND-TEST-UNKNOWN';
const unknownDom = new JSDOM(refundDetailHTML(unknownOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${unknownOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      unknownCalls.push({ url, method, body: init.body, idempotencyKey: new Headers(init.headers).get('Idempotency-Key') });
      if (url.pathname === `/api/admin/orders/${unknownOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(unknownOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds' && method === 'GET') return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === `/api/admin/wechat-pay/orders/${unknownOrderNo}/refunds` && method === 'POST') return new Promise((resolve) => { resolveUnknownPost = resolve; });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  unknownDom.window.eval(host);
  await pause();
  await unknownDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = unknownDom.window.document;
  const amount = document.querySelector('[data-order-refund-amount]');
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(amount && transaction && checked && submit, 'a native order with a known refundable amount must expose a guarded refund form');
  transaction.value = '4200000000000000000000000001';
  checked.checked = true;
  submit.dispatchEvent(new unknownDom.window.MouseEvent('click', { bubbles: true }));
  await pause();
  assert.equal(unknownCalls.filter((call) => call.method === 'POST').length, 1, 'the first click creates one refund intent');
  amount.value = '1.00';
  submit.dispatchEvent(new unknownDom.window.MouseEvent('click', { bubbles: true }));
  submit.dispatchEvent(new unknownDom.window.MouseEvent('click', { bubbles: true }));
  await pause();
  assert.equal(unknownCalls.filter((call) => call.method === 'POST').length, 1, 'double-clicks or a changed payload may not create another refund request');
  assert.match(document.body.textContent, /退款金额、原因和交易单号已锁定/, 'a changed payload is explicitly rejected while the original intent is unresolved');
  assert.ok(unknownCalls.find((call) => call.method === 'POST')?.idempotencyKey, 'the unresolved intent receives one stable idempotency key');
  resolveUnknownPost(new Response(JSON.stringify({ error: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } }));
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.match(document.body.textContent, /仅可读取当前订单退款记录/, 'a lost or 5xx response locks the intent to read-only verification');
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'unknown intent state removes mutable refund controls');
  const readBack = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '读取当前订单退款记录');
  assert.ok(readBack, 'unknown intent state offers readback instead of a mutation retry');
  readBack.click();
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(unknownCalls.filter((call) => call.method === 'POST').length, 1, 'readback performs no second refund mutation');
  assert.ok(unknownCalls.filter((call) => call.url.pathname === '/api/admin/refunds').every((call) => call.url.searchParams.get('provider') === 'wechat' && call.url.searchParams.get('order_no') === unknownOrderNo), 'all detail refund reads remain exact Payment-scoped');
} finally {
  unknownDom.window.close();
}

const acceptedCalls = [];
const acceptedOrderNo = 'M-REFUND-TEST-ACCEPTED';
const acceptedDom = new JSDOM(refundDetailHTML(acceptedOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${acceptedOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      acceptedCalls.push({ url, method, body: init.body });
      if (url.pathname === `/api/admin/orders/${acceptedOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(acceptedOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds' && method === 'GET') return new Response(JSON.stringify({ items: [{ refund_amount_total: 2000, status: 'effect_accepted', reason: '测试原因', created_at: '2026-09-30T16:01:02Z' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === `/api/admin/wechat-pay/orders/${acceptedOrderNo}/refunds` && method === 'POST') return new Response(JSON.stringify({ refund_id: 'RF-TEST-ACCEPTED', status: 'effect_accepted' }), { status: 202, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  acceptedDom.window.eval(host);
  await pause();
  await acceptedDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = acceptedDom.window.document;
  const amount = document.querySelector('[data-order-refund-amount]');
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(amount && transaction && checked && submit);
  amount.value = '20.01'; transaction.value = '4200000000000000000000000001'; checked.checked = true;
  submit.click(); await pause();
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 0, 'the browser blocks an amount above refundable_amount_total before any request');
  amount.value = '20.00'; transaction.value = 'v3pay_not_a_transaction';
  submit.click(); await pause();
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 0, 'the browser blocks a merchant-order style confirmation before any request');
  transaction.value = '4200000000000000000000000001';
  submit.click();
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 1, 'a verified transaction confirmation produces one request');
  assert.match(document.body.textContent, /退款申请已受理/, 'accepted intent state must be presented as accepted, not completed');
  assert.match(document.body.textContent, /¥20.00/, 'the success path reads the exact refund page and renders its minor-unit amount');
  assert.ok(acceptedCalls.filter((call) => call.url.pathname === '/api/admin/refunds').every((call) => call.url.searchParams.get('provider') === 'wechat' && call.url.searchParams.get('order_no') === acceptedOrderNo), 'success readback remains Payment-scoped');
} finally {
  acceptedDom.window.close();
}

const unavailableOrderNo = 'M-REFUND-TEST-UNAVAILABLE';
const unavailableDom = new JSDOM(refundDetailHTML(unavailableOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${unavailableOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${unavailableOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(unavailableOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify({ error: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  unavailableDom.window.eval(host);
  await pause();
  await unavailableDom.window.fetch('/api/admin/refunds');
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.match(unavailableDom.window.document.body.textContent, /退款记录暂不可读取/, 'a non-200 scoped refund read must be unavailable, never rendered as an empty page');
  assert.equal(unavailableDom.window.document.querySelector('[data-order-refund-amount]'), null, 'refund mutation is disabled when the exact refund page cannot be read');
} finally {
  unavailableDom.window.close();
}

console.log('order refund idempotency, exact readback, and unavailable-state journeys: PASS');
