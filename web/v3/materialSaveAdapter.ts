// V3 feedback overlay for byte-frozen media forms.  The adapter does not own
// media writes; it makes the existing request/result boundary observable and
// prevents a second click while that write or its mandatory readback is live.

export {};
import { clearActionBusy, setActionBusy } from './actionFeedback';
import { formatShanghaiDateTime } from './adminDateTime';

type SaveState = {
  button: HTMLButtonElement;
  label: string;
  mutationStarted: boolean;
  mutationAccepted: boolean;
  idempotencyKey?: string;
  mutationFingerprint?: string;
  mutationPath?: string;
  mutationMethod?: string;
  pendingPreflight: number;
  readbackStarted: boolean;
  pendingReadbacks: number;
};

let activeSave: SaveState | undefined;
let suppressNextMaterialRejection = false;
let pendingUnknownMutation: { page: string; path: string; method: string; fingerprint: string; key: string; saved: boolean } | undefined;
const donorFetch = globalThis.fetch.bind(globalThis);

// Keep file selection feedback next to the control: transient global toasts
// disappear before users finish entering the material name and tags.
function installFileFeedback(): void {
  if (typeof document === 'undefined' || !document.body) return;
  if (!['images', 'attach'].includes(document.body.dataset.page || '')) return;
  for (const input of document.querySelectorAll<HTMLInputElement>('#fImgUpFile, #fAttUpFile')) {
    if (input.dataset.materialFileFeedback) continue;
    input.dataset.materialFileFeedback = 'true';
    const image = input.id === 'fImgUpFile';
    input.accept = image ? 'image/png,image/jpeg,image/gif' : 'application/pdf';
    input.setAttribute('aria-label', image ? '选择图片文件' : '选择 PDF 文件');
    const status = document.createElement('p');
    status.id = `${input.id}-status`;
    status.setAttribute('role', 'status');
    status.style.cssText = 'margin:8px 0 0;color:#646A73;font-size:12px;line-height:1.5';
    input.setAttribute('aria-describedby', status.id);
    input.after(status);
    const describe = (cancelled = false) => {
      const file = input.files?.[0];
      status.textContent = file
        ? `已选择 ${file.name}（${Math.max(1, Math.ceil(file.size / 1024))} KB），点击上传后保存。`
        : `${cancelled ? '已取消选择。' : ''}${image ? '请选择 PNG、JPEG 或 GIF 图片' : '请选择 PDF 附件'}，最大 10 MB。`;
    };
    input.addEventListener('change', () => describe());
    input.addEventListener('cancel', () => describe(true));
    describe();
  }
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', installFileFeedback);
else installFileFeedback();
new MutationObserver(installFileFeedback).observe(document.documentElement, { childList: true, subtree: true });

function isMediaMutation(url: URL, method: string): boolean {
  if (method === 'GET' || method === 'HEAD') return false;
  return url.pathname.startsWith('/api/admin/image-library') ||
    url.pathname.startsWith('/api/admin/miniprogram-library') ||
    url.pathname.startsWith('/api/admin/attachment-library');
}

function isMediaReadback(url: URL, method: string): boolean {
  if (method !== 'GET') return false;
  return url.pathname === '/api/admin/image-library' ||
    url.pathname === '/api/admin/miniprogram-library' ||
    url.pathname === '/api/admin/attachment-library';
}

function message(text: string): void {
  document.getElementById('material-v3-save-message')?.remove();
  const node = document.createElement('div');
  node.id = 'material-v3-save-message';
  node.setAttribute('role', 'alert');
  node.textContent = text;
  node.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  document.body.appendChild(node);
  window.setTimeout(() => node.remove(), 5000);
}

function release(state: SaveState): void {
  if (activeSave !== state) return;
  clearActionBusy(state.button);
  activeSave = undefined;
}

function failMutation(state: SaveState): void {
  if (activeSave !== state) return;
  // The byte-frozen controller currently leaves these promise rejections
  // uncaught. It may gain local handling later, so reset at the actual HTTP
  // failure instead of waiting for a global unhandled-rejection event.
  suppressNextMaterialRejection = true;
  release(state);
  message('素材保存失败；编辑内容已保留，可修正后重试。');
}

function failReadback(state: SaveState): void {
  if (activeSave !== state) return;
  if (state.idempotencyKey && state.mutationFingerprint != null && state.mutationPath && state.mutationMethod) {
    pendingUnknownMutation = { page: document.body.dataset.page || '', path: state.mutationPath, method: state.mutationMethod, fingerprint: state.mutationFingerprint, key: state.idempotencyKey, saved: true };
  }
  release(state);
  message('素材已保存，但回读失败；编辑内容已保留，可刷新后核对。');
}

function finishReadback(state: SaveState): void {
  queueMicrotask(() => {
    if (activeSave === state && state.readbackStarted && state.pendingReadbacks === 0) {
      if (pendingUnknownMutation?.key === state.idempotencyKey) pendingUnknownMutation = undefined;
      release(state);
    }
  });
}

function mutationFingerprint(input: RequestInfo | URL, init: RequestInit | undefined): string {
  const request = input instanceof Request ? input : undefined;
  const body = init?.body ?? request?.body;
  if (typeof body === 'string') return body;
  if (body instanceof URLSearchParams) return body.toString();
  if (body instanceof FormData) return [...body.entries()].map(([key, value]) => `${key}=${typeof value === 'string' ? value : `${value.name}:${value.size}:${value.type}:${value.lastModified}`}`).join('&');
  return body == null ? '' : String(body);
}

function idempotencyKey(state: SaveState, url: URL, method: string, input: RequestInfo | URL, init?: RequestInit): string {
  const fingerprint = mutationFingerprint(input, init);
  const retry = pendingUnknownMutation;
  if (retry && retry.page === document.body.dataset.page && retry.path === url.pathname && retry.method === method && retry.fingerprint === fingerprint) {
    state.mutationFingerprint = fingerprint;
    state.idempotencyKey = retry.key;
    return retry.key;
  }
  const key = `material-save-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`}`;
  state.mutationFingerprint = fingerprint;
  state.idempotencyKey = key;
  return key;
}

function unknownMutation(state: SaveState, url: URL, method: string): void {
  if (activeSave !== state || !state.idempotencyKey || state.mutationFingerprint == null) return;
  pendingUnknownMutation = { page: document.body.dataset.page || '', path: url.pathname, method, fingerprint: state.mutationFingerprint, key: state.idempotencyKey, saved: false };
  suppressNextMaterialRejection = true;
  release(state);
  message('素材保存结果未知；请在当前页面保持内容不变后重试，或先查看列表核对。');
}

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : undefined;
  const method = (init?.method || request?.method || 'GET').toUpperCase();
  const url = new URL(request?.url || String(input), location.origin);
  const state = activeSave;
  const mutation = Boolean(state && isMediaMutation(url, method));
  const readback = Boolean(state && state.mutationAccepted && isMediaReadback(url, method));
  const preflight = Boolean(state && !state.mutationStarted && !state.mutationAccepted && isMediaReadback(url, method));
  if (mutation && state) state.mutationStarted = true;
  if (mutation && state) { state.mutationPath = url.pathname; state.mutationMethod = method; }
  if (readback && state) {
    state.readbackStarted = true;
    state.pendingReadbacks += 1;
  }
  if (preflight && state) state.pendingPreflight += 1;
  let nextInit = init;
  if (mutation && state) {
    const fingerprint = mutationFingerprint(input, init);
    const previous = pendingUnknownMutation;
    if (previous && previous.page === (document.body.dataset.page || '') && previous.path === url.pathname && previous.method === method && previous.fingerprint !== fingerprint) {
      suppressNextMaterialRejection = true;
      release(state);
      message(previous.saved ? '素材已保存但列表回读失败；请先刷新或查看列表核对，不能直接修改后再次创建。' : '素材保存结果未知；请保持内容不变后重试，或先查看列表核对。');
      return new Response(JSON.stringify({ code: 'material_save_recovery_required' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
    }
    const headers = new Headers(request?.headers);
    new Headers(init?.headers).forEach((value, name) => headers.set(name, value));
    headers.set('Idempotency-Key', idempotencyKey(state, url, method, input, init));
    nextInit = { ...init, headers };
  }
  try {
    const response = await donorFetch(input, nextInit);
    if (mutation && state) {
      if (response.ok) {
        state.mutationAccepted = true;
        pendingUnknownMutation = undefined;
      }
      else failMutation(state);
    }
    if (readback && state) {
      if (!response.ok) failReadback(state);
      else {
        state.pendingReadbacks -= 1;
        finishReadback(state);
      }
    }
    if (preflight && state) {
      if (!response.ok) failMutation(state);
      else state.pendingPreflight -= 1;
    }
    return response;
  } catch (error) {
    if (mutation && state) unknownMutation(state, url, method);
    if (preflight && state) failMutation(state);
    if (readback && state) failReadback(state);
    throw error;
  }
};

document.addEventListener('click', (event) => {
  const page = document.body.dataset.page;
  if (page !== 'images' && page !== 'mpLib' && page !== 'attach') return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest('button');
  if (!button || !['保存', '创建', '上传'].includes(button.textContent?.trim() || '')) return;
  if (activeSave) {
    event.preventDefault();
    event.stopImmediatePropagation();
    return;
  }
  const state: SaveState = { button, label: button.textContent.trim(), mutationStarted: false, mutationAccepted: false, pendingPreflight: 0, readbackStarted: false, pendingReadbacks: 0 };
  activeSave = state;
  setActionBusy(button, '保存中…');
  // Validation may reject before it sends a request. That is not a save
  // failure and must leave the editor usable with its values intact. A
  // microtask runs after the frozen click handler, without creating a
  // time-based completion boundary for a real write. A resource-ID lookup
  // belongs to a real edit chain and remains locked until its PUT follows.
  queueMicrotask(() => { if (!state.mutationStarted && state.pendingPreflight === 0) release(state); });
}, true);

window.addEventListener('unhandledrejection', (event) => {
  const state = activeSave;
  if (!state) {
    if (suppressNextMaterialRejection) {
      suppressNextMaterialRejection = false;
      event.preventDefault();
    }
    return;
  }
  event.preventDefault();
  if (state.mutationAccepted) failReadback(state);
  else failMutation(state);
});

type MaterialProjection = Record<string, unknown> & {
  source_ref?: string;
  source_type?: string;
  state?: string;
  credential_state?: string;
  credential_usable?: boolean;
  media_id?: string;
  expires_at?: string;
};
type MaterialSourceFailureProjection = Record<string, unknown> & {
  source_ref?: string;
  failure_code?: string;
  credential_state?: string;
  credential_usable?: boolean;
};
type RefreshRoundProjection = Record<string, unknown> & {
  id?: number | string;
  state?: string;
};

const materialRefreshBase = '/api/admin/media-preparations';
const materialRefreshPages = new Set(['images', 'mpLib', 'attach']);
const mediaContentChangedEvent = 'aicrm:media-content-changed';

function materialObject(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function materialString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function materialNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

function materialRefreshKey(scope: string): string {
  return `media-refresh-${scope}-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`}`;
}

