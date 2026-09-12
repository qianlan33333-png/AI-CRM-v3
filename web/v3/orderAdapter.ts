// V3-owned request and presentation Host for the byte-frozen Orders renderer.
// It only maps server-owned filters and display data; identity resolution stays
// behind the existing Order/Customer Ports.

// @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
import { AdminController } from '../src/admin/controller';
// @ts-ignore Frozen transport reused only for same-origin CSRF/session headers.
import { apiRequestOptions } from '../src/api/transport';
import { commerceProviderLabel, commerceStatusLabel } from './commercePresentation';
import { formatShanghaiDateTime } from './adminDateTime';

type OrderController = { page: string; api: { mode: string }; state: { orderFilters: Record<string, string> } };
type DetailRecord = Record<string, unknown>;
type DetailContext = { order?: DetailRecord; items?: unknown[]; refunds?: unknown[]; effects?: unknown[]; refundsUnavailable?: boolean };

const orderPrototype = AdminController.prototype as unknown as { renderVals(this: OrderController): Record<string, any> };
const donorRenderOrders = orderPrototype.renderVals;
orderPrototype.renderVals = function () {
  if (this.page !== 'orders' || this.api.mode !== 'http') return donorRenderOrders.call(this);
  const filters = this.state.orderFilters;
  // Server already applies trusted payer/name filters; donor-side matching
  // would discard valid OneID-resolved results.
  this.state.orderFilters = { ...filters, transactionId: '', payer: '', product: '' };
  try {
    const values = donorRenderOrders.call(this);
    if (values.orderPage) values.orderPage.filters = filters;
    return values;
  } finally { this.state.orderFilters = filters; }
};

const originalFetch = globalThis.fetch.bind(globalThis);
let detailContext: DetailContext = {};

function inputValue(id: string): string {
  const element = document.getElementById(id);
  return element instanceof HTMLInputElement || element instanceof HTMLSelectElement ? element.value.trim() : '';
}

function isOrderDetailPage(): boolean {
  return document.body.dataset.page === 'orderDetail' || /\/orderDetail\.html$/.test(location.pathname);
}

function detailReference(): string {
  return new URL(location.href).searchParams.get('id')?.trim() || '';
}

function asRecord(value: unknown): DetailRecord | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as DetailRecord : undefined;
}

function text(value: unknown, fallback = '未提供'): string {
  return typeof value === 'string' && value.trim() ? value.trim() : fallback;
}

function arrayField(value: unknown, ...keys: string[]): unknown[] {
  const record = asRecord(value);
  for (const key of keys) {
    if (Array.isArray(record?.[key])) return record[key] as unknown[];
  }
  return [];
}

function orderQuery(url: URL): boolean {
  if (url.pathname !== '/api/admin/orders') return false;
  const orderReference = inputValue('orderTransactionId');
  const product = inputValue('orderProductCode');
  const payer = inputValue('orderMobile');
  if (orderReference) url.searchParams.set('order_ref', orderReference);
  if (product) url.searchParams.set('product', product);
  // The browser passes a query to Order. It never turns a phone/contact into a
  // customer ID and never combines both identity dimensions.
  if (payer) {
    const phone = payer.replace(/[\s()-]/g, '').replace(/^\+?86/, '');
    if (/^1[3-9][0-9]{9}$/.test(phone)) url.searchParams.set('phone', phone);
    else url.searchParams.set('external_userid', payer);
  }
  return Boolean(orderReference || product || payer);
}

function showOrderMessage(messageText: string): void {
  const current = document.getElementById('order-v3-query-error');
  if (current) current.remove();
  const message = document.createElement('div');
  message.id = 'order-v3-query-error';
  message.setAttribute('role', 'alert');
  message.textContent = messageText;
  message.style.cssText = 'position:fixed;right:24px;bottom:24px;z-index:10002;padding:12px 16px;border-radius:8px;background:#D83931;color:#fff;font-size:13px;box-shadow:0 8px 28px rgba(0,0,0,.18)';
  document.body.appendChild(message);
  window.setTimeout(() => message.remove(), 5000);
}

function orderReference(row: HTMLTableRowElement): string | undefined {
  const cell = row.querySelectorAll('td')[1];
  const value = cell?.querySelector('div')?.textContent?.trim() || '';
  return value || undefined;
}

