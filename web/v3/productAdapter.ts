import { createFieldMappingEditor, type FieldMapping, type MappingField, type MappingPreview } from './fieldMappingEditor';
// This is the only v3-owned browser seam for the byte-frozen Product UI.
// It validates the authoritative lifecycle/sales projection, supplies Chinese
// display labels, and replaces only the ordinary-product share interaction.
import { api } from '../src/shared/api/client';
// @ts-ignore Byte-frozen controller; navigation is adapted only at the Host.
import { AdminController } from '../src/admin/controller';
import { apiRequestOptions } from '../src/api/transport';
import type { AdminDb, Product, Tone } from '../src/shared/api/types';
import { productPageDto, type AdminReadContext } from '../src/api/admin';
import { downloadQr, renderQr } from '../src/admin/sections/qr';
import { rememberActionClicks, rememberActionInputs, runAction } from './actionFeedback';
import { createTagCatalogPageLoader, unresolvedTagRecord, type TagPickerRecord } from './shared/ui/tagPickerAdapter';

type RecordValue = Record<string, unknown>;
type ProductProjection = Product & { resourceId: number };

const object = (value: unknown): RecordValue => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const nonNegativeInteger = (value: unknown, field: string): number => {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 0) throw new Error(`商品响应缺少有效 ${field}`);
  return parsed;
};

function strictProjection(value: unknown, base: Product | undefined): ProductProjection {
  const item = object(value);
  const id = Number(item.id);
  if (!Number.isSafeInteger(id) || id < 1 || !base || base.resourceId !== id) throw new Error('商品响应缺少有效 id');
  const lifecycle = item.lifecycle;
  if (lifecycle !== 'draft' && lifecycle !== 'enabled' && lifecycle !== 'disabled') throw new Error('商品响应缺少有效 lifecycle');
  if (typeof item.enabled !== 'boolean' || item.enabled !== (lifecycle === 'enabled')) throw new Error('商品状态投影矛盾');
  const adminProjection = object(item.admin_projection);
  if (typeof adminProjection.enabled !== 'boolean' || adminProjection.enabled !== item.enabled) throw new Error('商品运营投影与生命周期矛盾');
  const paid = nonNegativeInteger(item.paid_order_count, 'paid_order_count');
  const refunded = nonNegativeInteger(item.refund_order_count, 'refund_order_count');
  const sold = nonNegativeInteger(item.sold_count, 'sold_count');
  if (sold !== Math.max(0, paid - refunded)) throw new Error('商品销量投影矛盾');
  const labels = { draft: '草稿', enabled: '已启用', disabled: '已停用' } as const;
  const tones: Record<typeof lifecycle, Tone> = { draft: 'warn', enabled: 'ok', disabled: 'gray' };
  return { ...base, resourceId: id, lifecycle, status: labels[lifecycle], tone: tones[lifecycle], sold: String(sold) };
}

function externalPushProjection(value: unknown, productID: number): NonNullable<Product['externalPush']> {
  const item = object(value);
  if (Number(item.product_id) !== productID || item.product_kind !== 'wechat_pay' || typeof item.enabled !== 'boolean') {
    throw new Error('商品外推配置响应不完整');
  }
  const reference = typeof item.configuration_reference === 'string' ? item.configuration_reference : '';
  const updatedAt = typeof item.updated_at === 'string' ? item.updated_at : '';
  if (!item.enabled && reference !== '') throw new Error('商品外推配置状态矛盾');
  return { enabled: item.enabled, configurationReference: reference, updatedAt };
}

async function readJSON(path: string): Promise<unknown> {
  const response = await fetch(path, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } });
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new Error(`商品读取失败（HTTP ${response.status}）`); }
  if (!response.ok) throw new Error(`商品读取失败（HTTP ${response.status}）`);
  return payload;
}

let loadedProducts: ProductProjection[] = [];
const openedProductPayloads = new Map<number, RecordValue>();
const purchaseActionByProduct = new Map<number, { enabled: boolean; mode: '' | 'qr' | 'redirect' }>();
const productLifecycleKeys = new Map<string, string>();

type ProductSaveContext = {
  productID?: number;
  opened: RecordValue | undefined;
  subjectKey: string;
  externalPushKey: string;
  createdProductID?: number;
  createdProduct?: RecordValue;
  externalPushAttempted: boolean;
};

type PendingExternalPush = {
  productID: number;
  subjectFingerprint: string;
  rawProduct: RecordValue;
  externalPushKey: string;
};

let productSaveContext: ProductSaveContext | undefined;
let productSaveInFlight: Promise<Product> | undefined;
const productSaveKeys = new Map<string, { subjectKey: string; externalPushKey: string }>();
let pendingExternalPush: PendingExternalPush | undefined;

