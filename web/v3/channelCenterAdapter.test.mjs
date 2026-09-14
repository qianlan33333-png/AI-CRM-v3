// The Channel Center form now mounts the standard admission Host. Keep this
// historical entrypoint in the required test set while the journey itself
// lives beside the Host it verifies.
await import('./channelAdmissionHost.test.mjs');

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole } = jsdom;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const adapter = await buildTestBrowserBundle(path.join(root, 'web/v3/channelCenterAdapter.ts'));
const pause = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(check, message) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const value = check();
    if (value) return value;
    await pause();
  }
  throw new Error(message);
}

// This fixture starts with the stale active-looking DOM observed in the
// console. The adapter must remove that interaction and keep the edit path
// while showing why the channel cannot send entrant actions.
const list = new JSDOM(`<!doctype html><body data-page="channels"><table><tbody>
  <tr id="archived"><td>多客服测试</td><td>普通二维码</td><td><span>归档</span></td><td>欢迎语</td><td>0</td><td><a>下载二维码</a><a>查看</a><a>编辑</a><a aria-disabled="true" title="后端暂无渠道归档 operation">下架</a><a aria-disabled="true" title="后端暂无渠道删除 operation">删除</a></td></tr>
  <tr id="active"><td>活动渠道</td><td>普通二维码</td><td><span>启用</span></td><td>欢迎语</td><td>0</td><td><a>下载二维码</a><a>查看</a><a>编辑</a></td></tr>
  <tr id="archive-action"><td>待归档渠道</td><td>普通二维码</td><td><span>启用</span></td><td>欢迎语</td><td>0</td><td><a aria-disabled="true" title="后端暂无渠道归档 operation">下架</a><a aria-disabled="true" title="后端暂无渠道删除 operation">删除</a></td></tr>
</tbody></table></body>`, {
  url: 'https://test.invalid/admin/channels', runScripts: 'dangerously', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.document.cookie = 'aicrm_csrf=fixture-csrf; Path=/';
    const calls = [];
    const state = { archiveStatus: 'active', outcome: 'success', etag: '"5"', name: '待归档渠道', detailReads: 0 };
    window.__channelArchiveTest = { calls, state };
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input.url, window.location.href);
      const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase();
      calls.push({ method, path: `${url.pathname}${url.search}`, headers: new Headers(init.headers), body: init.body ? JSON.parse(init.body) : undefined });
      if (url.pathname === '/api/admin/channels/9' && method === 'GET') {
        state.detailReads += 1;
        if (state.outcome === 'preflight_network' || (state.outcome === 'post_patch_readback_network' && state.detailReads > 1)) throw new TypeError('detail network unavailable');
        return new Response(JSON.stringify({ channel: {
          channel_type: 'qrcode', carrier_type: 'qrcode', channel_name: state.name, channel_code: 'keep-code', scene_value: 'scene', qr_url: 'https://example.invalid/qr', status: state.archiveStatus, owner_staff_id: '7', customer_channel: 'source', link_url: '', final_url: '', welcome_message: '欢迎{{客户名}}', welcome_image_library_ids: [11], welcome_miniprogram_library_ids: [12], welcome_attachment_library_ids: [13], welcome_group_invite_library_ids: [14], auto_accept_friend: false, entry_tag_id: '3', entry_tag_name: '新客', entry_tag_group_name: '来源', assignment_mode: 'multi_staff', assignment_strategy: 'ratio', overflow_policy: '', assignment_config_json: { assignees: [{ staff_id: 7, priority: 1, ratio_percent: 100, max_scans_24h: 0 }] },
        } }), { status: 200, headers: { 'Content-Type': 'application/json', ETag: state.etag } });
      }
      if (url.pathname === '/api/admin/channels/9' && method === 'PATCH') {
        if (state.outcome === 'network_unknown') throw new TypeError('network unavailable');
        if (state.outcome === 'forbidden') return new Response(JSON.stringify({ code: 'FORBIDDEN' }), { status: 403, headers: { 'Content-Type': 'application/json' } });
        if (state.outcome === 'conflict') return new Response(JSON.stringify({ code: 'VERSION_CONFLICT' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
        if (state.outcome === 'proxy_502') return new Response(JSON.stringify({ code: 'BAD_GATEWAY' }), { status: 502, headers: { 'Content-Type': 'application/json' } });
        state.archiveStatus = 'archived';
        state.etag = '"' + (Number(state.etag.replace(/\D/g, '')) + 1) + '"';
        return new Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.pathname === '/api/admin/channels') {
        if (state.outcome === 'list_failure') throw new TypeError('list network unavailable');
        return new Response(JSON.stringify({
        channels: [
          { id: 1, channel_name: '多客服测试', status: 'archived', qr_download_url: '/api/admin/channels/1/qrcode/download' },
          { id: 2, channel_name: '活动渠道', status: 'active', qr_download_url: '/api/admin/channels/2/qrcode/download' },
          { id: 9, channel_name: '待归档渠道', status: state.archiveStatus, qr_download_url: '' },
        ],
        }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      return new Response(JSON.stringify({ code: 'NOT_FOUND' }), { status: 404, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  list.window.eval(adapter);
  const adapted = await list.window.fetch('/api/admin/channels');
  const payload = await adapted.json();
  const renderSignal = list.window.document.createElement('i');
  list.window.document.body.append(renderSignal); renderSignal.remove();
  await waitFor(() => list.window.document.querySelector('[data-channel-archive-id="9"]'), 'the V3 adapter must replace the frozen archive placeholder after the catalog render');
  assert.equal(payload.channels[0].qr_download_url, '', 'the list projection must never advertise a retained archived QR as scan-ready');
  assert.equal(payload.channels[1].qr_download_url, '/api/admin/channels/2/qrcode/download', 'an active channel keeps its download path');
  const archivedRow = await waitFor(() => list.window.document.getElementById('archived')?.dataset.channelEntrantActionsBlocked === 'archived' && list.window.document.getElementById('archived'), 'archived row readiness must be repaired');
  assert.equal([...archivedRow.querySelectorAll('a')].some((node) => node.textContent === '下载二维码'), false, 'an archived row must not retain a download interaction');
  assert.match(archivedRow.querySelector('[data-channel-entrant-actions-blocked]')?.textContent || '', /扫码不会发送欢迎语或入渠标签/, 'archived row must explain the no-send state');
  const archivedAction = archivedRow.querySelector('[data-channel-archive-id="1"]');
  assert.equal(archivedAction.disabled, true, 'an archived row must show a disabled completed action');
  assert.equal(archivedAction.textContent, '已归档', 'an archived row must not offer a second archive');
  const activeRow = list.window.document.getElementById('active');
  assert.equal([...activeRow.querySelectorAll('a')].some((node) => node.textContent === '下载二维码'), true, 'an active row keeps its normal QR action');
  const archiveButton = list.window.document.querySelector('[data-channel-archive-id="9"]');
  assert.equal(list.window.document.querySelector('[aria-disabled="true"][title*="永久删除"]')?.textContent, '删除不可用', 'the legacy delete placeholder must become a visibly unavailable action');
  const resetActive = ({ outcome = 'success', etag = '"6"', name = '待归档渠道' } = {}) => {
    const state = list.window.__channelArchiveTest.state;
    state.archiveStatus = 'active'; state.outcome = outcome; state.etag = etag; state.name = name; state.detailReads = 0;
    archiveButton.disabled = false; archiveButton.removeAttribute('aria-disabled'); archiveButton.textContent = '归档';
    archiveButton.closest('tr').querySelectorAll(':scope > td')[2].textContent = '启用';
  };
  assert.equal(archiveButton.__dcBound, true, 'the archive action must be owned by the V3 adapter instead of the generic backend-blocked delegate');
  archiveButton.click();
  await waitFor(() => list.window.document.querySelector('#fb-mask')?.hidden === false, 'archive must require the shared confirmation dialog');
  list.window.document.querySelector('#fb-cancel').click();
  assert.equal(list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length, 0, 'cancelling archive must send no write');
  archiveButton.click();
  list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length === 1, 'confirmed archive must send one PATCH');
  await waitFor(() => list.window.__channelArchiveTest.calls.some((item) => item.path === '/api/admin/channels?limit=50&include_archived=true&status=archived&q=keep-code'), 'archive must read back the archived row in the list before reporting success');
  const write = list.window.__channelArchiveTest.calls.find((item) => item.method === 'PATCH');
  assert.equal(write.headers.get('If-Match'), '"5"', 'archive must use the server ETag');
  assert.ok(write.headers.get('Idempotency-Key'), 'archive must use a stable idempotency key');
  assert.equal(write.headers.get('X-CSRF-Token'), 'fixture-csrf', 'archive must carry the current session CSRF token');
  assert.equal(write.body.status, 'archived');
  assert.equal(write.body.channel_code, 'keep-code', 'archive must retain the immutable code');
  assert.deepEqual(write.body.welcome_image_library_ids, [11]);
  assert.deepEqual(write.body.welcome_miniprogram_library_ids, [12]);
  assert.deepEqual(write.body.welcome_attachment_library_ids, [13]);
  assert.deepEqual(write.body.welcome_group_invite_library_ids, [14]);
  assert.equal(write.body.entry_tag_id, '3', 'archive must retain tag configuration');
  assert.deepEqual(write.body.assignment_config_json.assignees, [{ staff_id: 7, priority: 1, ratio_percent: 100, max_scans_24h: 0 }]);
  assert.equal(list.window.__channelArchiveTest.calls.some((item) => item.method === 'DELETE'), false, 'archive must never use a DELETE request');

  resetActive({ outcome: 'forbidden', etag: '"6"' });
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'permission failure must restore the action');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /没有归档渠道的权限/, '403 must state that no archive was applied');

  resetActive({ outcome: 'conflict', etag: '"7"' });
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'CAS failure must restore the action');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /已被其他人更新/, '409 must require a refresh instead of overwriting another update');

  resetActive({ outcome: 'network_unknown', etag: '"8"', name: '未知结果前配置' });
  const writesBeforeUnknown = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'unknown outcome must restore the action after readback');
  const unknownWrites = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').slice(writesBeforeUnknown);
  assert.equal(unknownWrites.length, 1, 'unknown outcome must not blindly retry the PATCH');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /尚未确认/, 'unknown result must instruct readback rather than claim success');

  list.window.__channelArchiveTest.state.outcome = 'success';
  list.window.__channelArchiveTest.state.etag = '"9"';
  list.window.__channelArchiveTest.state.name = '未知结果后已改配置';
  const writesBeforeChangedIntent = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'changed configuration must end the old unknown intent without writing');
  assert.equal(list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length, writesBeforeChangedIntent, 'changed payload and ETag must never be sent with the old key');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /配置或版本已变化/, 'changed configuration must require a new confirmation');
  const writesBeforeNewIntent = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length === writesBeforeNewIntent + 1, 'new confirmation must send one independently versioned PATCH');
  await waitFor(() => archiveButton.disabled, 'new confirmation after a changed unknown intent must finish independently');
  const changedIntentWrite = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').at(-1);
  assert.equal(changedIntentWrite.headers.get('If-Match'), '"9"', 'new intent must use the new ETag');
  assert.notEqual(changedIntentWrite.headers.get('Idempotency-Key'), unknownWrites[0].headers.get('Idempotency-Key'), 'new payload/version must receive a new idempotency key');

  resetActive({ outcome: 'preflight_network', etag: '"11"' });
  const beforePreflight = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'preflight read failure must restore the action');
  assert.equal(list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length, beforePreflight, 'preflight read failure must send no PATCH');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /读取渠道配置失败/, 'preflight read failure must be visible');

  resetActive({ outcome: 'proxy_502', etag: '"12"' });
  const writesBeforeProxy = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'a 502 must leave the action available only after readback');
  const proxyWrite = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').at(-1);
  assert.equal(list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length, writesBeforeProxy + 1, 'a 502 must make one PATCH attempt');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /尚未确认/, 'a 502 must not claim a rejected archive');
  list.window.__channelArchiveTest.state.outcome = 'success'; list.window.__channelArchiveTest.state.detailReads = 0;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length === writesBeforeProxy + 2, 'same version retry after a 502 must reuse one saved intent');
  await waitFor(() => archiveButton.disabled && archiveButton.textContent === '已归档', 'same version retry must confirm archive before the next scenario');
  const proxyRetry = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').at(-1);
  assert.equal(proxyRetry.headers.get('If-Match'), proxyWrite.headers.get('If-Match'), 'same intent retry keeps the original ETag');
  assert.equal(proxyRetry.headers.get('Idempotency-Key'), proxyWrite.headers.get('Idempotency-Key'), 'same intent retry keeps the original key');
  assert.deepEqual(proxyRetry.body, proxyWrite.body, 'same intent retry keeps the exact original payload');

  resetActive({ outcome: 'post_patch_readback_network', etag: '"14"' });
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'post-PATCH readback failure must restore an unconfirmed action');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /回读渠道失败/, 'post-PATCH readback failure must be visible as unknown');

  resetActive({ outcome: 'list_failure', etag: '"15"' });
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => !archiveButton.disabled, 'a changed unknown intent must be ended before a fresh list-readback attempt');
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => archiveButton.disabled && archiveButton.textContent === '已归档', 'confirmed archive must stay disabled when list readback fails');
  assert.match(list.window.document.querySelector('#fb-toast')?.textContent || '', /列表回读失败/, 'list readback failure must be visible without reopening archive');

  resetActive({ outcome: 'success', etag: '"17"' });
  list.window.__channelArchiveTest.state.archiveStatus = 'archived';
  const beforeStaleRow = list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length;
  archiveButton.click(); list.window.document.querySelector('#fb-ok').click();
  await waitFor(() => archiveButton.disabled && archiveButton.textContent === '已归档', 'stale active-looking row must become completed after detail readback');
  assert.equal(list.window.__channelArchiveTest.calls.filter((item) => item.method === 'PATCH').length, beforeStaleRow, 'already archived detail must never PATCH again');
} finally { list.window.close(); }

console.log('channel list archived readiness: PASS');
