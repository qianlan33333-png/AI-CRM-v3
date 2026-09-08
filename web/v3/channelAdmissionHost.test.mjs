import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
import fs from 'node:fs/promises';

const { JSDOM, VirtualConsole, requestInterceptor } = jsdom;

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/channelCenterAdapter.ts'));
const donorForm = await fs.readFile(path.join(root, 'web/donors/standard-components-production/channel/channel_code_form.html'), 'utf8');
const donorScript = await fs.readFile(path.join(root, 'web/donors/standard-components-production/channel/channel_admission_pages.js'), 'utf8');
const pause = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(check, message) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const value = check();
    if (value) return value;
    await pause();
  }
  throw new Error(message);
}

function response(payload, status = 200, headers = {}) {
  return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json', ...headers } });
}

function channel(overrides = {}) {
  return {
    id: 17, version: 7, config_version: 7, channel_name: '原渠道', channel_code: 'origin-code',
    channel_type: 'qrcode', carrier_type: 'qrcode', status: 'active', auto_accept_friend: true,
    assignment_config_json: { assignees: [{ staff_id: 12, priority: 1, ratio_percent: 100, max_scans_24h: 0 }] },
    welcome_image_library_ids: [], welcome_miniprogram_library_ids: [], welcome_attachment_library_ids: [], welcome_group_invite_library_ids: [],
    ...overrides,
  };
}

function createPage({ saved = channel(), mutations = [], creates = [], resourceID = '17', donorScriptStatus = 200, delayDonorScript = false } = {}) {
  const calls = [];
  let releaseDonorScript;
  const resourceAttribute = resourceID ? ` data-channel-resource-id="${resourceID}"` : '';
  const dom = new JSDOM(`<!doctype html><body data-page="channelForm"${resourceAttribute}><main></main></body>`, {
    url: resourceID ? `https://test.invalid/admin/channels/${resourceID}/edit` : 'https://test.invalid/admin/channels/new', runScripts: 'dangerously',
    resources: { interceptors: [requestInterceptor(async (request) => {
      if (request.url === 'https://test.invalid/assets/standard-components/channel_admission_pages.js') {
        if (delayDonorScript) return new Promise((resolve) => { releaseDonorScript = () => resolve(new Response(donorScript, { status: donorScriptStatus, headers: { 'Content-Type': 'application/javascript' } })); });
        return new Response(donorScript, { status: donorScriptStatus, headers: { 'Content-Type': 'application/javascript' } });
      }
      return undefined;
    })] }, pretendToBeVisual: true,
    virtualConsole: new VirtualConsole(),
    beforeParse(window) {
      window.Request = Request; window.Response = Response; window.Headers = Headers;
      window.AdminConsole = { showToast() {} };
      window.AICRMStandardComponents = { ready: async () => undefined };
      window.AICRMSendContentComposer = { mount(_container, options) { window.__channelComposerOptions = options; } };
      window.AICRMWeComTagPicker = { open() {} };
      window.OperationMemberPicker = { open({ onConfirm }) { onConfirm([{ user_id: '12', display_name: '测试客服' }]); } };
      window.fetch = async (input, init = {}) => {
        const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
        const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase();
        const headers = new Headers(init.headers || (typeof input === 'string' ? undefined : input.headers));
        calls.push({ path: url.pathname, method, headers, body: init.body || '' });
        if (method === 'GET' && url.pathname === '/assets/standard-components/channel_code_form.html') return new Response(donorForm, { status: 200 });
        if (method === 'GET' && url.pathname === '/api/admin/channels/17') return response({ ok: true, channel: saved }, 200, { ETag: '"7"' });
        if (method === 'PATCH' && url.pathname === '/api/admin/channels/17') {
          const next = mutations.shift() || { ok: true, channel: { ...saved, id: 17, version: 8 } };
          return response(next.payload || next, next.status || 200, next.headers || { ETag: '"8"' });
        }
        if (method === 'POST' && url.pathname === '/api/admin/channels') {
          const next = creates.shift() || { ok: true, channel: { id: 19, ...JSON.parse(init.body || '{}') } };
          return response(next, 201, { ETag: '"1"' });
        }
        return response({ code: 'NOT_FOUND' }, 404);
      };
    },
  });
  dom.window.eval(host);
  return { dom, calls, get releaseDonorScript() { return releaseDonorScript; } };
}

