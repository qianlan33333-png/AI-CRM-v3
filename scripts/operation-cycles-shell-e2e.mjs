#!/usr/bin/env node
import { JSDOM, VirtualConsole } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = await buildTestBrowserBundle(path.join(repository, 'web', 'v3', 'operationCyclesAdapter.ts'));
const donor = fs.readFileSync(path.join(repository, 'web', 'donors', 'operation-cycles-v2', 'src', 'admin', 'templates', 'cycles.html'), 'utf8')
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
  .replace(/<\/sc-for>/g, '</template>');
const requests = [];
const alerts = [];
const jsdomErrors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => {
  if (!String(error?.message || error).includes('navigation to another Document')) jsdomErrors.push(String(error?.message || error));
});
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, clone() { return this; }, json: async () => body, text: async () => JSON.stringify(body) });
const snapshot = {
  schema_version: 'operation_cycle_snapshot.v1', name: '每周复盘', cron: '每周一 09:00', dot: '#2EA121',
  action: '开始复盘', action_key: 'start_review', run_key: 'weekly.review.001',
  steps: [{ label: '复盘', color: '#2EA121', dim: false }],
};
const dom = new JSDOM(`<!doctype html><html><body class="admin-shell" data-page="cycles"><main id="stage"></main><template id="tpl">${donor}</template><script>${bundle}</script></body></html>`, {
  url: 'https://test.invalid/admin/operation-cycles', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.alert = (message) => alerts.push(String(message));
    window.document.cookie = `aicrm_csrf=${'c'.repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      requests.push({ path: url.pathname + url.search, method: init.method || 'GET', headers: Object.fromEntries(new Headers(init.headers).entries()), body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (url.pathname === '/api/admin/operation-cycles/strategies' && (init.method || 'GET') === 'GET') return json({ items: [
        { strategy_key: 'weekly.review', title: '每周复盘', status: 'active', version: 4, snapshot },
        { strategy_key: 'paused.review', title: '暂停复盘', status: 'paused', version: 2, snapshot: { ...snapshot, name: '暂停复盘', run_key: '' } },
      ] });
      if (url.pathname.endsWith('/actions/start_review/start') && init.method === 'POST') return json({ request_id: 'ocact_0123456789012345678901234567', status: 'queued' }, 202);
      if (url.pathname.endsWith('/paused.review/status') && init.method === 'POST') return json({ strategy_key: 'paused.review', status: 'active', version: 3 });
      return json({ ok: false, code: 'unexpected_request' }, 500);
    };
  },
});

try {
  await new Promise((resolve) => setTimeout(resolve, 500));
  const buttons = Array.from(dom.window.document.querySelectorAll('tbody tr button'));
  if (buttons.length !== 4 || buttons[0].textContent?.trim() !== '开始复盘' || buttons[2].textContent?.trim() !== '开始复盘') throw new Error(`frozen donor primary actions did not render unchanged: ${dom.window.document.getElementById('stage')?.innerHTML} requests=${JSON.stringify(requests)}`);
  buttons[0].click();
  await new Promise((resolve) => setTimeout(resolve, 80));
  buttons[0].click();
  await new Promise((resolve) => setTimeout(resolve, 80));
  buttons[2].click();
  await new Promise((resolve) => setTimeout(resolve, 80));
  const writes = requests.filter((item) => item.method === 'POST');
  const starts = writes.filter((item) => item.path === '/api/admin/operation-cycles/strategies/weekly.review/actions/start_review/start');
  const activations = writes.filter((item) => item.path === '/api/admin/operation-cycles/strategies/paused.review/status');
  if (starts.length !== 2 || activations.length !== 1) throw new Error(`primary actions did not reach the real admin commands: ${JSON.stringify(writes)}`);
  if (starts.some((item) => item.body?.run_key !== 'weekly.review.001' || item.body?.parent_request_id !== '') || activations[0].body?.expected_version !== 2 || activations[0].body?.status !== 'active') throw new Error('primary action DTO drifted');
  if (writes.some((item) => !item.headers['x-csrf-token']) || starts[0].headers['idempotency-key'] !== starts[1].headers['idempotency-key']) throw new Error('CSRF or stable retry idempotency binding is absent');
  if (alerts.some((message) => message.includes('DTO 不等价')) || !alerts.includes('复盘请求已受理') || !alerts.includes('运营周期已启用')) throw new Error(`donor blocked actions were not replaced by real receipt feedback: ${JSON.stringify(alerts)}`);
  if (jsdomErrors.length) throw new Error(`browser errors: ${JSON.stringify(jsdomErrors)}`);
  console.log('operation-cycle frozen primary action browser Journey: PASS');
} finally {
  dom.window.close();
}
