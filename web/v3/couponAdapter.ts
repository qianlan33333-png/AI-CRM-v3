// V3 bridge around the byte-preserved dd8 coupon editor.
//
// OneID: not involved. Persistence: coupon rules use the Coupon owner's local
// PostgreSQL transaction and idempotency receipt. External Effects: not
// involved; publishing changes the coupon lifecycle only.

// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { AdminController } from '../src/admin/controller';
// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { api } from '../src/shared/api/client';
// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import type { AdminDb } from '../src/shared/api/types';
import { formatShanghaiDateTime, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime';

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
const couponDateFields = [
  ['couponClaimStart', 'claim_starts_at', true],
  ['couponClaimEnd', 'claim_ends_at', true],
  ['couponUseStart', 'use_starts_at', false],
  ['couponUseEnd', 'use_ends_at', false],
] as const;
type CouponDateField = typeof couponDateFields[number];
type CouponDateSnapshot = { original: string | null; controlValue: string };
const couponDateSnapshots = new Map<string, CouponDateSnapshot>();
type CouponPresentation = { scope: string; window: string };
const couponPresentations = new Map<number, CouponPresentation>();
// Detail projection deliberately has no current price. Remember those refs
// only while mounting this editor so the frozen form can describe that limit
// without inventing a zero-price fact.
const couponTargetsWithUnverifiedPrice = new Set<string>();

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
function couponWrite(url: URL, method: string): boolean {
  return (method === 'POST' && url.pathname === '/api/admin/coupons') || (method === 'PUT' && /^\/api\/admin\/coupons\/[1-9][0-9]*$/.test(url.pathname));
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

function objectList(value: unknown, keys: string[]): Json[] {
  const source = asJson(value);
  for (const key of keys) {
    if (Array.isArray(source[key])) return source[key].filter((item): item is Json => Boolean(item) && typeof item === 'object' && !Array.isArray(item));
  }
  return [];
}
function positiveCouponID(value: unknown): number | undefined {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}
function exactCouponTargetNames(coupon: Json): string {
  const refs = Array.isArray(coupon.target_refs) ? coupon.target_refs.map(String) : [];
  const targets = Array.isArray(coupon.target_products) ? coupon.target_products : [];
  if (!refs.length || targets.length !== refs.length) return '适用商品暂不可用';
  const labels: string[] = [];
  for (let index = 0; index < refs.length; index += 1) {
    const target = asJson(targets[index]);
    if (String(target.target_ref || '') !== refs[index]) return '适用商品暂不可用';
    if (target.state === 'available' && typeof target.name === 'string' && target.name.trim()) {
      labels.push(target.name.trim());
      continue;
    }
    if (target.state === 'not_found') {
      labels.push('商品已删除或不可用');
      continue;
    }
    return '适用商品暂不可用';
  }
  return labels.join('、');
}
function couponClaimWindow(coupon: Json): string {
  const start = formatShanghaiDateTime(coupon.claim_starts_at);
  const end = formatShanghaiDateTime(coupon.claim_ends_at);
  return start === '未提供' || end === '未提供' ? '领取时间范围暂不可用' : `${start} 至 ${end}`;
}
async function rememberCouponPresentations(response: Response): Promise<Response> {
  if (!response.ok) return response;
  const payload = await response.clone().json().catch(() => null);
  for (const coupon of objectList(payload, ['coupons', 'items'])) {
    const id = positiveCouponID(coupon.id);
    if (id) couponPresentations.set(id, { scope: exactCouponTargetNames(coupon), window: couponClaimWindow(coupon) });
  }
  return response;
}

function couponDateSnapshotKey(id: string): string { return `coupon-date:${id}`; }
function couponControlValue(raw: unknown): string {
  const displayed = formatShanghaiDateTime(raw);
  return displayed === '未提供' ? '' : displayed.replace(' ', 'T').slice(0, 16);
}
function captureCouponDateSnapshots(initial: Json): void {
  couponDateSnapshots.clear();
  for (const [controlID, payloadField] of couponDateFields) {
    const control = document.getElementById(controlID) as HTMLInputElement | null;
    if (!control) continue;
    const raw = typeof initial[payloadField] === 'string' ? initial[payloadField] : null;
    couponDateSnapshots.set(couponDateSnapshotKey(payloadField), { original: raw, controlValue: couponControlValue(raw) || control.value });
  }
}
function couponPayloadDate(field: CouponDateField): string | null | undefined {
  const [controlID, payloadField, required] = field;
  const control = document.getElementById(controlID) as HTMLInputElement | null;
  if (!control) return undefined;
  const snapshot = couponDateSnapshots.get(couponDateSnapshotKey(payloadField));
  if (snapshot?.original && control.value === snapshot.controlValue) return snapshot.original;
  if (!control.value) return required ? undefined : null;
  return shanghaiDateTimeLocalToRFC3339(control.value);
}
function normalizeCouponWriteBody(url: URL, method: string, body: string): { body?: string; error?: Response } {
  if (!couponWrite(url, method) || !body) return { body };
  let payload: Json;
  try { payload = asJson(JSON.parse(body)); } catch { return { body }; }
  // The Coupon Host owns the four editor controls.  Preserve a valid direct
  // API caller verbatim if this page has not mounted that editor, rather than
  // treating the caller as an incomplete browser form.
  if (!couponDateFields.some(([controlID]) => document.getElementById(controlID))) return { body };
  // The donor's complete form always carries the mode. Do not reinterpret an
  // unrelated direct API probe merely because this editor happens to be open.
  if (!Object.prototype.hasOwnProperty.call(payload, 'validity_mode')) return { body };
  const validityMode = payload.validity_mode;
  if (validityMode !== 'fixed_range' && validityMode !== 'relative_days') {
    return { error: jsonResponse(400, { code: 'invalid_request', message: '请选择有效期模式后再保存；未提交任何修改。' }) };
  }
  for (const field of couponDateFields.slice(0, 2)) {
    const value = couponPayloadDate(field);
    if (value === undefined) return { error: jsonResponse(400, { code: 'invalid_request', message: '请填写有效的时间后再保存；未提交任何修改。' }) };
    payload[field[1]] = value;
  }
  if (validityMode === 'relative_days') {
    // The donor keeps hidden fixed-range control values in the DOM. They are
    // intentionally not part of a relative-days rule and must not be written
    // back when an editor changes modes.
    payload.use_starts_at = null;
    payload.use_ends_at = null;
  } else {
    for (const field of couponDateFields.slice(2)) {
      const value = couponPayloadDate(field);
      if (value === undefined) return { error: jsonResponse(400, { code: 'invalid_request', message: '请填写有效的时间后再保存；未提交任何修改。' }) };
      payload[field[1]] = value;
    }
    payload.relative_validity_days = null;
  }
  return { body: JSON.stringify(payload) };
}

// The frozen coupon controller and the original editor both call fetch.  This
// one scoped transport gives every lifecycle mutation an original stable key,
// including DELETE, and refuses a changed create payload after an unknown
// create outcome.  Retrying unchanged content reuses the same receipt key.
window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = requestURL(input); const method = requestMethod(input, init);
  if (url.origin !== location.origin) return nativeFetch(input, init);
  if (method === 'GET' && url.pathname === '/api/admin/coupons/product-options') return normalizeProductOptions(await nativeFetch(input, init));
  if (method === 'GET' && url.pathname === '/api/admin/coupons') return rememberCouponPresentations(await nativeFetch(input, init));
  if (!couponMutation(url, method)) return nativeFetch(input, init);
  const normalized = normalizeCouponWriteBody(url, method, bodyText(input, init));
  if (normalized.error) return normalized.error;
  const body = normalized.body || ''; const operation = fingerprint(method, url, body);
  if (method === 'POST' && url.pathname === '/api/admin/coupons' && unresolvedCreate && unresolvedCreate !== operation) {
    return jsonResponse(409, { code: 'CREATE_OUTCOME_UNKNOWN', message: '上一份优惠券的保存结果未知。请保持内容不变后重试，或先返回列表核对；尚未创建新的优惠券。' });
  }
  const headers = new Headers(init?.headers || (typeof input === 'string' || input instanceof URL ? undefined : input.headers));
  headers.set('Idempotency-Key', mutationKeys.get(operation) || newKey());
  mutationKeys.set(operation, headers.get('Idempotency-Key') || '');
  const token = csrf(); if (token) headers.set('X-CSRF-Token', token);
  let response: Response;
  try { response = await nativeFetch(input, { ...init, method, body, headers, credentials: 'same-origin' }); }
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
  const source = asJson(payload);
  if (source.code === 'unavailable') return '商品或优惠券信息暂不可用，请稍后重试。';
  return typeof source.message === 'string' && source.message.trim() ? source.message : fallback;
}
async function readCoupon(id: number): Promise<Json> {
  if (!id) return {};
  const response = await nativeFetch(`/api/admin/coupons/${id}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) {
    const payload = await response.clone().json().catch(() => null);
    if (asJson(payload).code === 'unavailable') throw new Error('商品或优惠券信息暂不可用，请稍后重试。');
    throw new Error(`优惠券读取失败（HTTP ${response.status}）`);
  }
  const payload = asJson(await response.json()); const coupon = asJson(payload.coupon || payload.item || payload);
  const refs = Array.isArray(coupon.target_refs) ? coupon.target_refs.map((value) => String(value)) : [];
  if (!refs.length || Array.isArray(coupon.products) && coupon.products.length) return coupon;
  const names = Array.isArray(coupon.target_products) ? coupon.target_products : [];
  const exact = names.length === refs.length && names.every((value, index) => String(asJson(value).target_ref || '') === refs[index]);
  couponTargetsWithUnverifiedPrice.clear();
  coupon.products = refs.map((ref, index) => {
    const target = exact ? asJson(names[index]) : {};
    const available = target.state === 'available' && typeof target.name === 'string' && target.name.trim();
    couponTargetsWithUnverifiedPrice.add(ref);
    return {
      target_ref: ref,
      title: available ? target.name.trim() : target.state === 'not_found' ? '商品已删除或不可用' : '商品目录暂不可读取',
      product_type: ref.startsWith('service_period:') ? 'service_period' : ref.startsWith('standard_product:') ? 'standard_product' : 'unknown',
      status: available ? '当前商品' : target.state === 'not_found' ? '商品已删除或不可用' : '目录暂不可用',
    };
  });
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
    if (couponMutation(absolute, method)) {
      const receipt = (payload as Json).coupon as Json | undefined;
      const receiptID = Number(receipt?.id);
      const expectedID = absolute.pathname.match(/^\/api\/admin\/coupons\/([1-9][0-9]*)$/)?.[1];
      if (!Number.isSafeInteger(receiptID) || receiptID <= 0 || expectedID && receiptID !== Number(expectedID)) throw new Error('保存结果无法确认；请保持内容不变后重试或先返回列表核对。');
    }
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
  // Only the controls wired by this exact donor runtime are owned. Register
  // them after initialization succeeds so shared feedback cannot claim that
  // a real save is unavailable; unrelated actions retain its normal guard.
  const markOwned = () => {
    document.querySelector('#stage')?.querySelectorAll<HTMLElement>('#saveCoupon,#openProductSelector,#couponProductSearchButton,#couponProductPrev,#couponProductNext,#confirmProductSelection,[data-close-product-dialog],[data-product-type],#selectedProductList [data-remove-product]').forEach((node) => {
      (node as HTMLElement & { __dcBound?: boolean }).__dcBound = true;
      node.dataset.capabilityState = 'real';
      node.removeAttribute('aria-description');
    });
  };
  markOwned();
  const selected = document.getElementById('selectedProductList');
  if (selected) new MutationObserver(markOwned).observe(selected, { childList: true, subtree: true });
}

function hideTechnicalTargetReferences(): void {
  document.querySelectorAll<HTMLElement>('#selectedProductList .coupon-selected-copy small').forEach((node) => {
    const before = node.textContent || '';
    const targetRef = before.match(/(?:standard_product|service_period):[1-9][0-9]*/)?.[0] || '';
    const after = couponTargetsWithUnverifiedPrice.has(targetRef)
      ? before.replace(/\s*·\s*(?:standard_product|service_period):[1-9][0-9]*\s*·\s*¥[^\s]+\s*$/, ' · 价格待核验')
      : before.replace(/\s*·\s*(?:standard_product|service_period):[1-9][0-9]*\s*·\s*/, ' · ');
    if (after !== before) node.textContent = after;
  });
}

function removeCouponTimeZoneLabels(): void {
  document.querySelectorAll<HTMLLabelElement>('#stage label').forEach((label) => {
    for (const node of Array.from(label.childNodes)) {
      if (node.nodeType === Node.TEXT_NODE && node.textContent?.includes('（北京时间）')) {
        node.textContent = node.textContent.replaceAll('（北京时间）', '');
      }
    }
  });
}

function couponStatusLabel(value: unknown): string {
  return ({
    draft: '草稿', published: '已发布', scheduled: '未到领取时间', active: '可领取', sold_out: '已领完',
    ended: '已结束', stopped: '已停用', archived: '已归档', deleted: '已删除',
  } as Record<string, string>)[String(value)] || '状态待确认';
}

function installCouponListBridge(): void {
  if (document.body.dataset.page !== 'coupons') return;
  const originalLoadDb = api.loadDb.bind(api);
  api.loadDb = async (context) => {
    const db = await originalLoadDb(context);
    if (context?.page !== 'coupons') return db;
    return {
      ...db,
      rows: {
        ...db.rows,
        coupons: db.rows.coupons.map((coupon) => {
          const presentation = coupon.resourceId == null ? undefined : couponPresentations.get(coupon.resourceId);
          return presentation ? { ...coupon, scope: presentation.scope, window: presentation.window } : { ...coupon, scope: '适用商品暂不可用', window: '领取时间范围暂不可用' };
        }),
      },
    } as AdminDb;
  };
  const originalRenderVals = AdminController.prototype.renderVals;
  AdminController.prototype.renderVals = function couponRenderVals() {
    const values = originalRenderVals.call(this) as Json;
    const rows = asJson(values.rows);
    if (this.page !== 'coupons' || api.mode !== 'http' || !Array.isArray(rows.coupons)) return values;
    return {
      ...values,
      rows: {
        ...rows,
        coupons: rows.coupons.map((coupon) => ({ ...asJson(coupon), displayStatus: couponStatusLabel(asJson(coupon).displayStatus) })),
      },
    };
  };
  const applyPresentation = () => {
    document.querySelectorAll('th').forEach((header) => {
      if (header.textContent?.trim() === '领取时间（北京时间）') header.textContent = '领取时间范围';
    });
    const table = document.querySelector<HTMLTableElement>('#stage table');
    if (!table) return;
    table.dataset.couponPresentationList = 'true';
    table.style.minWidth = '860px';
    const card = table.parentElement;
    if (card instanceof HTMLElement) { card.style.overflowX = 'auto'; card.style.overflowY = 'hidden'; }
  };
  new MutationObserver(applyPresentation).observe(document.documentElement, { childList: true, subtree: true });
  applyPresentation();
}

async function mountCouponEditor(): Promise<void> {
  if (mounted || mountFailed || document.body.dataset.page !== 'couponForm') return;
  const stage = document.querySelector<HTMLElement>('#stage');
  // The production Webshell keeps the immutable donor markup in #tpl and
  // leaves #stage as its neutral loading shell. Earlier fixture markup put a
  // form sentinel directly in #stage, which concealed that real-route shape.
  const donorTemplate = document.querySelector<HTMLTemplateElement>('#tpl');
  const hasFormAnchor = Boolean(stage?.querySelector('#coupon-target-refs, [data-coupon-form-mode]') || donorTemplate?.content.querySelector('#coupon-target-refs, [data-coupon-form-mode]'));
  if (!stage || !hasFormAnchor) return;
  mounted = true;
  try {
    const id = couponID(); const [raw, initial] = await Promise.all([
      nativeFetch(standardFormURL, { credentials: 'same-origin' }).then(async (response) => { if (!response.ok) throw new Error('标准优惠券表单加载失败，请刷新页面后重试。'); return response.text(); }),
      readCoupon(id),
    ]);
    await installStyles(); installAdminAPI(); stage.innerHTML = contentFromDonor(raw, initial, id); removeCouponTimeZoneLabels(); await executeDonorScript(); captureCouponDateSnapshots(initial); hideTechnicalTargetReferences();
    const selected = document.getElementById('selectedProductList');
    if (selected) new MutationObserver(hideTechnicalTargetReferences).observe(selected, { childList: true, subtree: true });
  } catch (error) {
    mounted = false; mountFailed = true; const message = error instanceof Error ? error.message : '标准优惠券表单加载失败'; const notice = document.createElement('div'); notice.setAttribute('role', 'alert'); notice.textContent = `${message}；未提交任何保存。`;
    const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = '重试加载'; retry.addEventListener('click', () => { mountFailed = false; notice.remove(); void mountCouponEditor(); }); (retry as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true; notice.append(' ', retry); stage.prepend(notice);
  }
}
new MutationObserver(() => { void mountCouponEditor(); }).observe(document.documentElement, { childList: true, subtree: true });
void mountCouponEditor();
installCouponListBridge();

const couponRuntime = window as Window & { __AICRM_TEST_COUPON_ADAPTER_ONLY__?: boolean };
if (!couponRuntime.__AICRM_TEST_COUPON_ADAPTER_ONLY__ && (document.body.dataset.page === 'coupons' || document.body.dataset.page === 'couponData')) {
  void import('../src/admin/main');
}