function materialCsrf(): string {
  for (const part of document.cookie.split(';')) {
    const [name, ...value] = part.trim().split('=');
    if (name === 'aicrm_csrf' || name === 'aicrm_admin_csrf')
      return decodeURIComponent(value.join('='));
  }
  return '';
}

class MaterialUIError extends Error {}

function materialError(status: number, body: Record<string, unknown>): MaterialUIError {
  const code = materialString(body.code);
  const known = ({
    idempotency_conflict: '本次刷新与已提交操作不一致，请重新读取后重试。',
    not_found: '刷新对象不存在，请重新读取刷新状态。',
    unavailable: '刷新服务暂不可用，请稍后重试。',
  } as Record<string, string>)[code];
  if (known) return new MaterialUIError(known);
  if (status === 401) return new MaterialUIError('登录状态已失效，请重新登录后继续操作。');
  if (status === 403) return new MaterialUIError('没有此操作权限。');
  if (status === 404) return new MaterialUIError('刷新对象不存在，请重新读取刷新状态。');
  if (status === 409) return new MaterialUIError('刷新状态已变化，请重新读取后重试。');
  if (status === 400 || status === 405 || status === 422) return new MaterialUIError('刷新请求无效，请检查后重试。');
  if (status >= 500) return new MaterialUIError('刷新服务暂不可用，请稍后重试。');
  return new MaterialUIError('刷新请求失败，请稍后重试。');
}

