// V3-owned Channel Center seam. The list retains its byte-frozen controller;
// the channel form mounts the standard admission page through a narrow Catalog
// transport boundary that owns resource IDs, CAS and idempotency headers.

import { api } from '../src/shared/api/client';
import type { AdminDb } from '../src/shared/api/types';
import { confirmBox, toast } from '../src/shared/ui/feedback';
import { startChannelAdmissionHost } from './channelAdmissionHost';

// The standard admission form is a complete, persistent V3 page.  Keep the
// older frozen-controller seam only for the list and any legacy fixture route.
if (document.body.dataset.page === 'channelForm') {
  void startChannelAdmissionHost();
} else {

const queryChannelResourceID = new URLSearchParams(location.search).get('id') || '';
const channelResourceID = document.body.dataset.channelResourceId || queryChannelResourceID;
if (document.body.dataset.page === 'channelForm' && channelResourceID) {
  if (!/^[1-9][0-9]*$/.test(channelResourceID)) throw new Error('渠道资源 ID 无效');
  const query = new URLSearchParams(location.search);
  if (!query.has('id')) {
    query.set('id', channelResourceID);
    history.replaceState(history.state, '', `${location.pathname}?${query.toString()}${location.hash}`);
  }
}

const donorFetch = globalThis.fetch.bind(globalThis);
const donorLoadDb = api.loadDb.bind(api);
let channelFormDb: AdminDb | null = null;
let staffPickerSource: 'common' | 'channel' | null = null;
let staffPickerTrigger: HTMLButtonElement | null = null;
type ArchiveIntent = { payload: Record<string, unknown>; etag: string; key: string };
const archiveIntents = new Map<string, ArchiveIntent>();
const archiveBusy = new Set<string>();
const channelIDByVisibleName = new Map<string, string | null>();

const catalogWriteFields = [
  'channel_type', 'carrier_type', 'channel_name', 'channel_code', 'scene_value', 'qr_url', 'status', 'owner_staff_id', 'customer_channel', 'link_url', 'final_url',
  'welcome_message', 'welcome_image_library_ids', 'welcome_miniprogram_library_ids', 'welcome_attachment_library_ids', 'welcome_group_invite_library_ids',
  'auto_accept_friend', 'entry_tag_id', 'entry_tag_name', 'entry_tag_group_name', 'assignment_mode', 'assignment_strategy', 'overflow_policy', 'assignment_config_json',
] as const;

function archiveKey(): string {
  return globalThis.crypto?.randomUUID ? `channel-archive-${globalThis.crypto.randomUUID()}` : `channel-archive-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function channelArchivePayload(channel: unknown): Record<string, unknown> | null {
  if (!channel || typeof channel !== 'object' || Array.isArray(channel)) return null;
  const source = channel as Record<string, unknown>;
  const payload: Record<string, unknown> = {};
  for (const field of catalogWriteFields) {
    if (!(field in source)) return null;
    payload[field] = source[field];
  }
  payload.status = 'archived';
  return payload;
}

function sameArchiveIntent(intent: ArchiveIntent, payload: Record<string, unknown>, etag: string): boolean {
  return intent.etag === etag && JSON.stringify(intent.payload) === JSON.stringify(payload);
}

function csrfToken(): string {
  return document.cookie.split(';').map((part) => part.trim()).map((part) => part.split('='))
    .find(([name]) => name === 'aicrm_csrf' || name === 'aicrm_admin_csrf')?.slice(1).join('=') || '';
}

async function readArchiveChannel(channelID: string): Promise<{ channel: Record<string, unknown>; etag: string } | null> {
  const response = await donorFetch(`/api/admin/channels/${channelID}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) return null;
  const body = await response.json().catch(() => null) as { channel?: unknown } | null;
  if (!body?.channel || !response.headers.get('ETag')) return null;
  return { channel: body.channel as Record<string, unknown>, etag: response.headers.get('ETag') || '' };
}

