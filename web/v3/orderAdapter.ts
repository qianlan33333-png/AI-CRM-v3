// V3-owned request shim for the byte-frozen Orders renderer.  It only maps
// server-owned filters, read presentation and donor controller bootstrap.
// Identity matching remains in the existing Order / OneID port.

// @ts-ignore The frozen controller is materialized by prepare-donor-views.
import { AdminController } from '../src/admin/controller';

type OrderController = { page: string; api: { mode: string }; state: { orderFilters: Record<string, string> } };
const orderPrototype = AdminController.prototype as unknown as { renderVals(this: OrderController): Record<string, any> };
const donorRenderOrders = orderPrototype.renderVals;
orderPrototype.renderVals = function () {
  if (this.page !== 'orders' || this.api.mode !== 'http') return donorRenderOrders.call(this);
  const filters = this.state.orderFilters;
  // The server has already filtered the whole result set. The frozen donor's
  // old payer/name filter would discard resolved phone/contact matches again.
  this.state.orderFilters = { ...filters, transactionId: '', payer: '', product: '' };
  try {
    const values = donorRenderOrders.call(this);
    if (values.orderPage) values.orderPage.filters = filters;
    return values;
  } finally { this.state.orderFilters = filters; }
};

const originalFetch = globalThis.fetch.bind(globalThis);

function inputValue(id: string): string {
  const element = document.getElementById(id);
  return element instanceof HTMLInputElement || element instanceof HTMLSelectElement ? element.value.trim() : '';
}

function orderQuery(url: URL): boolean {
  if (url.pathname !== '/api/admin/orders') return false;
  const orderReference = inputValue('orderTransactionId');
  const product = inputValue('orderProductCode');
  const payer = inputValue('orderMobile');
  if (orderReference) url.searchParams.set('order_ref', orderReference);
  if (product) url.searchParams.set('product', product);
  // The Order owner resolves only one of these through its stable Customer /
  // Identity Port. The browser never turns a phone or external contact into a
  // Customer ID itself, and it never sends both identity dimensions together.
  if (payer) {
    const phone = payer.replace(/[\s()-]/g, '').replace(/^\+?86/, '');
    if (/^1[3-9][0-9]{9}$/.test(phone)) url.searchParams.set('phone', phone);
    else url.searchParams.set('external_userid', payer);
  }
  return Boolean(orderReference || product || payer);
}

function showOrderMessage(text: string): void {
  const current = document.getElementById('order-v3-query-error');
  if (current) return;
  const message = document.createElement('div');
  message.id = 'order-v3-query-error';
  message.setAttribute('role', 'alert');
  message.textContent = text;
  message.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  document.body.appendChild(message);
  window.setTimeout(() => message.remove(), 5000);
}

function orderReference(row: HTMLTableRowElement): string | undefined {
  const cell = row.querySelectorAll('td')[1];
  const value = cell?.querySelector('div')?.textContent?.trim() || '';
  return value || undefined;
}



// The order export endpoint deliberately does not accept trusted identity
// filters.  Stopping this click avoids downloading an unfiltered report when
// the visible result set is narrowed by phone or external contact ID.
document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest('button');
  if (!button || button.textContent?.trim() !== '导出微信支付 CSV' || !inputValue('orderMobile')) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  showOrderMessage('当前手机号或外部联系人 ID 筛选暂不支持导出。请清空该筛选后再导出，避免导出全量订单。');
}, true);

document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const link = target.closest('a');
  if (!link || link.textContent?.trim() !== '查看详情') return;
  const row = link.closest('tr');
  if (!(row instanceof HTMLTableRowElement)) return;
  const reference = orderReference(row);
  event.preventDefault();
  event.stopImmediatePropagation();
  if (!reference) {
    showOrderMessage('订单缺少服务端单号，无法打开详情。');
    return;
  }
  link.textContent = '正在打开…';
  link.setAttribute('aria-disabled', 'true');
  const next = new URL('orderDetail.html', location.href);
  next.searchParams.set('id', reference);
  // Retain a usable real link before navigating.  This makes the detail
  // destination inspectable and preserves keyboard/open-in-new-tab behavior.
  link.href = next.toString();
  location.assign(next.toString());
}, true);

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = input instanceof Request ? input : undefined;
  const method = (init?.method || request?.method || 'GET').toUpperCase();
  if (method !== 'GET') return originalFetch(input, init);
  const url = new URL(request?.url || String(input), location.origin);
  if (url.origin !== location.origin) return originalFetch(input, init);
  orderQuery(url);
  const response = await originalFetch(url.toString(), init);
  if (url.pathname !== '/api/admin/orders' || !response.ok) return response;
  try {
    const payload = await response.clone().json() as { items?: unknown[] };
    if (!Array.isArray(payload.items)) return response;
    const items = payload.items.map((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
      const item = value as Record<string, unknown>;
      // The donor renders its payment-channel slot from `currency`.  The
      // Order owner already returns the authoritative provider label; map only
      // that display field so amount and filter semantics remain unchanged.
      const channel = typeof item.provider_label === 'string' && item.provider_label.trim()
        ? item.provider_label
        : typeof item.provider === 'string' ? item.provider : item.currency;
      return { ...item, currency: channel };
    });
    const headers = new Headers(response.headers);
    headers.delete('content-length');
    return new Response(JSON.stringify({ ...payload, items }), { status: response.status, statusText: response.statusText, headers });
  } catch {
    // Let the frozen renderer own malformed read-response handling.
    return response;
  }
};

