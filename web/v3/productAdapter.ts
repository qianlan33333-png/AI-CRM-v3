// This is the only v3-owned browser seam for the byte-frozen Product UI.
// It validates the authoritative lifecycle/sales projection, supplies Chinese
// display labels, and replaces only the ordinary-product share interaction.
import { api } from '../src/shared/api/client';
import { apiRequestOptions } from '../src/api/transport';
import type { AdminDb, Product, Tone } from '../src/shared/api/types';
import type { AdminReadContext } from '../src/api/admin';
import { downloadQr, renderQr } from '../src/admin/sections/qr';

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

function showMessage(message: string): void {
  const previous = document.getElementById('product-v3-toast');
  previous?.remove();
  const toast = document.createElement('div');
  toast.id = 'product-v3-toast';
  toast.setAttribute('role', 'alert');
  toast.textContent = message;
  toast.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
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

function externalPushPage(): ExternalPushPage | undefined {
  const id = new URLSearchParams(location.search).get('id') || '';
  if (!/^[1-9][0-9]*$/.test(id)) return undefined;
  const productID = Number(id);
  if (location.pathname.endsWith('/admin/productForm.html')) {
    return { productID, productKind: 'wechat_pay', anchor: '#product-push', endpoint: `/api/admin/wechat-pay/products/${productID}/external-push/test`, configurationEndpoint: `/api/admin/wechat-pay/products/${productID}/external-push` };
  }
  if (location.pathname.endsWith('/admin/spProductForm.html')) {
    return { productID, productKind: 'service_period', anchor: '#sp-push', endpoint: `/api/admin/service-period-products/${productID}/external-push/test`, configurationEndpoint: `/api/admin/service-period-products/${productID}/external-push` };
  }
  return undefined;
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
  enabled: boolean;
  configurationReference: string;
  revision: number;
  pushType: string;
  day: number | null;
  frequency: number | null;
  remark: string;
  customParamsText: string;
};

function parseExternalPushConfiguration(value: unknown, page: ExternalPushPage): ExternalPushConfigurationDetails {
  const item = object(value);
  const enabled = item.enabled;
  const reference = item.configuration_reference;
  const revision = Number(item.revision);
  const pushType = item.type;
  const remark = item.remark;
  const customParams = item.custom_params;
  const customParamsJSON = item.custom_params_json;
  const optionalInteger = (field: 'day' | 'frequency'): number | null => {
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
  return { enabled, configurationReference: reference, revision, pushType, day: optionalInteger('day'), frequency: optionalInteger('frequency'), remark, customParamsText: customParamsJSON };
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
  title.textContent = '推送业务参数';
  const note = ownerDocument.createElement('p');
  note.textContent = 'URL 与密钥仍由受控配置引用管理；此处只保存旧商品配置中的业务字段。custom_params 可填 JSON 对象或 key/value 列表。';
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
  const pushType = field('类型', 'product-v3-external-push-type');
  const day = field('服务天数', 'product-v3-external-push-day', 'text');
  const frequency = field('频次', 'product-v3-external-push-frequency', 'text');
  const remark = field('备注', 'product-v3-external-push-remark');
  const paramsLabel = ownerDocument.createElement('label');
  paramsLabel.style.cssText = 'display:grid;gap:5px;color:#646A73;font-size:12px';
  paramsLabel.textContent = 'custom_params';
  const params = ownerDocument.createElement('textarea');
  params.id = 'product-v3-external-push-custom-params';
  params.rows = 5;
  params.style.cssText = 'width:100%;border:1px solid #DEE0E3;border-radius:6px;padding:8px 9px;resize:vertical;box-sizing:border-box;font-family:ui-monospace,Menlo,monospace';
  paramsLabel.appendChild(params);
  const actions = ownerDocument.createElement('div');
  actions.style.cssText = 'display:flex;align-items:center;gap:8px;flex-wrap:wrap';
  const save = button('保存外推参数', ownerDocument);
  save.dataset.externalPushConfigurationSave = '';
  const status = ownerDocument.createElement('span');
  status.style.cssText = 'font-size:12px;color:#646A73';
  actions.append(save, status);
  editor.append(title, note, grid, paramsLabel, actions);
  panel.prepend(editor);

  let configuration: ExternalPushConfigurationDetails | undefined;
  const load = async (): Promise<void> => {
    status.textContent = '正在读取配置…';
    const value = parseExternalPushConfiguration(await externalPushRequest(page.configurationEndpoint, { method: 'GET', headers: { Accept: 'application/json' } }), page);
    if (!panel.isConnected) return;
    configuration = value;
    pushType.value = value.pushType;
    day.value = value.day == null ? '' : String(value.day);
    frequency.value = value.frequency == null ? '' : String(value.frequency);
    remark.value = value.remark;
    params.value = value.customParamsText;
    status.textContent = `配置版本 ${value.revision}`;
  };
  save.addEventListener('click', () => {
    if (!configuration) return showMessage('外推配置尚未读取完成');
    let customParamsText: string;
    let binding: { enabled: boolean; reference: string };
    let configuredDay: number | null;
    let configuredFrequency: number | null;
    try {
      customParamsText = params.value.trim() || '{}';
      const customParams = JSON.parse(customParamsText);
      if (customParams === null || typeof customParams !== 'object') throw new Error('custom_params 必须是 JSON 对象或 key/value 列表');
      binding = configurationBinding(page, ownerDocument);
      configuredDay = externalPushOptionalInteger(day);
      configuredFrequency = externalPushOptionalInteger(frequency);
    } catch (error) {
      showMessage(error instanceof Error ? error.message : '外推参数无效');
      return;
    }
    save.disabled = true;
    void externalPushRequest(page.configurationEndpoint, {
      method: 'PUT',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Idempotency-Key': externalPushConfigurationIdempotencyKey() },
      body: JSON.stringify({ enabled: binding.enabled, configuration_reference: binding.enabled ? binding.reference : '', type: pushType.value, day: configuredDay, frequency: configuredFrequency, remark: remark.value, custom_params: customParamsText, expected_revision: configuration.revision }),
    }).then((saved) => {
      configuration = parseExternalPushConfiguration(saved, page);
      status.textContent = `配置版本 ${configuration.revision}`;
      showMessage('外推业务参数已保存；未发送外部请求。');
    }).catch((error) => showMessage(error instanceof Error ? error.message : '外推参数保存失败')).finally(() => { save.disabled = false; });
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
  if (frozenNotice) frozenNotice.textContent = '保存更新本地外推绑定；“运行测试”只创建受控投递意图，投递结果在下方回读。';
  const panel = ownerDocument.createElement('section');
  panel.id = 'product-v3-external-push-test';
  panel.style.cssText = 'display:grid;gap:9px;margin-top:14px;padding-top:14px;border-top:1px solid #EFF0F1';
  const description = ownerDocument.createElement('p');
  description.textContent = '测试请求仅在已启用绑定后创建本地受理记录；接收方回执不代表业务送达，结果未知不会自动重试。';
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

// Dynamic import is deliberate: validation and click interception must be
// installed before the byte-frozen donor runtime reads the current page.
// @ts-expect-error The donor entry is a side-effect-only script.
void import('../src/admin/main');
