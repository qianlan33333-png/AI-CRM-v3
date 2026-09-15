// The admission Host remains part of this entrypoint's required journey set.
await import('./channelAdmissionHost.test.mjs');

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole } = jsdom;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const frozenTemplate = fs.readFileSync(path.join(root, 'web/src/admin/templates/channels.html'), 'utf8');
const template = frozenTemplate
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
  .replace(/<\/sc-for>/g, '</template>')
  .replace(/<sc-if\s+([^>]*?)value="([^"]*)"([^>]*)>/g, (_match, _a, value) => `<template data-sc-if="${value}">`)
  .replace(/<\/sc-if>/g, '</template>');
const adapter = await buildTestBrowserBundle(path.join(root, 'web/v3/channelCenterAdapter.ts'));
const pause = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(check, message) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = check();
    if (value) return value;
    await pause();
  }
  throw new Error(message);
}

function channel(id, status, name) {
  return {
    id, channel_name: name, channel_code: `code-${id}`, channel_type: 'qrcode', carrier_type: 'qrcode', status,
    channel_contact_count: 0, scene_value: 'scene', qr_url: 'https://example.invalid/qr', qr_download_url: `/api/admin/channels/${id}/qrcode/download`, owner_staff_id: '7', customer_channel: 'source', link_url: '', final_url: '',
    welcome_message: '欢迎{{客户名}}', welcome_image_library_ids: [11], welcome_miniprogram_library_ids: [12], welcome_attachment_library_ids: [13], welcome_group_invite_library_ids: [14],
    auto_accept_friend: false, entry_tag_id: '3', entry_tag_name: '新客', entry_tag_group_name: '来源', assignment_mode: 'multi_staff', assignment_strategy: 'ratio', overflow_policy: '',
    assignment_config_json: { assignees: [{ staff_id: 7, priority: 1, ratio_percent: 100, max_scans_24h: 0 }] },
  };
}

