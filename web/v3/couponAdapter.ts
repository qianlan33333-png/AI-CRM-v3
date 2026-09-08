// V3 bridge around the byte-preserved dd8 coupon editor.
//
// OneID: not involved. Persistence: coupon rules use the Coupon owner's local
// PostgreSQL transaction and idempotency receipt. External Effects: not
// involved; publishing changes the coupon lifecycle only.

export {};

type Json = Record<string, unknown>;
const nativeFetch = window.fetch.bind(window);
const mutationKeys = new Map<string, string>();
let unresolvedCreate: string | null = null;
const createdCouponIDs = new Map<string, number>();
let mounted = false;
let mountFailed = false;
const standardFormURL = '/assets/standard-components/coupon_form.html';
const standardStyleURL = '/assets/standard-components/coupon_styles.html';
const standardRuntimeURL = '/assets/standard-components/coupon_form_runtime.js';

function csrf(): string {
  return document.cookie.split(';').map((item) => item.trim().split('='))
    .find(([name]) => name === 'aicrm_csrf' || name === 'aicrm_admin_csrf')?.slice(1).join('=') || '';
}
function newKey(): string { return `coupon-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`; }
function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), location.origin);
  if (typeof input === 'string') return new URL(input, location.origin);
  return new URL(input.url, location.origin);
}
function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  return String(init?.method || (typeof input === 'string' || input instanceof URL ? 'GET' : input.method)).toUpperCase();
}
function bodyText(input: RequestInfo | URL, init?: RequestInit): string {
  return typeof init?.body === 'string' ? init.body : typeof input !== 'string' && !(input instanceof URL) && typeof input.body === 'string' ? input.body : '';
}
function couponMutation(url: URL, method: string): boolean {
  return method !== 'GET' && /^\/api\/admin\/coupons(?:\/[1-9][0-9]*(?:\/(?:publish|stop|copy|archive))?)?$/.test(url.pathname);
}
function jsonResponse(status: number, payload: Json): Response {
  return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } });
}
function fingerprint(method: string, url: URL, body: string): string { return `${method}:${url.pathname}:${body}`; }
function createdID(payload: unknown): number {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return 0;
  const record = payload as Json;
  const coupon = record.coupon && typeof record.coupon === 'object' && !Array.isArray(record.coupon) ? record.coupon as Json : record;
  const id = Number(coupon.id ?? coupon.resource_id);
  return Number.isSafeInteger(id) && id > 0 ? id : 0;
}
function normalizedCouponProduct(value: unknown): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  const item = value as Json; const ref = String(item.target_ref || '');
  if (!ref) return value;
  return { ...item, title: typeof item.title === 'string' ? item.title : String(item.name || '未命名商品'), product_type: ref.startsWith('service_period:') ? 'service_period' : ref.startsWith('standard_product:') ? 'standard_product' : 'unknown', price_cents: Number(item.price_cents ?? item.price_minor ?? 0), status: typeof item.status === 'string' && item.status ? item.status : '状态未提供' };
}
async function normalizeProductOptions(response: Response): Promise<Response> {
  if (!response.ok) return response;
  const payload = await response.clone().json().catch(() => null) as Json | null;
  if (!payload || !Array.isArray(payload.items)) return response;
  const headers = new Headers(response.headers); headers.delete('Content-Length'); headers.set('Content-Type', 'application/json');
  return new Response(JSON.stringify({ ...payload, items: payload.items.map(normalizedCouponProduct) }), { status: response.status, statusText: response.statusText, headers });
}

// The frozen coupon controller and the original editor both call fetch.  This
// one scoped transport gives every lifecycle mutation an original stable key,
// including DELETE, and refuses a changed create payload after an unknown
// create outcome.  Retrying unchanged content reuses the same receipt key.
window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = requestURL(input); const method = requestMethod(input, init);
  if (url.origin !== location.origin) return nativeFetch(input, init);
  if (method === 'GET' && url.pathname === '/api/admin/coupons/product-options') return normalizeProductOptions(await nativeFetch(input, init));
  if (!couponMutation(url, method)) return nativeFetch(input, init);
  const body = bodyText(input, init); const operation = fingerprint(method, url, body);
  if (method === 'POST' && url.pathname === '/api/admin/coupons' && unresolvedCreate && unresolvedCreate !== operation) {
    return jsonResponse(409, { code: 'CREATE_OUTCOME_UNKNOWN', message: '上一份优惠券的保存结果未知。请保持内容不变后重试，或先返回列表核对；尚未创建新的优惠券。' });
  }
  const headers = new Headers(init?.headers || (typeof input === 'string' || input instanceof URL ? undefined : input.headers));
  headers.set('Idempotency-Key', mutationKeys.get(operation) || newKey());
  mutationKeys.set(operation, headers.get('Idempotency-Key') || '');
  const token = csrf(); if (token) headers.set('X-CSRF-Token', token);
  let response: Response;
  try { response = await nativeFetch(input, { ...init, method, headers, credentials: 'same-origin' }); }
  catch (error) {
    if (method === 'POST' && url.pathname === '/api/admin/coupons') unresolvedCreate = operation;
    throw error;
  }
  if (method === 'POST' && url.pathname === '/api/admin/coupons') {
    if (!response.ok) {
      if (response.status >= 500) unresolvedCreate = operation;
    } else {
      const payload = await response.clone().json().catch(() => null);
      const id = createdID(payload);
      if (!id) {
        unresolvedCreate = operation;
        return jsonResponse(503, { code: 'CREATE_OUTCOME_UNKNOWN', message: '优惠券保存结果未知；请保持内容不变后重试，或先返回列表核对。' });
      }
      unresolvedCreate = null; createdCouponIDs.set(operation, id);
    }
  } else if (response.ok) {
    // A key is shared only while this intent is pending.  Once the lifecycle
    // command is confirmed, a later deliberate transition (publish -> stop ->
    // publish) must create a fresh receipt rather than replay old state.
    mutationKeys.delete(operation);
  }
  return response;
};

