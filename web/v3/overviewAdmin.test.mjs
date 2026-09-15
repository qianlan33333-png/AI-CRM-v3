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
  const days = period === '30d' ? 30 : period === '7d' ? 7 : 1;
  const rangeStart = new Date(Date.UTC(2026, 8, 15 - days, 16)).toISOString();
  const rangeEnd = new Date(Date.UTC(2026, 8, 15, 16)).toISOString();
  const sourceDays = days === 1 ? [0] : [0, Math.floor(days / 2), days - 1];
  const dailyAmount = Math.floor(amount / sourceDays.length);
  const trend = sourceDays.map((offset, index) => {
    const date = new Date(Date.UTC(2026, 8, 15 - (days - 1) + offset)).toISOString().slice(0, 10);
    const amountMinor = index === sourceDays.length - 1 ? amount - dailyAmount * (sourceDays.length - 1) : dailyAmount;
    return { date, gross: [{ amount_minor: amountMinor, currency: 'CNY' }], order_count: amountMinor > 0 ? 1 : 0 };
  });
  return {
    range: { period, timezone: 'Asia/Shanghai', start: rangeStart, end: rangeEnd },
    contract: { scope: 'admin_authorized_global' },
    paid: { ...section, gross: [{ amount_minor: amount, currency: 'CNY' }], order_count: 2, distinct_canonical_payers: 1, missing_confirmation_evidence_count: 1, trend },
    customers: { ...section, status: 'data_missing', reason_code: 'customer_creation_source_unknown', new_canonical_customers: 1, historical_excluded: 2, unknown_source_count: 3 },
    refunds: { ...section, completed_amount: refund ? [{ amount_minor: refund, currency: 'CNY' }] : [], completed_count: refund ? 1 : 0, net_amount: [{ amount_minor: amount - refund, currency: 'CNY' }] },
    distribution: { ...section, period_paid_sales_minor: amount, period_initial_commission_minor: 1600, period_commission_count: 1, current_unsettled_minor: 900, current_settled_minor: 700, currency: 'CNY' },
    todos: { ...section, items: [{ code: 'distribution_exceptions', count: 2, href: '/admin/distribution' }] },
  };
}
let scenario = 'today';
let releaseDeferred;
const calls = [];
const dom = new JSDOM('<!doctype html><main id="overview-admin-root"></main>', {
  url: 'https://crm.example/admin', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href); calls.push(url);
      if (scenario === 'network') throw new Error('网络暂时不可用');
      if (scenario === 'deferred') return new Promise((resolve) => { releaseDeferred = () => resolve(reply(overview('7d', { amount: 20000 }))); });
      if (scenario === 'forbidden') return reply({ error: { message: 'permission_denied' } }, 403);
      if (scenario === 'malformed') { const bad = overview('30d'); bad.paid.gross = [{ amount_minor: Number.MAX_SAFE_INTEGER + 1, currency: 'CNY' }]; return reply(bad); }
      if (scenario === 'negative') return reply(overview(url.searchParams.get('period'), { amount: 500, refund: 1200 }));
      if (scenario === 'refund-zero') return reply(overview(url.searchParams.get('period'), { refund: 0 }));
      if (scenario === 'trend-missing') { const body = overview(url.searchParams.get('period')); body.paid.status = 'data_missing'; body.paid.reason_code = 'paid_confirmation_time_missing'; body.paid.trend = []; return reply(body); }
      if (scenario === 'canonical-payer-unavailable') { const body = overview(url.searchParams.get('period')); body.paid.status = 'data_missing'; body.paid.reason_code = 'canonical_payer_unavailable'; delete body.paid.distinct_canonical_payers; return reply(body); }
      if (scenario === 'payment-timeout') { const body = overview(url.searchParams.get('period')); body.paid.status = 'failed'; body.paid.reason_code = 'payment_aggregate_timeout'; delete body.paid.distinct_canonical_payers; return reply(body); }
      if (scenario === 'distribution-not-configured') { const body = overview(url.searchParams.get('period')); body.distribution.status = 'data_missing'; body.distribution.reason_code = 'distribution_not_configured'; body.distribution.period_paid_sales_minor = 0; body.distribution.period_initial_commission_minor = 0; body.distribution.current_unsettled_minor = 0; body.distribution.current_settled_minor = 0; body.todos.status = 'data_missing'; body.todos.reason_code = 'distribution_not_configured'; body.todos.items = []; return reply(body); }
      if (scenario === 'long-custom') { const body = overview('custom'); body.range = { period: 'custom', timezone: 'Asia/Shanghai', start: '2026-01-01T16:00:00Z', end: '2026-02-03T16:00:00Z' }; return reply(body); }
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
assert.ok(dom.window.document.body.textContent.includes('数据读取：支付'), 'the unified observation summary must include normal section timestamps');
assert.equal(dom.window.document.querySelector('a[href="/admin/distribution"]')?.textContent?.includes('分销异常待处理'), true, 'real todo route must stay usable');
assert.equal(dom.window.document.body.textContent.includes('OneID'), false, 'internal identity terminology must not render');
assert.equal(dom.window.document.body.textContent.toLowerCase().includes('contract'), false, 'API contract must not render');
assert.equal(dom.window.document.querySelectorAll('.overview-metric a').length, 0, 'metric cards must not fabricate a date-filtered drill-down');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 1, 'today must use a narrow single trend column');
assert.equal(dom.window.document.querySelector('.overview-chart__bar')?.getAttribute('height'), '112', 'the maximum payment amount must use the defined SVG plot height');