async function refreshArchivedChannelList(channelID: string, channelCode: string): Promise<boolean> {
  const query = new URLSearchParams({ limit: '50', include_archived: 'true', status: 'archived', q: channelCode });
  const response = await donorFetch(`/api/admin/channels?${query}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) return false;
  const body = await response.json().catch(() => null) as { channels?: unknown; items?: unknown } | null;
  const rows = Array.isArray(body?.channels) ? body.channels : Array.isArray(body?.items) ? body.items : [];
  return rows.some((row) => row && typeof row === 'object' && String((row as Record<string, unknown>).id) === channelID && (row as Record<string, unknown>).status === 'archived');
}

function archiveFailureMessage(status: number): string {
  if (status === 401) return '登录已失效，未归档渠道。请重新登录后重试。';
  if (status === 403) return '当前账号没有归档渠道的权限，未归档渠道。';
  if (status === 409) return '渠道已被其他人更新，未归档渠道。请刷新后核对再试。';
  return `渠道归档失败（HTTP ${status}），当前配置未在页面中改写。`;
}

function markArchived(button: HTMLButtonElement): void {
  const row = button.closest('tr');
  const statusCell = row?.querySelectorAll(':scope > td')[2];
  const label = statusCell?.querySelector('span') || statusCell;
  if (label) label.textContent = '归档';
  button.disabled = true;
  button.textContent = '已归档';
  button.setAttribute('aria-disabled', 'true');
  button.title = '渠道已归档：扫码不会发送欢迎语或入渠标签；可编辑后再启用。';
  button.style.color = '#A6AAB0';
  button.style.cursor = 'not-allowed';
  repairBlockedChannelReadiness();
}

async function archiveChannel(button: HTMLButtonElement): Promise<void> {
  const channelID = button.dataset.channelArchiveId || '';
  if (!/^[1-9][0-9]*$/.test(channelID) || archiveBusy.has(channelID)) return;
  archiveBusy.add(channelID);
  button.disabled = true;
  const initialLabel = button.textContent || '归档';
  button.textContent = '归档中…';
  let confirmed = false;
  try {
    let current: { channel: Record<string, unknown>; etag: string } | null;
    try {
      current = await readArchiveChannel(channelID);
    } catch {
      toast('读取渠道配置失败，未发送归档请求。请刷新后重试。', true);
      return;
    }
    if (current?.channel.status === 'archived') {
      archiveIntents.delete(channelID);
      markArchived(button);
      confirmed = true;
      toast('渠道已归档：扫码不会发送欢迎语或入渠标签；可编辑后再启用。');
      return;
    }
    const payload = current && channelArchivePayload(current.channel);
    if (!current || !payload) {
      toast('未取得完整渠道配置或版本，未发送归档请求。请刷新后重试。', true);
      return;
    }
    const existing = archiveIntents.get(channelID);
    if (existing && !sameArchiveIntent(existing, payload, current.etag)) {
      archiveIntents.delete(channelID);
      toast('上次归档结果尚未确认，渠道配置或版本已变化；请核对后重新确认。', true);
      return;
    }
    const intent = existing || { payload, etag: current.etag, key: archiveKey() };
    archiveIntents.set(channelID, intent);
    const headers = new Headers({ Accept: 'application/json', 'Content-Type': 'application/json', 'If-Match': intent.etag, 'Idempotency-Key': intent.key });
    const token = csrfToken();
    if (token) headers.set('X-CSRF-Token', token);
    let response: Response | undefined;
    let readback: { channel: Record<string, unknown>; etag: string } | null | undefined;
    try {
      response = await donorFetch(`/api/admin/channels/${channelID}`, { method: 'PATCH', credentials: 'same-origin', headers, body: JSON.stringify(intent.payload) });
    } catch {
      try {
        readback = await readArchiveChannel(channelID);
      } catch {
        toast('归档结果尚未确认，回读渠道失败；请稍后刷新核对。', true);
        return;
      }
      if (readback?.channel.status !== 'archived') {
        toast('归档结果尚未确认，已回读但仍未确认归档；请稍后刷新核对。', true);
        return;
      }
    }
    if (response && !response.ok && response.status >= 400 && response.status < 500) {
      archiveIntents.delete(channelID);
      toast(archiveFailureMessage(response.status), true);
      return;
    }
    if (!readback) {
      try {
        readback = await readArchiveChannel(channelID);
      } catch {
        toast('归档结果尚未确认，回读渠道失败；请稍后刷新核对。', true);
        return;
      }
    }
    if (readback?.channel.status !== 'archived') {
      toast('归档结果尚未确认，回读未确认归档；请刷新后核对。', true);
      return;
    }
    archiveIntents.delete(channelID);
    markArchived(button);
    confirmed = true;
    let listReadback = false;
    try {
      listReadback = await refreshArchivedChannelList(channelID, String(intent.payload.channel_code));
    } catch {
      toast('渠道已归档，但列表回读失败；请刷新后核对归档状态。', true);
      return;
    }
    if (!listReadback) {
      toast('渠道已归档，但列表回读失败；请刷新后核对归档状态。', true);
      return;
    }
    toast('渠道已归档：扫码不会发送欢迎语或入渠标签；历史与配置仍保留，可编辑后再启用。');
    window.location.reload();
  } finally {
    archiveBusy.delete(channelID);
    if (!confirmed) {
      button.disabled = false;
      button.textContent = initialLabel;
    }
  }
}

function installChannelArchiveActions(): void {
  if (document.body?.dataset.page !== 'channels') return;
  for (const row of document.querySelectorAll<HTMLTableRowElement>('tbody tr')) {
    const cells = row.querySelectorAll(':scope > td');
    if (cells.length !== 6) continue;
    const visibleName = cells[0]?.textContent?.trim() || '';
    const channelID = channelIDByVisibleName.get(visibleName);
    if (!channelID) continue;
    const actions = cells[5];
    let button = actions.querySelector<HTMLButtonElement>('[data-channel-archive-id]');
    if (!button) {
      const placeholder = Array.from(actions.querySelectorAll<HTMLElement>('[aria-disabled="true"]'))
        .find((item) => item.textContent?.trim() === '下架' && item.title === '后端暂无渠道归档 operation');
      if (!placeholder) continue;
      button = document.createElement('button');
      button.type = 'button';
      button.dataset.channelArchiveId = channelID;
      button.textContent = '归档';
      button.style.cssText = `${placeholder.style.cssText};padding:0;border:0;background:transparent;cursor:pointer;`;
      placeholder.replaceWith(button);
      const deletion = Array.from(actions.querySelectorAll<HTMLElement>('[aria-disabled="true"]'))
        .find((item) => item.textContent?.trim() === '删除');
      if (deletion) {
        deletion.textContent = '删除不可用';
        deletion.title = '暂不支持永久删除；归档会保留历史配置和归因记录';
        deletion.style.color = '#A6AAB0';
        deletion.style.cursor = 'not-allowed';
      }
    }
    if ((button as HTMLButtonElement & { __dcBound?: boolean }).__dcBound) continue;
    (button as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
    const status = button.closest('tr')?.querySelectorAll(':scope > td')[2]?.textContent?.trim();
    if (status === '归档') {
      markArchived(button);
      continue;
    }
    button.addEventListener('click', () => {
      const channelID = button.dataset.channelArchiveId || '';
      if (!/^[1-9][0-9]*$/.test(channelID) || archiveBusy.has(channelID)) return;
      confirmBox('归档渠道', '归档会停止新的扫码欢迎语和入渠标签，保留历史、配置与归因记录；可在编辑页选择“启用”后恢复。确认归档？', '确认归档', true, () => { void archiveChannel(button); });
    });
  }
}

function dependencyUnavailable(): Response {
  return new Response(JSON.stringify({ code: 'DEPENDENCY_UNAVAILABLE' }), {
    status: 503,
    headers: { 'Cache-Control': 'private, no-store', 'Content-Type': 'application/json' },
  });
}

function channelMutation(url: URL, method: string): { channelID: string } | null {
  const match = url.pathname.match(/^\/api\/admin\/channels\/([1-9][0-9]*)(\/assignees)?$/);
  if (!match || (method === 'PATCH' && match[2]) || (method === 'PUT' && match[2] !== '/assignees') || (method !== 'PATCH' && method !== 'PUT')) return null;
  return { channelID: match[1] };
}

const terminalAssetStates = new Set(['executed', 'reconciled', 'outcome_unknown', 'final_failed']);

function blockedEntrantActionStatus(value: unknown): 'inactive' | 'archived' | '' {
  return value === 'inactive' || value === 'archived' ? value : '';
}

function blockedEntrantActionText(status: 'inactive' | 'archived'): string {
  return status === 'archived'
    ? '已归档：扫码不会发送欢迎语或入渠标签；请编辑后选择“启用”并保存。'
    : '已停用：扫码不会发送欢迎语或入渠标签；请编辑后选择“启用”并保存。';
}

function channelAssetPath(url: URL): { channelID: string; effectID: string } | null {
  const match = url.pathname.match(/^\/api\/admin\/channels\/([1-9][0-9]*)\/acquisition-assets(?:\/([^/]+))?$/);
  return match ? { channelID: match[1], effectID: match[2] || '' } : null;
}

function donorCompatibleAsset(value: unknown): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  const asset = { ...(value as Record<string, unknown>) };
  const usable = typeof asset.download_url === 'string' && asset.download_url !== '' || typeof asset.asset_url === 'string' && String(asset.asset_url).trim() !== '';
  if (asset.state === 'legacy_verified_active' || asset.state === 'reconciled' && usable) asset.state = 'executed';
  else if (asset.state === 'legacy_stale') asset.state = 'final_failed';
  else if (asset.state === 'legacy_unverified') asset.state = 'queued';
  return asset;
}

function assetUsable(value: unknown): boolean {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const asset = value as Record<string, unknown>;
  return asset.state === 'executed' && (typeof asset.download_url === 'string' && asset.download_url !== '' || typeof asset.asset_url === 'string' && String(asset.asset_url).trim() !== '');
}

function responseWithJSON(response: Response, payload: unknown): Response {
  const headers = new Headers(response.headers);
  headers.delete('Content-Length');
  headers.set('Content-Type', 'application/json');
  return new Response(JSON.stringify(payload), { status: response.status, statusText: response.statusText, headers });
}

function donorCompatibleCatalog(value: unknown): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  const payload = { ...(value as Record<string, unknown>) };
  const rememberVisibleNames = (rows: unknown): void => {
    if (!Array.isArray(rows)) return;
    channelIDByVisibleName.clear();
    for (const value of rows) {
      if (!value || typeof value !== 'object' || Array.isArray(value)) continue;
      const row = value as Record<string, unknown>;
      const name = String(row.channel_name || '').trim();
      const id = String(row.id || '').trim();
      if (!name || !/^[1-9][0-9]*$/.test(id)) continue;
      channelIDByVisibleName.set(name, channelIDByVisibleName.has(name) ? null : id);
    }
  };
  const normalizeRows = (rows: unknown): unknown => {
    if (!Array.isArray(rows)) return rows;
    return rows.map((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
      const row = { ...(value as Record<string, unknown>) };
      const materialIDs = [
        row.welcome_image_library_ids,
        row.welcome_miniprogram_library_ids,
        row.welcome_attachment_library_ids,
        row.welcome_group_invite_library_ids,
      ].flatMap((ids) => Array.isArray(ids) ? ids : []);
      // The frozen donor counts only welcome_image_library_ids. This
      // compatibility payload is list-page-only; detail/edit reads continue
      // to receive the four canonical, separately owned material arrays.
      row.welcome_image_library_ids = materialIDs;
      if (blockedEntrantActionStatus(row.status)) row.qr_download_url = '';
      return row;
    });
  };
  rememberVisibleNames(payload.channels);
  payload.channels = normalizeRows(payload.channels);
  payload.items = normalizeRows(payload.items);
  return payload;
}

function showProviderReadDegraded(): void {
  if (!document.body) return;
  if (document.getElementById('channel-provider-read-degraded')) return;
  const notice = document.createElement('div');
  notice.id = 'channel-provider-read-degraded';
  notice.setAttribute('role', 'status');
  notice.textContent = '企微实时客服目录暂不可用；已展示本地保存客服，保存客服和发布渠道码仍被严格阻止。';
  notice.style.cssText = 'margin:12px 24px;padding:10px 14px;border:1px solid #f5c26b;border-radius:8px;background:#fff8e8;color:#8a5700;font-size:13px;';
  document.body.prepend(notice);
}

function makePreviewDonorCompatible(payload: Record<string, unknown>): Record<string, unknown> {
  if (payload.provider_execution_eligible !== true) return payload;
  return {
    ...payload,
    // The frozen donor DTO describes whether this read response itself may
    // execute a Provider call. Keep that false while retaining the canonical
    // API readiness fact for diagnostics.
    adapter_provider_execution_eligible: true,
    provider_execution_eligible: false,
  };
}

async function normalizeChannelResponse(response: Response, url: URL): Promise<Response> {
  if (!response.ok || !String(response.headers.get('Content-Type')).toLowerCase().includes('application/json')) return response;
  if (url.pathname === '/api/admin/channels' && document.body?.dataset.page === 'channels') {
    return responseWithJSON(response, donorCompatibleCatalog(await response.clone().json()));
  }
  if (url.pathname.match(/^\/api\/admin\/channels\/[1-9][0-9]*\/acquisition-staff$/)) {
    const payload = await response.clone().json() as Record<string, unknown>;
    if (payload.provider_read_succeeded === false) {
      showProviderReadDegraded();
      payload.provider_read_succeeded = true; // Compatibility field required by the frozen donor DTO.
      payload.adapter_provider_read_succeeded = false;
      return responseWithJSON(response, payload);
    }
  }
  if (url.pathname.match(/^\/api\/admin\/channels\/[1-9][0-9]*\/acquisition-preview$/)) {
    return responseWithJSON(response, makePreviewDonorCompatible(await response.clone().json() as Record<string, unknown>));
  }
  if (channelAssetPath(url)) {
    const payload = await response.clone().json() as Record<string, unknown>;
    if (Array.isArray(payload.items)) {
      const items = payload.items.map(donorCompatibleAsset);
      items.sort((left, right) => Number(assetUsable(right)) - Number(assetUsable(left)));
      payload.items = items;
    }
    if (payload.asset) payload.asset = donorCompatibleAsset(payload.asset);
    else if (payload.effect_id) return responseWithJSON(response, donorCompatibleAsset(payload));
    return responseWithJSON(response, payload);
  }
  return response;
}

async function waitForAsset(response: Response, url: URL, headers: Headers, credentials: RequestCredentials): Promise<Response> {
  if (response.status !== 202) return normalizeChannelResponse(response, url);
  const payload = await response.clone().json() as Record<string, unknown>;
  const statusURL = typeof payload.status_url === 'string' ? new URL(payload.status_url, location.href) : null;
  if (!statusURL || statusURL.origin !== location.origin) return normalizeChannelResponse(response, url);
  const delays = [500, 1000, 1500, 2500, 4000, 6000, 8000, 10000];
  for (const delay of delays) {
    await new Promise<void>((resolve) => window.setTimeout(resolve, delay));
    const statusResponse = await donorFetch(statusURL, { method: 'GET', credentials, headers });
    if (!statusResponse.ok) continue;
    const statusPayload = await statusResponse.clone().json() as Record<string, unknown>;
    const asset = statusPayload.asset && typeof statusPayload.asset === 'object' ? statusPayload.asset as Record<string, unknown> : statusPayload;
    if (terminalAssetStates.has(String(asset.state || ''))) return normalizeChannelResponse(responseWithJSON(response, asset), url);
  }
  return normalizeChannelResponse(response, url);
}

function replaceFrozenQRHint(): void {
  if (!document.body) return;
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  let node: Node | null;
  while ((node = walker.nextNode())) {
    if (node.nodeValue?.trim() === '二维码载体不生成本地链接预览') node.nodeValue = '二维码由服务端异步生成，成功后可在上方打开或下载';
  }
}

function labelFrozenAssetAction(): void {
  if (document.body?.dataset.page !== 'channelForm') return;
  const copyButton = Array.from(document.querySelectorAll<HTMLButtonElement>('button'))
    .find((button) => button.textContent?.trim() === '复制已保存链接');
  const actionButton = Array.from(copyButton?.parentElement?.querySelectorAll<HTMLButtonElement>('button') || [])
    .find((button) => button !== copyButton && button.textContent?.trim() === '');
  if (!actionButton) return;
  const carrier = document.getElementById('channelCarrier') as HTMLSelectElement | null;
  const label = carrier?.value === 'link' ? '生成获客链接' : '生成渠道码';
  actionButton.textContent = label;
  actionButton.setAttribute('aria-label', label);
}

function repairFrozenChannelUI(): void {
  replaceFrozenQRHint();
  labelFrozenAssetAction();
  repairBlockedChannelReadiness();
  installChannelArchiveActions();
}

function repairBlockedChannelReadiness(): void {
  if (document.body?.dataset.page !== 'channels') return;
  for (const row of document.querySelectorAll<HTMLTableRowElement>('tbody tr')) {
    const cells = row.querySelectorAll(':scope > td');
    if (cells.length !== 6) continue;
    const status = cells[2]?.textContent?.trim() === '归档' ? 'archived'
      : cells[2]?.textContent?.trim() === '停用' ? 'inactive' : '';
    if (!status) continue;
    row.dataset.channelEntrantActionsBlocked = status;
    const actions = cells[5];
    for (const link of Array.from(actions.querySelectorAll('a'))) {
      if (link.textContent?.trim() === '下载二维码') link.remove();
    }
    for (const placeholder of Array.from(actions.querySelectorAll<HTMLElement>('[aria-disabled="true"]'))) {
      if (placeholder.textContent?.trim() === '后端未返回二维码地址') placeholder.remove();
    }
    if (actions.querySelector('[data-channel-entrant-actions-blocked]')) continue;
    const notice = document.createElement('span');
    notice.dataset.channelEntrantActionsBlocked = status;
    notice.setAttribute('role', 'status');
    notice.style.cssText = 'font-size:12px;color:#8a5700;white-space:normal;text-align:left;max-width:230px;line-height:18px;';
    notice.textContent = blockedEntrantActionText(status);
    actions.prepend(notice);
  }
}

function showScopedStaffPickerError(): void {
  if (!document.body) return;
  document.getElementById('channel-staff-picker-error')?.remove();
  const notice = document.createElement('div');
  notice.id = 'channel-staff-picker-error';
  notice.setAttribute('role', 'alert');
  notice.textContent = '可分配客服目录读取失败，请稍后重试。未更改渠道客服分配。';
  notice.style.cssText = 'margin:12px 24px;padding:10px 14px;border:1px solid #f2b8b5;border-radius:8px;background:#fff1f0;color:#b42318;font-size:13px;';
  document.body.prepend(notice);
}

type PickerStaff = { name: string; uid: string; dept: string };

async function commonChannelStaff(): Promise<PickerStaff[]> {
  const response = await donorFetch('/api/admin/common/operation-members?scope=channel_code&page_size=100', {
    credentials: 'same-origin', headers: { Accept: 'application/json' }, method: 'GET',
  });
  if (!response.ok) throw new Error(`operation-members HTTP ${response.status}`);
  const payload = await response.json() as { items?: unknown };
  if (!Array.isArray(payload.items)) throw new Error('operation-members 响应不完整');
  return payload.items.flatMap((value): PickerStaff[] => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
    const item = value as Record<string, unknown>;
    const uid = String(item.staff_id || item.sender_userid || '').trim();
    const name = String(item.display_name || '').trim();
    return uid && name && item.active !== false ? [{ name, uid, dept: '可分配客服' }] : [];
  });
}

function renderStaffPickerFailure(): void {
  const mask = document.querySelector<HTMLElement>('.pk-mask');
  if (!mask || !staffPickerTrigger) { showScopedStaffPickerError(); return; }
  mask.replaceChildren();
  const card = document.createElement('section');
  card.setAttribute('role', 'dialog'); card.setAttribute('aria-modal', 'true');
  card.style.cssText = 'width:min(520px,100%);border-radius:12px;background:#fff;box-shadow:0 24px 64px rgba(15,23,42,.22);padding:20px';
  card.innerHTML = '<h2 style="margin:0 0 10px;font-size:16px">选择客服</h2><p role="alert" style="margin:0;color:#b42318;font-size:13px;line-height:22px">可分配客服目录读取失败，未更改渠道客服分配。</p><div style="display:flex;gap:8px;justify-content:flex-end;margin-top:18px"><button type="button" data-staff-retry>重试</button><button type="button" data-staff-cancel>取消</button></div>';
  card.querySelector<HTMLButtonElement>('[data-staff-retry]')?.addEventListener('click', () => {
    const trigger = staffPickerTrigger; mask.remove(); if (trigger) trigger.click();
  });
  card.querySelector<HTMLButtonElement>('[data-staff-cancel]')?.addEventListener('click', () => mask.remove());
  mask.appendChild(card);
}

// The donor controller owns the picker result and its private cfStaff state.
// It incorrectly starts that picker with an unscoped loadDb() call, even after
// the form has loaded a channel-specific directory. Substitute only that one
// read with the exact saved channel's acquisition-staff catalog; the picker
// then completes through the donor controller as usual.
api.loadDb = async (context) => {
  if (context?.page === 'channelForm') {
    const db = await donorLoadDb(context);
    channelFormDb = db;
    return db;
  }
  if (!context && staffPickerSource && channelFormDb) {
    const source = staffPickerSource;
    staffPickerSource = null;
    try {
      const staff = source === 'channel'
        ? (await api.listChannelAcquisitionStaff(Number(channelResourceID))).map((item) => ({ name: item.name, uid: item.staffId, dept: '企微可用客服' }))
        : await commonChannelStaff();
      return {
        ...channelFormDb,
        staff,
      };
    } catch (_error) {
      window.setTimeout(renderStaffPickerFailure, 0);
      return { ...channelFormDb, staff: [] };
    }
  }
  return donorLoadDb(context);
};

document.addEventListener('click', (event) => {
  if (document.body?.dataset.page !== 'channelForm' || !channelFormDb) return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest('button');
  if (button?.textContent?.trim() !== '选择客服') return;
  staffPickerTrigger = button;
  staffPickerSource = channelResourceID ? 'channel' : 'common';
}, true);

new MutationObserver(repairFrozenChannelUI).observe(document.documentElement, { childList: true, subtree: true, characterData: true });
repairFrozenChannelUI();

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : null;
  const method = String(init?.method || request?.method || 'GET').toUpperCase();
  const url = new URL(request?.url || String(input), location.href);
  const mutation = url.origin === location.origin ? channelMutation(url, method) : null;
  const headers = new Headers(init?.headers || request?.headers);
  if (!mutation || headers.has('If-Match')) {
    const response = await donorFetch(input, init);
    if (url.origin !== location.origin) return response;
    if (method === 'POST' && channelAssetPath(url)) return waitForAsset(response, url, headers, init?.credentials || request?.credentials || 'same-origin');
    return normalizeChannelResponse(response, url);
  }

  const preflightHeaders = new Headers(headers);
  preflightHeaders.delete('Content-Type');
  preflightHeaders.set('Accept', 'application/json');
  const preflight = await donorFetch(`/api/admin/channels/${mutation.channelID}`, {
    credentials: init?.credentials || request?.credentials || 'same-origin',
    headers: preflightHeaders,
    method: 'GET',
  });
  if (!preflight.ok) return preflight;
  const etag = preflight.headers.get('ETag');
  if (!etag) return dependencyUnavailable();
  headers.set('If-Match', etag);
  if (request) return donorFetch(new Request(request, { ...init, headers }));
  return donorFetch(input, { ...init, headers });
};

// Dynamic import is deliberate: the binding must be installed before the
// unmodified donor entry reads location.search and issues mutations.
// @ts-expect-error The byte-frozen donor entry is a side-effect-only script.
void import('../src/admin/main');
}