function refundScope(order: DetailRecord | undefined): { provider: string; orderNo: string } | undefined {
  if (!order) return undefined;
  const orderNo = text(order.merchant_order_no, '');
  const rawProvider = text(order.provider, '');
  const provider = rawProvider === 'wechat_pay' ? 'wechat' : rawProvider;
  return orderNo && (provider === 'wechat' || provider === 'wechat_shop') ? { provider, orderNo } : undefined;
}

function emptyScopedRefundPage(): Response {
  return new Response(JSON.stringify({ items: [], refunds: [], total: 0, limit: 50, offset: 0, has_more: false }), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  });
}

function schedulePresentation(): void {
  window.queueMicrotask(() => {
    applyOrderContractCopy();
    applyOrderPresentation();
    applyOrderDetailPresentation();
  });
}

function captureDetailResponse(kind: keyof DetailContext, response: Response): void {
  if (!response.ok) return;
  void response.clone().json().then((payload: unknown) => {
    if (kind === 'order') {
      const order = asRecord(payload);
      if (order) detailContext.order = order;
    } else if (kind === 'items' || kind === 'refunds' || kind === 'effects') {
      detailContext[kind] = arrayField(payload, kind === 'items' ? 'items' : kind, 'items');
    }
    schedulePresentation();
  }).catch(() => { /* The donor keeps malformed-response handling. */ });
}

// Never permit the frozen detail renderer to request the global refund page.
// The provider/merchant pair is the Payment identity, so a failed prefetch is
// rendered as unavailable instead of silently showing another order's refunds.
async function primeOrderDetail(): Promise<void> {
  if (!isOrderDetailPage()) return;
  const ref = detailReference();
  if (!ref) { detailContext.refundsUnavailable = true; return; }
  try {
    const response = await originalFetch(`/api/admin/orders/${encodeURIComponent(ref)}`, { credentials: 'same-origin' });
    if (!response.ok) { detailContext.refundsUnavailable = true; return; }
    const order = asRecord(await response.json());
    if (!order || !refundScope(order)) { detailContext.refundsUnavailable = true; return; }
    detailContext.order = order;
  } catch {
    detailContext.refundsUnavailable = true;
  } finally {
    schedulePresentation();
  }
}

// The export endpoint cannot carry a trusted customer query. Prevent a visible
// identity-filtered result from exporting all orders.
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
  if (!reference) { showOrderMessage('订单缺少服务端单号，无法打开详情。'); return; }
  link.textContent = '正在打开…';
  link.setAttribute('aria-disabled', 'true');
  const next = new URL('orderDetail.html', location.href);
  next.searchParams.set('id', reference);
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

  if (isOrderDetailPage() && url.pathname === '/api/admin/refunds') {
    const scope = refundScope(detailContext.order);
    if (!scope) {
      detailContext.refundsUnavailable = true;
      schedulePresentation();
      return emptyScopedRefundPage();
    }
    url.search = '';
    url.searchParams.set('provider', scope.provider);
    url.searchParams.set('order_no', scope.orderNo);
  }

  const response = await originalFetch(url.toString(), init);
  if (isOrderDetailPage() && response.ok) {
    if (/^\/api\/admin\/orders\/[^/]+$/.test(url.pathname)) captureDetailResponse('order', response);
    else if (/^\/api\/admin\/orders\/[^/]+\/items$/.test(url.pathname)) captureDetailResponse('items', response);
    else if (url.pathname === '/api/admin/refunds') captureDetailResponse('refunds', response);
    else if (/^\/api\/admin\/wechat-pay\/orders\/[^/]+\/external-push-deliveries$/.test(url.pathname)) captureDetailResponse('effects', response);
  }
  if (url.pathname !== '/api/admin/orders' || !response.ok) return response;
  try {
    const payload = await response.clone().json() as { items?: unknown[] };
    if (!Array.isArray(payload.items)) return response;
    const items = payload.items.map((value) => {
      const item = asRecord(value);
      if (!item) return value;
      const channel = typeof item.provider_label === 'string' && item.provider_label.trim()
        ? item.provider_label : typeof item.provider === 'string' ? item.provider : item.currency;
      return { ...item, currency: channel };
    });
    const headers = new Headers(response.headers);
    headers.delete('content-length');
    return new Response(JSON.stringify({ ...payload, items }), { status: response.status, statusText: response.statusText, headers });
  } catch {
    return response;
  }
};

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
    if (!created.dataset.orderCreatedAt && /T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})$/.test(raw)) created.dataset.orderCreatedAt = raw;
    if (created.dataset.orderCreatedAt) {
      const normalized = formatShanghaiDateTime(created.dataset.orderCreatedAt);
      if (created.textContent !== normalized) created.textContent = normalized;
    }
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
  if (note) { if (note.textContent !== copy) note.textContent = copy; return; }
  const form = document.getElementById('orderTransactionId')?.closest('div');
  if (!form) return;
  const message = document.createElement('p');
  message.dataset.orderContractNote = '';
  message.style.cssText = 'flex:1 1 100%;margin:0;font-size:12px;color:#A6AAB0';
  message.textContent = copy;
  form.appendChild(message);
}

