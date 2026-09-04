// This is the only v3-owned browser seam for the byte-frozen Product UI.
// It validates the authoritative lifecycle/sales projection, supplies Chinese
// display labels, and replaces only the ordinary-product share interaction.
import { api } from '../src/shared/api/client';
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
  const db = await donorLoadDb(context);
  if (context?.page !== 'products' && context?.page !== 'productForm') return db;
  let rawItems: unknown[];
  if (context.page === 'productForm' && /^[1-9][0-9]*$/.test(context.id || '')) {
    rawItems = [await readJSON(`/api/v1/products/${context.id}`)];
  } else {
    rawItems = list(object(await readJSON('/api/v1/products')).items);
  }
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

function button(label: string): HTMLButtonElement {
  const node = document.createElement('button');
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

// Dynamic import is deliberate: validation and click interception must be
// installed before the byte-frozen donor runtime reads the current page.
// @ts-expect-error The donor entry is a side-effect-only script.
void import('../src/admin/main');
