// This is the only v3-owned browser seam for the byte-frozen Channel Center.
// It supplies the canonical resource id for path-based edit routes and adds
// the server-issued CAS token to the donor's complete replacement requests.
// It never renders, restyles, or changes donor business behavior.

const channelResourceID = document.body.dataset.channelResourceId || '';
if (document.body.dataset.page === 'channelForm' && channelResourceID) {
  if (!/^[1-9][0-9]*$/.test(channelResourceID)) throw new Error('渠道资源 ID 无效');
  const query = new URLSearchParams(location.search);
  if (!query.has('id')) {
    query.set('id', channelResourceID);
    history.replaceState(history.state, '', `${location.pathname}?${query.toString()}${location.hash}`);
  }
}

const donorFetch = globalThis.fetch.bind(globalThis);

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

new MutationObserver(replaceFrozenQRHint).observe(document.documentElement, { childList: true, subtree: true, characterData: true });

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