function newIdempotencyKey(scope: string): string {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${scope}-${suffix}`;
}

function productLifecycleKey(productID: number, version: number, enabled: boolean): string {
  const operation = enabled ? 'enable' : 'disable';
  const identity = `${productID}:${version}:${operation}`;
  let key = productLifecycleKeys.get(identity);
  if (!key) {
    key = newIdempotencyKey(`product-${operation}`);
    productLifecycleKeys.set(identity, key);
  }
  return key;
}

function stableProductSaveKeys(input: Parameters<typeof api.saveProduct>[0]): { subjectKey: string; externalPushKey: string } {
  // One click and its recovery retry must keep their original keys.  The
  // key is intentionally held only in this page runtime: it never enters a
  // URL, log, or persisted product field.
  const key = JSON.stringify([input, input.id ? openedProductPayloads.get(input.id)?.version ?? periodicSnapshots.get(input.id)?.version : undefined]);
  let saved = productSaveKeys.get(key);
  if (!saved) {
    saved = { subjectKey: newIdempotencyKey('product-save'), externalPushKey: newIdempotencyKey('product-external-push') };
    productSaveKeys.set(key, saved);
  }
  return saved;
}

function subjectFingerprint(input: Parameters<typeof api.saveProduct>[0]): string {
  const { id: _id, externalPush: _externalPush, ...subject } = input;
  return JSON.stringify(subject);
}

async function recoverExternalPush(input: Parameters<typeof api.saveProduct>[0], pending: PendingExternalPush): Promise<Product> {
  if (!input.externalPush) throw new Error('商品主体已保存；请刷新后补充外推配置。');
  const response = await fetch(`/api/admin/wechat-pay/products/${pending.productID}/external-push`, apiRequestOptions({
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': pending.externalPushKey },
    body: JSON.stringify({
      enabled: input.externalPush.enabled,
      configuration_reference: input.externalPush.enabled ? input.externalPush.configurationReference : undefined,
    }),
  }));
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new Error(`商品外推配置保存失败（HTTP ${response.status}）`); }
  if (!response.ok) throw new Error(`商品外推配置保存失败（HTTP ${response.status}）`);
  // Product DTO validation also verifies that the response is bound to this
  // newly created subject before the editor navigates away.
  const product = productPageDto(pending.rawProduct, payload);
  if (product.resourceId !== pending.productID) throw new Error('商品外推配置响应未绑定已保存商品');
  pendingExternalPush = undefined;
  return product;
}

const donorFetch = globalThis.fetch.bind(globalThis);

type MaterialPickerItem = { library_id: number; title?: string; subtitle?: string; thumbnail_url?: string; metadata?: Record<string, unknown> };
type StandardWindow = Window & { AdminApi?: { requestJson?: (path: string) => Promise<unknown> }; AICRMStandardComponents?: { ready?: () => Promise<void> } };

async function materialPickerItems(path: string): Promise<unknown> {
  const url = new URL(path, location.origin);
  if (url.pathname !== '/api/admin/material-picker/items') throw new Error('素材选择请求不受支持');
  const type = url.searchParams.get('type');
  const endpoint = type === 'image' ? '/api/admin/image-library' : type === 'miniprogram' ? '/api/admin/miniprogram-library' : type === 'attachment' ? '/api/admin/attachment-library' : type === 'group_invite' ? '/api/admin/group-invite-library' : '';
  if (!endpoint) throw new Error('素材类型不受支持');
  const q = url.searchParams.get('q') || '';
  const items: RecordValue[] = [];
  for (let offset = 0; ; ) {
    const source = new URL(endpoint, location.origin);
    source.searchParams.set('limit', '100'); source.searchParams.set('offset', String(offset)); source.searchParams.set('q', q); source.searchParams.set('enabled_only', 'true');
    const response = await donorFetch(source, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(`素材目录读取失败（HTTP ${response.status}）`);
    const page = list(object(payload).items).map(object);
    items.push(...page);
    const next = Number(object(payload).next_offset);
    if (object(payload).has_more !== true || !Number.isSafeInteger(next) || next <= offset) break;
    offset = next;
  }
  return { items: items.map((item) => {
    const id = Number(item.id ?? item.library_id);
    const originalURL = String(item.original_url ?? item.variant_url ?? (type === 'image' ? `/api/admin/image-library/${id}/variants/original` : ''));
    return { type, library_id: id, title: String(item.name ?? item.title ?? item.file_name ?? `素材 ${id}`), subtitle: String(item.description ?? item.category ?? ''), thumbnail_url: String(item.thumb_320_url ?? item.thumbnail_url ?? item.variant_url ?? ''), enabled: item.enabled !== false, selectable: item.enabled !== false, metadata: { ...item, original_url: originalURL } };
  }) };
}

function installMaterialPickerTransport(): void {
  const target = window as StandardWindow;
  const prior = target.AdminApi?.requestJson;
  target.AdminApi ||= {};
  target.AdminApi.requestJson = async (path: string): Promise<unknown> => {
    if (new URL(path, location.origin).pathname === '/api/admin/material-picker/items') return materialPickerItems(path);
    if (prior) return prior(path);
    const response = await donorFetch(path, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } });
    const payload = await response.json();
    if (!response.ok) throw new Error(`请求失败（HTTP ${response.status}）`);
    return payload;
  };
}
installMaterialPickerTransport();

const periodicSnapshots = new Map<number, RecordValue>();

// Distribution policy is part of the Product command, never a follow-up
// browser write.  The Product owner accepts the same wire object for ordinary
// and service-period products; the UI only converts the human percentage into
// the integer basis-points value owned by the server contract.
type DistributionPolicy = { enabled: boolean; commissionRateBasisPoints: number; waitDays: number; version: number };
const defaultDistributionPolicy = (): DistributionPolicy => ({ enabled: false, commissionRateBasisPoints: 0, waitDays: 7, version: 0 });

function distributionPolicy(raw: unknown): DistributionPolicy {
  if (raw === undefined || raw === null) return defaultDistributionPolicy();
  const value = object(raw);
  const enabled = value.enabled;
  const rate = Number(value.commission_rate_basis_points);
  const days = Number(value.wait_days);
  const version = Number(value.version);
  if (typeof enabled !== 'boolean' || !Number.isSafeInteger(rate) || rate < 0 || rate > 3000 || !Number.isSafeInteger(days) || days < 0 || days > 29 || !Number.isSafeInteger(version) || version < 0) {
    throw new Error('商品分销设置响应无效');
  }
  return { enabled, commissionRateBasisPoints: rate, waitDays: days, version };
}

function productSaleDimension(): string | undefined {
  const prefix = productPrefix();
  return prefix === 'pf' ? 'product-sale' : prefix === 'spf' ? 'sp-sale' : undefined;
}

function distributionPolicyDimensionSelected(): boolean {
  const sale = productSaleDimension();
  if (!sale) return false;
  const nav = document.querySelector<HTMLAnchorElement>(`a[href="#${sale}"]`)?.parentElement;
  // The frozen new-product form reaches its default sale panel before the
  // Host's dimension observer records a selection. That initial state is
  // still sale information; later tab clicks always set this dataset.
  return !nav?.dataset.productDimension || nav.dataset.productDimension === sale;
}

function currentDistributionPolicy(): DistributionPolicy | undefined {
  // Policy is deliberately part of the sale dimension only. The frozen form
  // sends one Product command for every dimension, but the Product contract
  // treats an omitted policy as "leave the saved policy unchanged".
  if (!distributionPolicyDimensionSelected()) return undefined;
  const host = document.querySelector<HTMLElement>('[data-distribution-policy]');
  // A new product genuinely has no stored policy. An existing editor without
  // its authoritative product payload is still loading (or failed), and must
  // never overwrite the saved policy with the new-product default on save.
  if (!host) {
    if (!newProductEditor()) throw new Error('分销设置尚未加载，未提交保存。');
    return defaultDistributionPolicy();
  }
  const enabled = host.querySelector<HTMLInputElement>('[data-distribution-policy-enabled]')?.checked === true;
  const rateInput = host.querySelector<HTMLInputElement>('[data-distribution-policy-rate]');
  const daysInput = host.querySelector<HTMLInputElement>('[data-distribution-policy-wait-days]');
  const version = Number(host.dataset.distributionPolicyVersion || '0');
  if (!rateInput || !daysInput || rateInput.value.trim() === '' || daysInput.value.trim() === '') throw new Error('分销设置无效：请填写佣金比例和等待天数。');
  const percentage = Number(rateInput.value);
  const waitDays = Number(daysInput.value);
  // Decimal percentage is converted exactly to basis points.  Reject an
  // imprecise browser value rather than silently rounding a financial policy.
  const basisPoints = Math.round(percentage * 100);
  if (!Number.isFinite(percentage) || percentage < 0 || percentage > 30 || Math.abs(percentage * 100 - basisPoints) > 1e-8 || !Number.isSafeInteger(basisPoints) || !Number.isSafeInteger(waitDays) || waitDays < 0 || waitDays > 29 || !Number.isSafeInteger(version) || version < 0) {
    throw new Error('分销设置无效：佣金比例为 0.00%～30.00%，等待天数为 0～29 天。');
  }
  return { enabled, commissionRateBasisPoints: basisPoints, waitDays, version };
}

function newProductEditor(): boolean {
  const prefix = productPrefix();
  if (!prefix) return false;
  try { return !new URLSearchParams(location.search).has('id'); } catch { return false; }
}

function editorDistributionPolicy(): DistributionPolicy {
  const route = productEditorRoute();
  if (!route) return defaultDistributionPolicy();
  const raw = route.prefix === 'pf' ? openedProductPayloads.get(route.id) : periodicSnapshots.get(route.id);
  if (!raw) throw new Error('分销设置正在读取，请稍候后再保存。');
  return distributionPolicy(raw?.distribution_policy);
}

function mountDistributionPolicyControls(): void {
  if (typeof document === 'undefined' || !document.documentElement) return;
  const route = productEditorRoute();
  if (document.querySelector('[data-distribution-policy]')) return;
  const prefix = route?.prefix || productPrefix();
  if (!prefix || (!route && !newProductEditor())) return;
  const snapshot = route ? prefix === 'pf' ? openedProductPayloads.get(route.id) : periodicSnapshots.get(route.id) : undefined;
  // The observer can fire while a frozen donor form is still loading. Wait for
  // its authoritative snapshot hook instead of mutating the DOM with an error,
  // which would trigger the observer again and fabricate a draft policy.
  if (route && !snapshot) return;
  const anchor = document.getElementById(prefix === 'pf' ? 'product-sale' : 'sp-sale');
  if (!anchor) return;
  let policy: DistributionPolicy;
  try { policy = route ? distributionPolicy(snapshot?.distribution_policy) : defaultDistributionPolicy(); } catch (error) { showMessage(error instanceof Error ? error.message : '分销设置读取失败'); return; }
  const host = document.createElement('section');
  host.className = 'product-distribution-policy';
  host.dataset.distributionPolicy = '';
  host.dataset.distributionPolicyVersion = String(policy.version);
  const head = document.createElement('div'); head.className = 'product-distribution-policy__head';
  const intro = document.createElement('div');
  const heading = document.createElement('h3'); heading.className = 'product-distribution-policy__title'; heading.textContent = '分销设置';
  const hint = document.createElement('p'); hint.className = 'product-distribution-policy__hint'; hint.textContent = '仅本人有效购买过本商品，才可参与推广。';
  intro.append(heading, hint);
  const toggle = document.createElement('label'); toggle.className = 'product-distribution-policy__toggle';
  const enabled = document.createElement('input'); enabled.type = 'checkbox'; enabled.dataset.distributionPolicyEnabled = '';
  toggle.append(enabled, document.createTextNode('启用分销'));
  head.append(intro, toggle);
  const fields = document.createElement('div'); fields.className = 'product-distribution-policy__fields'; fields.dataset.distributionPolicyFields = '';
  const policyField = (label: string, name: string, max: string, step: string, inputmode: 'decimal' | 'numeric'): HTMLInputElement => {
    const field = document.createElement('label'); field.className = 'product-distribution-policy__field'; field.append(document.createTextNode(label));
    const input = document.createElement('input'); input.name = name; input.type = 'number'; input.min = '0'; input.max = max; input.step = step; input.inputMode = inputmode; field.append(input); fields.append(field); return input;
  };
  const rate = policyField('佣金比例（%）', 'distribution-policy-rate', '30', '0.01', 'decimal'); rate.dataset.distributionPolicyRate = '';
  const days = policyField('退款复核等待（天）', 'distribution-policy-wait-days', '29', '1', 'numeric'); days.dataset.distributionPolicyWaitDays = '';
  host.append(head, fields);
  enabled.checked = policy.enabled;
  rate.value = (policy.commissionRateBasisPoints / 100).toFixed(2);
  days.value = String(policy.waitDays);
  const policyFields = host.querySelector<HTMLElement>('[data-distribution-policy-fields]')!;
  const update = () => { policyFields.classList.toggle('is-disabled', !enabled.checked); };
  enabled.addEventListener('change', update); update();
  // Product editing owns policy only. Distributor application, link copying,
  // and QR entry points belong to the public Distribution centre.
  anchor.firstElementChild?.after(host);
}

function syncDistributionPolicyControls(raw: unknown, submitted: DistributionPolicy): void {
  if (typeof document === 'undefined' || !document.documentElement) return;
  const host = document.querySelector<HTMLElement>('[data-distribution-policy]');
  if (!host || raw === undefined || raw === null) return;
  let policy: DistributionPolicy;
  try { policy = distributionPolicy(raw); } catch (error) { showMessage(error instanceof Error ? error.message : '分销设置读取失败'); return; }
  const enabled = host.querySelector<HTMLInputElement>('[data-distribution-policy-enabled]');
  const rate = host.querySelector<HTMLInputElement>('[data-distribution-policy-rate]');
  const days = host.querySelector<HTMLInputElement>('[data-distribution-policy-wait-days]');
  if (!enabled || !rate || !days) return;
  const currentRate = Number(rate.value);
  const currentDays = Number(days.value);
  const currentMatchesSubmission = enabled.checked === submitted.enabled && Number.isFinite(currentRate) &&
    Math.round(currentRate * 100) === submitted.commissionRateBasisPoints && Number.isSafeInteger(currentDays) &&
    currentDays === submitted.waitDays && Number(host.dataset.distributionPolicyVersion || '0') === submitted.version;
  host.dataset.distributionPolicyVersion = String(policy.version);
  // A user may continue editing while the Product command is in flight. Do
  // not replace that later sale draft with the response for an earlier save;
  // only advance its base version for the next explicit save.
  if (!currentMatchesSubmission) return;
  enabled.checked = policy.enabled;
  rate.value = (policy.commissionRateBasisPoints / 100).toFixed(2);
  days.value = String(policy.waitDays);
  host.querySelector<HTMLElement>('[data-distribution-policy-fields]')?.classList.toggle('is-disabled', !policy.enabled);
}

function mountNewServicePeriodDuration(): void {
  if (typeof document === 'undefined' || !document.documentElement || !newProductEditor() || productPrefix() !== 'spf' || document.getElementById('spfDurationDays')) return;
  const sale = document.getElementById('sp-sale');
  const fields = Array.from(sale?.children || []).find((node) => node instanceof HTMLElement && node.style.display === 'grid' && node.style.gridTemplateColumns) as HTMLElement | undefined;
  if (!fields) return;
  const field = document.createElement('label');
  field.style.cssText = 'display:grid;gap:6px'; field.textContent = '服务周期（天）';
  const input = document.createElement('input');
  input.id = 'spfDurationDays'; input.type = 'number'; input.min = '1'; input.step = '1'; input.inputMode = 'numeric'; input.required = true;
  input.style.cssText = 'width:100%;min-height:36px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;padding:8px 10px;font-size:13px;box-sizing:border-box';
  field.append(input); fields.append(field);
}

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : undefined;
  const url = new URL(request?.url || String(input), location.origin);
  const method = (init?.method || request?.method || 'GET').toUpperCase();
  const context = productSaveContext;
  const periodicMatch = url.origin === location.origin && url.pathname.match(/^\/api\/admin\/service-period-products\/([1-9][0-9]*)$/);
  const periodicCollection = url.origin === location.origin && url.pathname === '/api/admin/service-period-products';

  // The donor save helper re-reads immediately before PUT and would otherwise
  // silently replace the version observed when this editor opened.  Replay
  // the verified opening snapshot only during that write, so a 409 remains a
  // real concurrent-edit signal rather than an implicit last-write-wins save.
  if (context?.productID && method === 'GET' && url.pathname === `/api/v1/products/${context.productID}` && context.opened) {
    return new Response(JSON.stringify(context.opened), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }

  let nextInit = init;
  if (context && method !== 'GET' && method !== 'HEAD') {
    const isSubject = url.pathname === '/api/v1/products' || url.pathname === `/api/v1/products/${context.productID}` ||
      url.pathname === '/api/admin/service-period-products' || url.pathname === `/api/admin/service-period-products/${context.productID}`;
    const isExternalPush = /\/api\/admin\/wechat-pay\/products\/\d+\/external-push$/.test(url.pathname);
    if (isSubject || isExternalPush) {
      const headers = new Headers(request?.headers);
      new Headers(init?.headers).forEach((value, name) => headers.set(name, value));
      headers.set('Idempotency-Key', isSubject ? context.subjectKey : context.externalPushKey);
      nextInit = { ...init, headers };
      if (isExternalPush) context.externalPushAttempted = true;
    }
  }

  if (periodicMatch && method === 'PUT') {
    const prior = periodicSnapshots.get(Number(periodicMatch[1]));
    const duration = Number(prior?.duration_days);
    if (!Number.isSafeInteger(duration) || duration < 1) throw new Error('周期商品缺少已保存的服务天数，请刷新后重试');
    const body = JSON.parse(String(nextInit?.body || '{}'));
    nextInit = { ...nextInit, body: JSON.stringify({ ...body, duration_days: duration, expected_version: prior?.version }) };
  }
  if (periodicCollection && method === 'POST') {
    const input = document.getElementById('spfDurationDays') as HTMLInputElement | null;
    const duration = Number(input?.value);
    if (!Number.isSafeInteger(duration) || duration < 1) throw new Error('请填写正整数服务周期天数。');
    const body = JSON.parse(String(nextInit?.body || '{}'));
    nextInit = { ...nextInit, body: JSON.stringify({ ...body, duration_days: duration }) };
  }
  if (isProductSubjectWrite(url, method)) nextInit = adaptPurchaseActionWrite(nextInit);
  if (isDistributionProductSubjectWrite(url, method)) nextInit = adaptDistributionPolicyWrite(nextInit);
  let submittedDistributionPolicy: DistributionPolicy | undefined;
  if (isDistributionProductSubjectWrite(url, method) && typeof nextInit?.body === 'string') {
    try {
      const body = object(JSON.parse(nextInit.body));
      if (Object.prototype.hasOwnProperty.call(body, 'distribution_policy')) submittedDistributionPolicy = distributionPolicy(body.distribution_policy);
    } catch { /* the Product API validates malformed JSON */ }
  }
  const response = await donorFetch(input, nextInit);
  if ((periodicMatch && (method === 'GET' || method === 'PUT')) || (periodicCollection && method === 'POST')) {
    if (response.ok) {
      const value = object(await response.clone().json());
      const product = object(value.product || value);
      const id = Number(product.service_product_id || product.id);
      if (Number.isSafeInteger(id) && id > 0) {
        // POST, GET, and PUT all return an authoritative service-period
        // snapshot. Keep it for the next saved dimension; never synthesize it
        // from the still-editable donor form.
        if (method === 'POST' || method === 'PUT' || !periodicSnapshots.has(id)) periodicSnapshots.set(id, product);
        mountDistributionPolicyControls();
        if (submittedDistributionPolicy) syncDistributionPolicyControls(product.distribution_policy, submittedDistributionPolicy);
        const action = object(product.admin_projection);
        purchaseActionByProduct.set(id, {
          enabled: action.purchase_action_enabled === true,
          mode: action.purchase_action_mode === 'qr' || action.purchase_action_mode === 'redirect' ? action.purchase_action_mode : '',
        });
      }
    }
  }

  if (url.origin === location.origin && method === 'PUT' && /^\/api\/v1\/products\/[1-9][0-9]*$/.test(url.pathname) && response.ok) {
    const saved = object(await response.clone().json());
    const id = Number(url.pathname.split('/').pop());
    if (Number(saved.id) === id && Number.isSafeInteger(Number(saved.version))) {
      openedProductPayloads.set(id, saved);
      if (submittedDistributionPolicy) syncDistributionPolicyControls(saved.distribution_policy, submittedDistributionPolicy);
      if (context?.productID === id) { context.createdProductID = id; context.createdProduct = saved; }
    }
  }
  if (context && method === 'POST' && url.pathname === '/api/v1/products' && response.ok) {
    try {
      const value = await response.clone().json();
      const created = object(value);
      const id = Number(created.id);
      if (Number.isSafeInteger(id) && id > 0) {
        context.createdProductID = id;
        context.createdProduct = created;
        openedProductPayloads.set(id, created);
        if (submittedDistributionPolicy) syncDistributionPolicyControls(created.distribution_policy, submittedDistributionPolicy);
      }
    } catch {
      // The frozen DTO parser will surface the malformed create response.
    }
  }
  return response;
};

const donorSaveProduct = api.saveProduct.bind(api);
api.saveProduct = (input) => {
  // The frozen controller does not disable its save button.  Deduplicate every
  // in-page click until the current operation has reached a known result.
  if (productSaveInFlight) return productSaveInFlight;
  const recovered = pendingExternalPush;
  if (recovered && input.id === recovered.productID && subjectFingerprint(input) === recovered.subjectFingerprint) {
    productSaveInFlight = recoverExternalPush(input, recovered);
    void productSaveInFlight.then(
      () => { productSaveInFlight = undefined; },
      () => { productSaveInFlight = undefined; },
    );
    return productSaveInFlight;
  }
  const keys = stableProductSaveKeys(input);
  const productID = input.id;
  const context: ProductSaveContext = {
    productID,
    opened: productID ? openedProductPayloads.get(productID) : undefined,
    subjectKey: keys.subjectKey,
    externalPushKey: keys.externalPushKey,
    externalPushAttempted: false,
  };
  productSaveInFlight = (async () => {
    productSaveContext = context;
    try {
      const saved = await donorSaveProduct(input);
      // An editor may intentionally change the subject after an earlier
      // external-push failure.  That normal PUT is still an edit of the same
      // product, never a second create; its completed push supersedes the
      // page-local recovery marker.
      if (pendingExternalPush?.productID === input.id) pendingExternalPush = undefined;
      return saved;
    } catch (error) {
      // Subject creation/update and external-push configuration are separate writes.
      // Recover the confirmed subject rather than submitting it again when
      // the later configuration write fails. Keep the original push key.
      if (context.createdProductID && context.createdProduct && context.externalPushAttempted) {
        const retry = new URL(location.href);
        retry.searchParams.set('id', String(context.createdProductID));
        history.replaceState(null, '', retry.pathname + retry.search + retry.hash);
        const created = productPageDto(context.createdProduct);
        const createdVersion = created.version;
        if (created.resourceId !== context.createdProductID || createdVersion == null || !Number.isSafeInteger(createdVersion) || createdVersion < 1) {
          throw new Error('商品主体已保存，但响应缺少可恢复的 ID 或版本；请刷新后核对。');
        }
        openedProductPayloads.set(context.createdProductID, context.createdProduct);
        pendingExternalPush = {
          productID: context.createdProductID,
          subjectFingerprint: subjectFingerprint(input),
          rawProduct: context.createdProduct,
          externalPushKey: context.externalPushKey,
        };
        throw new Error(`商品主体已保存（ID ${context.createdProductID}）；外推配置保存失败，可直接重试。`);
      }
      throw error;
    } finally {
      productSaveContext = undefined;
    }
  })();
  void productSaveInFlight.then(
    () => { productSaveInFlight = undefined; },
    () => { productSaveInFlight = undefined; },
  );
  return productSaveInFlight;
};

const donorSaveServiceProduct = api.saveServiceProduct.bind(api);
api.saveServiceProduct = (input) => {
  if (productSaveInFlight) return productSaveInFlight;
  const keys = stableProductSaveKeys(input);
  const productID = input.id;
  const context: ProductSaveContext = {
    productID,
    opened: productID ? periodicSnapshots.get(productID) : undefined,
    subjectKey: keys.subjectKey,
    externalPushKey: keys.externalPushKey,
    externalPushAttempted: false,
  };
  productSaveInFlight = (async () => {
    productSaveContext = context;
    try { return await donorSaveServiceProduct(input); }
    finally { productSaveContext = undefined; }
  })();
  void productSaveInFlight.then(
    () => { productSaveInFlight = undefined; },
    () => { productSaveInFlight = undefined; },
  );
  return productSaveInFlight;
};

const donorLoadDb = api.loadDb.bind(api);
api.loadDb = async (context?: AdminReadContext): Promise<AdminDb> => {
  if (context?.page === 'productForm' && /^[1-9][0-9]*$/.test(context.id || '')) {
    const productID = Number(context.id);
    const page = (name: AdminReadContext['page']): AdminReadContext => ({ ...context, page: name, id: undefined });
    const optionalChannels = donorLoadDb(page('channels')).catch((error: unknown) => {
      const failure = object(error);
      const details = object(failure.details);
      const expectedCatalogFailure = failure.status === 400 && details.code === 'MALFORMED_REQUEST' ||
        failure.status === 503 && details.code === 'DEPENDENCY_UNAVAILABLE';
      if (expectedCatalogFailure) return undefined;
      throw error;
    });
    // The byte-frozen donor loader couples Product forms to the whole Channel
    // catalog. Compose the form from independent local reads so malformed
    // imported Channel rows cannot hide an otherwise valid Product definition.
    const [db, imageDb, tagDb, channelDb, rawProduct, rawExternalPush] = await Promise.all([
      donorLoadDb(page('products')),
      donorLoadDb(page('images')),
      donorLoadDb(page('tags')),
      optionalChannels,
      readJSON(`/api/v1/products/${productID}`),
      readJSON(`/api/admin/wechat-pay/products/${productID}/external-push`),
    ]);
    db.rows.images = imageDb.rows.images;
    db.tagGroups = tagDb.tagGroups;
    db.wecomTags = tagDb.wecomTags;
    db.rows.channels = channelDb?.rows.channels || [];
    const base = db.rows.products.find((item) => item.resourceId === productID);
    const product = strictProjection(rawProduct, base);
    const rawAction = object(object(rawProduct).admin_projection);
    purchaseActionByProduct.set(productID, {
      enabled: rawAction.purchase_action_enabled === true,
      mode: rawAction.purchase_action_mode === 'qr' || rawAction.purchase_action_mode === 'redirect' ? rawAction.purchase_action_mode : '',
    });
    openedProductPayloads.set(productID, object(rawProduct));
    // The donor can render #product-action before this authoritative Product
    // payload is saved locally. Mount explicitly after the snapshot write so
    // a revisit cannot miss the policy controls or application link.
    mountDistributionPolicyControls();
    product.externalPush = externalPushProjection(rawExternalPush, productID);
    loadedProducts = [product];
    db.rows.products = loadedProducts;
    db.rows.orderKv = [];
    return db;
  }

  const db = await donorLoadDb(context);
  if (context?.page !== 'products') return db;
  let rawItems: unknown[];
  rawItems = list(object(await readJSON('/api/v1/products')).items);
  const byID = new Map(db.rows.products.map((item) => [item.resourceId, item]));
  for (const item of rawItems) {
    const raw = object(item);
    const id = Number(raw.id);
    if (Number.isSafeInteger(id) && id > 0) openedProductPayloads.set(id, raw);
  }
  loadedProducts = rawItems.map((item) => strictProjection(item, byID.get(Number(object(item).id))));
  db.rows.products = loadedProducts;
  return db;
};

async function readShare(product: ProductProjection): Promise<string> {
  const response = await fetch(`/api/admin/wechat-pay/products/${product.resourceId}/share`, { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' } });
  let payload: RecordValue;
  try { payload = object(await response.json()); } catch { throw new Error(`商品分享地址读取失败（HTTP ${response.status}）`); }
  if (response.status === 409 && (payload.code === 'product_not_enabled' || payload.error === 'product_not_enabled')) throw new Error('请先启用商品');
  if (!response.ok) throw new Error(`商品分享地址读取失败（HTTP ${response.status}）`);
  const path = typeof payload.purchase_url === 'string' ? payload.purchase_url : '';
  if (payload.product_id !== product.resourceId || payload.lifecycle !== 'enabled' || payload.available !== true || path !== `/p/${product.resourceId}` || payload.qr_code_url != null) throw new Error('商品分享响应不完整或越过站内边界');
  const url = new URL(path, location.origin);
  if (url.origin !== location.origin || url.pathname !== path || url.search || url.hash) throw new Error('商品分享地址必须是当前站点的公开路径');
  return url.toString();
}

function button(label: string, ownerDocument: Document = document): HTMLButtonElement {
  const node = ownerDocument.createElement('button');
  // The shared feedback delegate recognizes this existing V3 Host binding and
  // must not relabel a real HTTP action as backend_blocked.
  (node as HTMLButtonElement & { __dcBound?: boolean }).__dcBound = true;
  node.type = 'button';
  node.textContent = label;
  node.style.cssText = 'height:34px;padding:0 14px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;color:#1F2329;font-size:13px;cursor:pointer';
  return node;
}

function showMessage(message: string, success = false): void {
  const previous = document.getElementById('product-v3-toast');
  previous?.remove();
  const toast = document.createElement('div');
  toast.id = 'product-v3-toast';
  toast.setAttribute('role', 'alert');
  toast.textContent = message;
  toast.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  if (success) toast.style.background = '#16803C';
  document.body.appendChild(toast);
  window.setTimeout(() => toast.remove(), 5000);
}

function showShare(product: ProductProjection, url: string): void {
  document.getElementById('product-share-overlay')?.remove();
  const overlay = document.createElement('div');
  overlay.id = 'product-share-overlay';
  overlay.style.cssText = 'position:fixed;inset:0;z-index:10001;display:grid;place-items:center;background:rgba(15,23,42,.38);padding:20px';
  const panel = document.createElement('section');
  panel.setAttribute('role', 'dialog');
  panel.setAttribute('aria-modal', 'true');
  panel.style.cssText = 'width:min(520px,100%);border-radius:12px;background:#fff;padding:22px;box-shadow:0 20px 60px rgba(0,0,0,.22);box-sizing:border-box';
  const title = document.createElement('h2');
  title.textContent = `商品分享 · ${product.name}`;
  title.style.cssText = 'margin:0 0 16px;font-size:18px';
  const input = document.createElement('input');
  input.readOnly = true;
  input.value = url;
  input.style.cssText = 'width:100%;height:38px;padding:0 10px;border:1px solid #DEE0E3;border-radius:6px;box-sizing:border-box';
  const qr = document.createElement('div');
  qr.id = 'shareQrBox';
  qr.style.cssText = 'width:220px;height:220px;margin:18px auto';
  const actions = document.createElement('div');
  actions.style.cssText = 'display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap';
  const copy = button('复制链接');
  const preview = button('预览');
  const save = button('保存二维码');
  const close = button('关闭');
  copy.addEventListener('click', () => void navigator.clipboard?.writeText(url).catch(() => undefined));
  preview.addEventListener('click', () => window.open(url, '_blank', 'noopener,noreferrer'));
  save.addEventListener('click', () => downloadQr(url, `${product.code || product.resourceId}-qr.svg`));
  close.addEventListener('click', () => overlay.remove());
  overlay.addEventListener('click', (event) => { if (event.target === overlay) overlay.remove(); });
  actions.append(copy, preview, save, close);
  panel.append(title, input, qr, actions);
  overlay.appendChild(panel);
  document.body.appendChild(overlay);
  renderQr(qr, url, '商品分享');
}

document.addEventListener('click', (event) => {
  if (document.body.dataset.page !== 'products') return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const share = target.closest('button');
  const row = share?.closest('tbody tr');
  if (!share || !row || share.textContent?.trim() !== '分享') return;
  const rowIndex = Array.from(row.parentElement?.querySelectorAll(':scope > tr') || []).indexOf(row);
  const product = loadedProducts[rowIndex];
  event.preventDefault();
  event.stopImmediatePropagation();
  if (!product) return showMessage('商品缺少服务端 ID');
  void readShare(product).then((url) => showShare(product, url)).catch((error) => showMessage(error instanceof Error ? error.message : '分享地址读取失败'));
}, true);

async function toggleProductLifecycle(button: HTMLButtonElement, product: ProductProjection): Promise<void> {
  const opened = openedProductPayloads.get(product.resourceId);
  const version = Number(opened?.version ?? product.version);
  if (!Number.isSafeInteger(version) || version < 1) throw new Error('商品缺少打开时版本，请刷新后重试');
  const enabled = product.lifecycle !== 'enabled';
  const action = enabled ? 'enable' : 'disable';
  button.disabled = true;
  button.textContent = enabled ? '正在启用…' : '正在停用…';
  const response = await fetch(`/api/admin/wechat-pay/products/${product.resourceId}/${action}`, apiRequestOptions({
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': productLifecycleKey(product.resourceId, version, enabled) },
    body: JSON.stringify({ expected_version: version }),
  }));
  const payload = object(await response.json().catch(() => ({})));
  if (!response.ok) throw new Error(`商品${enabled ? '启用' : '停用'}失败（HTTP ${response.status}）`);
  const nextVersion = Number(payload.version);
  const lifecycle = payload.lifecycle;
  if (Number(payload.id) !== product.resourceId || !Number.isSafeInteger(nextVersion) || nextVersion !== version + 1 ||
    lifecycle !== (enabled ? 'enabled' : 'disabled') || payload.enabled !== enabled) {
    throw new Error('商品状态响应不完整，未刷新列表');
  }
  button.textContent = enabled ? '已启用' : '已停用';
  showMessage(`商品已${enabled ? '启用' : '停用'}，服务端版本 ${nextVersion}`);
  window.setTimeout(() => location.reload(), 550);
}

document.addEventListener('click', (event) => {
  if (document.body.dataset.page !== 'products') return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest('button');
  if (!button || (button.textContent?.trim() !== '启用' && button.textContent?.trim() !== '停用')) return;
  const row = button.closest('tbody tr');
  const index = Array.from(row?.parentElement?.querySelectorAll(':scope > tr') || []).indexOf(row as HTMLTableRowElement);
  const product = loadedProducts[index];
  event.preventDefault();
  event.stopImmediatePropagation();
  if (!product) return showMessage('商品缺少服务端 ID，未发送状态变更请求');
  void toggleProductLifecycle(button, product).catch((error) => {
    button.disabled = false;
    button.textContent = product.lifecycle === 'enabled' ? '停用' : '启用';
    showMessage(error instanceof Error ? error.message : '商品状态变更失败');
  });
}, true);

type ExternalPushPage = {
  productID: number;
  productKind: 'wechat_pay' | 'service_period';
  anchor: string;
  endpoint: string;
  configurationEndpoint: string;
};

type ExternalPushTimelineItem = {
  productID: number;
  productKind: 'wechat_pay' | 'service_period';
  effectID: string;
  state: string;
  attemptCount: number;
  providerAccepted: boolean;
  deliveryProven: boolean;
  realExternalCallExecuted: boolean;
  autoRetryAllowed: boolean;
};

function productEditorRoute(): { id: number; prefix: 'pf' | 'spf' } | undefined {
  let pathname: string; let search: string;
  // Mutation observers can be flushed by JSDOM after its Window is closed.
  // Browser routes remain unchanged; a disposed document simply has no editor.
  try { pathname = location.pathname; search = location.search; } catch { return undefined; }
  const canonical = pathname.match(/^\/admin\/(wechat-pay\/products|service-period-products)\/([1-9][0-9]*)\/edit$/);
  const prefix = canonical ? canonical[1] === 'wechat-pay/products' ? 'pf' : 'spf'
    : /^\/admin\/(?:wechat-pay\/)?productForm\.html$/.test(pathname) ? 'pf'
    : /^\/admin\/(?:wechat-pay\/)?spProductForm\.html$/.test(pathname) ? 'spf' : undefined;
  const raw = canonical?.[2] || new URLSearchParams(search).get('id') || '';
  const id = Number(raw);
  if (!prefix || !/^[1-9][0-9]*$/.test(raw) || !Number.isSafeInteger(id)) return undefined;
  return { id, prefix };
}

function externalPushPage(): ExternalPushPage | undefined {
  const route = productEditorRoute();
  if (!route) return undefined;
  const productID = route.id;
  if (route.prefix === 'pf') return { productID, productKind: 'wechat_pay', anchor: '#product-push', endpoint: `/api/admin/wechat-pay/products/${productID}/external-push/test`, configurationEndpoint: `/api/admin/wechat-pay/products/${productID}/external-push` };
  return { productID, productKind: 'service_period', anchor: '#sp-push', endpoint: `/api/admin/service-period-products/${productID}/external-push/test`, configurationEndpoint: `/api/admin/service-period-products/${productID}/external-push` };
}

const externalPushStateLabel: Record<string, string> = {
  accepted: '已受理，等待投递',
  queued: '已排队，等待投递',
  attempted: '已尝试，等待结果',
  provider_accepted: '接收方已回执；未证明业务送达',
  final_failed: '请求已失败',
  outcome_unknown: '结果未知，需按原投递 ID 对账',
  reconciled: '已对账',
};

function parseExternalPushTimeline(value: unknown, page: ExternalPushPage): ExternalPushTimelineItem[] {
  const items = list(object(value).items);
  if (items.length > 20) throw new Error('外推状态响应超出上限');
  return items.map((raw) => {
    const item = object(raw);
    const effectID = typeof item.effect_id === 'string' ? item.effect_id : '';
    const state = typeof item.state === 'string' ? item.state : '';
    const attemptCount = Number(item.attempt_count);
    if (Number(item.product_id) !== page.productID || item.product_kind !== page.productKind ||
      !/^[A-Za-z0-9_-]{1,128}$/.test(effectID) || !Object.prototype.hasOwnProperty.call(externalPushStateLabel, state) ||
      !Number.isSafeInteger(attemptCount) || attemptCount < 0 ||
      typeof item.provider_accepted !== 'boolean' || typeof item.delivery_proven !== 'boolean' ||
      typeof item.real_external_call_executed !== 'boolean' || typeof item.auto_retry_allowed !== 'boolean' ||
      item.delivery_proven !== false || item.auto_retry_allowed !== false) {
      throw new Error('外推状态响应不完整');
    }
    return {
      productID: page.productID, productKind: page.productKind, effectID, state, attemptCount,
      providerAccepted: item.provider_accepted, deliveryProven: false,
      realExternalCallExecuted: item.real_external_call_executed, autoRetryAllowed: false,
    };
  });
}

async function externalPushRequest(path: string, init: RequestInit): Promise<unknown> {
  const response = await fetch(path, apiRequestOptions(init));
  let payload: unknown;
  try { payload = await response.json(); } catch { throw new Error(`外推请求失败（HTTP ${response.status}）`); }
  if (!response.ok) throw new Error(`外推请求失败（HTTP ${response.status}）`);
  return payload;
}

type ExternalPushConfigurationDetails = {
  fieldMapping?: FieldMapping;
  url: string;
  enabled: boolean;
  configurationReference: string;
  revision: number;
  pushType: string;
  day: number | null;
  frequency: number | null;
  expiresAtTS: number | null;
  remark: string;
  customParamsText: string;
};

type ExternalPushConfigurationState = {
  value?: ExternalPushConfigurationDetails;
  pending?: Promise<ExternalPushConfigurationDetails>;
};

// The frozen Product renderer clears and remounts its form after its own
// asynchronous read. Keep an in-flight configuration response per page so a
// remounted V3 panel receives the same verified result instead of leaving the
// visible panel empty because the first panel was detached.
const externalPushConfigurationStates = new Map<string, ExternalPushConfigurationState>();

function externalPushConfigurationState(page: ExternalPushPage): ExternalPushConfigurationState {
  const key = `${page.productKind}:${page.productID}`;
  let state = externalPushConfigurationStates.get(key);
  if (!state) {
    state = {};
    externalPushConfigurationStates.set(key, state);
  }
  return state;
}

function parseExternalPushConfiguration(value: unknown, page: ExternalPushPage): ExternalPushConfigurationDetails {
  const item = object(value);
  const enabled = item.enabled;
  const reference = item.configuration_reference;
  const revision = Number(item.revision);
  const pushType = item.type;
  const remark = item.remark;
  const customParams = item.custom_params;
  const customParamsJSON = item.custom_params_json;
  const optionalInteger = (field: 'day' | 'frequency' | 'expires_at_ts'): number | null => {
    const raw = item[field];
    if (raw === null) return null;
    const parsed = Number(raw);
    if (!Number.isSafeInteger(parsed) || parsed < 0) throw new Error('外推业务参数响应不完整');
    return parsed;
  };
  if (Number(item.product_id) !== page.productID || item.product_kind !== page.productKind || typeof enabled !== 'boolean' ||
    typeof reference !== 'string' || !Number.isSafeInteger(revision) || revision < 0 || typeof pushType !== 'string' ||
    typeof remark !== 'string' || customParams === null || typeof customParams !== 'object' || Array.isArray(customParams) ||
    typeof customParamsJSON !== 'string' || customParamsJSON.length > 32768 ||
    (enabled === false && reference !== '')) {
    throw new Error('外推配置响应不完整');
  }
  try {
    const parsed = JSON.parse(customParamsJSON);
    if (parsed === null || typeof parsed !== 'object') throw new Error('invalid custom_params_json');
  } catch {
    throw new Error('外推配置响应不完整');
  }
  return { fieldMapping: item.field_mapping == null ? undefined : item.field_mapping as FieldMapping, url: typeof item.url === 'string' ? item.url : '', enabled, configurationReference: reference, revision, pushType, day: optionalInteger('day'), frequency: optionalInteger('frequency'), expiresAtTS: optionalInteger('expires_at_ts'), remark, customParamsText: customParamsJSON };
}

function configurationBinding(page: ExternalPushPage, ownerDocument: Document): { enabled: boolean; reference: string } {
  const prefix = page.productKind === 'wechat_pay' ? 'pf' : 'spf';
  const enabled = ownerDocument.getElementById(`${prefix}ExternalPushEnabled`) as HTMLSelectElement | null;
  const reference = ownerDocument.getElementById(`${prefix}ExternalPushReference`) as HTMLInputElement | null;
  if (!enabled || !reference || (enabled.value !== 'true' && enabled.value !== 'false')) throw new Error('冻结商品外推绑定未加载');
  return { enabled: enabled.value === 'true', reference: reference.value.trim() };
}

function externalPushConfigurationIdempotencyKey(): string {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `product-external-push-config-${suffix}`;
}

function externalPushOptionalInteger(input: HTMLInputElement): number | null {
  const raw = input.value.trim();
  if (raw === '') return null;
  if (!/^\d+$/.test(raw)) throw new Error('天数和频次必须是非负整数或留空');
  const value = Number(raw);
  if (!Number.isSafeInteger(value)) throw new Error('天数和频次超出安全范围');
  return value;
}

function mountExternalPushConfiguration(page: ExternalPushPage, ownerDocument: Document, panel: HTMLElement): void {
  const editor = ownerDocument.createElement('section');
  editor.dataset.externalPushConfiguration = '';
  editor.style.cssText = 'display:grid;gap:9px;padding-top:12px;border-top:1px solid #EFF0F1';
  const title = ownerDocument.createElement('strong');
  title.textContent = '推送配置';
  const prefix = page.productKind === 'wechat_pay' ? 'pf' : 'spf';
  ownerDocument.getElementById(`${prefix}ExternalPushReference`)?.parentElement?.setAttribute('hidden', '');
  const note = ownerDocument.createElement('p');
  note.textContent = '支付成功后，向配置的地址推送通知。';
  note.style.cssText = 'margin:0;font-size:12px;line-height:19px;color:#646A73';
  const grid = ownerDocument.createElement('div');
  grid.style.cssText = 'display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px';
  const field = (label: string, id: string, type = 'text'): HTMLInputElement => {
    const wrap = ownerDocument.createElement('label');
    wrap.style.cssText = 'display:grid;gap:5px;color:#646A73;font-size:12px';
    wrap.textContent = label;
    const input = ownerDocument.createElement('input');
    input.id = id;
    input.type = type;
    input.style.cssText = 'min-height:34px;border:1px solid #DEE0E3;border-radius:6px;padding:0 9px;box-sizing:border-box';
    wrap.appendChild(input);
    grid.appendChild(wrap);
    return input;
  };
  const targetURL = field('推送地址', 'product-v3-external-push-url', 'url');
  targetURL.placeholder = 'https://';
  const pushType = field('推送类型', 'product-v3-external-push-type');
  const day = field('服务天数', 'product-v3-external-push-day', 'text');
  const frequency = field('频次', 'product-v3-external-push-frequency', 'text');
  const expiresAtTS = field('到期时间戳', 'product-v3-external-push-expires-at-ts', 'text');
  const remark = field('备注', 'product-v3-external-push-remark');
  const paramsLabel = ownerDocument.createElement('label');
  paramsLabel.style.cssText = 'display:grid;gap:5px;color:#646A73;font-size:12px';
  paramsLabel.textContent = '自定义参数（JSON）';
  const params = ownerDocument.createElement('textarea');
  params.id = 'product-v3-external-push-custom-params';
  params.rows = 5;
  params.style.cssText = 'width:100%;border:1px solid #DEE0E3;border-radius:6px;padding:8px 9px;resize:vertical;box-sizing:border-box;font-family:ui-monospace,Menlo,monospace';
  const rows = ownerDocument.createElement('div');
  rows.dataset.externalPushParamRows = '';
  rows.style.cssText = 'display:grid;gap:8px';
  const addParam = button('新增参数', ownerDocument);
  const renderParams = (): void => {
    rows.replaceChildren();
    addParam.onclick = () => { advanced.open = true; showMessage('当前参数包含结构化数据，请在高级配置中编辑'); };
    let values: Record<string, string>;
    try { const parsed = JSON.parse(params.value || '{}'); if (!parsed || Array.isArray(parsed) || Object.values(parsed).some((value) => typeof value !== 'string')) return; values = parsed; } catch { return; }
    const entries = Object.entries(values);
    const saveRows = (): void => {
      const result: Record<string, string> = Object.create(null);
      for (const row of rows.children) { const fields = row.querySelectorAll('input'); if (fields[0].value.trim()) result[fields[0].value.trim()] = fields[1].value; }
      params.value = JSON.stringify(result);
    };
    const addRow = (key = '', value = ''): void => {
      const row = ownerDocument.createElement('div'); row.style.cssText = 'display:flex;gap:8px';
      const keyInput = ownerDocument.createElement('input'); keyInput.placeholder = '参数名'; keyInput.value = key;
      const valueInput = ownerDocument.createElement('input'); valueInput.placeholder = '参数值'; valueInput.value = value;
      for (const input of [keyInput, valueInput]) { input.style.cssText = 'min-width:0;flex:1;height:36px;border:1px solid #DEE0E3;border-radius:6px;padding:0 9px'; input.addEventListener('input', saveRows); }
      const remove = button('删除', ownerDocument); remove.addEventListener('click', () => { row.remove(); saveRows(); });
      row.append(keyInput, valueInput, remove); rows.append(row);
    };
    for (const [key, value] of entries) addRow(key, value);
    addParam.onclick = () => addRow();
  };
  addParam.onclick = () => showMessage('当前参数包含结构化数据，请在高级配置中编辑');
  params.addEventListener('change', renderParams);
  const advanced = ownerDocument.createElement('details');
  const advancedTitle = ownerDocument.createElement('summary'); advancedTitle.textContent = '高级参数（JSON）';
  advanced.append(advancedTitle, params);
  paramsLabel.textContent = '自定义参数';
  paramsLabel.append(rows, addParam, advanced);
  const actions = ownerDocument.createElement('div');
  actions.style.cssText = 'display:flex;align-items:center;gap:8px;flex-wrap:wrap';
  const save = button('保存配置', ownerDocument);
  save.dataset.externalPushConfigurationSave = '';
  const status = ownerDocument.createElement('span');
  status.dataset.externalPushConfigurationStatus = '';
  status.setAttribute('role', 'status'); status.setAttribute('aria-live', 'polite');
  status.style.cssText = 'font-size:12px;color:#646A73';
  actions.append(save, status);
  editor.append(title, note, grid, paramsLabel, actions);
  panel.prepend(editor);
  for(const legacySave of ownerDocument.querySelectorAll<HTMLButtonElement>(`${page.anchor} button`)) if(legacySave.textContent?.trim()==='保存当前维度') legacySave.hidden = true;
  const globalSave = [...ownerDocument.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim()==='保存当前维度' && !candidate.closest(page.anchor));
  save.hidden = !!globalSave;

  const mappingMount = ownerDocument.createElement('div');
  mappingMount.hidden = true; mappingMount.style.display = 'none';
  const mappingMode = ownerDocument.createElement('div');
  mappingMode.style.cssText = 'display:flex;gap:12px;align-items:center;margin:12px 0';
  const modeLabel = ownerDocument.createElement('span');
  const convert = button('转换为字段映射', ownerDocument);
  mappingMode.append(modeLabel, convert);
  actions.before(mappingMode, mappingMount);
  let mappingActive = false;
  const previewMapping = async (field_mapping: FieldMapping): Promise<MappingPreview> => {
    return await externalPushRequest(page.configurationEndpoint + '/preview', {method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json'},body:JSON.stringify({field_mapping})}) as MappingPreview;
  };
  const mappingEditor = createFieldMappingEditor(mappingMount, {preview:previewMapping});
  const setMappingMode = (mapping?: FieldMapping): void => {
    mappingActive = !!mapping;
    mappingMount.hidden = !mappingActive;
    // Inline display is explicit because the shared component itself is a grid.
    mappingMount.style.display = mappingActive ? '' : 'none';
    for (const field of [pushType, day, frequency, expiresAtTS, remark]) { field.parentElement!.hidden = mappingActive; field.parentElement!.style.display = mappingActive ? 'none' : 'grid'; }
    paramsLabel.hidden = mappingActive; paramsLabel.style.display = mappingActive ? 'none' : 'grid';
    convert.hidden = mappingActive;
    modeLabel.textContent = mappingActive ? '字段映射 · 仅发送下方配置的字段' : '沿用原有推送格式';
    if(mapping) mappingEditor.setMapping(mapping);
  };
  convert.addEventListener('click', () => {
    let before: Record<string,unknown>;
    try { before = {type:pushType.value,day:externalPushOptionalInteger(day),frequency:externalPushOptionalInteger(frequency),remark:remark.value}; }
    catch { status.textContent='原配置包含无效字段，请先修正';return; }
    const fields:MappingField[] = Object.entries(before).map(([key,value])=>({key,source:'fixed',value_type:value===null?'null':typeof value==='string'?'string':typeof value==='number'?'number':typeof value==='boolean'?'boolean':'json',value}));
    const proposal:FieldMapping={version:1,fields};
    convert.disabled=true;
    void previewMapping(proposal).then(preview=>{
      if(typeof (preview as MappingPreview & {legacy_payload_json?:string}).legacy_payload_json !== 'string') throw new Error('原格式预览暂不可用，未转换配置');
      const review=ownerDocument.createElement('section');review.className='fm-editor';review.dataset.mappingConversion='';
      const heading=ownerDocument.createElement('h4');heading.textContent='确认格式转换';
      const description=ownerDocument.createElement('p');description.textContent='转换后只发送右侧字段，原协议的其他字段将不再发送。确认仅更新当前草稿，点击保存配置后生效。';
      const diff=ownerDocument.createElement('div');diff.className='fm-layout';
      const old=ownerDocument.createElement('pre');old.className='fm-json';old.textContent='转换前（原协议模拟）\n'+(typeof (preview as MappingPreview & {legacy_payload_json?:string}).legacy_payload_json === 'string' ? (preview as MappingPreview & {legacy_payload_json:string}).legacy_payload_json : '原协议包含订单、付款人、商品及投递信息；新映射仅发送右侧明确配置的字段。');
      const next=ownerDocument.createElement('pre');next.className='fm-json';next.textContent='转换后（模拟）\n'+preview.payload_json;
      diff.append(old,next);
      const confirm=button('确认转换',ownerDocument);const cancel=button('取消',ownerDocument);
      confirm.onclick=()=>{setMappingMode(proposal);review.remove();};cancel.onclick=()=>review.remove();
      review.append(heading,description,diff,confirm,cancel);mappingMode.after(review);
    }).catch(error=>{status.textContent=error instanceof Error?error.message:'转换预览失败';}).finally(()=>{convert.disabled=false;});
  });
  const state = externalPushConfigurationState(page);
  let configuration: ExternalPushConfigurationDetails | undefined;
  const load = async (): Promise<void> => {
    status.textContent = '正在读取配置…';
    if (!state.pending && !state.value) {
      state.pending = (async () => {
        const response = await externalPushRequest(page.configurationEndpoint, { method: 'GET', headers: { Accept: 'application/json' } });
        const value = parseExternalPushConfiguration(response, page);
        state.value = value;
        return value;
      })();
    }
    const pending = state.pending;
    let value: ExternalPushConfigurationDetails;
    try {
      value = state.value || await pending!;
    } finally {
      if (pending && state.pending === pending) state.pending = undefined;
    }
    if (!panel.isConnected) return;
    configuration = value;
    targetURL.value = value.url;
    pushType.value = value.pushType;
    day.value = value.day == null ? '' : String(value.day);
    frequency.value = value.frequency == null ? '' : String(value.frequency);
    expiresAtTS.value = value.expiresAtTS == null ? '' : String(value.expiresAtTS);
    remark.value = value.remark;
    params.value = value.customParamsText;
    renderParams();
    setMappingMode(value.fieldMapping || (value.revision === 0 && !value.configurationReference ? {version:1,fields:[]} : undefined));
    status.dataset.configurationRevision = String(value.revision);
    status.textContent = `配置版本 ${value.revision}`;
  };
  save.addEventListener('click', () => {
    if (!configuration) return showMessage('外推配置尚未读取完成');
    let customParamsText: string;
    let binding: { enabled: boolean; reference: string };
    let configuredDay: number | null;
    let configuredFrequency: number | null;
    let configuredExpiresAtTS: number | null;
    let fieldMapping: FieldMapping | undefined;
    try {
      fieldMapping = mappingActive ? mappingEditor.getMapping() : undefined;
      customParamsText = mappingActive ? configuration.customParamsText : params.value.trim() || '{}';
      if (!mappingActive) {
        const customParams = JSON.parse(customParamsText);
        if (customParams === null || typeof customParams !== 'object') throw new Error('自定义参数必须是 JSON 对象或 key/value 列表');
      }
      binding = configurationBinding(page, ownerDocument);
      if (binding.enabled) {
        let destination: URL;
        try { destination = new URL(targetURL.value.trim()); } catch { throw new Error('请填写有效的 HTTPS 推送地址'); }
        if (destination.protocol !== 'https:' || destination.username || destination.password) throw new Error('请填写有效的 HTTPS 推送地址');
      }
      configuredDay = mappingActive ? configuration.day : externalPushOptionalInteger(day);
      configuredFrequency = mappingActive ? configuration.frequency : externalPushOptionalInteger(frequency);
      configuredExpiresAtTS = mappingActive ? configuration.expiresAtTS : externalPushOptionalInteger(expiresAtTS);
    } catch (error) {
      status.textContent = error instanceof Error ? error.message : '请修正配置后保存';
      return;
    }
    save.disabled = true;
    void externalPushRequest(page.configurationEndpoint, {
      method: 'PUT',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushConfigurationIdempotencyKey() },
      body: JSON.stringify({ ...(fieldMapping ? {field_mapping:fieldMapping} : {}), url: targetURL.value.trim(), enabled: binding.enabled, configuration_reference: binding.enabled ? binding.reference : '', type: pushType.value, day: configuredDay, frequency: configuredFrequency, expires_at_ts: configuredExpiresAtTS, remark: remark.value, custom_params: customParamsText, expected_revision: configuration.revision }),
    }).then((saved) => {
      configuration = parseExternalPushConfiguration(saved, page);
      state.value = configuration;
      status.dataset.configurationRevision = String(configuration.revision);
      status.textContent = '配置已保存';
      showMessage('配置已保存', true);
    }).catch((error) => { status.textContent = error instanceof Error ? error.message : '配置保存失败'; }).finally(() => { save.disabled = false; });
  });
  void load().catch((error) => { status.textContent = error instanceof Error ? error.message : '外推配置读取失败'; });
}

function renderExternalPushTimeline(panel: HTMLElement, entries: ExternalPushTimelineItem[]): void {
  const listNode = panel.querySelector<HTMLElement>('[data-external-push-timeline]');
  if (!listNode) return;
  const ownerDocument = panel.ownerDocument;
  if (!ownerDocument) return;
  listNode.replaceChildren();
  if (entries.length === 0) {
    listNode.textContent = '暂无测试投递记录。';
    return;
  }
  for (const entry of entries) {
    const row = ownerDocument.createElement('div');
    row.style.cssText = 'display:flex;gap:8px;align-items:center;flex-wrap:wrap';
    const label = ownerDocument.createElement('strong');
    label.textContent = externalPushStateLabel[entry.state];
    const detail = ownerDocument.createElement('span');
    detail.textContent = `投递 ${entry.effectID} · 已尝试 ${entry.attemptCount} 次`;
    detail.style.color = '#646A73';
    row.append(label, detail);
    listNode.appendChild(row);
  }
}

function externalPushIdempotencyKey(): string {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `product-external-push-test-${suffix}`;
}

function mountExternalPushTest(page: ExternalPushPage, ownerDocument: Document): boolean {
  // The frozen renderer can replace the document element while it initializes.
  // Keep the V3 seam tied to this page's Document rather than the ambient
  // global so a queued observer callback cannot access a closed test window.
  if (!ownerDocument.documentElement) return false;
  if (ownerDocument.getElementById('product-v3-external-push-test')) return true;
  const anchor = ownerDocument.querySelector<HTMLElement>(page.anchor);
  if (!anchor) return false;
  const frozenNotice = [...anchor.querySelectorAll('p')].find((node) => node.textContent?.includes('保存只更新 V2 本地配置'));
  frozenNotice?.remove();
  const panel = ownerDocument.createElement('section');
  panel.id = 'product-v3-external-push-test';
  panel.style.cssText = 'display:grid;gap:9px;margin-top:14px;padding-top:14px;border-top:1px solid #EFF0F1';
  const description = ownerDocument.createElement('p');
  description.textContent = '保存配置后可测试推送，并查看投递结果。';
  description.style.cssText = 'margin:0;font-size:12px;color:#646A73;line-height:19px';
  const actions = ownerDocument.createElement('div');
  actions.style.cssText = 'display:flex;gap:8px;align-items:center';
  const run = button('运行测试', ownerDocument);
  run.dataset.externalPushTest = 'run';
  const refresh = button('刷新状态', ownerDocument);
  refresh.dataset.externalPushTest = 'refresh';
  const status = ownerDocument.createElement('div');
  status.dataset.externalPushTimeline = '';
  status.style.cssText = 'display:grid;gap:6px;font-size:12px;line-height:19px;color:#344054';
  actions.append(run, refresh);
  panel.append(description, actions, status);
  anchor.appendChild(panel);
  mountExternalPushConfiguration(page, ownerDocument, panel);

  const reload = async (): Promise<void> => {
    const values = parseExternalPushTimeline(await externalPushRequest(page.endpoint, { method: 'GET', headers: { Accept: 'application/json' } }), page);
    renderExternalPushTimeline(panel, values);
  };
  refresh.addEventListener('click', () => { void reload().catch((error) => showMessage(error instanceof Error ? error.message : '外推状态读取失败')); });
  run.addEventListener('click', () => {
    run.disabled = true;
    void externalPushRequest(page.endpoint, {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushIdempotencyKey() },
      body: '{}',
    }).then((created) => {
      const item = object(created);
      if (Number(item.product_id) !== page.productID || item.product_kind !== page.productKind ||
        (item.state !== 'accepted' && item.state !== 'queued') || item.provider_accepted !== false ||
        item.delivery_proven !== false || item.real_external_call_executed !== false || item.auto_retry_allowed !== false) {
        throw new Error('外推测试受理响应不完整');
      }
      showMessage('测试已受理，等待受控投递；接收方回执不代表业务送达。');
      return reload();
    }).catch((error) => showMessage(error instanceof Error ? error.message : '外推测试创建失败')).finally(() => { run.disabled = false; });
  });
  void reload().catch((error) => { status.textContent = error instanceof Error ? error.message : '外推状态读取失败'; });
  return true;
}

function installExternalPushTestHost(): void {
  const page = externalPushPage();
  if (!page) return;
  const ownerDocument = document;
  if (!ownerDocument.documentElement) return;
  let disposed = false;
  let observer: MutationObserver | undefined;
  const dispose = (): void => {
    if (disposed) return;
    disposed = true;
    observer?.disconnect();
  };
  // Observe the Document, not its initial documentElement: the byte-frozen
  // renderer replaces that root during bootstrap. pagehide/unload prevents an
  // orphaned observer from running after navigation or test-window teardown.
  observer = new MutationObserver(() => {
    if (!disposed) mountExternalPushTest(page, ownerDocument);
  });
  observer.observe(ownerDocument, { childList: true, subtree: true });
  window.addEventListener('pagehide', dispose, { once: true });
  window.addEventListener('unload', dispose, { once: true });
  mountExternalPushTest(page, ownerDocument);
}

installExternalPushTestHost();
// The editor's shared save action must use the same complete external-push
// configuration command as this dimension's own save button.
document.addEventListener('click', (event) => {
  const target = (event.target as Element | null)?.closest<HTMLButtonElement>('button');
  if (!target || target.textContent?.trim() !== '保存当前维度') return;
  const page = externalPushPage();
  if (!page) return;
  const anchor = document.querySelector<HTMLElement>(page.anchor);
  if (!anchor) return;
  for(let ancestor:HTMLElement|null=anchor;ancestor;ancestor=ancestor.parentElement) { if(ancestor.hidden || getComputedStyle(ancestor).display==='none') return; }
  const save = anchor.querySelector<HTMLButtonElement>('[data-external-push-configuration-save]');
  if (!save) return;
  event.preventDefault(); event.stopImmediatePropagation();
  if (!save.disabled) save.click();
}, true);


// Dynamic import is deliberate: validation and click interception must be
// installed before the byte-frozen donor runtime reads the current page.
void (async () => {
  await (window as StandardWindow).AICRMStandardComponents?.ready?.();
  // @ts-ignore The frozen side-effect entry has no TypeScript export declaration.
  await import('../src/admin/main');
})();

function safeTagging(input: HTMLTextAreaElement): RecordValue {
  try { return object(JSON.parse(input.value || '{}')); } catch { return {}; }
}

function productTagCatalogLoadFailure(response: Response): Error {
  const error = new Error(`标签目录读取失败（HTTP ${response.status}）`) as Error & { status?: number };
  error.status = response.status;
  return error;
}

function mountProductTagPicker(): void {
  if (typeof document === 'undefined' || !document.body) return;
  const prefix = document.body.dataset.page === 'productForm' ? 'pf' : document.body.dataset.page === 'spProductForm' ? 'spf' : '';
  if (!prefix) return;
  if (prefix === 'spf') mountPeriodicTagDimension();
  const input = document.getElementById(`${prefix}WecomTagging`) as HTMLTextAreaElement | null;
  const panel = document.getElementById(prefix === 'pf' ? 'product-wecom' : 'sp-wecom');
  if (!input || !panel || panel.querySelector('[data-product-standard-tag-picker]')) return;
  input.closest('details')?.setAttribute('hidden', '');
  for (const note of panel.querySelectorAll('p,div')) if (!note.children.length && note.textContent?.includes('OpenAPI')) note.remove();
  const state = safeTagging(input);
  const tagSource = 'local_tag_catalog';
  const selected: TagPickerRecord[] = list(state.tags).map((item) => unresolvedTagRecord(tagSource, String(object(item).tag_id || object(item).id || '').trim())).filter((item): item is TagPickerRecord => Boolean(item));
  if (!selected.length) for (const raw of list(state.tag_ids)) { const record = unresolvedTagRecord(tagSource, String(raw || '').trim()); if (record) selected.push(record); }
  const host = document.createElement('section');
  host.dataset.productStandardTagPicker = '';
  host.style.cssText = 'display:grid;gap:10px;padding:12px;border:1px solid #DEE0E3;border-radius:8px;background:#fff';
  host.innerHTML = '<div style="display:flex;align-items:center;justify-content:space-between;gap:10px"><label style="display:flex;align-items:center;gap:8px;font-size:13px"><input type="checkbox" role="switch" data-product-tag-enabled> 启用购买后企微标签</label><button type="button" data-product-tag-open style="height:30px;padding:0 12px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;cursor:pointer">选择标签</button></div><div data-product-tag-summary style="font-size:12px;color:#646A73"></div><p data-product-tag-error style="margin:0;font-size:12px;color:#D83931" hidden></p>';
  panel.querySelector('div[style*="display:grid"]')?.append(host);
  const enabled = host.querySelector<HTMLInputElement>('[data-product-tag-enabled]')!;
  const summary = host.querySelector<HTMLElement>('[data-product-tag-summary]')!;
  const error = host.querySelector<HTMLElement>('[data-product-tag-error]')!;
  enabled.checked = typeof state.enabled === 'boolean' ? state.enabled : list(state.tag_ids).length > 0;
  const sync = (): void => {
    const tagIDs = [...new Set(selected.map((tag) => Number(tag.tag_id)).filter((tagID) => Number.isSafeInteger(tagID) && tagID > 0))];
    input.value = JSON.stringify({ enabled: enabled.checked, tag_ids: tagIDs }); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true }));
    summary.textContent = enabled.checked && selected.length ? `已选：${selected.map((tag) => `${tag.group_name ? `${tag.group_name} / ` : ''}${tag.tag_name || tag.tag_id}`).join('、')}` : enabled.checked ? '暂未选择标签' : '未启用购买后企微标签';
  };
  enabled.addEventListener('change', sync); sync();
  host.querySelector('[data-product-tag-open]')?.addEventListener('click', () => {
    error.hidden = true;
    const picker = window.AICRMTagPicker;
    if (!picker) { error.textContent = '标签选择器加载失败，请刷新后重试'; error.hidden = false; return; }
    picker.open({
      title: '选择购买后企微标签',
      source: tagSource,
      scope: 'product.purchase_after_tag',
      mode: 'multiple',
      selectedRecords: selected,
      loadPage: createTagCatalogPageLoader(tagSource, async ({ signal }) => {
        const response = await donorFetch('/api/admin/wecom/tags', { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' }, signal });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw productTagCatalogLoadFailure(response);
        return payload;
      }),
      onCommit: (result) => { selected.splice(0, selected.length, ...result.selected); sync(); },
      accessLossMessage: (failure) => (failure as { status?: number } | undefined)?.status === 403 ? '标签目录权限已失效；当前商品草稿选择仍保留，请取消后重新登录。' : undefined,
    });
  });
}

const productStandardObserver = new MutationObserver(mountProductTagPicker);
productStandardObserver.observe(document, { childList: true, subtree: true });
mountProductTagPicker();

type PurchaseActionMode = '' | 'qr' | 'redirect';

type PurchaseActionDOM = { enabled: boolean; mode: PurchaseActionMode };

function productPrefix(): 'pf' | 'spf' | '' {
  if (typeof document === 'undefined' || !document.body) return '';
  return document.body.dataset.page === 'productForm' ? 'pf' : document.body.dataset.page === 'spProductForm' ? 'spf' : '';
}

function productActionState(prefix: string): PurchaseActionDOM {
  const route = productEditorRoute();
  const saved = route?.prefix === prefix ? purchaseActionByProduct.get(route.id) : undefined;
  return saved || { enabled: false, mode: '' };
}

function purchaseActionControls(prefix: string): HTMLElement | null {
  const action = document.getElementById(prefix === 'pf' ? 'product-action' : 'sp-action');
  if (!action || action.querySelector('[data-product-purchase-action]')) return null;
  const host = document.createElement('section');
  host.dataset.productPurchaseAction = '';
  host.style.cssText = 'display:grid;gap:10px;margin:0 0 14px;padding:12px;border:1px solid #DEE0E3;border-radius:8px;background:#fff';
  host.innerHTML = `<label style="display:flex;align-items:center;gap:8px;font-size:13px;color:#344054"><input type="checkbox" data-product-purchase-enabled> 启用购买后动作</label><div data-product-purchase-modes style="display:flex;gap:18px;align-items:center;font-size:13px;color:#4E5969"><label style="display:flex;align-items:center;gap:6px"><input type="radio" name="${prefix}PurchaseActionMode" value="qr"> 展示二维码</label><label style="display:flex;align-items:center;gap:6px"><input type="radio" name="${prefix}PurchaseActionMode" value="redirect"> 直接跳转</label></div>`;
  const grid = action.querySelector(':scope > div[style*="grid-template-columns"]');
  grid?.parentElement?.insertBefore(host, grid);
  const current = productActionState(prefix);
  const enabled = host.querySelector<HTMLInputElement>('[data-product-purchase-enabled]')!;
  const modes = host.querySelector<HTMLElement>('[data-product-purchase-modes]')!;
  enabled.checked = current.enabled;
  const radio = host.querySelector<HTMLInputElement>(`input[value="${current.mode}"]`);
  if (radio) radio.checked = true;

  const fieldFor = (id: string): HTMLElement | null => document.getElementById(id)?.closest<HTMLElement>('div') || null;
  const qr = [`${prefix}LeadChannelId`, `${prefix}LeadQrTitle`, `${prefix}LeadQrSubtitle`].map(fieldFor);
  const redirect = [`${prefix}CompletionRedirectUrl`, `${prefix}CompletionTarget`].map(fieldFor);
  const oldRedirect = fieldFor(`${prefix}CompletionRedirectEnabled`);
  const update = (): void => {
    const selected = host.querySelector<HTMLInputElement>(`input[name="${prefix}PurchaseActionMode"]:checked`)?.value as PurchaseActionMode | undefined;
    setProductVisible(modes, enabled.checked);
    for (const field of qr) if (field) setProductVisible(field, enabled.checked && selected === 'qr');
    for (const field of redirect) if (field) setProductVisible(field, enabled.checked && selected === 'redirect');
    if (oldRedirect) setProductVisible(oldRedirect, false);
    // The frozen serializer always parses this hidden JSON field. Keep it
    // syntactically empty when redirect is not the active choice.
    if (!enabled.checked || selected !== 'redirect') {
      const target = document.getElementById(`${prefix}CompletionTarget`) as HTMLTextAreaElement | null;
      if (target) target.value = '';
    }
  };
  enabled.addEventListener('change', update);
  modes.addEventListener('change', update);
  update();
  return host;
}

function currentPurchaseAction(): PurchaseActionDOM {
  const prefix = productPrefix();
  if (!prefix) return { enabled: false, mode: '' };
  const host = document.querySelector<HTMLElement>('[data-product-purchase-action]');
  const enabled = host?.querySelector<HTMLInputElement>('[data-product-purchase-enabled]')?.checked === true;
  const mode = host?.querySelector<HTMLInputElement>(`input[name="${prefix}PurchaseActionMode"]:checked`)?.value;
  return { enabled, mode: enabled && (mode === 'qr' || mode === 'redirect') ? mode : '' };
}

function isProductSubjectWrite(url: URL, method: string): boolean {
  return (method === 'POST' && url.pathname === '/api/v1/products') || (method === 'PUT' && /^\/api\/v1\/products\/[1-9][0-9]*$/.test(url.pathname));
}

function isDistributionProductSubjectWrite(url: URL, method: string): boolean {
  return isProductSubjectWrite(url, method)
    || (method === 'POST' && url.pathname === '/api/admin/wechat-pay/products')
    || (method === 'PUT' && /^\/api\/admin\/wechat-pay\/products\/[1-9][0-9]*$/.test(url.pathname))
    || (method === 'POST' && url.pathname === '/api/admin/service-period-products')
    || (method === 'PUT' && /^\/api\/admin\/service-period-products\/[1-9][0-9]*$/.test(url.pathname));
}

function adaptPurchaseActionWrite(init: RequestInit | undefined): RequestInit | undefined {
  if (!init || typeof init.body !== 'string') return init;
  let body: RecordValue;
  try { body = object(JSON.parse(init.body)); } catch { return init; }
  const projection = object(body.admin_projection);
  const action = currentPurchaseAction();
  projection.purchase_action_enabled = action.enabled;
  projection.purchase_action_mode = action.mode;
  if (!action.enabled || action.mode !== 'qr') {
    projection.lead_channel_id = null;
    projection.lead_qr_title = '';
    projection.lead_qr_subtitle = '';
  }
  if (!action.enabled || action.mode !== 'redirect') {
    projection.completion_redirect_enabled = false;
    projection.completion_redirect_url = '';
    projection.completion_target = null;
  } else {
    projection.completion_redirect_enabled = true;
  }
  const tagging = object(projection.wecom_tagging);
  const rawTagIDs = list(tagging.tag_ids);
  const tagIDs = rawTagIDs.map(Number).filter((id) => Number.isSafeInteger(id) && id > 0);
  projection.wecom_tagging = { enabled: tagging.enabled === true, tag_ids: [...new Set(tagIDs)] };
  body.admin_projection = projection;
  return { ...init, body: JSON.stringify(body) };
}

function adaptDistributionPolicyWrite(init: RequestInit | undefined): RequestInit | undefined {
  if (!init || typeof init.body !== 'string') return init;
  let body: RecordValue;
  try { body = object(JSON.parse(init.body)); } catch { throw new Error('商品保存请求无效，未提交分销设置。'); }
  const policy = currentDistributionPolicy();
  if (!policy) return init;
  body.distribution_policy = {
    enabled: policy.enabled,
    commission_rate_basis_points: policy.commissionRateBasisPoints,
    wait_days: policy.waitDays,
    version: policy.version,
  };
  return { ...init, body: JSON.stringify(body) };
}

function distributionPolicyWritePath(url: URL, method: string): boolean {
  return isDistributionProductSubjectWrite(url, method) && method !== 'GET' && method !== 'HEAD';
}

// The frozen Product API client is materialized from an ignored donor view, so
// policy injection lives at this tracked Host seam.  Install it only while the
// one normal Product save runs: this preserves the Product owner's single
// command/UoW and avoids changing an immutable donor API source.
async function saveWithDistributionPolicy<T>(policy: DistributionPolicy | undefined, save: () => Promise<T>): Promise<T> {
  if (!policy) return save();
  const prior = globalThis.fetch;
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = input instanceof Request ? input : undefined;
    const url = new URL(request?.url || String(input), location.origin);
    const method = (init?.method || request?.method || 'GET').toUpperCase();
    if (!distributionPolicyWritePath(url, method) || typeof init?.body !== 'string') return prior(input, init);
    let body: RecordValue;
    try { body = object(JSON.parse(init.body)); } catch { throw new Error('商品保存请求无效，未提交分销设置。'); }
    body.distribution_policy = {
      enabled: policy.enabled,
      commission_rate_basis_points: policy.commissionRateBasisPoints,
      wait_days: policy.waitDays,
      version: policy.version,
    };
    return prior(input, { ...init, body: JSON.stringify(body) });
  };
  try {
    return await save();
  } finally {
    globalThis.fetch = prior;
  }
}

function mountPurchaseActionControls(): void {
  const prefix = productPrefix();
  if (!prefix) return;
  purchaseActionControls(prefix);
}

const purchaseActionObserver = new MutationObserver(mountPurchaseActionControls);
purchaseActionObserver.observe(document, { childList: true, subtree: true });
mountPurchaseActionControls();
const distributionPolicyObserver = new MutationObserver(mountDistributionPolicyControls);
distributionPolicyObserver.observe(document, { childList: true, subtree: true });
mountDistributionPolicyControls();
const servicePeriodDurationObserver = new MutationObserver(mountNewServicePeriodDuration);
servicePeriodDurationObserver.observe(document, { childList: true, subtree: true });
mountNewServicePeriodDuration();

type ProductMaterialPickerWindow = Window & { AICRMMaterialPicker?: { open(options: { type: 'image'; title: string; selectedIds: number[]; limit: number; onConfirm(item: MaterialPickerItem): void; onCancel(): void }): void } };
let pendingProductMaterialObserver: MutationObserver | undefined;

// The frozen product forms await their scoped page data before appending the
// legacy generic picker.  Keep that callback path for drafts/save, while the
// user sees the released original material picker.
document.addEventListener('click', (event) => {
  const button = (event.target as Element | null)?.closest('button');
  if (!button || button.textContent?.trim() !== '从素材库选择' || !button.closest('#product-media, #sp-media')) return;
  pendingProductMaterialObserver?.disconnect();
  const observer = new MutationObserver((records) => {
    for (const record of records) for (const node of record.addedNodes) {
      if (!(node instanceof HTMLElement) || !node.classList.contains('pk-mask')) continue;
      observer.disconnect(); if (pendingProductMaterialObserver === observer) pendingProductMaterialObserver = undefined;
      const picker = (window as ProductMaterialPickerWindow).AICRMMaterialPicker;
      if (!picker) return;
      node.style.setProperty('display', 'none', 'important'); node.setAttribute('aria-hidden', 'true');
      picker.open({ type: 'image', title: '选择页面素材', selectedIds: [], limit: 10,
        onConfirm(item) {
          const row = Array.from(node.querySelectorAll<HTMLElement>('[data-pk-id]')).find((candidate) => candidate.dataset.pkId === String(item.library_id));
          if (!row) {
            const hint = button.closest<HTMLElement>('#product-media, #sp-media')?.querySelector<HTMLElement>('[data-product-material-error]') || document.createElement('p');
            hint.dataset.productMaterialError = ''; hint.textContent = '素材目录已变化，未改动当前草稿；请刷新页面后重新选择。'; hint.setAttribute('role', 'alert');
            if (!hint.parentElement) button.closest<HTMLElement>('#product-media, #sp-media')?.append(hint);
            node.querySelector<HTMLElement>('[data-pk="cancel"]')?.click(); return;
          }
          row.click(); node.querySelector<HTMLElement>('[data-pk="ok"]')?.click();
        },
        onCancel() { node.querySelector<HTMLElement>('[data-pk="cancel"]')?.click(); },
      });
      return;
    }
  });
  pendingProductMaterialObserver = observer;
  observer.observe(document.body, { childList: true, subtree: true });
}, true);
window.addEventListener('pagehide', () => pendingProductMaterialObserver?.disconnect(), { once: true });


// Preserve the frozen form nodes and serializer while switching only the visible
// dimension, as in the standard product editor. Inactive drafts stay in the DOM.
function setProductVisible(node: HTMLElement, visible: boolean): void {
  if (node.dataset.productOriginalDisplay === undefined) node.dataset.productOriginalDisplay = node.style.display;
  node.hidden = !visible;
  node.style.setProperty('display', visible ? node.dataset.productOriginalDisplay : 'none', visible ? '' : 'important');
}

function mountPeriodicTagDimension(): void {
  if (document.getElementById('sp-wecom')) return;
  const action = document.getElementById('sp-action');
  const input = document.getElementById('spfWecomTagging');
  const group = input?.closest('details')?.parentElement;
  const navLink = document.querySelector<HTMLAnchorElement>('a[href="#sp-action"]');
  if (!action || !group || !navLink) return;
  const panel = document.createElement('div');
  panel.id = 'sp-wecom'; panel.style.cssText = action.style.cssText;
  const heading = action.firstElementChild!.cloneNode(true) as HTMLElement;
  heading.querySelector('h3')!.textContent = '企微标签';
  panel.append(heading, group); action.after(panel);
  const link = navLink.cloneNode(true) as HTMLAnchorElement;
  link.href = '#sp-wecom'; link.lastElementChild!.textContent = '企微标签'; navLink.after(link);
  Array.from(navLink.parentElement!.querySelectorAll('a')).forEach((item, index) => { item.firstElementChild!.textContent = String(index + 1); });
}

function mountProductDimensions(): void {
  const prefix = productPrefix();
  if (!prefix) return;
  const first = prefix === 'pf' ? 'product-sale' : 'sp-sale';
  const nav = document.querySelector<HTMLAnchorElement>(`a[href="#${first}"]`)?.parentElement;
  if (!nav) return;
  const links = Array.from(nav.querySelectorAll<HTMLAnchorElement>('a[href^="#"]'));
  const select = (id: string): void => {
    nav.dataset.productDimension = id;
    for (const link of links) {
      const active = link.hash === `#${id}`;
      const panel = document.getElementById(link.hash.slice(1));
      if (panel) setProductVisible(panel, active);
      link.setAttribute('aria-current', active ? 'step' : 'false');
      link.style.background = active ? '#EFF4FF' : '#fff'; link.style.color = active ? 'var(--accent,#3370ff)' : '#4E5969';
      const badge = link.firstElementChild as HTMLElement | null;
      if (badge) { badge.style.background = active ? 'var(--accent,#3370ff)' : '#EEF2F7'; badge.style.color = active ? '#fff' : '#667085'; }
    }
  };
  for (const link of links) {
    if (link.dataset.productDimensionBound) continue;
    link.dataset.productDimensionBound = 'true';
    link.addEventListener('click', event => { event.preventDefault(); select(link.hash.slice(1)); });
  }
  select(nav.dataset.productDimension || first);
}
const productDimensionsObserver = new MutationObserver(mountProductDimensions);
productDimensionsObserver.observe(document, { childList: true, subtree: true });
mountProductDimensions();

