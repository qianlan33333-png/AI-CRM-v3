import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./overviewAdmin.ts', import.meta.url)));
const delay = (milliseconds = 10) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) { for (let attempt = 0; attempt < 100; attempt += 1) { if (check()) return; await delay(); } throw new Error(message); }
const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
function overview(period, { amount = 12500, refund = 1200 } = {}) {
  const section = { status: 'ready', as_of: '2026-09-15T02:00:00Z', scope: 'admin_authorized_global' };
  return {
    range: { period, timezone: 'Asia/Shanghai', start: '2026-09-14T16:00:00Z', end: '2026-09-15T16:00:00Z' },
    contract: { scope: 'admin_authorized_global' },
    paid: { ...section, gross: [{ amount_minor: amount, currency: 'CNY' }], order_count: 2, distinct_canonical_payers: 1, missing_confirmation_evidence_count: 1, trend: [{ date: '2026-09-15', gross: [{ amount_minor: amount, currency: 'CNY' }], order_count: 2 }] },
    customers: { ...section, status: 'data_missing', reason_code: 'customer_creation_source_unknown', new_canonical_customers: 1, historical_excluded: 2, unknown_source_count: 3 },
    refunds: { ...section, completed_amount: refund ? [{ amount_minor: refund, currency: 'CNY' }] : [], completed_count: refund ? 1 : 0, net_amount: [{ amount_minor: amount - refund, currency: 'CNY' }] },
    distribution: { ...section, period_paid_sales_minor: amount, period_initial_commission_minor: 1600, period_commission_count: 1, current_unsettled_minor: 900, current_settled_minor: 700, currency: 'CNY' },
    todos: { ...section, items: [{ code: 'distribution_exceptions', count: 2, href: '/admin/distribution' }] },
  };
}
let scenario = 'today';
const calls = [];
const dom = new JSDOM('<!doctype html><main id="overview-admin-root"></main>', {
  url: 'https://crm.example/admin', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href); calls.push(url);
      if (scenario === 'network') throw new Error('网络暂时不可用');
      if (scenario === 'forbidden') return reply({ error: { message: 'permission_denied' } }, 403);
      if (scenario === 'malformed') { const bad = overview('30d'); bad.paid.gross = [{ amount_minor: Number.MAX_SAFE_INTEGER + 1, currency: 'CNY' }]; return reply(bad); }
      if (scenario === 'negative') return reply(overview(url.searchParams.get('period'), { amount: 500, refund: 1200 }));
      if (scenario === 'refund-zero') return reply(overview(url.searchParams.get('period'), { refund: 0 }));
      return reply(overview(url.searchParams.get('period'), { amount: scenario === 'seven' ? 20000 : 12500 }));
    };
  },
});
dom.window.eval(bundle);
await waitFor(() => dom.window.document.body.textContent.includes('已确认支付'), 'overview did not render its first response');
assert.equal(calls[0].pathname, '/api/admin/overview');
assert.equal(calls[0].search, '?period=today', 'initial request must explicitly use today');
assert.ok(dom.window.document.body.textContent.includes('来源待核实'), 'missing provenance must be visible');
assert.ok(dom.window.document.body.textContent.includes('最近读取：'), 'a warning must retain its section observation time');
assert.equal(dom.window.document.querySelector('a[href="/admin/distribution"]')?.textContent?.includes('分销异常待处理'), true, 'real todo route must stay usable');
assert.equal(dom.window.document.body.textContent.includes('OneID'), false, 'internal identity terminology must not render');
assert.equal(dom.window.document.body.textContent.toLowerCase().includes('contract'), false, 'API contract must not render');
assert.equal(dom.window.document.querySelectorAll('.overview-metric a').length, 0, 'metric cards must not fabricate a date-filtered drill-down');

scenario = 'network';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 7 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('网络暂时不可用'), 'failed refresh did not expose retry');
assert.ok(dom.window.document.body.textContent.includes('¥125.00'), 'ordinary failed refresh must retain prior values');
assert.ok(dom.window.document.body.textContent.includes('当前显示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'retained figures must disclose their actual successful range');

scenario = 'seven';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '重试').click();
await waitFor(() => dom.window.document.body.textContent.includes('¥200.00'), 'retry did not load the selected range');
assert.equal(calls.at(-1).search, '?period=7d', 'retry must retain selected period');

scenario = 'negative';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('-¥7.00'), 'a valid negative net amount was rejected');

scenario = 'network';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('网络暂时不可用'), 'a same-period retry must mark its previous-day-capable cache as stale');
assert.ok(dom.window.document.body.textContent.includes('当前显示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'same-period cached data must show its actual successful range');
assert.ok(dom.window.document.body.textContent.includes('-¥7.00'), 'same-period failure must retain the last verified figures');

scenario = 'refund-zero';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '重试').click();
await waitFor(() => dom.window.document.body.textContent.includes('0（本期无退款）'), 'a confirmed refund zero must not render as missing');

scenario = 'malformed';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 30 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('经营数据格式无效'), 'malformed data must be rejected');
assert.ok(dom.window.document.body.textContent.includes('¥125.00'), 'malformed data must not replace known figures with a synthetic zero');

scenario = 'forbidden';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('暂无查看权限'), '403 must show a distinct permission state');
assert.equal(dom.window.document.body.textContent.includes('¥200.00'), false, 'permission loss must hide cached operating metrics');

[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '自定义').click();
const custom = dom.window.document.querySelector('[data-overview-custom]');
custom.querySelector('[name="from"]').value = '2026-09-01';
custom.querySelector('[name="to"]').value = '';
custom.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
await waitFor(() => dom.window.document.body.textContent.includes('请选择有效的开始和结束日期'), 'invalid custom range did not show validation feedback');
assert.equal(dom.window.document.querySelector('[name="from"]').value, '2026-09-01', 'custom-range draft must survive validation feedback');

dom.window.close();
console.log('overview admin host: PASS');