function panelForHeading(predicate: (heading: string) => boolean): HTMLElement | undefined {
  const heading = Array.from(document.querySelectorAll('h2')).find((item) => predicate(item.textContent?.trim() || ''));
  const panel = heading?.parentElement?.parentElement;
  return panel instanceof HTMLElement ? panel : undefined;
}

function element(tag: string, textContent?: string): HTMLElement {
  const item = document.createElement(tag);
  if (textContent != null) item.textContent = textContent;
  return item;
}

function appendDetailSection(parent: HTMLElement, heading: string, entries: Array<[string, string]>): void {
  const section = element('section');
  section.style.cssText = 'padding:14px 16px;border-top:1px solid #EFF0F1';
  const title = element('h3', heading);
  title.style.cssText = 'margin:0 0 10px;font-size:14px;font-weight:600;color:#1F2329';
  const grid = element('div');
  grid.style.cssText = 'display:grid;grid-template-columns:112px minmax(0,1fr);gap:8px 16px;align-items:baseline';
  for (const [label, value] of entries) {
    const key = element('span', label);
    key.style.cssText = 'font-size:12px;color:#8F959E';
    const content = element('span', value);
    content.style.cssText = 'font-size:13px;color:#1F2329;word-break:break-all';
    grid.append(key, content);
  }
  section.append(title, grid);
  parent.appendChild(section);
}

function customerReference(value: unknown): string {
  const canonical = text(value, '');
  const match = canonical.match(/^customer:([1-9][0-9]*)$/);
  return match ? `CID-${match[1]}` : '未提供';
}

function money(value: unknown): string {
  const amount = text(value, '');
  return /^\d+(?:\.\d{1,2})?$/.test(amount) ? `¥${amount}` : '金额待确认';
}

function replaceRefundPanel(order: DetailRecord): void {
  const native = order.record_origin === 'native';
  const oldPanel = panelForHeading((heading) => heading === '申请退款' || heading === '退款确认' || heading === '退款' || heading.includes('历史只读') || heading === '历史订单，仅供查询');
  if (!oldPanel) return;
  const refunds = detailContext.refunds || [];
  const fingerprint = JSON.stringify({ native, transaction: order.transaction_id, provider: order.provider, refunds, unavailable: detailContext.refundsUnavailable });
  if (oldPanel.dataset.orderRefundFingerprint === fingerprint) return;
  oldPanel.dataset.orderRefundFingerprint = fingerprint;
  oldPanel.replaceChildren();
  const header = element('div');
  header.style.cssText = 'padding:12px 16px;border-bottom:1px solid #EFF0F1';
  const title = element('h2', native ? '退款' : '历史订单，仅供查询');
  title.style.cssText = 'margin:0;font-size:14px;font-weight:600';
  const description = element('p', native
    ? '提交前请核对商品、金额和已核验的微信支付交易单号。退款结果以后续退款记录为准。'
    : '该订单保留历史事实，仅供查询，不支持退款确认。');
  description.style.cssText = 'margin:2px 0 0;font-size:12px;color:#8F959E';
  header.append(title, description);
  oldPanel.appendChild(header);
  const body = element('div');
  body.style.cssText = 'padding:16px;display:grid;gap:10px';
  if (detailContext.refundsUnavailable) {
    const warning = element('p', '退款记录暂不可读取，为避免混入其他订单记录，本页未展示退款列表。');
    warning.style.cssText = 'margin:0;color:#8F5A16;font-size:13px';
    body.appendChild(warning);
  } else if (refunds.length === 0) {
    body.appendChild(element('p', native ? '当前订单没有退款记录。' : '没有历史退款记录。'));
  } else {
    for (const raw of refunds) {
      const refund = asRecord(raw);
      if (!refund) continue;
      const row = element('div');
      row.style.cssText = 'padding:10px;border:1px solid #EFF0F1;border-radius:6px;display:grid;gap:4px';
      row.append(element('strong', `${commerceStatusLabel('refund', refund.status)} · ${money(Number(refund.amount_minor || 0) / 100)}`));
      row.append(element('span', formatShanghaiDateTime(refund.created_at)));
      const reason = text(refund.reason, '');
      if (reason) row.append(element('span', reason));
      body.appendChild(row);
    }
  }
  if (native) appendRefundForm(body, order);
  oldPanel.appendChild(body);
}

