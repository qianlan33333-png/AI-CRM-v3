// V3 transport and bootstrap seam for the standard Channel Center form.
//
// OneID: not involved. A channel definition does not resolve or assign a
// customer identity. Persistence: local Catalog transaction using its
// server-owned version/ETag and idempotency receipt. External Effects: not
// involved here; showing an already-issued QR/link is a read-only projection.


type Json = Record<string, unknown>;
type Channel = Json & { id?: number; version?: number; config_version?: number };
type PreservedFormFields = { qrURL: string; sceneValue: string; overflowPolicy: string };

const nativeFetch = window.fetch.bind(window);
const mutationKeys = new Map<string, string>();
const detailEtags = new Map<string, string>();
const detailCodes = new Map<string, string>();
const detailPreservedFields = new Map<string, PreservedFormFields>();

function escapeHTML(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[character] || character));
}

function ids(value: unknown): string {
  return Array.isArray(value) ? value.map((item) => Number(item)).filter((item) => Number.isSafeInteger(item) && item > 0).join(',') : '';
}

function csrf(): string {
  return document.cookie.split(';').map((part) => part.trim()).map((part) => part.split('='))
    .find(([name]) => name === 'aicrm_csrf' || name === 'aicrm_admin_csrf')?.slice(1).join('=') || '';
}

function key(): string {
  if (globalThis.crypto?.randomUUID) return `channel-${globalThis.crypto.randomUUID()}`;
  return `channel-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), location.origin);
  if (typeof input === 'string') return new URL(input, location.origin);
  return new URL(input.url, location.origin);
}

function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  return String(init?.method || (typeof input === 'string' || input instanceof URL ? 'GET' : input.method)).toUpperCase();
}

function bodyText(input: RequestInfo | URL, init?: RequestInit): string {
  if (typeof init?.body === 'string') return init.body;
  if (typeof input !== 'string' && !(input instanceof URL) && typeof input.body === 'string') return input.body;
  return '';
}

function catalogMutation(url: URL, method: string): boolean {
  return method === 'POST' && url.pathname === '/api/admin/channels' ||
    method === 'PATCH' && /^\/api\/admin\/channels\/[1-9][0-9]*$/.test(url.pathname);
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function has(payload: Json, name: string): boolean {
  return Object.prototype.hasOwnProperty.call(payload, name);
}

function preserveFields(channel: Json): PreservedFormFields {
  return {
    qrURL: has(channel, 'qr_url') ? text(channel.qr_url) : text(channel.qrcode_url),
    sceneValue: text(channel.scene_value),
    overflowPolicy: text(channel.overflow_policy),
  };
}

function rememberDetail(url: URL, channel: Json): void {
  if (!/^\/api\/admin\/channels\/[1-9][0-9]*$/.test(url.pathname)) return;
  detailPreservedFields.set(url.pathname, preserveFields(channel));
}

function visibleFormControl(names: string[]): boolean {
  return names.some((name) => {
    const field = document.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[name="${name}"]`);
    if (!field || field.disabled || field instanceof HTMLInputElement && field.type === 'hidden' || 'readOnly' in field && field.readOnly) return false;
    for (let node: HTMLElement | null = field; node; node = node.parentElement) {
      if (node.hidden) return false;
    }
    return true;
  });
}

function preserveUnrenderedDonorFields(url: URL, payload: Json): void {
  const current = detailPreservedFields.get(url.pathname);
  if (!current) return;
  const isLink = payload.channel_type === 'wecom_customer_acquisition' || payload.carrier_type === 'link';
  if (!isLink && !visibleFormControl(['qr_url', 'qrcode_url'])) payload.qr_url = current.qrURL;
  if (!visibleFormControl(['scene_value', 'customer_channel'])) payload.scene_value = current.sceneValue;
  if (!visibleFormControl(['overflow_policy'])) payload.overflow_policy = current.overflowPolicy;
}