assert.match(frozenTemplate, /后端暂无渠道归档 operation/, 'the checked-in donor template remains byte-frozen');
let releaseInitialListRead;
const initialListReadGate = new Promise((resolve) => { releaseInitialListRead = resolve; });
const state = {
  ids: ['9', '10', '11', '12'],
  names: new Map([['9', '同名渠道'], ['10', '同名渠道'], ['11', '权限渠道'], ['12', '预读渠道']]),
  status: new Map([['9', 'archived'], ['10', 'active'], ['11', 'active'], ['12', 'active']]),
  calls: [], etag: new Map([['9', '"5"'], ['10', '"7"'], ['11', '"11"'], ['12', '"13"']]),
  listReads: 0, listSnapshots: [], holdInitialListRead: true, failNextListRead: false, outcomes: new Map([['10', 'proxy_502']]),
};
const list = new JSDOM(`<!doctype html><body data-page="channels"><template id="tpl">${template}</template><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/channels', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.document.cookie = 'aicrm_csrf=fixture-csrf; Path=/';
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input.url, window.location.href);
      const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase();
      const id = url.pathname.match(/^\/api\/admin\/channels\/([1-9][0-9]*)$/)?.[1];
      state.calls.push({ method, path: `${url.pathname}${url.search}`, headers: new Headers(init.headers), body: init.body ? JSON.parse(init.body) : undefined });
      if (url.pathname === '/api/admin/channels' && method === 'GET') {
        state.listReads += 1;
        if (state.holdInitialListRead) {
          state.holdInitialListRead = false;
          await initialListReadGate;
        }
        if (state.failNextListRead) {
          state.failNextListRead = false;
          throw new TypeError('list refresh unavailable');
        }
        const ids = state.ids.filter((rowID) => url.searchParams.get('include_archived') === 'true' || state.status.get(rowID) !== 'archived');
        state.listSnapshots.push(ids);
        return new Response(JSON.stringify({ channels: ids.map((rowID) => channel(Number(rowID), state.status.get(rowID), state.names.get(rowID))) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (id && method === 'GET') {
        if (state.outcomes.get(id) === 'preflight_network') throw new TypeError('detail preflight unavailable');
        return new Response(JSON.stringify({ channel: channel(Number(id), state.status.get(id), state.names.get(id)) }), { status: 200, headers: { 'Content-Type': 'application/json', ETag: state.etag.get(id) } });
      }
      if (id && method === 'PATCH') {
        const outcome = state.outcomes.get(id);
        if (outcome === 'network_unknown') throw new TypeError('write transport unavailable');
        if (outcome === 'proxy_502') return new Response(JSON.stringify({ code: 'BAD_GATEWAY' }), { status: 502, headers: { 'Content-Type': 'application/json' } });
        if (outcome === '401' || outcome === '403' || outcome === '409') return new Response(JSON.stringify({ code: outcome }), { status: Number(outcome), headers: { 'Content-Type': 'application/json' } });
        state.status.set(id, 'archived');
        state.etag.set(id, `"${Number((state.etag.get(id) || '"0"').replace(/\D/g, '')) + 1}"`);
        return new Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      return new Response(JSON.stringify({ code: 'NOT_FOUND' }), { status: 404, headers: { 'Content-Type': 'application/json' } });
    };
  },
});

function actionForName(name) {
  return [...list.window.document.querySelectorAll('a')].find((node) => node.textContent === '删除' && node.closest('tr')?.textContent?.includes(name));
}

async function confirm(action, message) {
  action.click();
  await waitFor(() => list.window.document.querySelector('#fb-mask')?.hidden === false, message);
  list.window.document.querySelector('#fb-ok').click();
}

try {
  list.window.eval(adapter);
  await waitFor(() => state.listReads === 1, 'the initial frozen channel list read must be in flight');
  const archivedDetailDuringListRead = await list.window.fetch('/api/admin/channels/9');
  assert.equal((await archivedDetailDuringListRead.json()).channel.status, 'archived', 'a concurrent historical detail read remains unchanged while the normal list request is pending');
  assert.equal(state.calls.at(-1)?.path, '/api/admin/channels/9', 'the normal-list seam never appends or removes detail query parameters');
  releaseInitialListRead();
  await waitFor(() => actionForName('同名渠道'), 'the real frozen controller and SC list must bind active archive actions');
  const initialListRead = state.calls.find((call) => call.method === 'GET' && call.path.startsWith('/api/admin/channels?'));
  assert.equal(initialListRead?.path, '/api/admin/channels?limit=50', 'the normal channel list removes the frozen include_archived=true parameter before its real GET');
  assert.deepEqual(state.listSnapshots.at(0), ['10', '11', '12'], 'the normal list total projects only its three non-archived resources');
  assert.equal([...list.window.document.querySelectorAll('tbody tr')].length, 3, 'the normal channel list renders only its non-archived rows');
  assert.equal([...list.window.document.querySelectorAll('tbody tr')].some((row) => row.querySelectorAll(':scope > td')[2]?.textContent?.trim() === '归档'), false, 'an archived row is not retained as a disabled normal-list row');
  assert.equal([...list.window.document.querySelectorAll('tr')].some((row) => row.textContent?.includes('同名渠道') && [...row.querySelectorAll('a')].some((node) => node.textContent === '下载二维码')), true, 'an active row keeps its QR download action');
  assert.equal(list.window.document.querySelector('[title*="永久删除"]')?.textContent, '删除不可用', 'permanent deletion remains visibly unavailable');
  const activeAction = actionForName('同名渠道');
  activeAction.click();
  await waitFor(() => list.window.document.querySelector('#fb-mask')?.hidden === false, 'delete must use the shared confirmation dialog');
  list.window.document.querySelector('#fb-cancel').click();
  assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, 0, 'cancelling archive sends zero writes');
  await confirm(activeAction, 'a 502 attempt requires confirmation');
  await waitFor(() => /尚未确认/.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), 'a 502 remains an unknown result');
  const proxyWrite = state.calls.find((call) => call.method === 'PATCH');
  state.outcomes.set('10', 'success');
  activeAction.click();
  await waitFor(() => list.window.document.querySelector('#fb-mask')?.hidden === false, 'an unconfirmed intent can be retried after a fresh confirmation');
  // This is a real controller render: filter down to another record while the
  // confirm callback still refers to the prior row's captured resourceId.
  const search = list.window.document.querySelector('input[aria-label="搜索渠道名称"]');
  search.value = '权限渠道'; search.dispatchEvent(new list.window.Event('input', { bubbles: true }));
  await pause();
  assert.ok(actionForName('同名渠道'), 'typing keeps the frozen channel list as a draft until Enter');
  search.dispatchEvent(new list.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' }));
  await waitFor(() => Boolean(actionForName('权限渠道')) && !actionForName('同名渠道'), 'an ordinary Enter must invoke the frozen controller and rerender a different row');
  list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => state.calls.filter((call) => call.method === 'PATCH').length === 2, 'same-version retry must issue one further write');
  const write = state.calls.filter((call) => call.method === 'PATCH').at(-1);
  assert.equal(write.path, '/api/admin/channels/10', 'duplicate names and actual controller rerender retain the original resourceId closure');
  assert.equal(write.headers.get('If-Match'), proxyWrite.headers.get('If-Match'), 'the same unconfirmed intent retains its original ETag');
  assert.equal(write.headers.get('Idempotency-Key'), proxyWrite.headers.get('Idempotency-Key'), 'the same unconfirmed intent retains its idempotency key');
  assert.deepEqual(write.body, proxyWrite.body, 'the same unconfirmed intent retains its payload');
  assert.deepEqual(write.body.welcome_image_library_ids, [11]); assert.deepEqual(write.body.welcome_miniprogram_library_ids, [12]);
  assert.deepEqual(write.body.welcome_attachment_library_ids, [13]); assert.deepEqual(write.body.welcome_group_invite_library_ids, [14]);
  assert.equal(write.body.entry_tag_id, '3');
  await waitFor(() => /渠道已删除：/.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), 'confirmed delete completes after the authoritative normal-list readback');
  const listReadback = state.calls.filter((call) => call.method === 'GET' && call.path.startsWith('/api/admin/channels?')).at(-1);
  assert.equal(listReadback?.path, '/api/admin/channels?limit=50', 'delete readback keeps the normal-list request free of include_archived');
  assert.deepEqual(state.listSnapshots.at(-1), ['11', '12'], 'delete readback updates the normal list total without the archived resource');
  search.value = ''; search.dispatchEvent(new list.window.Event('input', { bubbles: true }));
  search.dispatchEvent(new list.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' }));
  await waitFor(() => Boolean(actionForName('权限渠道')) && Boolean(actionForName('预读渠道')) && !actionForName('同名渠道'), 'refreshing the visible list keeps the archived channel hidden');
  const writesAfterConfirmedReadback = state.calls.filter((call) => call.method === 'PATCH').length;
  activeAction.click(); await pause();
  assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, writesAfterConfirmedReadback, 'a stale callback after confirmation cannot create a new write');

  const permissionAction = actionForName('权限渠道');
  for (const [status, message] of [['401', /登录已失效/], ['403', /没有删除渠道的权限/], ['409', /已被其他人更新/]]) {
    state.outcomes.set('11', status);
    const before = state.calls.filter((call) => call.method === 'PATCH').length;
    await confirm(permissionAction, `HTTP ${status} requires confirmation`);
    await waitFor(() => message.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), `HTTP ${status} must be visible`);
    assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, before + 1, `HTTP ${status} sends only its one attempted PATCH`);
  }

  search.value = '预读渠道'; search.dispatchEvent(new list.window.Event('input', { bubbles: true }));
  search.dispatchEvent(new list.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' }));
  await waitFor(() => Boolean(actionForName('预读渠道')), 'an ordinary Enter renders the preflight fixture');
  const preflightAction = actionForName('预读渠道');
  state.outcomes.set('12', 'preflight_network');
  const beforePreflight = state.calls.filter((call) => call.method === 'PATCH').length;
  await confirm(preflightAction, 'preflight failure still requires confirmation');
  await waitFor(() => /读取渠道配置失败/.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), 'preflight read failure is visible');
  assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, beforePreflight, 'preflight read failure sends zero writes');

  state.outcomes.set('12', 'network_unknown');
  await confirm(preflightAction, 'network outcome still requires confirmation');
  await waitFor(() => /尚未确认/.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), 'network outcome requires readback');
  const beforeChangedIntent = state.calls.filter((call) => call.method === 'PATCH').length;
  state.outcomes.set('12', 'success'); state.etag.set('12', '"14"'); state.names.set('12', '配置已变渠道');
  await confirm(preflightAction, 'changed configuration requires a fresh confirmation attempt');
  await waitFor(() => /配置已变化/.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), 'changed payload/version blocks reuse of the unknown intent');
  assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, beforeChangedIntent, 'changed payload/version sends zero PATCH with the old intent');
  state.failNextListRead = true;
  await confirm(preflightAction, 'a confirmed delete can still have an unavailable normal-list readback');
  await waitFor(() => /已删除，但列表未更新/.test(list.window.document.querySelector('#fb-toast')?.textContent || ''), 'a failed authoritative list readback remains visible instead of inventing a refreshed list');
  assert.equal(state.calls.some((call) => call.method === 'DELETE'), false, 'the V3 archive path never issues DELETE');
} finally {
  list.window.close();
}

console.log('channel list stable-resource archive binding: PASS');