function minorAmount(raw: string): number | undefined {
  const match = raw.trim().match(/^(0|[1-9]\d*)(?:\.(\d{1,2}))?$/);
  if (!match) return undefined;
  const minor = Number(match[1]) * 100 + Number((match[2] || '').padEnd(2, '0') || '0');
  return Number.isSafeInteger(minor) && minor > 0 ? minor : undefined;
}

function appendRefundForm(parent: HTMLElement, order: DetailRecord): void {
  const scope = refundScope(order);
  if (!scope || scope.provider !== 'wechat') {
    parent.appendChild(element('p', '当前支付来源暂不支持在本页确认退款。'));
    return;
  }
  const transactionID = text(order.transaction_id, '');
  if (!transactionID) {
    parent.appendChild(element('p', '无法确认退款：未获得已核验的微信支付交易单号。'));
    return;
  }
  const form = element('div');
  form.style.cssText = 'display:grid;gap:12px;border-top:1px solid #EFF0F1;padding-top:14px';
  const amountLabel = element('label');
  amountLabel.append(element('span', '退款金额'));
  const amount = document.createElement('input');
  amount.type = 'text'; amount.value = text(order.amount_yuan, ''); amount.inputMode = 'decimal'; amount.dataset.orderRefundAmount = '';
  amountLabel.appendChild(amount);
  const confirmationLabel = element('label');
  confirmationLabel.append(element('span', '再次输入微信支付交易单号'));
  const confirmation = document.createElement('input');
  confirmation.type = 'text'; confirmation.placeholder = '请输入已核验的微信支付交易单号'; confirmation.dataset.orderRefundTransaction = '';
  confirmationLabel.appendChild(confirmation);
  const reasonLabel = element('label');
  reasonLabel.append(element('span', '退款原因'));
  const reason = document.createElement('select');
  reason.dataset.orderRefundReason = '';
  for (const label of ['客户主动申请退款', '商品或服务异常']) { const option = document.createElement('option'); option.value = label; option.textContent = label; reason.appendChild(option); }
  reasonLabel.appendChild(reason);
  const checkedLabel = element('label');
  const checked = document.createElement('input'); checked.type = 'checkbox'; checked.dataset.orderRefundChecked = '';
  checkedLabel.append(checked, document.createTextNode('已核对付款人、商品、金额、支付来源和微信支付交易单号'));
  const submit = element('button', '确认提交退款申请') as HTMLButtonElement;
  submit.type = 'button';
  submit.addEventListener('click', () => { void submitRefundConfirmation(order, scope, amount, confirmation, reason, checked, submit); });
  form.append(amountLabel, confirmationLabel, reasonLabel, checkedLabel, submit);
  parent.appendChild(form);
}