function beijingTime(value: string): string {
  const instant = new Date(value);
  if (Number.isNaN(instant.getTime())) return value;
  const parts = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(instant);
  const part = (kind: Intl.DateTimeFormatPartTypes): string => parts.find((item) => item.type === kind)?.value || '';
  return `${part('year')}-${part('month')}-${part('day')} ${part('hour')}:${part('minute')}:${part('second')}`;
}

function applyOrderPresentation(): void {
  const table = Array.from(document.querySelectorAll('table')).find((candidate) => candidate.querySelector('thead')?.textContent?.includes('微信 / 平台单号'));
  if (!table) return;
  const header = table.querySelectorAll('th')[2];
  if (header && header.textContent !== '付款人') header.textContent = '付款人';
  table.querySelectorAll<HTMLTableRowElement>('tbody tr').forEach((row) => {
    const cells = row.querySelectorAll<HTMLTableCellElement>('td');
    if (cells.length < 3) return;
    const created = cells[0];
    const raw = created.dataset.orderCreatedAt || created.textContent?.trim() || '';
    // The donor can rerender rows under this observer.  Only an original ISO
    // instant may be converted; a finished Beijing display must remain text,
    // otherwise a non-Shanghai browser would add eight hours on each pass.
    if (!created.dataset.orderCreatedAt && /T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})$/.test(raw)) created.dataset.orderCreatedAt = raw;
    if (created.dataset.orderCreatedAt) {
      const normalized = beijingTime(created.dataset.orderCreatedAt);
      if (normalized && created.textContent !== normalized) created.textContent = normalized;
    }
    // `payer_id` may be a canonical internal reference. Keep the real payer
    // name and suppress that implementation detail in the frozen row markup.
    const internal = cells[2].querySelector<HTMLElement>('div:nth-child(2)');
    if (internal && !internal.hidden) internal.hidden = true;
  });
}

function applyOrderContractCopy(): void {
  const payer = document.getElementById('orderMobile');
  if (payer instanceof HTMLInputElement && payer.placeholder !== '手机号或外部联系人 ID') payer.placeholder = '手机号或外部联系人 ID';
  const payerLabel = payer?.closest('label')?.querySelector('span');
  if (payerLabel && payerLabel.textContent !== '手机号 / 外部联系人 ID') payerLabel.textContent = '手机号 / 外部联系人 ID';
  const copy = '支持按单号、商品、手机号或外部联系人 ID 查询。';
  const oldNote = Array.from(document.querySelectorAll('p')).find((item) => item.textContent?.includes('OpenAPI 暂不支持这三项的跨页检索'));
  if (oldNote) { oldNote.dataset.orderContractNote = ''; oldNote.textContent = copy; }
  const note = document.querySelector<HTMLElement>('[data-order-contract-note]');
  if (note) {
    if (note.textContent !== copy) note.textContent = copy;
    return;
  }
  const form = document.getElementById('orderTransactionId')?.closest('div');
  if (!form) return;
  const message = document.createElement('p');
  message.dataset.orderContractNote = '';
  message.style.cssText = 'flex:1 1 100%;margin:0;font-size:12px;color:#A6AAB0';
  message.textContent = copy;
  form.appendChild(message);
}

const observer = new MutationObserver(() => {
  applyOrderContractCopy();
  applyOrderPresentation();
});
observer.observe(document, { childList: true, subtree: true });
window.addEventListener('pagehide', () => observer.disconnect(), { once: true });
applyOrderContractCopy();
applyOrderPresentation();

// Own the donor bootstrap so this Host and the renderer share one controller.
if (document.getElementById('stage') && ['orders', 'orderDetail'].includes(document.body.dataset.page || '')) {
  // @ts-ignore Byte-frozen side-effect entry.
  void import('../src/admin/main');
}