function materialFailureText(error: unknown, fallback: string): string {
  return error instanceof MaterialUIError ? error.message : fallback;
}

async function materialResponse(response: Response): Promise<Record<string, unknown>> {
  const body = materialObject(await response.json().catch(() => ({})));
  if (!response.ok || body.ok === false) throw materialError(response.status, body);
  return body;
}

async function materialGet(path: string): Promise<Record<string, unknown>> {
  const response = await fetch(path, {
    method: 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });
  return materialResponse(response);
}

async function materialPost(path: string, body: Record<string, unknown>, idempotencyKey: string): Promise<Record<string, unknown>> {
  const response = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-CSRF-Token': materialCsrf(),
      'Idempotency-Key': idempotencyKey,
    },
    body: JSON.stringify(body),
  });
  return materialResponse(response);
}

function materialFormatTime(value: unknown): string {
  const raw = materialString(value);
  if (!raw) return '—';
  const formatted = formatShanghaiDateTime(raw);
  return formatted === '未提供' ? '时间暂不可用' : formatted;
}

function materialTypeLabel(value: unknown): string {
  switch (materialString(value)) {
    case 'image': return '图片';
    case 'file':
    case 'attachment': return '附件';
    case 'miniprogram':
    case 'mini_program':
    case 'miniprogram_cover': return '小程序封面';
    default: return '素材';
  }
}

