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

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : null;
  const method = String(init?.method || request?.method || 'GET').toUpperCase();
  const url = new URL(request?.url || String(input), location.href);
  const mutation = url.origin === location.origin ? channelMutation(url, method) : null;
  const headers = new Headers(init?.headers || request?.headers);
  if (!mutation || headers.has('If-Match')) return donorFetch(input, init);

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
