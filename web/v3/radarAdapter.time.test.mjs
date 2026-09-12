import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/radarAdapter.ts'));
const frozenRadar = (await build({
  stdin: {
    contents: "import { mountRadar } from '../src/admin/sections/radar'; window.FrozenRadar = { mountRadar };",
    resolveDir: path.join(root, 'web/v3'), sourcefile: 'radar-frozen-time-renderer-entry.ts',
  },
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning',
})).outputFiles[0].text;

const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

const requests = [];
let failSecondPage = true;
let holdFilteredQuery = false;
let resolveHeldQuery;
let invalidCSVResponse = false;
let failCurrentFilteredQuery = false;
let downloads = 0;
const event = (suffix, stage = 'image_loaded') => ({
  receipt_id: `rre_${suffix.padStart(32, '0')}`,
  stage,
  created_at: '2026-09-05T00:01:02.611265Z',
});
const page = (offset, hasMore, limit = 100) => ({
  items: offset === 100 ? [event('2', 'pdf_opened')] : [event('1')],
  total: 101,
  limit,
  offset: Number.isFinite(offset) ? offset : 0,
  has_more: hasMore,
  identity_attributed: false,
  real_external_call_executed: false,
});

const dom = new JSDOM('<!doctype html><body data-page="radarDetail"><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/radarDetail.html?id=11', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.Blob = Blob;
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.URL.createObjectURL = () => 'blob:test';
    window.URL.revokeObjectURL = () => {};
    window.HTMLAnchorElement.prototype.click = function click() { downloads += 1; };
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      requests.push(url);
      if (url.pathname === '/api/admin/radar-links/11/events/export') {
        if (invalidCSVResponse) return new Response('<!doctype html><title>sign in</title>', { status: 200, headers: { 'Content-Type': 'text/html' } });
        return new Response('receipt_id,stage,occurred_at\nrre_1,image_loaded,2026-09-05 08:01:02\n', { status: 200, headers: { 'Content-Type': 'text/csv' } });
      }
      if (url.pathname !== '/api/admin/radar-links/11/events') return new Response(JSON.stringify({ code: 'unexpected' }), { status: 500 });
      const offset = Number(url.searchParams.get('offset') || '0');
      if (holdFilteredQuery && offset === 0 && url.searchParams.get('start_at') === '2026-09-08T00:00:00.000Z') {
        return await new Promise((resolve) => { resolveHeldQuery = resolve; });
      }
      if (failCurrentFilteredQuery && offset === 0 && url.searchParams.get('start_at') === '2026-09-09T00:00:00.000Z') {
        return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      }
      if (offset === 100 && failSecondPage) {
        failSecondPage = false;
        return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      }
      const limit = Number(url.searchParams.get('limit') || '500');
      return new Response(JSON.stringify(page(offset, limit !== 500 && offset === 0, limit)), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});

dom.window.eval(host);
dom.window.eval(frozenRadar);
const api = {
  mode: 'http',
  loadDb: async () => ({
    radarLinks: [{ id: 11, title: '上海时间雷达', target_type: 'link', original_url: 'https://example.test', file_name_snapshot: '', media_item_id: '', enabled: true, auth_required: true, staff_id: '7', total_landings: 2, authorized_users: 1, view_count: 1 }],
  }),
  getRadarSharePath: async () => '/r/rd_abcdefghijklmnopqrstuv',
};
await dom.window.FrozenRadar.mountRadar(dom.window.document.querySelector('#stage'), api, { view: 'detail', id: 11 });
await waitFor(() => dom.window.document.querySelector('[data-v3-radar-event-host]'), 'the V3 Radar event Host replaces the frozen filters and table');
await waitFor(() => dom.window.document.querySelector('[data-v3-radar-event-host] tbody')?.textContent?.includes('2026-09-05 08:01:02'), 'the initial event page renders Shanghai seconds');
const document = dom.window.document;
const hostRoot = document.querySelector('[data-v3-radar-event-host]');
assert.ok(hostRoot?.textContent?.includes('2026-09-05 08:01:02'));
assert.equal(hostRoot?.textContent?.includes('T00:01:02'), false, 'user-visible event time must not expose RFC3339');
assert.ok(hostRoot?.textContent?.includes('本页搜索'), 'keyword filtering is explicitly scoped to the loaded page');
assert.ok(hostRoot?.textContent?.includes('导出仅按已查询的时间范围，不包含本页搜索。'), 'CSV scope is explicit to prevent a local keyword search from implying a server filter');
const [keyword, start, end] = hostRoot.querySelectorAll('input');
start.value = '2026-09-05T08:01:02';
end.value = '2026-09-05T09:00:00';
document.querySelector('[data-v3-radar-event-host] button')?.click();
await waitFor(() => requests.some((url) => url.pathname.endsWith('/events') && url.searchParams.get('start_at') === '2026-09-05T00:01:02.000Z'), 'the Shanghai datetime-local filter reaches the event API as UTC');
await waitFor(() => document.querySelector('#dExport')?.disabled === false, 'the filtered page is rendered before CSV export');
assert.equal(requests.at(-1).searchParams.get('end_at'), '2026-09-05T01:00:00.000Z');

