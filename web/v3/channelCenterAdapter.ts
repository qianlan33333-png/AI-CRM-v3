// V3-owned Channel Center seam. The list retains its byte-frozen controller;
// the channel form mounts the standard admission page through a narrow Catalog
// transport boundary that owns resource IDs, CAS and idempotency headers.

import { api } from '../src/shared/api/client';
import type { AdminDb } from '../src/shared/api/types';
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