// Standard panel switching preserves the one form instance; this reproduces
// the production name/code loss before the user reaches the carrier panel.
const stable = createPage();
try {
  await waitFor(() => stable.dom.window.document.querySelector('[data-channel-admission-page]'), 'standard channel form must mount');
  await waitFor(() => stable.dom.window.__channelComposerOptions, 'the externally loaded standard donor script must initialize');
  const document = stable.dom.window.document;
  assert.equal(document.querySelector('script[data-aicrm-channel-donor]')?.src, 'https://test.invalid/assets/standard-components/channel_admission_pages.js', 'the byte-preserved donor script must load as a same-origin external resource');
  assert.equal(document.querySelectorAll('[data-channel-bootstrap]').length, 1, 'Host hydration must replace the donor placeholder with one V3 bootstrap payload');
  assert.equal(document.querySelectorAll('[name="status"] option[selected]').length, 1, 'Jinja status branches must render one selected option');
  assert.equal(document.querySelector('[name="status"] option[selected]')?.value, 'active');
  assert.equal(document.querySelectorAll('[name="channel_type"]:checked').length, 1, 'Jinja carrier branches must render one checked type');
  assert.equal(document.querySelector('[data-qrcode-section]').hidden, false, 'the QR branch must be visible for a QR channel');
  assert.ok([...document.querySelectorAll('[data-link-section]')].every((node) => node.hidden), 'all link-only donor branches must be hidden for a QR channel');
  assert.equal(document.querySelector('[data-summary-channel-status]')?.textContent, '启用', 'the donor script must hydrate the rendered status summary');
  assert.equal(document.querySelectorAll('[data-generate-form-qrcode]').length, 1, 'an edit form must render one generate action');
  assert.equal(document.querySelectorAll('[data-download-channel-qrcode]').length, 0, 'an absent download URL must not leave a duplicate donor action');
  assert.equal(document.documentElement.innerHTML.includes('{%'), false, 'no Jinja control syntax may reach the Host DOM');
  assert.equal(document.documentElement.innerHTML.includes('{{ channel'), false, 'no unresolved channel substitution may reach the Host DOM');
  document.querySelector('[name="channel_name"]').value = '维度切换后仍保留';
  document.querySelector('[name="channel_code"]').value = 'channel-draft-retained';
  document.querySelector('[data-channel-panel="carrier"]').click();
  document.querySelector('[data-channel-panel="basic"]').click();
  assert.equal(document.querySelector('[name="channel_name"]').value, '维度切换后仍保留');
  assert.equal(document.querySelector('[name="channel_code"]').value, 'channel-draft-retained');
  assert.ok(document.querySelector('[data-qrcode-section]'), 'ordinary QR uses the standard QR section');
  document.querySelector('[data-channel-type-card="wecom_customer_acquisition"]').click();
  assert.ok([...document.querySelectorAll('[data-link-section]')].every((node) => !node.hidden), 'acquisition links expose every standard link-only donor control');
} finally { stable.dom.window.close(); }

// A real Host save supplies Catalog's complete replacement DTO, server ETag
// and stable idempotency receipt. It does not make a second write for 409.
const conflict = createPage({ mutations: [{ status: 409, payload: { code: 'VERSION_CONFLICT' } }] });
try {
  await waitFor(() => conflict.dom.window.document.querySelector('[data-save-channel]'), 'save button must mount');
  await waitFor(() => conflict.dom.window.__channelComposerOptions, 'the standard donor interactions must be installed');
  const document = conflict.dom.window.document;
  document.querySelector('[name="channel_name"]').value = '本地草稿保留';
  // The standard composer returns its Media library IDs. A group chat's
  // external chat ID never crosses this boundary as a fake numeric value.
  conflict.dom.window.__channelComposerOptions.onChange({
    content_text: '欢迎 {{客户名}}', image_library_ids: [], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [901],
  });
  const save = document.querySelector('[data-save-channel]');
  save.click();
  save.click();
  await waitFor(() => document.querySelector('[data-channel-save-feedback]')?.textContent.includes('当前草稿已保留'), '409 must give a non-destructive conflict explanation');
  const patch = conflict.calls.find((call) => call.method === 'PATCH');
  assert.ok(patch, 'save must issue one PATCH');
  assert.equal(patch.headers.get('If-Match'), '"7"', 'PATCH must use the server ETag');
  assert.match(patch.headers.get('Idempotency-Key'), /^channel-/, 'PATCH must carry a stable idempotency key');
  const payload = JSON.parse(patch.body);
  assert.ok(Array.isArray(payload.assignment_config_json.assignees), 'standard assignees must map to the V3 Catalog DTO');
  assert.equal(payload.assignees, undefined, 'unsupported donor assignees field must never reach Catalog');
  assert.deepEqual(payload.welcome_group_invite_library_ids, [901], 'group selection must keep the eligible GroupInvite library ID');
  assert.equal(document.querySelector('[name="channel_name"]').value, '本地草稿保留', '409 must retain local fields');
  assert.equal(conflict.calls.filter((call) => call.method === 'PATCH').length, 1, '409 must not overwrite or retry automatically');
  assert.equal(conflict.calls.filter((call) => call.method === 'GET' && call.path === '/api/admin/channels/17').length, 1, '409 must use the ETag read with the page and never preflight-read a newer version');
} finally { conflict.dom.window.close(); }