scenario = 'deferred';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 7 天').click();
await waitFor(() => typeof releaseDeferred === 'function', 'deferred preset request did not start');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '自定义').click();
assert.equal(dom.window.document.querySelector('[data-overview-custom]')?.classList.contains('is-open'), true, 'custom draft must open while a preset request is pending');
releaseDeferred();
await delay(30);
assert.equal(dom.window.document.querySelector('[data-overview-custom]')?.classList.contains('is-open'), true, 'an obsolete preset response must not close the custom draft');
assert.ok(dom.window.document.body.textContent.includes('当前显示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'a custom draft must retain the range of the last successful response');

scenario = 'network';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 7 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('网络暂时不可用'), 'failed refresh did not expose retry');
assert.ok(dom.window.document.body.textContent.includes('¥125.00'), 'ordinary failed refresh must retain prior values');
assert.ok(dom.window.document.body.textContent.includes('当前显示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'retained figures must disclose their actual successful range');

scenario = 'seven';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '重试').click();
await waitFor(() => dom.window.document.body.textContent.includes('¥200.00'), 'retry did not load the selected range');
assert.equal(calls[calls.length - 1].search, '?period=7d', 'retry must retain selected period');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 7, 'the seven-day trend must retain all daily columns');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__bar').length, 3, 'ready ranges must infer zero-value days only between known payment dates');
assert.ok([...dom.window.document.querySelectorAll('.overview-chart__bar')].some((bar) => bar.getAttribute('height') === '112'), 'the seven-day maximum must use a visible fixed-height bar');

scenario = 'trend-missing';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 30 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('部分历史支付缺少确认时间'), 'missing payment evidence did not render');
assert.ok(dom.window.document.body.textContent.includes('暂无可定位到日期的支付记录，仍有数据待核实'), 'missing payment evidence must not be shown as a confirmed zero');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 0, 'data-missing payment trends must not infer zero-value dates');

scenario = 'canonical-payer-unavailable';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('付款客户归并关系暂时无法核实'), 'unknown canonical payer count did not expose its reason');
const payerMetric = [...dom.window.document.querySelectorAll('.overview-metric')].find((metric) => metric.textContent.includes('支付客户'));
assert.equal(payerMetric?.querySelector('strong')?.textContent, '—', 'omitted canonical payer count must not render as zero');

scenario = 'payment-timeout';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 7 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('已确认支付读取超时'), 'payment timeout did not expose its reason');
assert.equal([...dom.window.document.querySelectorAll('.overview-metric')].find((metric) => metric.textContent.includes('支付客户'))?.querySelector('strong')?.textContent, '—', 'timed-out omitted payer count must not render as zero');

scenario = 'distribution-not-configured';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('分销数据暂未接入，暂无法确认待处理事项'), 'unconfigured distribution did not expose its unavailable todo state');
const distributionValues = [...dom.window.document.querySelectorAll('.overview-panel')].find((panel) => panel.textContent.includes('分销进度'))?.querySelectorAll('dd') || [];
assert.deepEqual([...distributionValues].map((node) => node.textContent), ['—', '—', '—', '—'], 'unconfigured distribution must not present default zeroes as known amounts');
assert.equal([...dom.window.document.querySelectorAll('.overview-status')].filter((node) => node.textContent === '暂未接入').length, 2, 'unconfigured distribution and todos must disclose that their source is not connected');
assert.equal(dom.window.document.body.textContent.includes('暂无需要处理的事项。'), false, 'unconfigured distribution must not present an empty todo list as confirmed');

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

scenario = 'seven';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 30 天').click();
await waitFor(() => dom.window.document.querySelectorAll('.overview-chart__column').length === 30, 'a ready thirty-day period did not fill its complete Beijing date range');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 30, 'the thirty-day trend must retain all daily columns');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__bar').length, 3, 'zero-value days must not paint a misleading non-zero bar');
assert.equal(dom.window.document.querySelector('.overview-chart')?.classList.contains('overview-chart--dense'), true, 'thirty-day charts must use the dense layout');
assert.equal([...dom.window.document.querySelectorAll('.overview-chart--dense .overview-chart__value')].filter((node) => node.textContent.trim()).length, 0, 'dense charts must not truncate monetary labels');
assert.equal([...dom.window.document.querySelectorAll('.overview-chart--dense .overview-chart__bar title')].map((node) => node.textContent).join('|'), '¥66.66|¥66.66|¥66.68', 'dense payment bars must retain complete hover labels');
assert.equal(dom.window.document.querySelector('.overview-trend-details')?.open, false, 'the thirty-day trend table must stay collapsed until requested');

[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '自定义').click();
let custom = dom.window.document.querySelector('[data-overview-custom]');
scenario = 'long-custom';
custom.querySelector('[name="from"]').value = '2026-01-02';
custom.querySelector('[name="to"]').value = '2026-02-03';
custom.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
await waitFor(() => dom.window.document.body.textContent.includes('展示有确认支付的日期'), 'long custom ranges must explain that their trends are sparse payment dates');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 1, 'long custom ranges must not fabricate every date');

custom = dom.window.document.querySelector('[data-overview-custom]');
custom.querySelector('[name="from"]').value = '2026-09-01';
custom.querySelector('[name="to"]').value = '';
custom.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
await waitFor(() => dom.window.document.body.textContent.includes('请选择有效的开始和结束日期'), 'invalid custom range did not show validation feedback');
assert.equal(dom.window.document.querySelector('[name="from"]').value, '2026-09-01', 'custom-range draft must survive validation feedback');

dom.window.close();
console.log('overview admin host: PASS');