document.querySelector('#dExport').click();
await waitFor(() => requests.at(-1).pathname.endsWith('/events/export'), 'the Host owns the frozen export button');
await waitFor(() => hostRoot.textContent.includes('已导出 CSV。'), 'the CSV completion feedback is visible before the next query');
assert.equal(requests.at(-1).searchParams.get('start_at'), '2026-09-05T00:01:02.000Z', 'CSV uses the same normalized range as the visible result page');
assert.equal(requests.at(-1).searchParams.get('end_at'), '2026-09-05T01:00:00.000Z');
assert.equal(downloads, 1, 'a verified CSV response is downloaded once');

invalidCSVResponse = true;
document.querySelector('#dExport').click();
await waitFor(() => hostRoot.textContent.includes('雷达事件暂时无法导出'), 'a 2xx non-CSV response is rejected without downloading an HTML login page');
assert.equal(downloads, 1, 'a non-CSV response never starts a download');
invalidCSVResponse = false;

const next = [...hostRoot.querySelectorAll('button')].find((button) => button.textContent === '下一页');
next.click();
await waitFor(() => hostRoot.textContent.includes('雷达事件暂时无法读取'), 'failed next page keeps a visible retry state');
assert.ok(hostRoot.textContent.includes('2026-09-05 08:01:02'), 'failed next page preserves the successful page');
const retry = [...hostRoot.querySelectorAll('button')].find((button) => button.textContent === '重试');
assert.equal(next.disabled, false, 'a page-two failure leaves the last successful page and its original next offset usable');
assert.equal(retry.disabled, false, 'a page-two failure can retry its original offset');
retry.click();
await waitFor(() => hostRoot.textContent.includes('第 101–101 条'), 'retry reuses the failed page offset after a transient error');
const pageOffsets = requests.filter((url) => url.pathname.endsWith('/events')).map((url) => url.searchParams.get('offset'));
assert.deepEqual(pageOffsets.slice(-2), ['100', '100'], 'a page-two failure must retry page two rather than skip to page three');

const reset = [...hostRoot.querySelectorAll('button')].find((button) => button.textContent === '清空筛选');
reset.click();
await waitFor(() => requests.at(-1).pathname.endsWith('/events') && !requests.at(-1).searchParams.has('start_at'), 'clearing filters reloads the unbounded first page');
await waitFor(() => document.querySelector('#dExport')?.disabled === false && hostRoot.textContent.includes('第 1–1 条'), 'the cleared page finishes rendering before teardown');
assert.equal(start.value, '');
assert.equal(end.value, '');

holdFilteredQuery = true;
start.value = '2026-09-08T08:00:00';
end.value = '2026-09-08T09:00:00';
document.querySelector('[data-v3-radar-event-host] button')?.click();
await waitFor(() => typeof resolveHeldQuery === 'function', 'the filtered query is in flight before editing its time range');
start.value = '2026-09-09T08:00:00';
start.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
assert.equal(document.querySelector('#dExport')?.disabled, true, 'editing time while a request is in flight immediately disables exporting the old range');
assert.equal(next.disabled, true, 'editing time while a request is in flight immediately disables pagination');
resolveHeldQuery(new Response(JSON.stringify(page(0, true)), { status: 200, headers: { 'Content-Type': 'application/json' } }));
await waitFor(() => hostRoot.textContent.includes('筛选已变更，请查询后查看结果。'), 'an old in-flight response remains visibly stale after the filter changes');
assert.equal(document.querySelector('#dExport')?.disabled, true, 'a completed stale response does not re-enable export');
assert.equal(next.disabled, true, 'a completed stale response does not re-enable pagination');
await waitFor(() => [...hostRoot.querySelectorAll('button')].find((button) => button.textContent === '查询')?.disabled === false, 'the first query has settled before the edited-range retry');
failCurrentFilteredQuery = true;
end.value = '2026-09-09T10:00:00';
end.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
const editedRangeQuery = [...hostRoot.querySelectorAll('button')].find((button) => button.textContent === '查询');
assert.equal(editedRangeQuery?.disabled, false, 'the query action remains available before retrying an edited range');
editedRangeQuery?.click();
await wait(30);
await waitFor(() => hostRoot.textContent.includes('当前筛选尚未查询，请点击“查询”。'), 'a failed query against an edited range gives the user a concrete next action');
assert.equal([...hostRoot.querySelectorAll('button')].find((button) => button.textContent === '重试')?.hidden, true, 'a stale retry button is hidden because its old offset is not valid for the edited range');
assert.equal(editedRangeQuery?.disabled, false, 'the query action remains available after the edited-range request fails');
dom.window.dispatchEvent(new dom.window.Event('pagehide'));
dom.window.close();
console.log('radar event time Host filters, pagination, retry, CSV and Shanghai display: PASS');