function materialStateLabel(value: unknown): string {
  switch (materialString(value)) {
    case 'missing': return '待首次刷新';
    case 'accepted':
    case 'queued':
    case 'running': return '刷新中';
    case 'ready':
    case 'executed':
    case 'succeeded':
    case 'completed': return '已就绪';
    case 'retryable_failed':
    case 'failed':
    case 'final_failed':
    case 'completed_with_failures': return '刷新失败';
    case 'outcome_unknown':
    case 'unknown': return '结果待核实';
    default: return '状态暂不可用';
  }
}

type MaterialCredentialState = 'ready' | 'expired' | 'missing' | 'unavailable';

function materialCredentialState(item: MaterialProjection): MaterialCredentialState {
  const expiresAt = materialString(item.expires_at);
  if (expiresAt) {
    const expiry = new Date(expiresAt);
    if (!Number.isNaN(expiry.getTime()) && expiry.getTime() <= Date.now()) return 'expired';
  }
  switch (materialString(item.credential_state)) {
    case 'ready': return item.credential_usable === true ? 'ready' : 'unavailable';
    case 'expired': return 'expired';
    case 'missing': return 'missing';
    default: return 'unavailable';
  }
}

function materialStatusLabel(item: MaterialProjection): string {
  const refresh = materialStateLabel(item.state);
  switch (materialCredentialState(item)) {
    case 'ready':
      return ['retryable_failed', 'failed', 'final_failed', 'completed_with_failures'].includes(materialString(item.state))
        ? '刷新失败 · 旧凭据仍可用'
        : `${refresh} · 当前凭据可用`;
    case 'expired': return `${refresh} · 凭据已过期`;
    case 'missing': return `${refresh} · 尚无可用凭据`;
    default: return `${refresh} · 凭据状态暂不可用`;
  }
}

function materialFailureReason(item: MaterialProjection): string {
  switch (materialString(item.state)) {
    case 'retryable_failed':
    case 'failed':
    case 'final_failed':
    case 'completed_with_failures':
      return materialFailureHint(item);
    default:
      return '—';
  }
}

function materialStableID(item: MaterialProjection): string {
  const source = materialString(item.source_ref);
  const match = source.match(/(?:^|:)([1-9][0-9]*)$/);
  return match?.[1] || '';
}

function materialCount(_item: MaterialProjection, round: RefreshRoundProjection | undefined, field: string): number | undefined {
  return materialNumber(round?.[field]);
}

function materialProgress(item: MaterialProjection, round: RefreshRoundProjection | undefined): string {
  const total = materialCount(item, round, 'total');
  const succeeded = materialCount(item, round, 'succeeded');
  const failed = materialCount(item, round, 'failed');
  const unknown = materialCount(item, round, 'unknown');
  if (total === undefined && succeeded === undefined && failed === undefined && unknown === undefined)
    return '当日进度暂不可用';
  const successLabel = succeeded === undefined ? '—' : String(succeeded);
  return `总计 ${total === undefined ? '—' : total} · 成功 ${successLabel} · 失败 ${failed === undefined ? '—' : failed} · 待核实 ${unknown === undefined ? '—' : unknown}`;
}