function normalizePayload(raw: string, url: URL): string {
  const payload = JSON.parse(raw || '{}') as Json;
  const standardDonorPayload = has(payload, 'admin_action_token');
  // The standard donor still emits its obsolete action token. V3 uses the
  // authenticated session and X-CSRF-Token; strict Catalog JSON rejects it.
  delete payload.admin_action_token;
  if (!has(payload, 'qr_url') && typeof payload.qrcode_url === 'string') payload.qr_url = payload.qrcode_url;
  delete payload.qrcode_url;
  if (standardDonorPayload) preserveUnrenderedDonorFields(url, payload);
  const assignees = Array.isArray(payload.assignees) ? payload.assignees : [];
  if ('assignees' in payload) delete payload.assignees;
  payload.assignment_config_json = {
    assignees: assignees.flatMap((value, index) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
      const item = value as Json;
      const staffID = Number(item.staff_id);
      if (!Number.isSafeInteger(staffID) || staffID < 1) return [];
      return [{
        staff_id: staffID,
        priority: Number(item.priority) || index + 1,
        ratio_percent: Number(item.ratio_percent) || 0,
        max_scans_24h: Number(item.max_scans_24h) || 0,
      }];
    }),
  };
  return JSON.stringify(payload);
}

async function catalogError(response: Response): Promise<Response> {
  if (response.ok) return response;
  const source = await response.clone().json().catch(() => ({})) as Json;
  const code = typeof source.code === 'string' ? source.code : '';
  const messages: Record<string, string> = {
    MALFORMED_REQUEST: '渠道保存数据不符合要求，请检查名称、编码、客服和标签后重试。',
    FORBIDDEN: '当前账号没有保存渠道的权限，请联系管理员。',
    UNAUTHORIZED: '登录已失效，请重新登录后保存。',
    CHANNEL_CODE_CONFLICT: '渠道编码已被使用，请更换编码后保存。',
  };
  const message = response.status === 409
    ? '渠道配置或编码发生冲突；当前草稿已保留。请重新读取最新配置后核对再保存。'
    : messages[code] || `渠道保存失败（HTTP ${response.status}），当前草稿已保留，请稍后重试。`;
  const headers = new Headers(response.headers);
  headers.set('Content-Type', 'application/json');
  headers.delete('Content-Length');
  return new Response(JSON.stringify({ ok: false, code: response.status === 409 ? 'CHANNEL_VERSION_CONFLICT' : code, message }),
    { status: response.status, statusText: response.statusText, headers });
}

function installErrorFormatter(): void {
  const previous = (window as Window & { AdminApi?: Json }).AdminApi?.formatErrorValue;
  const adminAPI = ((window as Window & { AdminApi?: Json }).AdminApi ||= {});
  adminAPI.formatErrorValue = (value: unknown): string => {
    const source = value && typeof value === 'object' ? value as Json : {};
    if (source.code === 'CHANNEL_VERSION_CONFLICT') return String(source.message);
    if (typeof source.message === 'string' && source.message) return source.message;
    if (typeof previous === 'function') return String(previous(value) || '');
    return typeof source.message === 'string' ? source.message : '';
  };
}

async function reportDirectoryReadState(response: Response, url: URL, method: string): Promise<Response> {
  if (method !== 'GET' || url.pathname !== '/api/admin/common/operation-members' || url.searchParams.get('scope') !== 'channel_code' || !response.ok) return response;
  const payload = await response.clone().json().catch(() => null) as Json | null;
  if (payload?.profile_read_state !== 'unavailable' || document.getElementById('channel-directory-read-notice')) return response;
  const notice = document.createElement('div');
  notice.id = 'channel-directory-read-notice';
  notice.setAttribute('role', 'status');
  notice.className = 'save-feedback is-error';
  notice.textContent = '企微客服姓名目录暂不可用；已保留上次可用姓名，请稍后刷新后再核对。';
  document.querySelector('[data-channel-admission-page]')?.prepend(notice);
  return response;
}