// A new channel follows the same full DTO path. Adding a member is required
// before the Catalog accepts its assignment and the receipt identifies the
// one logical create, rather than a panel-by-panel partial write.
const created = createPage({ resourceID: '', creates: [{ ok: true, channel: { id: 19, channel_name: '新渠道', channel_code: 'new-channel' } }] });
try {
  await waitFor(() => created.dom.window.document.querySelector('[data-add-channel-assignee]'), 'new standard channel form must mount');
  await waitFor(() => created.dom.window.__channelComposerOptions, 'the external donor script must bind the new-channel form');
  const document = created.dom.window.document;
  assert.equal(document.querySelectorAll('[data-generate-form-qrcode]').length, 0, 'a new channel must not expose the edit-only QR generation action');
  document.querySelector('[name="channel_name"]').value = '新渠道';
  document.querySelector('[name="channel_code"]').value = 'new-channel';
  document.querySelector('[data-add-channel-assignee]').click();
  await waitFor(() => document.querySelector('[data-assignee-list]')?.textContent.includes('测试客服'), 'new channel picker result must enter assignment state');
  document.querySelector('[data-save-channel]').click();
  await waitFor(() => created.calls.some((call) => call.method === 'POST' && call.path === '/api/admin/channels'), 'new channel must submit one Catalog create');
  const post = created.calls.find((call) => call.method === 'POST' && call.path === '/api/admin/channels');
  assert.match(post.headers.get('Idempotency-Key'), /^channel-/, 'new channel must use a Catalog idempotency receipt');
  const payload = JSON.parse(post.body);
  assert.equal(payload.channel_name, '新渠道');
  assert.equal(payload.channel_code, 'new-channel');
  assert.deepEqual(payload.assignment_config_json.assignees.map((item) => item.staff_id), [12], 'new channel must persist the picked staff owner ID');
  assert.equal(created.calls.filter((call) => call.method === 'POST' && call.path === '/api/admin/channels').length, 1, 'a single save must never double-create');
} finally { created.dom.window.close(); }

// Loading errors remain visible and leave the untouched channel configuration
// in place; the Host must not turn an external-script failure into a blank page.
const unavailableDonor = createPage({ donorScriptStatus: 503 });
try {
  await waitFor(() => unavailableDonor.dom.window.document.querySelector('[role="alert"]')?.textContent.includes('标准渠道交互脚本加载失败'), 'a donor script HTTP failure must render a retryable Host error');
  assert.equal(unavailableDonor.calls.filter((call) => call.method === 'PATCH' || call.method === 'POST').length, 0, 'a donor loading failure must not save or mutate a channel');
} finally { unavailableDonor.dom.window.close(); }

// The standard IIFE is a delayed external script in production. Its V3
// bootstrap must already be present when those bytes arrive, without issuing
// a mutation while the form is waiting for its behavior to bind.
const delayedDonor = createPage({ delayDonorScript: true });
try {
  await waitFor(() => typeof delayedDonor.releaseDonorScript === 'function', 'channel donor request must be pending before exercising delayed loading');
  delayedDonor.releaseDonorScript();
  await waitFor(() => delayedDonor.dom.window.__channelComposerOptions, 'delayed standard donor must read the hydrated V3 bootstrap');
  assert.equal(delayedDonor.dom.window.document.querySelectorAll('[data-channel-bootstrap]').length, 1, 'delayed donor receives exactly one bootstrap payload');
  assert.equal(delayedDonor.calls.some((call) => call.method === 'POST' || call.method === 'PATCH'), false, 'delayed donor bootstrap must not save a channel');
} finally { delayedDonor.dom.window.close(); }

console.log('standard channel Host draft, carrier, CAS, create journeys: PASS');