function materialSourceName(item: MaterialProjection): string {
  if (materialString(item.file_name)) return materialString(item.file_name);
  const source = materialString(item.source_ref);
  const id = materialStableID(item);
  const label = materialTypeLabel(item.source_type || source.split(':', 1)[0]);
  return id ? `${label}素材 #${id}` : `${label}素材`;
}

function materialNextRun(value: unknown): string {
  const next = materialFormatTime(value);
  return next === '—' ? '每天 02:00' : next;
}

function materialFailureHint(failure: MaterialSourceFailureProjection): string {
  switch (materialString(failure.failure_code)) {
    case 'source_bytes_missing':
    case 'source_blob_missing':
      return '原文件缺失，请补传';
    case 'invalid_metadata':
      return '素材元数据无效，请重新上传';
    case 'provider_rejected':
      return '服务未接受该素材，请重新上传后重试';
    case 'upload_outcome_unknown':
    case 'response_unknown':
      return '刷新结果待核实，请稍后读取进度';
    case 'read_unavailable':
      return '刷新服务暂不可用，请稍后重试';
    case 'cancelled':
      return '刷新已取消，请重新发起';
    case 'not_supported':
      return '该素材暂不支持刷新，请重新上传';
    default:
      return '素材无法读取，请重新上传';
  }
}

function materialScrollRegion(stage: HTMLElement): HTMLElement | undefined {
  const candidates = [...stage.querySelectorAll<HTMLElement>('div')];
  return candidates.find((node) => node.style.overflow === 'auto' && node.style.flex.includes('1')) ||
    candidates.find((node) => node.style.overflow === 'auto');
}

function materialBusy(button: HTMLButtonElement, busy: boolean, busyLabel: string): void {
  if (busy) {
    button.dataset.materialRefreshLabel = button.textContent || '';
    button.disabled = true;
    button.textContent = busyLabel;
  } else {
    button.disabled = false;
    button.textContent = button.dataset.materialRefreshLabel || button.textContent || '';
  }
}

class MaterialRefreshPanel {
  readonly root: HTMLElement;
  private items: MaterialProjection[] = [];
  private failures: MaterialSourceFailureProjection[] = [];
  private round: RefreshRoundProjection | undefined;
  private nextRefreshAt: unknown;
  private loading = false;
  private generation = 0;
  private fullRetryKey = '';
  private singleRetryKeys = new Map<string, string>();

  constructor(parent: HTMLElement) {
    this.root = document.createElement('section');
    this.root.id = 'material-refresh-panel';
    this.root.dataset.materialRefresh = 'true';
    this.root.setAttribute('aria-labelledby', 'material-refresh-title');
    this.root.style.cssText = 'grid-column:1/-1;background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:14px 16px;color:#1F2329';
    parent.prepend(this.root);
    this.render();
  }

  async load(): Promise<void> {
    const generation = ++this.generation;
    this.loading = true;
    this.render();
    try {
      let cursor = '';
      const seen = new Set<string>();
      const items: MaterialProjection[] = [];
      const failures: MaterialSourceFailureProjection[] = [];
      do {
        const query = new URLSearchParams({ limit: '100' });
        if (cursor) query.set('cursor', cursor);
        const result = await materialGet(`${materialRefreshBase}?${query}`);
        const pageItems = Array.isArray(result.items) ? result.items : [];
        const pageFailures = Array.isArray(result.failures) ? result.failures : [];
        if (generation === this.generation) {
          this.nextRefreshAt = result.next_refresh_at;
          this.round = materialObject(result.today_refresh_round) as RefreshRoundProjection;
          if (!Object.keys(this.round).length) this.round = undefined;
        }
        items.push(...pageItems.map((value) => materialObject(value) as MaterialProjection));
        failures.push(...pageFailures.map((value) => materialObject(value) as MaterialSourceFailureProjection));
        const next = materialString(result.next_cursor);
        if (!next || result.done === true) break;
        if (seen.has(next)) throw new Error('刷新状态分页游标重复，已停止读取');
        seen.add(next);
        cursor = next;
      } while (true);
      if (generation !== this.generation) return;
      this.items = items;
      this.failures = failures;
      this.loading = false;
      this.render();
    } catch (error) {
      if (generation !== this.generation) return;
      this.loading = false;
      this.renderError(materialFailureText(error, '刷新状态暂不可读取，请检查网络后重试。'));
    }
  }

