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

function createPage({ saved = channel(), mutations = [], creates = [], resourceID = '17', donorScriptStatus = 200, delayDonorScript = false, operationMembers = { status: 200, payload: { items: [{ staff_id: 12, user_id: 'wecom-alice', display_name: '测试客服' }] } } } = {}) {
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
      window.OperationMemberPicker = { open(options) { window.__pickerOptions = options; const { onConfirm } = options; onConfirm([{ staff_id: 12, user_id: 'wecom-alice', display_name: '测试客服' }]); } };
      window.fetch = async (input, init = {}) => {
        const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
        const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase();
        const headers = new Headers(init.headers || (typeof input === 'string' ? undefined : input.headers));
        calls.push({ path: url.pathname, method, headers, body: init.body || '' });
        if (['PATCH', 'POST'].includes(method) && /^\/api\/admin\/channels(?:\/[0-9]+)?$/.test(url.pathname)) {
          const allowed = new Set(['channel_type','carrier_type','channel_name','channel_code','scene_value','qr_url','status','owner_staff_id','customer_channel','link_url','final_url','welcome_message','welcome_image_library_ids','welcome_miniprogram_library_ids','welcome_attachment_library_ids','welcome_group_invite_library_ids','auto_accept_friend','entry_tag_id','entry_tag_name','entry_tag_group_name','assignment_mode','assignment_strategy','overflow_policy','assignment_config_json']);
          const unknown = Object.keys(JSON.parse(init.body || '{}')).filter((name) => !allowed.has(name));
          if (unknown.length) return response({code:'MALFORMED_REQUEST'}, 400);
        }
        if (method === 'GET' && url.pathname === '/api/admin/common/operation-members') return response(operationMembers.payload, operationMembers.status);
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
  assert.equal(document.querySelector('#channel-welcome-template-help')?.textContent.includes('可使用 {{客户名}} 自动带入客户姓名'), true, 'welcome editor must show the exact server-supported variable');
  assert.equal(document.querySelector('#channel-welcome-template-help')?.textContent.includes('朋友'), true, 'welcome editor must disclose the safe missing-name fallback');
  assert.equal(document.querySelectorAll('[name="status"] option[selected]').length, 1, 'Jinja status branches must render one selected option');
  assert.equal(document.querySelector('[name="status"] option[selected]')?.value, 'active');
  assert.equal(document.querySelectorAll('[name="channel_type"]:checked').length, 1, 'Jinja carrier branches must render one checked type');
  assert.equal(document.querySelector('[data-qrcode-section]').hidden, false, 'the QR branch must be visible for a QR channel');
  assert.ok([...document.querySelectorAll('[data-link-section]')].every((node) => node.hidden), 'all link-only donor branches must be hidden for a QR channel');
  assert.equal(document.querySelector('[data-summary-channel-status]')?.textContent, '启用', 'the donor script must hydrate the rendered status summary');
  assert.equal(document.querySelector('[data-assignee-list]')?.textContent.includes('测试客服'), true, 'saved channel assignees must use the trusted local directory display name');
  assert.equal(document.querySelector('[data-assignee-list]')?.textContent.includes('客服 #12'), false, 'saved channel assignees must not retain synthetic service labels after directory hydration');
  assert.equal(stable.calls.filter((call) => call.method === 'GET' && call.path === '/api/admin/common/operation-members').length, 1, 'saved channel names must use one local directory read');
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

// Saved assignments persist staff IDs only. One local directory read hydrates
// every saved staff label, while missing directory records retain an explicit
// ID fallback rather than becoming a false name or causing per-member reads.
const hydratedSavedMembers = createPage({ saved: channel({ assignment_config_json: { assignees: [
  { staff_id: 12, priority: 1, ratio_percent: 50, max_scans_24h: 100 },
  { staff_id: 99, priority: 2, ratio_percent: 50, max_scans_24h: 100 },
] } }) });
try {
  await waitFor(() => hydratedSavedMembers.dom.window.document.querySelector('[data-assignee-list]')?.textContent.includes('测试客服'), 'saved assignment display names must hydrate before donor initialization');
  const list = hydratedSavedMembers.dom.window.document.querySelector('[data-assignee-list]')?.textContent || '';
  assert.equal(list.includes('当前目录未找到客服姓名'), true, 'a staff member absent from the bounded local directory response must state that its name is unavailable without claiming a global absence');
  assert.equal(list.includes('客服 #99'), false, 'a staff ID must remain auxiliary information rather than a synthetic customer-service name');
  assert.equal(hydratedSavedMembers.calls.filter((call) => call.method === 'GET' && call.path === '/api/admin/common/operation-members').length, 1, 'multiple saved assignees must not issue N+1 directory reads');
} finally { hydratedSavedMembers.dom.window.close(); }

const unavailableSavedMembers = createPage({ operationMembers: { status: 503, payload: { ok: false, error: 'staff_directory_unavailable' } } });
try {
  await waitFor(() => unavailableSavedMembers.dom.window.document.querySelector('[data-assignee-list]')?.textContent.includes('客服姓名暂不可用'), 'a directory failure must preserve the saved selection while stating that its name is unavailable');
  assert.equal(unavailableSavedMembers.dom.window.document.querySelector('[data-assignee-list]')?.textContent.includes('客服 #12'), false, 'a staff ID must not stand in for a name when the directory is unavailable');
  assert.equal(unavailableSavedMembers.dom.window.document.querySelector('#channel-directory-read-notice')?.textContent.includes('客服姓名暂不可用'), true, 'a directory failure must show an explicit page-level unavailable state');
  assert.equal(unavailableSavedMembers.calls.filter((call) => call.method === 'PATCH' || call.method === 'POST').length, 0, 'directory fallback must not mutate saved channel configuration');
} finally { unavailableSavedMembers.dom.window.close(); }

// The standard channel entry point disables adding once all five places are
// occupied. That keeps a remaining capacity of zero from invoking the legacy
// picker call, whose historical max fallback was one.
const capped = createPage({ saved: channel({ assignment_config_json: { assignees: Array.from({ length: 5 }, (_, index) => ({
  staff_id: index + 1, priority: index + 1, ratio_percent: index === 0 ? 100 : 0, max_scans_24h: 100,
})) } }) });
try {
  await waitFor(() => capped.dom.window.document.querySelector('[data-channel-admission-page]'), 'capped channel form must mount');
  await waitFor(() => capped.dom.window.__channelComposerOptions, 'capped channel donor must initialize');
  const add = capped.dom.window.document.querySelector('[data-add-channel-assignee]');
  assert.equal(capped.dom.window.document.querySelector('[data-assignee-count]')?.textContent, '5 / 5');
  assert.equal(add.disabled, true, 'a channel with no remaining assignee capacity must not open the picker');
  add.click();
  assert.equal(capped.dom.window.__pickerOptions, undefined, 'a disabled capped entry point must not fall back to one additional picker slot');
} finally { capped.dom.window.close(); }

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
  assert.equal(Object.hasOwn(JSON.parse(patch.body), 'admin_action_token'), false, 'legacy donor action token must not reach strict Catalog decoder');
  assert.match(patch.headers.get('Idempotency-Key'), /^channel-/, 'PATCH must carry a stable idempotency key');
  const payload = JSON.parse(patch.body);
  assert.ok(Array.isArray(payload.assignment_config_json.assignees), 'standard assignees must map to the V3 Catalog DTO');
  assert.equal(payload.assignees, undefined, 'unsupported donor assignees field must never reach Catalog');
  assert.deepEqual(payload.welcome_group_invite_library_ids, [901], 'group selection must keep the eligible GroupInvite library ID');
  assert.equal(document.querySelector('[name="channel_name"]').value, '本地草稿保留', '409 must retain local fields');
  assert.equal(conflict.calls.filter((call) => call.method === 'PATCH').length, 1, '409 must not overwrite or retry automatically');
  assert.equal(conflict.calls.filter((call) => call.method === 'GET' && call.path === '/api/admin/channels/17').length, 1, '409 must use the ETag read with the page and never preflight-read a newer version');
} finally { conflict.dom.window.close(); }

// Template errors retain the exact draft and give a safe, code-specific
// correction. The Host does not trust or reflect an arbitrary server message.
const invalidWelcomeTemplate = createPage({ mutations: [{ status: 400, payload: { code: 'WELCOME_TEMPLATE_INVALID', message: 'untrusted server detail' } }] });
try {
  await waitFor(() => invalidWelcomeTemplate.dom.window.document.querySelector('[data-save-channel]'), 'template validation save button must mount');
  const { document } = invalidWelcomeTemplate.dom.window;
  const welcome = document.querySelector('[data-welcome-message]');
  welcome.value = '欢迎{{未知变量}}';
  document.querySelector('[data-save-channel]').click();
  await waitFor(() => document.querySelector('[data-channel-save-feedback]')?.textContent.includes('欢迎语仅支持 {{客户名}}'), 'invalid welcome template must explain the accepted marker');
  assert.equal(welcome.value, '欢迎{{未知变量}}', 'invalid template response must retain the original draft');
  assert.equal(document.querySelector('[data-channel-save-feedback]')?.textContent.includes('untrusted server detail'), false, 'Host must not reflect arbitrary server detail');
  assert.equal(invalidWelcomeTemplate.dom.window.location.pathname, '/admin/channels/17/edit', 'invalid template must not navigate as a successful save');
  assert.equal(invalidWelcomeTemplate.calls.filter((call) => call.method === 'PATCH').length, 1, 'invalid template must issue one command without automatic retry');
} finally { invalidWelcomeTemplate.dom.window.close(); }

// The frozen QR donor has no controls for these Catalog fields. A visible
// change must therefore retain their current values, and each accepted save
// becomes the baseline for the next CAS command.
const initialHiddenFields = channel({ qr_url: 'https://example.invalid/qr-v7', scene_value: 'state-v7', overflow_policy: 'least_loaded' });
const preserved = createPage({ saved: initialHiddenFields, mutations: [
  { headers: { ETag: '"8"' }, payload: { ok: true, channel: channel({ version: 8, qr_url: 'https://example.invalid/qr-v8', scene_value: 'state-v8', overflow_policy: 'next_available' }) } },
  { headers: { ETag: '"9"' }, payload: { ok: true, channel: channel({ version: 9, qr_url: 'https://example.invalid/qr-v9', scene_value: 'state-v9', overflow_policy: 'next_available' }) } },
] });
try {
  await waitFor(() => preserved.dom.window.__channelComposerOptions, 'QR edit interactions ready');
  const document = preserved.dom.window.document;
  const save = document.querySelector('[data-save-channel]');
  document.querySelector('[name="channel_name"]').value = '首个可见编辑';
  save.click();
  await waitFor(() => preserved.calls.filter((call) => call.method === 'PATCH').length === 1, 'first QR save must issue one PATCH');
  await waitFor(() => document.querySelector('[data-channel-save-feedback]')?.textContent.includes('保存成功'), 'first QR save must finish');
  const first = preserved.calls.filter((call) => call.method === 'PATCH')[0];
  const firstPayload = JSON.parse(first.body);
  assert.equal(firstPayload.qr_url, 'https://example.invalid/qr-v7', 'unrendered QR URL must retain the Catalog value');
  assert.equal(firstPayload.scene_value, 'state-v7', 'unrendered State must retain the Catalog value');
  assert.equal(firstPayload.overflow_policy, 'least_loaded', 'unrendered overflow policy must retain the Catalog value');
  assert.equal(firstPayload.qrcode_url, undefined, 'Catalog transport must only emit qr_url');
  document.querySelector('[name="channel_name"]').value = '第二个可见编辑';
  save.click();
  await waitFor(() => preserved.calls.filter((call) => call.method === 'PATCH').length === 2, 'second QR save must issue one PATCH');
  const second = preserved.calls.filter((call) => call.method === 'PATCH')[1];
  const secondPayload = JSON.parse(second.body);
  assert.equal(second.headers.get('If-Match'), '"8"', 'second save must use the accepted ETag');
  assert.equal(secondPayload.qr_url, 'https://example.invalid/qr-v8', 'second save must use the accepted QR URL baseline');
  assert.equal(secondPayload.scene_value, 'state-v8', 'second save must use the accepted State baseline');
  assert.equal(secondPayload.overflow_policy, 'next_available', 'second save must use the accepted overflow baseline');
} finally { preserved.dom.window.close(); }

// Switching to a link exposes customer_channel. Its deliberately empty value
// must clear State, and the link transition must not restore the former QR URL.
const switchedCarrier = createPage({ saved: initialHiddenFields });
try {
  await waitFor(() => switchedCarrier.dom.window.__channelComposerOptions, 'carrier switch interactions ready');
  const document = switchedCarrier.dom.window.document;
  document.querySelector('[data-channel-type-card="wecom_customer_acquisition"]').click();
  document.querySelector('[name="customer_channel"]').value = '';
  document.querySelector('[data-save-channel]').click();
  await waitFor(() => switchedCarrier.calls.filter((call) => call.method === 'PATCH').length === 1, 'carrier switch must issue one PATCH');
  const payload = JSON.parse(switchedCarrier.calls.find((call) => call.method === 'PATCH').body);
  assert.equal(payload.qr_url, '', 'carrier switch must keep its explicit QR clear');
  assert.equal(payload.scene_value, '', 'visible customer_channel clear must reach scene_value');
  assert.equal(payload.customer_channel, '', 'visible customer_channel clear must remain explicit');
  assert.equal(payload.overflow_policy, 'least_loaded', 'still-unrendered overflow policy must remain intact');
} finally { switchedCarrier.dom.window.close(); }

// The Host only preserves fields omitted by the frozen donor. A visible QR
// control (including a future donor variant) and direct Catalog callers keep
// explicit empty values, while the legacy qrcode_url input alias normalizes
// to Catalog's sole qr_url key.
const explicitClear = createPage({ saved: initialHiddenFields });
try {
  await waitFor(() => explicitClear.dom.window.__channelComposerOptions, 'explicit-clear interactions ready');
  const document = explicitClear.dom.window.document;
  const input = document.createElement('input'); input.name = 'qr_url'; input.value = '';
  document.querySelector('[data-channel-form]').append(input);
  document.querySelector('[data-save-channel]').click();
  await waitFor(() => explicitClear.calls.filter((call) => call.method === 'PATCH').length === 1, 'visible QR control save must issue one PATCH');
  assert.equal(JSON.parse(explicitClear.calls.find((call) => call.method === 'PATCH').body).qr_url, '', 'a visible QR control may explicitly clear its value');
  const direct = await explicitClear.dom.window.fetch('/api/admin/channels/17', { method: 'PATCH', headers: { 'If-Match': '"8"' }, body: JSON.stringify({ channel_code: 'origin-code', channel_name: '直接调用', qrcode_url: '', scene_value: '', overflow_policy: '', assignees: [] }) });
  assert.equal(direct.status, 200, 'direct Catalog callers remain accepted');
  const directPayload = JSON.parse(explicitClear.calls.filter((call) => call.method === 'PATCH')[1].body);
  assert.equal(directPayload.qr_url, '', 'qrcode_url alias must normalize to the sole Catalog key');
  assert.equal(directPayload.qrcode_url, undefined, 'legacy alias must not reach strict Catalog JSON');
  assert.equal(directPayload.scene_value, '', 'direct explicit State clear must not be restored');
  assert.equal(directPayload.overflow_policy, '', 'direct explicit overflow clear must not be restored');
} finally { explicitClear.dom.window.close(); }

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
  document.querySelector('[data-add-channel-assignee]').click();
  await waitFor(() => created.dom.window.__pickerOptions.disabledUserIds?.includes('wecom-alice'), 'existing local assignee IDs must disable the corresponding real WeCom ID in the shared picker');
  assert.equal(created.dom.window.__pickerOptions.context, 'channel_assignees', 'the channel Host must declare the shared picker business context');
  assert.deepEqual(JSON.parse(JSON.stringify(created.dom.window.__pickerOptions.selection)), { mode: 'multiple', max: 4 }, 'the channel Host must retain the donor remaining-capacity limit through the explicit multi-select contract');
  assert.equal(created.dom.window.__pickerOptions.multiple, true, 'the channel Host must retain the donor multi-select behavior');
  document.querySelector('[data-save-channel]').click();
  await waitFor(() => created.calls.some((call) => call.method === 'POST' && call.path === '/api/admin/channels'), 'new channel must submit one Catalog create');
  const post = created.calls.find((call) => call.method === 'POST' && call.path === '/api/admin/channels');
  assert.match(post.headers.get('Idempotency-Key'), /^channel-/, 'new channel must use a Catalog idempotency receipt');
  const payload = JSON.parse(post.body);
  assert.equal(payload.channel_name, '新渠道');
  assert.equal(payload.channel_code, 'new-channel');
  assert.deepEqual(payload.assignment_config_json.assignees.map((item) => item.staff_id), [12], 'a non-numeric WeCom user ID must retain its separate local staff ID through the frozen donor callback');
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

const saves = createPage({ mutations: [
  { headers: { ETag: '"8"' }, payload: { ok: true } },
  { headers: { ETag: '"9"' }, payload: { ok: true } },
  { status: 503, payload: { code: 'DEPENDENCY_UNAVAILABLE' } },
  { headers: { ETag: '"10"' }, payload: { ok: true } },
] });
try {
  await waitFor(() => saves.dom.window.__channelComposerOptions, 'edit interactions ready');
  const { document } = saves.dom.window;
  const code = document.querySelector('[name="channel_code"]');
  assert.equal(code.readOnly, true, 'persisted channel code cannot be edited');
  assert.match(document.getElementById('channel-code-fixed-hint').textContent, /创建后固定/);
  const send = (channelCode = 'origin-code') => saves.dom.window.fetch('/api/admin/channels/17', {
    method: 'PATCH', body: JSON.stringify({ channel_code: channelCode, channel_name: '多客服测试', assignees: [] }),
  });
  const immutable = await send('123334');
  assert.equal(immutable.status, 409);
  assert.equal((await immutable.json()).code, 'CHANNEL_CODE_IMMUTABLE');
  assert.equal(saves.calls.filter(call => call.method === 'PATCH').length, 0, 'changed code must not reach server');
  assert.equal((await send()).status, 200);
  assert.equal((await send()).status, 200);
  assert.equal((await send()).status, 503);
  assert.equal((await send()).status, 200);
  const patches = saves.calls.filter(call => call.method === 'PATCH');
  assert.deepEqual(patches.map(call => call.headers.get('If-Match')), ['"7"', '"8"', '"9"', '"9"']);
  assert.equal(patches[0].body, patches[1].body, 'same configuration may be saved again');
  assert.notEqual(patches[0].headers.get('Idempotency-Key'), patches[1].headers.get('Idempotency-Key'), 'new version needs a new command receipt');
  assert.equal(patches[2].headers.get('Idempotency-Key'), patches[3].headers.get('Idempotency-Key'), 'uncertain retry retains original command key');
} finally { saves.dom.window.close(); }

console.log('standard channel Host draft, carrier, CAS, create journeys: PASS');