async function submitRefundConfirmation(order: DetailRecord, scope: { provider: string; orderNo: string }, amount: HTMLInputElement, confirmation: HTMLInputElement, reason: HTMLSelectElement, checked: HTMLInputElement, submit: HTMLButtonElement): Promise<void> {
  const amountMinor = minorAmount(amount.value);
  if (!amountMinor || !checked.checked || !confirmation.value.trim()) {
    showOrderMessage('请完整核对退款金额，并勾选确认后输入已核验的微信支付交易单号。');
    return;
  }
  const requestBody = {
    provider: 'wechat', order_no: scope.orderNo, refund_amount_total: amountMinor,
    reason: reason.value, transaction_id_confirmation: confirmation.value.trim(), checked: true,
  };
  submit.disabled = true;
  try {
    const idempotencyKey = globalThis.crypto?.randomUUID?.() || `order-refund-${Date.now()}`;
    const response = await originalFetch(`/api/admin/wechat-pay/orders/${encodeURIComponent(scope.orderNo)}/refunds`, apiRequestOptions({
      method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(requestBody),
    }));
    if (!response.ok) {
      showOrderMessage('退款确认未通过，请核对交易单号、金额和订单信息后重试。');
      return;
    }
    showOrderMessage('退款申请已受理，实际退款结果请以后的退款记录为准。');
    detailContext.refunds = undefined;
    schedulePresentation();
  } catch {
    showOrderMessage('退款申请暂时无法提交，请稍后重试。');
  } finally { submit.disabled = false; }
}

function applyOrderDetailPresentation(): void {
  if (!isOrderDetailPage() || !detailContext.order) return;
  const order = detailContext.order;
  const card = document.querySelector<HTMLElement>('[data-order-detail-fingerprint]') || panelForHeading((heading) => heading === '订单详情');
  if (!card) return;
  const fingerprint = JSON.stringify({ order, items: detailContext.items?.map(asRecord), refunds: detailContext.refunds?.map(asRecord), unavailable: detailContext.refundsUnavailable });
  if (card.dataset.orderDetailFingerprint === fingerprint) { replaceRefundPanel(order); return; }
  card.dataset.orderDetailFingerprint = fingerprint;
  card.replaceChildren();
  const header = element('div');
  header.style.cssText = 'padding:12px 16px;border-bottom:1px solid #EFF0F1';
  const heading = element('h2', '订单信息'); heading.style.cssText = 'margin:0;font-size:14px;font-weight:600';
  const note = element('p', order.record_origin === 'native' ? '以下信息来自服务端订单事实。' : '历史订单，仅供查询。');
  note.style.cssText = 'margin:2px 0 0;font-size:12px;color:#8F959E';
  header.append(heading, note);
  card.appendChild(header);
  appendDetailSection(card, '订单信息', [
    ['商户订单号', text(order.merchant_order_no)],
    ['创建时间', formatShanghaiDateTime(order.created_at)],
    ['订单状态', order.record_origin === 'native' ? commerceStatusLabel('order', order.status_label || order.status) : `历史记录：${commerceStatusLabel('order', order.status_label || order.status)}`],
  ]);
  appendDetailSection(card, '支付信息', [
    ['支付来源', commerceProviderLabel(order.provider)],
    ['微信支付交易单号', text(order.transaction_id)],
  ]);
  appendDetailSection(card, '买家信息', [
    ['买家', text(order.payer_name, '未提供')],
    ['客户编号', customerReference(order.payer_id)],
    ['手机号', text(order.payer_phone_masked, '未提供')],
  ]);
  const items = detailContext.items || [];
  const names = items.map(asRecord).filter((item): item is DetailRecord => Boolean(item)).map((item) => text(item.name, '')).filter(Boolean);
  appendDetailSection(card, '商品与金额', [
    ['商品名称', names.join('、') || text(order.product_name, '未提供')],
    ['付款金额', money(order.amount_yuan)],
  ]);
  const timeline = panelForHeading((label) => label === '事件时间线');
  if (timeline) timeline.hidden = true;
  replaceRefundPanel(order);
}

const observer = new MutationObserver(() => {
  applyOrderContractCopy();
  applyOrderPresentation();
  applyOrderDetailPresentation();
});
observer.observe(document, { childList: true, subtree: true });
window.addEventListener('pagehide', () => observer.disconnect(), { once: true });
applyOrderContractCopy();
applyOrderPresentation();

// Prime the detail identity before the frozen renderer requests its parallel
// resources; then the refund request is always exact-scoped or safely empty.
void (async () => {
  await primeOrderDetail();
  if (document.getElementById('stage') && ['orders', 'orderDetail'].includes(document.body.dataset.page || '')) {
    // @ts-ignore Byte-frozen side-effect entry.
    await import('../src/admin/main');
  }
})();
