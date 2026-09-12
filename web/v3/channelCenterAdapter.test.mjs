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
  <tr id="archived"><td>多客服测试</td><td>普通二维码</td><td><span>归档</span></td><td>欢迎语</td><td>0</td><td><a>下载二维码</a><a>查看</a><a>编辑</a></td></tr>
  <tr id="active"><td>活动渠道</td><td>普通二维码</td><td><span>启用</span></td><td>欢迎语</td><td>0</td><td><a>下载二维码</a><a>查看</a><a>编辑</a></td></tr>
</tbody></table></body>`, {
  url: 'https://test.invalid/admin/channels', runScripts: 'dangerously', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input.url, window.location.href);
      if (url.pathname === '/api/admin/channels') return new Response(JSON.stringify({
        channels: [
          { id: 1, status: 'archived', qr_download_url: '/api/admin/channels/1/qrcode/download' },
          { id: 2, status: 'active', qr_download_url: '/api/admin/channels/2/qrcode/download' },
        ],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ code: 'NOT_FOUND' }), { status: 404, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  list.window.eval(adapter);
  const adapted = await list.window.fetch('/api/admin/channels');
  const payload = await adapted.json();
  assert.equal(payload.channels[0].qr_download_url, '', 'the list projection must never advertise a retained archived QR as scan-ready');
  assert.equal(payload.channels[1].qr_download_url, '/api/admin/channels/2/qrcode/download', 'an active channel keeps its download path');
  const archivedRow = await waitFor(() => list.window.document.getElementById('archived')?.dataset.channelEntrantActionsBlocked === 'archived' && list.window.document.getElementById('archived'), 'archived row readiness must be repaired');
  assert.equal([...archivedRow.querySelectorAll('a')].some((node) => node.textContent === '下载二维码'), false, 'an archived row must not retain a download interaction');
  assert.match(archivedRow.querySelector('[data-channel-entrant-actions-blocked]')?.textContent || '', /扫码不会发送欢迎语或入渠标签/, 'archived row must explain the no-send state');
  const activeRow = list.window.document.getElementById('active');
  assert.equal([...activeRow.querySelectorAll('a')].some((node) => node.textContent === '下载二维码'), true, 'an active row keeps its normal QR action');
} finally { list.window.close(); }

console.log('channel list archived readiness: PASS');