function escapeHTML(value: unknown): string { return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character] || character)); }
function asJson(value: unknown): Json { return value && typeof value === 'object' && !Array.isArray(value) ? value as Json : {}; }
function couponID(): number {
  const query = new URLSearchParams(location.search).get('id') || '';
  const path = location.pathname.match(/^\/admin\/coupons\/([1-9][0-9]*)\/edit$/)?.[1] || '';
  const id = Number(query || path); return Number.isSafeInteger(id) && id > 0 ? id : 0;
}
function responseMessage(payload: unknown, fallback: string): string {
  const source = asJson(payload); return typeof source.message === 'string' && source.message.trim() ? source.message : fallback;
}
async function readCoupon(id: number): Promise<Json> {
  if (!id) return {};
  const response = await nativeFetch(`/api/admin/coupons/${id}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`优惠券读取失败（HTTP ${response.status}）`);
  const payload = asJson(await response.json()); const coupon = asJson(payload.coupon || payload.item || payload);
  const refs = Array.isArray(coupon.target_refs) ? coupon.target_refs.map((value) => String(value)) : [];
  if (!refs.length || Array.isArray(coupon.products) && coupon.products.length) return coupon;
  // Detail deliberately returns only target_refs. Resolve visible names, types
  // and prices from the same server-owned product-options catalog; any ref not
  // returned by that catalog remains the donor's explicit fallback, never a
  // fabricated product row.
  try {
    const found = new Map<string, unknown>(); let offset = 0; let total = Number.POSITIVE_INFINITY;
    while (offset < total && found.size < refs.length) {
      const page = await window.fetch(`/api/admin/coupons/product-options?product_type=all&limit=100&offset=${offset}`, { credentials: 'same-origin' });
      if (!page.ok) break;
      const body = asJson(await page.json()); const items = Array.isArray(body.items) ? body.items : [];
      items.forEach((item) => { const normalized = normalizedCouponProduct(item) as Json; const ref = String(normalized.target_ref || ''); if (refs.includes(ref)) found.set(ref, normalized); });
      total = Number(body.total) || items.length; if (!items.length) break; offset += items.length;
    }
    coupon.products = refs.map((ref) => found.get(ref) || {
      target_ref: ref, title: '商品目录暂不可读取',
      product_type: ref.startsWith('service_period:') ? 'service_period' : ref.startsWith('standard_product:') ? 'standard_product' : 'unknown',
      price_cents: 0, status: '目录暂不可用',
    });
  } catch {
    coupon.products = refs.map((ref) => ({ target_ref: ref, title: '商品目录暂不可读取', product_type: 'unknown', price_cents: 0, status: '目录暂不可用' }));
  }
  return coupon;
}
function contentFromDonor(raw: string, initial: Json, id: number): string {
  const start = raw.indexOf('{% block content %}'); const end = raw.indexOf('{% endblock %}', start);
  if (start < 0 || end < 0) throw new Error('标准优惠券表单资源不完整');
  let content = raw.slice(start + '{% block content %}'.length, end);
  const isNew = id === 0;
  content = content.split('{{ coupon_form_mode }}').join(isNew ? 'new' : 'edit').split('{{ coupon_id }}').join(id ? String(id) : '');
  content = content.split('{{ "新建优惠券" if coupon_form_mode == "new" else "编辑优惠券" }}').join(isNew ? '新建优惠券' : '编辑优惠券');
  content = content.replace('{{ initial_coupon.name or \'\' }}', escapeHTML(initial.name));
  content = content.replace(/\s*{% for product in initial_coupon\.products or \[\] %}[\s\S]*?{% endfor %}/, '');
  content = content.replace('{{ initial_coupon | tojson }}', JSON.stringify(initial).replace(/</g, '\\u003c'));
  return content;
}
async function installStyles(): Promise<void> {
  if (document.querySelector('style[data-v3-standard-coupon-style]')) return;
  const response = await nativeFetch(standardStyleURL, { credentials: 'same-origin' });
  if (!response.ok) throw new Error('标准优惠券样式加载失败，请刷新页面后重试。');
  const template = document.createElement('template'); template.innerHTML = await response.text();
  const style = template.content.querySelector('style');
  if (!style) throw new Error('标准优惠券样式资源不完整');
  style.dataset.v3StandardCouponStyle = '';
  document.head.append(style);
}

type AdminAPI = { requestJson(url: string, options?: RequestInit & { body?: unknown }): Promise<Json>; safeJsonParse(raw: string): Json; escapeHtml(value: unknown): string };
function installAdminAPI(): void {
  const target = window as Window & { AdminApi?: Partial<AdminAPI> };
  const api = target.AdminApi ||= {};
  api.safeJsonParse = (raw: string): Json => { try { return asJson(JSON.parse(raw)); } catch { return {}; } };
  api.escapeHtml = escapeHTML;
  api.requestJson = async (url: string, options: RequestInit & { body?: unknown } = {}): Promise<Json> => {
    const headers = new Headers(options.headers); let body = options.body;
    if (body !== undefined && body !== null && typeof body !== 'string' && !(body instanceof FormData) && !(body instanceof URLSearchParams) && !(body instanceof Blob)) { headers.set('Content-Type', 'application/json'); body = JSON.stringify(body); }
    const method = String(options.method || 'GET').toUpperCase(); const absolute = new URL(url, location.origin); const rawBody = typeof body === 'string' ? body : '';
    const knownID = method === 'POST' && absolute.pathname === '/api/admin/coupons' ? createdCouponIDs.get(fingerprint(method, absolute, rawBody)) : 0;
    if (knownID) return { coupon: { id: knownID } };
    const response = await window.fetch(url, { ...options, headers, body: body as BodyInit | null | undefined, credentials: 'same-origin' });
    const payload = await response.json().catch(() => null);
    if (!response.ok) throw new Error(responseMessage(payload, `请求失败（HTTP ${response.status}）`));
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('保存结果无法确认；请保持内容不变后重试或先返回列表核对。');
    return payload as Json;
  };
}

// Load the byte-preserved donor body from a same-origin release asset and
// invoke exactly the DOM-ready handler it registered. Capturing that one
// registration is an outer bootstrap seam: picker, preview, validation and
// save behavior stay in the donor script while the runtime remains valid
// under the production CSP (which does not permit inline scripts or eval).
async function executeDonorScript(): Promise<void> {
  const originalAdd = document.addEventListener.bind(document);
  let ready: EventListener | null = null;
  let runtimeScript: HTMLScriptElement | null = null;
  document.querySelector('script[data-v3-standard-coupon-runtime]')?.remove();
  const documentWithInterceptor = document as Document & { addEventListener: typeof document.addEventListener };
  const replacement = ((type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions) => {
    // The Host loads this external script asynchronously. Other page modules
    // can register DOM-ready listeners during that delay, so capture only the
    // listener whose currently executing classic script is this donor asset.
    if (type === 'DOMContentLoaded' && !ready && document.currentScript === runtimeScript) { ready = typeof listener === 'function' ? listener : listener.handleEvent.bind(listener); return; }
    originalAdd(type, listener, options);
  }) as typeof document.addEventListener;
  documentWithInterceptor.addEventListener = replacement;
  try {
    await new Promise<void>((resolve, reject) => {
      runtimeScript = document.createElement('script'); runtimeScript.src = standardRuntimeURL; runtimeScript.defer = false; runtimeScript.dataset.v3StandardCouponRuntime = '';
      runtimeScript.onload = () => resolve(); runtimeScript.onerror = () => reject(new Error('标准优惠券交互脚本加载失败，请刷新页面后重试。')); document.head.append(runtimeScript);
    });
  } finally { documentWithInterceptor.addEventListener = originalAdd as typeof document.addEventListener; }
  const handler = ready as EventListener | null;
  if (!handler) throw new Error('标准优惠券交互脚本未注册初始化函数');
  handler.call(document, new Event('DOMContentLoaded'));
}

async function mountCouponEditor(): Promise<void> {
  if (mounted || mountFailed || document.body.dataset.page !== 'couponForm') return;
  const stage = document.querySelector<HTMLElement>('#stage'); if (!stage || (!stage.querySelector('#coupon-target-refs') && !stage.querySelector('[data-coupon-form-mode]'))) return;
  mounted = true;
  try {
    const id = couponID(); const [raw, initial] = await Promise.all([
      nativeFetch(standardFormURL, { credentials: 'same-origin' }).then(async (response) => { if (!response.ok) throw new Error('标准优惠券表单加载失败，请刷新页面后重试。'); return response.text(); }),
      readCoupon(id),
    ]);
    await installStyles(); installAdminAPI(); stage.innerHTML = contentFromDonor(raw, initial, id); await executeDonorScript();
  } catch (error) {
    mounted = false; mountFailed = true; const message = error instanceof Error ? error.message : '标准优惠券表单加载失败'; const notice = document.createElement('div'); notice.setAttribute('role', 'alert'); notice.textContent = `${message}；未提交任何保存。`;
    const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = '重试加载'; retry.addEventListener('click', () => { mountFailed = false; notice.remove(); void mountCouponEditor(); }); notice.append(' ', retry); stage.prepend(notice);
  }
}
new MutationObserver(() => { void mountCouponEditor(); }).observe(document.documentElement, { childList: true, subtree: true });
void mountCouponEditor();