function installCatalogTransport(): void {
  installErrorFormatter();
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = requestURL(input);
    const method = requestMethod(input, init);
    if (url.origin !== location.origin || !catalogMutation(url, method)) return reportDirectoryReadState(await nativeFetch(input, init), url, method);

    let body: string;
    try { body = normalizePayload(bodyText(input, init), url); }
    catch { return new Response(JSON.stringify({ ok: false, code: 'MALFORMED_REQUEST', message: '渠道保存数据无效，请检查后重试。' }), { status: 400, headers: { 'Content-Type': 'application/json' } }); }
    const headers = new Headers(init?.headers || (typeof input === 'string' || input instanceof URL ? undefined : input.headers));
    headers.set('Accept', 'application/json');
    headers.set('Content-Type', 'application/json');
    const token = csrf();
    if (token) headers.set('X-CSRF-Token', token);
    if (method === 'PATCH' && detailCodes.has(url.pathname) && JSON.parse(body).channel_code !== detailCodes.get(url.pathname)) {
      return new Response(JSON.stringify({ ok: false, code: 'CHANNEL_CODE_IMMUTABLE', message: '已有渠道编码不可修改；请恢复原编码后保存其他配置。' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
    }
    if (method === 'PATCH' && !headers.has('If-Match')) {
      const etag = detailEtags.get(url.pathname);
      if (!etag) return new Response(JSON.stringify({ ok: false, code: 'CHANNEL_VERSION_UNAVAILABLE', message: '未取得打开此渠道时的版本，当前草稿未保存。请刷新后手动合并再保存。' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      headers.set('If-Match', etag);
    }
    // Expected version participates in the server receipt payload. A later
    // save at a new version is a new command; uncertain retries keep its key.
    const fingerprint = `${method}:${url.pathname}:${headers.get('If-Match') || ''}:${body}`;
    headers.set('Idempotency-Key', mutationKeys.get(fingerprint) || key());
    mutationKeys.set(fingerprint, headers.get('Idempotency-Key') || '');
    const response = await nativeFetch(url, { ...init, method, body, headers, credentials: 'same-origin' });
    if (method === 'PATCH' && response.ok) {
      const etag = response.headers.get('ETag'); if (etag) detailEtags.set(url.pathname, etag);
      const result = await response.clone().json().catch(() => null) as Json | null;
      if (result?.channel && typeof result.channel === 'object' && !Array.isArray(result.channel)) rememberDetail(url, result.channel as Json);
    }
    return catalogError(response);
  };
}

function assignment(channel: Json): Json[] {
  const config = channel.assignment_config_json;
  const values = config && typeof config === 'object' && !Array.isArray(config) && Array.isArray((config as Json).assignees)
    ? (config as Json).assignees as Json[] : [];
  return values.map((item) => ({ ...item, display_name: item.display_name || `客服 #${item.staff_id}`, status: 'active' }));
}

function channelBootstrap(channel: Channel | null): Json {
  const safe = channel || {};
  const id = Number(safe.id);
  return {
    is_edit: Number.isSafeInteger(id) && id > 0,
    channel: { ...safe, assignees: assignment(safe) },
    api_urls: {
      channels: '/api/admin/channels',
      detail: Number.isSafeInteger(id) && id > 0 ? `/api/admin/channels/${id}` : '',
      qrcode_download: typeof safe.qr_download_url === 'string' ? safe.qr_download_url : '',
      wecom_tags: '/api/admin/wecom/tags',
    },
  };
}

const standardChannelFormURL = '/assets/standard-components/channel_code_form.html';
const standardChannelScriptURL = '/assets/standard-components/channel_admission_pages.js';

// The stored standard template stays intact. The V3 Host renders only its
// documented Jinja branches against the V3 bootstrap, then supplies scoped
// input values. It never recreates the component markup or alters the donor
// interaction script.
function renderDonorBranches(source: string, condition: (value: string) => boolean): string {
  const tokens = source.split(/(\{%\s*(?:if\b[^%]*|else|endif)\s*%\})/g);
  let cursor = 0;
  const render = (stop: Set<string>): { content: string; stop: string } => {
    let content = '';
    while (cursor < tokens.length) {
      const token = tokens[cursor++];
      const match = token.match(/^\{%\s*(if\b(.*?)|else|endif)\s*%\}$/);
      if (!match) { content += token; continue; }
      const directive = match[1];
      if (directive === 'else' || directive === 'endif') {
        if (stop.has(directive)) return { content, stop: directive };
        continue;
      }
      const yes = render(new Set(['else', 'endif']));
      let no = '';
      if (yes.stop === 'else') {
        const alternate = render(new Set(['endif']));
        no = alternate.content;
      }
      content += condition(match[2] || '') ? yes.content : no;
    }
    return { content, stop: '' };
  };
  return render(new Set()).content;
}

function renderDonorValues(source: string, channel: Channel | null): string {
  const safe = channel || {}; const isEdit = Boolean(safe.id);
  const isLink = safe.channel_type === 'wecom_customer_acquisition' || safe.carrier_type === 'link';
  const status = String(safe.status || 'active');
  const assigneeCount = assignment(safe).length;
  const value = (expression: string): string => {
    const normalized = expression.trim();
    const values: Record<string, unknown> = {
      "'1' if payload.is_edit else '0'": isEdit ? '1' : '0', 'payload.api_urls.channels': '/api/admin/channels', 'payload.api_urls.detail': isEdit ? `/api/admin/channels/${safe.id}` : '', 'payload.api_urls.qrcode_download': safe.qr_download_url || '', 'admin_action_token': '',
      '"编辑渠道" if payload.is_edit else "新建渠道"': isEdit ? '编辑渠道' : '新建渠道', 'channel.channel_name or "未命名渠道"': safe.channel_name || '未命名渠道', '"渠道获客链接" if is_link else "普通二维码"': isLink ? '渠道获客链接' : '普通二维码',
      'channel.channel_contact_count or 0': safe.channel_contact_count || 0, 'assignee_count': assigneeCount, 'channel.channel_name or \'\'': safe.channel_name || '', 'channel.channel_code or \'\'': safe.channel_code || '',
      'channel.customer_channel or channel.scene_value or \'\'': safe.customer_channel || safe.scene_value || '', 'channel.link_url or \'\'': safe.link_url || '', 'channel.final_url or channel.share_url or \'\'': safe.final_url || safe.share_url || '',
      'channel.owner_staff_id or \'\'': safe.owner_staff_id || '', 'channel.welcome_message or \'\'': safe.welcome_message || '', 'channel.entry_tag_id or \'\'': safe.entry_tag_id || '', 'channel.entry_tag_name': safe.entry_tag_name || '', 'channel.entry_tag_group_name or \'标签\'': safe.entry_tag_group_name || '标签',
      'channel.historical_scene_values | join(", ")': Array.isArray(safe.historical_scene_values) ? safe.historical_scene_values.join(', ') : '',
    };
    if (normalized === '(channel.status or "active") == "active"') return status === 'active' ? '启用' : status === 'inactive' ? '停用' : status === 'archived' ? '归档' : status;
    const arrayMatch = normalized.match(/^\(channel\.(welcome_(?:image|miniprogram|attachment|group_invite)_library_ids) or \[\]\) \| join\(','\)$/);
    if (arrayMatch) return ids(safe[arrayMatch[1]]);
    if (normalized === "url_for('api.admin_channels_page')") return '/admin/channels';
    return Object.prototype.hasOwnProperty.call(values, normalized) ? String(values[normalized] ?? '') : '';
  };
  return source.replace(/\{\{([\s\S]*?)\}\}/g, (_all, expression) => escapeHTML(value(expression)));
}

async function channelFormMarkup(channel: Channel | null): Promise<string> {
  const response = await nativeFetch(standardChannelFormURL, { credentials: 'same-origin' });
  if (!response.ok) throw new Error('标准渠道表单加载失败，请刷新页面后重试。');
  const raw = await response.text(); const start = raw.indexOf('{% block content %}'); const end = raw.indexOf('{% endblock %}', start);
  if (start < 0 || end < 0) throw new Error('标准渠道表单资源不完整');
  let content = raw.slice(start + '{% block content %}'.length, end);
  const status = String(channel?.status || 'active'); const isEdit = Boolean(channel?.id); const isLink = channel?.channel_type === 'wecom_customer_acquisition' || channel?.carrier_type === 'link';
  const condition = (value: string): boolean => ({
    'payload.is_edit': isEdit, 'payload.is_edit and channel.historical_scene_values': isEdit && Array.isArray(channel?.historical_scene_values) && channel.historical_scene_values.length > 0,
    'channel.auto_accept_friend': Boolean(channel?.auto_accept_friend), 'channel.qr_download_url': Boolean(channel?.qr_download_url), 'channel.entry_tag_name': Boolean(channel?.entry_tag_name), 'is_link': isLink, 'not is_link': !isLink,
    "(channel.status or 'active') == 'active'": status === 'active', "channel.status == 'inactive'": status === 'inactive', "channel.status == 'archived'": status === 'archived',
  }[value.trim()] || false);
  // Nested conditionals are rendered with a small stack parser, rather than
  // a broad regex that can keep both donor branches in the DOM.
  return renderDonorValues(renderDonorBranches(content, condition).replace(/\{%\s*set\s+[^%]*%\}/g, ''), channel);
}
function setField(root: HTMLElement, selector: string, value: unknown): void {
  const field = root.querySelector<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(selector); if (field) field.value = String(value ?? '');
}
function hydrateChannelDonor(root: HTMLElement, channel: Channel | null): void {
  const source = channel || {}; const id = Number(source.id); const isEdit = Number.isSafeInteger(id) && id > 0; const isLink = source.channel_type === 'wecom_customer_acquisition' || source.carrier_type === 'link'; const bootstrap = channelBootstrap(channel);
  root.dataset.isEdit = isEdit ? '1' : '0'; root.dataset.apiCreate = '/api/admin/channels'; root.dataset.apiDetail = isEdit ? `/api/admin/channels/${id}` : ''; root.dataset.apiQrcodeDownload = typeof source.qr_download_url === 'string' ? source.qr_download_url : '';
  root.querySelector('[data-channel-bootstrap]')?.remove();
  const bootstrapNode = document.createElement('script'); bootstrapNode.type = 'application/json'; bootstrapNode.dataset.channelBootstrap = ''; bootstrapNode.textContent = JSON.stringify(bootstrap).replace(/</g, '\\u003c'); root.prepend(bootstrapNode);
  setField(root, '[name="channel_name"]', source.channel_name); setField(root, '[name="channel_code"]', source.channel_code); setField(root, '[name="status"]', source.status || 'active'); setField(root, '[name="customer_channel"]', source.customer_channel || source.scene_value); setField(root, '[name="link_url"]', source.link_url); setField(root, '[name="final_url"]', source.final_url || source.share_url); setField(root, '[name="owner_staff_id"]', source.owner_staff_id); setField(root, '[data-welcome-message]', source.welcome_message); setField(root, '[data-image-ids]', ids(source.welcome_image_library_ids)); setField(root, '[data-miniprogram-ids]', ids(source.welcome_miniprogram_library_ids)); setField(root, '[data-attachment-ids]', ids(source.welcome_attachment_library_ids)); setField(root, '[data-group-invite-ids]', ids(source.welcome_group_invite_library_ids)); setField(root, '[data-entry-tag-id]', source.entry_tag_id); setField(root, '[data-entry-tag-name]', source.entry_tag_name); setField(root, '[data-entry-tag-group-name]', source.entry_tag_group_name);
  const type = root.querySelector<HTMLInputElement>(`[name="channel_type"][value="${isLink ? 'wecom_customer_acquisition' : 'qrcode'}"]`); if (type) type.checked = true;
  const auto = root.querySelector<HTMLInputElement>('[name="auto_accept_friend"]'); if (auto) auto.checked = Boolean(source.auto_accept_friend);
  root.querySelectorAll<HTMLElement>('[data-historical-scene-values]').forEach((node) => { node.hidden = !Array.isArray(source.historical_scene_values) || source.historical_scene_values.length === 0; });
}
async function executeChannelDonorScript(): Promise<void> {
  // Load the byte-preserved donor IIFE as a same-origin resource. This keeps
  // production CSP intact: no inline script and no unsafe-eval are required.
  await new Promise<void>((resolve, reject) => {
    const script = document.createElement('script'); script.src = standardChannelScriptURL; script.defer = false; script.dataset.aicrmChannelDonor = '';
    script.onload = () => resolve(); script.onerror = () => reject(new Error('标准渠道交互脚本加载失败，请刷新页面后重试。')); document.head.append(script);
  });
}

async function currentChannel(): Promise<Channel | null> {
  const id = document.body.dataset.channelResourceId || new URLSearchParams(location.search).get('id') || '';
  if (!/^[1-9][0-9]*$/.test(id)) return null;
  const response = await nativeFetch(`/api/admin/channels/${id}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`渠道读取失败（HTTP ${response.status}）`);
  const payload = await response.json() as Json;
  const channel = payload.channel && typeof payload.channel === 'object' ? payload.channel as Channel : null;
  const etag = response.headers.get('ETag'); if (channel && etag) detailEtags.set(`/api/admin/channels/${id}`, etag);
  if (channel && typeof channel.channel_code === 'string') detailCodes.set(`/api/admin/channels/${id}`, channel.channel_code);
  if (channel) rememberDetail(new URL(`/api/admin/channels/${id}`, location.origin), channel);
  return channel;
}

// The frozen donor uses user_id as its persisted staff key. Keep the shared
// picker payload untouched for display, and adapt only its channel callback.
function installChannelPickerIdentityAdapter(): void {
  type PickerOptions = Json & { onConfirm?: (members: Json[]) => void };
  const picker = (window as Window & { OperationMemberPicker?: { open: (options: PickerOptions) => unknown } }).OperationMemberPicker;
  if (!picker) return;
  const open = picker.open.bind(picker);
  picker.open = async (options) => {
    if (options.scope !== 'channel_code' || typeof options.onConfirm !== 'function') return open(options);
    const confirm = options.onConfirm;
    const disabled = Array.isArray(options.disabledUserIds) ? options.disabledUserIds.map(String) : [];
    let disabledUserIds: string[] = [];
    if (disabled.length) {
      const response = await nativeFetch('/api/admin/common/operation-members?scope=channel_code&page_size=100', { credentials: 'same-origin', headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error('客服目录读取失败，请重试');
      const payload = await response.json() as Json;
      if (!Array.isArray(payload.items)) throw new Error('客服目录响应不完整，请重试');
      disabledUserIds = payload.items.flatMap((member: Json) => disabled.includes(String(member.staff_id)) && member.user_id ? [String(member.user_id)] : []);
    }
    return open({ ...options, disabledUserIds, onConfirm: (members) => {
      const mapped = members.map((member) => {
        const staffID = Number(member.staff_id);
        if (!Number.isSafeInteger(staffID) || staffID < 1) throw new Error('客服本地标识缺失，请刷新客服后重试');
        return { ...member, user_id: String(staffID) };
      });
      confirm(mapped);
    } });
  };
}

export async function startChannelAdmissionHost(): Promise<void> {
  installCatalogTransport();
  try {
    const channel = await currentChannel();
    const mount = document.querySelector('main') || document.body;
    mount.innerHTML = await channelFormMarkup(channel);
    const root = mount.querySelector<HTMLElement>('[data-channel-admission-page]'); if (!root) throw new Error('标准渠道表单挂载失败');
    hydrateChannelDonor(root, channel);
    if (channel) {
      const codeInput = root.querySelector<HTMLInputElement>('[name="channel_code"]');
      if (codeInput) {
        codeInput.readOnly = true;
        codeInput.setAttribute('aria-describedby', 'channel-code-fixed-hint');
        const hint = document.createElement('div');
        hint.id = 'channel-code-fixed-hint';
        hint.className = 'form-text';
        hint.textContent = '编码创建后固定，用于识别渠道；名称及其他配置可继续修改。';
        codeInput.insertAdjacentElement('afterend', hint);
      }
    }
    await (window as Window & { AICRMStandardComponents?: { ready?: () => Promise<void> } }).AICRMStandardComponents?.ready?.();
    installChannelPickerIdentityAdapter();
    await executeChannelDonorScript();
  } catch (error) {
    const message = error instanceof Error ? error.message : '渠道读取失败';
    (document.querySelector('main') || document.body).innerHTML = `<div role="alert" style="margin:24px;color:#b42318">${escapeHTML(message)}；未修改当前配置，请刷新后重试。</div>`;
  }
}