// A successful dimension save updates this editor rather than invoking the
// frozen controller's list redirect. Explicit Back navigation is unaffected.
const completedEditorSaves: Product[] = [];
const takeProductSaveButton = rememberActionClicks((button) => {
  const page = document.body.dataset.page || '';
  return (page === 'productForm' || page === 'spProductForm') && /保存/.test(button.textContent || '');
});
const takeProductUploadButton = rememberActionClicks((button) => {
  const page = document.body.dataset.page || '';
  return (page === 'productForm' || page === 'spProductForm') && /上传/.test(button.textContent || '');
});
const takeProductUploadInput = rememberActionInputs((input) => {
  const page = document.body.dataset.page || '';
  return (page === 'productForm' || page === 'spProductForm') && Boolean(input.files?.length);
});
for (const method of ['saveProduct', 'saveServiceProduct'] as const) {
  const original = api[method].bind(api);
  api[method] = (input) => runAction(takeProductSaveButton(), () => {
    const policy = currentDistributionPolicy();
    return saveWithDistributionPolicy(policy, () => original(input)).then((saved) => {
    if (['productForm', 'spProductForm'].includes(document.body.dataset.page || '')) completedEditorSaves.push(saved);
    return saved;
    }).catch((error) => { showMessage(error instanceof Error ? error.message : '商品保存失败'); throw error; });
  }, '保存中…');
}
const originalSaveImageItem = api.saveImageItem.bind(api);
api.saveImageItem = (originalName, patch) => runAction(takeProductUploadInput() || takeProductUploadButton(), () => originalSaveImageItem(originalName, patch), '上传中…');
type ProductController = {
  page: string;
  db: AdminDb;
  goto(page: string, query?: string): void;
  qs(): URLSearchParams;
};
const productController = AdminController.prototype as unknown as ProductController;
const donorProductQuery = productController.qs;
productController.qs = function () {
  const query = donorProductQuery.call(this);
  const route = productEditorRoute();
  if (route && ((this.page === 'productForm' && route.prefix === 'pf') || (this.page === 'spProductForm' && route.prefix === 'spf'))) query.set('id', String(route.id));
  return query;
};
const donorGotoProduct = productController.goto;
productController.goto = function (page, query = '') {
  const expected = this.page === 'productForm' ? 'products' : this.page === 'spProductForm' ? 'spProducts' : '';
  const saved = completedEditorSaves[0];
  if (!saved || page !== expected || query) return donorGotoProduct.call(this, page, query);
  completedEditorSaves.shift();
  const id = saved.resourceId;
  if (!id || !Number.isSafeInteger(id) || id < 1) { showMessage('已保存，但返回的商品 ID 无效，请刷新核对'); return; }
  const next = new URL(location.href);
  next.searchParams.set('id', String(id));
  history.replaceState(null, '', next.pathname + next.search + next.hash);
  // Keep the server version for the next dimension save, while retaining all
  // unsaved DOM controls and the active dimension in this same editor.
  if (this.page === 'productForm') this.db.rows.products = [saved];
  else this.db.rows.spProducts = [saved];
  const stage = document.getElementById('stage') || document.body;
  for (const node of stage.querySelectorAll<HTMLElement>('div,span,p')) {
    if (node.children.length === 0 && node.textContent?.startsWith('服务端版本：')) node.textContent = `服务端版本：${saved.version} · 生命周期：${saved.lifecycle || ''}`;
  }
  showMessage(`已保存当前维度，服务端版本 ${saved.version}`, true);
};