  private statusNode(): HTMLElement | undefined {
    return this.root.querySelector<HTMLElement>('[data-material-refresh-status]') || undefined;
  }

  private setStatus(value: string, alert = false): void {
    const node = this.statusNode();
    if (!node) return;
    node.textContent = value;
    node.setAttribute('role', alert ? 'alert' : 'status');
  }

  private renderError(value: string): void {
    this.root.replaceChildren();
    const header = document.createElement('div');
    header.dataset.materialRefreshHeader = 'true';
    header.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap';
    const title = document.createElement('h2');
    title.id = 'material-refresh-title';
    title.textContent = '素材刷新状态';
    title.style.cssText = 'margin:0;font-size:14px';
    const retry = document.createElement('button');
    retry.type = 'button';
    retry.textContent = '重试读取刷新状态';
    retry.onclick = () => { void this.load(); };
    header.append(title, retry);
    const status = document.createElement('p');
    status.dataset.materialRefreshStatus = 'true';
    status.setAttribute('role', 'alert');
    status.textContent = value;
    status.style.cssText = 'margin:10px 0 0;color:#B42318;font-size:12px';
    this.root.append(header, status);
  }

  private render(): void {
    if (this.loading) {
      this.root.replaceChildren();
      const status = document.createElement('p');
      status.dataset.materialRefreshStatus = 'true';
      status.setAttribute('role', 'status');
      status.textContent = '正在读取素材刷新状态…';
      status.style.cssText = 'margin:0;color:#646A73;font-size:12px';
      this.root.append(status);
      return;
    }
    this.root.replaceChildren();
    const header = document.createElement('div');
    header.dataset.materialRefreshHeader = 'true';
    header.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap';
    const titleWrap = document.createElement('div');
    const title = document.createElement('h2');
    title.id = 'material-refresh-title';
    title.textContent = '素材刷新状态';
    title.style.cssText = 'margin:0;font-size:14px';
    const note = document.createElement('p');
    note.textContent = '启用图片、附件和小程序封面每天 02:00 全量刷新；刷新只更新临时凭据，不触发群发。';
    note.style.cssText = 'margin:4px 0 0;color:#646A73;font-size:12px;line-height:18px';
    titleWrap.append(title, note);
    const all = document.createElement('button');
    all.type = 'button';
    all.className = 'admin-button admin-button--primary';
    all.textContent = '立即刷新全部启用素材';
    all.dataset.materialRefreshAll = 'true';
    all.onclick = () => { void this.refreshAll(all); };
    header.append(titleWrap, all);
    this.root.append(header);

    const status = document.createElement('p');
    status.dataset.materialRefreshStatus = 'true';
    status.setAttribute('role', 'status');
    status.style.cssText = 'margin:10px 0 0;color:#646A73;font-size:12px;min-height:18px';
    this.root.append(status);
    const progress = document.createElement('p');
    progress.dataset.materialRefreshProgress = 'true';
    progress.style.cssText = 'margin:4px 0 10px;color:#646A73;font-size:12px';
    progress.textContent = this.round ? this.roundProgress(this.round) : '当日刷新进度暂不可用';
    this.root.append(progress);
    if (this.round && materialNumber(this.round.id) !== undefined) {
      const refreshProgress = document.createElement('button');
      refreshProgress.type = 'button';
      refreshProgress.className = 'admin-button admin-button--secondary';
      refreshProgress.textContent = '刷新进度';
      refreshProgress.dataset.materialRefreshRound = String(this.round.id);
      refreshProgress.onclick = () => { void this.loadRound(refreshProgress); };
      this.root.append(refreshProgress);
    }
    const next = document.createElement('p');
    next.style.cssText = 'margin:8px 0 10px;color:#646A73;font-size:12px';
    next.textContent = `下次运行：${materialNextRun(this.nextRefreshAt)}`;
    this.root.append(next);

    if (this.failures.length) {
      const missing = document.createElement('section');
      missing.dataset.materialSourceFailures = 'true';
      missing.style.cssText = 'margin:0 0 10px;padding:10px 12px;border:1px solid #FDA29B;border-radius:6px;background:#FFFBFA;color:#B42318;font-size:12px';
      const title = document.createElement('strong');
      title.textContent = '需要补传的原文件';
      missing.append(title);
      const list = document.createElement('ul');
      list.style.cssText = 'margin:6px 0 0;padding-left:18px;display:grid;gap:4px';
      this.failures.forEach((failure) => {
        const entry = document.createElement('li');
        const sourceRef = materialString(failure.source_ref);
        const id = materialStableID({ source_ref: sourceRef } as MaterialProjection);
        const type = materialTypeLabel(sourceRef.split(':', 1)[0]);
        entry.append(document.createTextNode(`${id ? `${type}素材 #${id}` : `${type}素材`}：${materialFailureHint(failure)}`));
        const details = document.createElement('details');
        details.style.cssText = 'margin-top:4px';
        const summary = document.createElement('summary');
        summary.textContent = '技术详情';
        summary.style.cursor = 'pointer';
        const technical = document.createElement('small');
        technical.style.cssText = 'display:block;margin-top:4px;color:#646A73;white-space:pre-wrap;overflow-wrap:anywhere';
        technical.textContent = `素材引用：${sourceRef || '未提供'}\n失败代码：${materialString(failure.failure_code) || '未提供'}`;
        details.append(summary, technical);
        entry.append(details);
        list.append(entry);
      });
      missing.append(list);
      this.root.append(missing);
    }

    if (!this.items.length) {
      const empty = document.createElement('p');
      empty.textContent = '当前没有启用的图片、附件或小程序封面。';
      empty.style.cssText = 'margin:0;color:#646A73;font-size:12px';
      this.root.append(empty);
      return;
    }
    const table = document.createElement('table');
    table.style.cssText = 'border-collapse:collapse;width:100%;font-size:12px';
    const head = document.createElement('tr');
    ['素材', '类型', '刷新 / 凭据', '最近成功刷新', '到期时间', '当日进度', '失败原因', '操作'].forEach((label) => {
      const cell = document.createElement('th');
      cell.textContent = label;
      cell.style.cssText = 'padding:8px;border-bottom:1px solid #EFF0F1;text-align:left;font-weight:500;color:#8F959E;white-space:nowrap';
      head.append(cell);
    });
    table.append(head);
    this.items.forEach((item) => table.append(this.itemRow(item)));
    this.root.append(table);
  }

  private roundProgress(round: RefreshRoundProjection): string {
    const count = (field: string): string => {
      const value = materialNumber(round[field]);
      return value === undefined ? '—' : String(value);
    };
    const total = count('total');
    if (total === '—' && count('succeeded') === '—' && count('failed') === '—' && count('unknown') === '—') return '当日刷新进度暂不可用';
    return `当日进度：总计 ${total} · 已排队 ${count('queued')} · 成功 ${count('succeeded')} · 失败 ${count('failed')} · 待核实 ${count('unknown')}`;
  }

  private itemRow(item: MaterialProjection): HTMLTableRowElement {
    const row = document.createElement('tr');
    const stableID = materialStableID(item);
    const name = `${materialSourceName(item)}${stableID ? ` · 素材 #${stableID}` : ''}`;
    const values = [
      name,
      materialTypeLabel(item.source_type),
      materialStatusLabel(item),
      materialFormatTime(item.last_succeeded_at),
      materialFormatTime(item.expires_at),
      materialProgress(item, this.round),
      materialFailureReason(item),
    ];
    values.forEach((value) => {
      const cell = document.createElement('td');
      cell.textContent = value;
      cell.style.cssText = 'padding:8px;border-bottom:1px solid #EFF0F1;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere';
      row.append(cell);
    });
    const actions = document.createElement('td');
    actions.style.cssText = 'padding:8px;border-bottom:1px solid #EFF0F1;vertical-align:top;white-space:nowrap';
    const refresh = document.createElement('button');
    refresh.type = 'button';
    refresh.className = 'admin-button admin-button--primary';
    refresh.textContent = '立即刷新单个';
    refresh.dataset.materialRefreshSource = materialString(item.source_ref);
    refresh.disabled = !materialString(item.source_ref);
    refresh.onclick = () => { void this.refreshOne(item, refresh); };
    actions.append(refresh);
    const details = document.createElement('details');
    details.style.cssText = 'margin-top:5px;white-space:normal';
    const summary = document.createElement('summary');
    summary.textContent = '技术详情';
    summary.style.cursor = 'pointer';
    const technical = document.createElement('small');
    technical.style.cssText = 'display:block;margin-top:4px;color:#646A73;white-space:pre-wrap;overflow-wrap:anywhere';
    const mediaID = materialString(item.media_id);
    technical.textContent = mediaID ? `media_id：${mediaID}` : '当前没有可显示的 media_id';
    details.append(summary, technical);
    actions.append(details);
    row.append(actions);
    return row;
  }

  private async refreshOne(item: MaterialProjection, button: HTMLButtonElement): Promise<void> {
    const sourceRef = materialString(item.source_ref);
    if (!sourceRef) return;
    const key = this.singleRetryKeys.get(sourceRef) || materialRefreshKey(`one-${sourceRef}`);
    this.singleRetryKeys.set(sourceRef, key);
    materialBusy(button, true, '刷新中…');
    try {
      const result = await materialPost(`${materialRefreshBase}/${encodeURIComponent(sourceRef)}/prepare`, { force: true }, key);
      const material = materialObject(result.material) as MaterialProjection;
      const index = this.items.findIndex((value) => value.source_ref === sourceRef);
      if (index >= 0 && Object.keys(material).length) this.items[index] = { ...this.items[index], ...material };
      this.singleRetryKeys.delete(sourceRef);
      this.render();
      this.setStatus('单素材刷新已受理；完成状态可通过“刷新进度”核对。');
    } catch (error) {
      materialBusy(button, false, '立即刷新单个');
      this.setStatus(`${materialFailureText(error, '单素材刷新失败，请检查网络后重试。')}可重新发起刷新。`, true);
    }
  }

  private async refreshAll(button: HTMLButtonElement): Promise<void> {
    const key = this.fullRetryKey || materialRefreshKey('all');
    this.fullRetryKey = key;
    materialBusy(button, true, '刷新中…');
    try {
      const result = await materialPost(`${materialRefreshBase}/refresh-rounds`, { force: true }, key);
      const round = materialObject(result.refresh_round) as RefreshRoundProjection;
      this.round = round;
      this.fullRetryKey = '';
      this.render();
      this.setStatus('全量刷新已异步受理；刷新不会触发群发。');
      const id = materialNumber(round.id);
      if (id !== undefined) await this.loadRound();
    } catch (error) {
      materialBusy(button, false, '立即刷新全部启用素材');
      this.setStatus(`${materialFailureText(error, '全量刷新失败，请检查网络后重试。')}可重新发起刷新。`, true);
    }
  }

  private async loadRound(button?: HTMLButtonElement): Promise<void> {
    const id = materialNumber(this.round?.id);
    if (id === undefined) return;
    if (button) materialBusy(button, true, '读取中…');
    try {
      const result = await materialGet(`${materialRefreshBase}/refresh-rounds/${encodeURIComponent(String(id))}`);
      const round = materialObject(result.refresh_round) as RefreshRoundProjection;
      this.round = round;
      this.render();
      this.setStatus('已读取最新刷新进度。');
    } catch (error) {
      if (button) materialBusy(button, false, '刷新进度');
      this.setStatus(materialFailureText(error, '刷新进度暂不可读取，请检查网络后重试。'), true);
    }
  }
}

function installMaterialRefreshPanel(): void {
  if (typeof document === 'undefined' || !document.body || !materialRefreshPages.has(document.body.dataset.page || '')) return;
  const stage = document.querySelector<HTMLElement>('main#stage');
  if (!stage || stage.querySelector('[data-material-refresh="true"]')) return;
  const region = materialScrollRegion(stage);
  if (!region) return;
  const panel = new MaterialRefreshPanel(region);
  // Source-owned image mutations announce only after their required list
  // readback succeeds. Reuse this panel's existing bounded read rather than
  // duplicate refresh-state rendering in the image Host.
  window.addEventListener(mediaContentChangedEvent, () => { void panel.load(); });
  void panel.load();
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', installMaterialRefreshPanel);
else installMaterialRefreshPanel();
new MutationObserver(installMaterialRefreshPanel).observe(document.documentElement, { childList: true, subtree: true });
