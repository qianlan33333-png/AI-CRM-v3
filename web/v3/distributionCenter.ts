// This browser entry is independently bundled by the host-adapter build.  Mark
// it as an ES module so the repository-wide TypeScript check does not merge its
// private helpers into the administrator entry's global scope.
export {};

type Row = Record<string, unknown>;
type Distributor = { publicNo: string; enabled: boolean; agreementVersion: string; registeredAt: string };
type Readiness = { ready: boolean; reason: string; appID: string };
type Me = { distributor?: Distributor; readiness: Readiness; registrationRequired: boolean; currentAgreementVersion: string };
type ReceiverPreparation = { readiness: Readiness; state: 'ready' | 'processing' | 'requires_wechat_session' | 'unavailable'; retryAfterSeconds: number };
type Agreement = { version: string; content: string };
type PromotionProduct = { id: number; type: string; coverURL: string; purchaseURL: string; name: string; priceMinor: number; currency: string; rate: number; estimatedMinor: number; waitDays: number; ready: boolean; blockReason: string };
type Earnings = { gross: number; refunds: number; initial: number; adjustments: number; unsettled: number; paid: number; recovered: number; currency: string };
type Commission = { id: string; order: string; product: string; initial: number; payable: number; paid: number; status: string; holdReason: string; cancelReason: string; exceptionReason: string; paidConfirmedAt: string; dueAt: string; paidAt: string; createdAt: string; currency: string };

const rootElement = document.getElementById('distribution-root');
if (!rootElement) throw new Error('分销中心容器缺失');
const root: HTMLElement = rootElement;

const obj = (value: unknown): Row => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Row : {};
const string = (value: unknown, field: string, optional = false): string => {
  if (optional && (value === undefined || value === null)) return '';
  if (typeof value !== 'string') throw new Error(`分销响应缺少 ${field}`);
  return value;
};
const integer = (value: unknown, field: string, minimum = 0): number => {
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < minimum) throw new Error(`分销响应缺少有效 ${field}`);
  return number;
};
const signedInteger = (value: unknown, field: string): number => { const number = Number(value); if (!Number.isSafeInteger(number)) throw new Error(`分销响应缺少有效 ${field}`); return number; };
const bool = (value: unknown, field: string): boolean => {
  if (typeof value !== 'boolean') throw new Error(`分销响应缺少 ${field}`);
  return value;
};
function errorText(status: number, payload: unknown): string {
  const code = obj(payload).error;
  const known: Record<string, string> = {
    qualification_unavailable: '推广资格暂时无法确认，未生成推广入口。', receiver_unavailable: '收款准备未完成，暂不能生成推广入口。', conflict: '数据已变化，请刷新后重试。', forbidden: '当前会话无权执行此操作。', unavailable: '服务暂时不可用，请稍后重试。', not_found: '请求的分销记录不存在。', invalid_request: '提交内容无效，未执行操作。',
  };
  return typeof code === 'string' && known[code] ? known[code] : `请求失败（HTTP ${status}）`;
}
function distributionCSRF(): string {
  for (const part of document.cookie.split(';')) {
    const [name, ...rest] = part.trim().split('=');
    if (name === 'aicrm_distribution_csrf') return decodeURIComponent(rest.join('='));
  }
  return '';
}
const idempotencyKeys = new Map<string, string>();
function mutationHeaders(operation: string): HeadersInit {
  let key = idempotencyKeys.get(operation);
  if (!key) {
    key = typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `${Date.now()}-${crypto.getRandomValues(new Uint32Array(2)).join('-')}`;
    idempotencyKeys.set(operation, key);
  }
  return { 'Idempotency-Key': key };
}
async function request(path: string, init: RequestInit = {}): Promise<unknown> {
  const headers = new Headers(init.headers); headers.set('Accept', 'application/json');
  if (init.method && init.method !== 'GET') {
    if (init.body !== undefined) headers.set('Content-Type', 'application/json');
    // The bridge deliberately has no distribution CSRF cookie yet. The server
    // permits only that first POST with same-origin and short Payment-session
    // checks, then issues the cookie used by every later mutation.
    if (path !== '/api/v1/distribution/session/bridge') {
      const token = distributionCSRF();
      if (token) headers.set('X-Distribution-CSRF', token);
    }
  }
  let response: Response;
  try { response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' }); } catch { throw new Error('网络不可用，未确认任何分销状态。'); }
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) { const error = new Error(errorText(response.status, payload)); (error as Error & { status?: number }).status = response.status; throw error; }
  return payload;
}
function parseMe(raw: unknown): Me {
	const value = obj(raw); const readiness = obj(value.receiver); const distributorRaw = value.distributor;
  let distributor: Distributor | undefined;
  if (distributorRaw !== undefined && distributorRaw !== null) {
    const row = obj(distributorRaw);
    distributor = { publicNo: string(row.public_no, 'distributor.public_no'), enabled: bool(row.enabled, 'distributor.enabled'), agreementVersion: string(row.agreement_version, 'distributor.agreement_version'), registeredAt: string(row.registered_at, 'distributor.registered_at') };
  }
  return { distributor, readiness: { ready: bool(readiness.ready, 'receiver_readiness.ready'), reason: string(readiness.reason, 'receiver_readiness.reason', true), appID: string(readiness.app_id, 'receiver_readiness.app_id', true) }, registrationRequired: bool(value.registration_required, 'registration_required'), currentAgreementVersion: string(value.current_agreement_version, 'current_agreement_version', true) };
}
function parseAgreement(raw: unknown): Agreement { const value = obj(raw); return { version: string(value.version, 'agreement.version'), content: string(value.content, 'agreement.content') }; }
function parseReceiverPreparation(raw: unknown): ReceiverPreparation {
  const value = obj(raw); const readiness = obj(value.receiver); const setup = obj(value.setup); const state = string(setup.state, 'setup.state');
  if (!['ready', 'processing', 'requires_wechat_session', 'unavailable'].includes(state)) throw new Error('收款准备状态无效');
  return { readiness: { ready: bool(readiness.ready, 'receiver_readiness.ready'), reason: string(readiness.reason, 'receiver_readiness.reason', true), appID: string(readiness.app_id, 'receiver_readiness.app_id', true) }, state: state as ReceiverPreparation['state'], retryAfterSeconds: integer(setup.retry_after_seconds, 'setup.retry_after_seconds') };
}
function serverPurchaseURL(value: unknown): string {
  const raw = string(value, 'purchase_url');
  const url = new URL(raw, location.origin);
  if (url.origin !== location.origin || !/^\/(p|s)\//.test(url.pathname)) throw new Error('购买入口地址无效');
  return url.pathname + url.search + url.hash;
}
function parseProducts(raw: unknown): PromotionProduct[] {
  const value = obj(raw); if (!Array.isArray(value.items)) throw new Error('推广商品响应无效');
  return value.items.map((item) => { const row = obj(item); const rate = integer(row.commission_rate_basis_points, 'commission_rate_basis_points'); if (rate > 3000) throw new Error('推广商品佣金比例超出合同范围'); const waitDays = integer(row.wait_days, 'wait_days'); if (waitDays > 29) throw new Error('推广商品等待天数超出合同范围'); return { id: integer(row.product_id, 'product_id', 1), type: string(row.product_type, 'product_type'), coverURL: string(row.cover_url, 'cover_url', true), purchaseURL: serverPurchaseURL(row.purchase_url), name: string(row.name, 'name'), priceMinor: integer(row.price_minor, 'price_minor'), currency: string(row.currency, 'currency'), rate, estimatedMinor: integer(row.estimated_commission_minor, 'estimated_commission_minor'), waitDays, ready: bool(row.promotion_ready, 'promotion_ready'), blockReason: string(row.promotion_block_reason, 'promotion_block_reason', true) }; });
}
function parseEarnings(raw: unknown): Earnings { const row = obj(raw); return { gross: integer(row.gross_paid_sales_minor, 'gross_paid_sales_minor'), refunds: integer(row.successful_refunds_minor, 'successful_refunds_minor'), initial: integer(row.initial_commission_minor, 'initial_commission_minor'), adjustments: signedInteger(row.commission_adjustments_minor, 'commission_adjustments_minor'), unsettled: integer(row.unsettled_payable_minor, 'unsettled_payable_minor'), paid: integer(row.paid_commission_minor, 'paid_commission_minor'), recovered: integer(row.recovered_minor, 'recovered_minor'), currency: string(row.currency, 'currency') }; }
function parseCommissions(raw: unknown): Commission[] { const row = obj(raw); if (!Array.isArray(row.items)) throw new Error('收益明细响应无效'); return row.items.map((item) => { const value = obj(item); return { id: string(value.commission_id, 'commission_id'), order: string(value.order_reference, 'order_reference'), product: string(value.product_name, 'product_name'), initial: integer(value.initial_minor, 'initial_minor'), payable: integer(value.current_payable_minor, 'current_payable_minor'), paid: integer(value.paid_minor, 'paid_minor'), status: string(value.status, 'status'), holdReason: string(value.hold_reason, 'hold_reason', true), cancelReason: string(value.cancel_reason, 'cancel_reason', true), exceptionReason: string(value.exception_reason, 'exception_reason', true), paidConfirmedAt: string(value.paid_confirmed_at, 'paid_confirmed_at'), dueAt: string(value.due_at, 'due_at', true), paidAt: string(value.paid_at, 'paid_at', true), createdAt: string(value.created_at, 'created_at'), currency: string(value.currency, 'currency') }; }); }
function money(minor: number, currency = 'CNY'): string { return new Intl.NumberFormat('zh-CN', { style: 'currency', currency, minimumFractionDigits: 2 }).format(minor / 100); }
function time(value: string): string { const date = new Date(value); return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Asia/Shanghai' }).format(date); }
function el<K extends keyof HTMLElementTagNameMap>(tag: K, text?: string): HTMLElementTagNameMap[K] { const node = document.createElement(tag); if (text !== undefined) node.textContent = text; return node; }
function action(label: string, handler: () => void | Promise<void>, className = 'distribution-button'): HTMLButtonElement { const button = el('button', label); button.type = 'button'; button.className = className; button.addEventListener('click', () => void handler()); return button; }
function status(value: string): HTMLSpanElement { const node = el('span', value); node.className = `distribution-status distribution-status-${value}`; return node; }

let me: Me | undefined; let agreement: Agreement | undefined; let bridgeAttempted = false; let products: PromotionProduct[] = []; let productCursor = ''; let earnings: Earnings | undefined; let commissions: Commission[] = []; let commissionCursor = ''; let tab: 'products' | 'earnings' = 'products'; let commissionStatus = '';
function message(text: string, isError = false): void { const node = document.querySelector<HTMLElement>('[data-distribution-message]'); if (node) { node.textContent = text; node.dataset.error = String(isError); } }
async function reload(): Promise<void> {
  message('正在读取服务端分销状态…');
  try {
    const meRaw = await request('/api/v1/distribution/me'); me = parseMe(meRaw);
    if (me.registrationRequired) {
      agreement = parseAgreement(await request('/api/v1/distribution/agreement'));
    } else {
      const [productRaw, earningsRaw, commissionRaw] = await Promise.all([request('/api/v1/distribution/products?limit=50'), request('/api/v1/distribution/earnings'), request('/api/v1/distribution/commissions?limit=50')]);
      products = parseProducts(productRaw); productCursor = string(obj(productRaw).next_cursor, 'next_cursor', true); earnings = parseEarnings(earningsRaw); commissions = parseCommissions(commissionRaw); commissionCursor = string(obj(commissionRaw).next_cursor, 'next_cursor', true);
    }
    render(); message('');
  } catch (error) {
    if ((error as Error & { status?: number }).status === 401 && !bridgeAttempted) { bridgeAttempted = true; try { await request('/api/v1/distribution/session/bridge', { method: 'POST', headers: mutationHeaders('session-bridge') }); await reload(); return; } catch { renderLogin(); return; } }
    if ((error as Error & { status?: number }).status === 401) { renderLogin(); return; }
    root.replaceChildren(el('section', error instanceof Error ? error.message : '分销状态读取失败')); root.firstElementChild?.classList.add('distribution-error');
  }
}
function renderLogin(): void { root.replaceChildren(); const card = el('section'); card.className = 'distribution-card distribution-login'; card.append(el('h1', '分销中心'), el('p', '请先使用微信登录，再查看推广资格和收益。')); const link = el('a', '使用微信登录'); link.href = '/auth/wechat/start?next=%2Fdistribution'; link.className = 'distribution-button'; card.append(link); root.append(card); }
function renderRegistration(): void { root.replaceChildren(); const card = el('section'); card.className = 'distribution-card distribution-login'; card.append(el('h1', '申请成为分销员'), el('p', '注册成功后，可查看推广商品和我的收益；微信收款准备完成后才能生成推广入口。'));
  if (!me?.currentAgreementVersion || !agreement || agreement.version !== me.currentAgreementVersion) { card.append(el('p', '当前分销协议版本读取失败，未提交注册。')); root.append(card); return; }
  const agree = el('label'); const check = document.createElement('input'); check.type = 'checkbox'; agree.append(check, document.createTextNode(` 我已阅读并同意当前分销协议（${me.currentAgreementVersion}）`));
  const agreementDetails = el('details'); agreementDetails.append(el('summary', `查看分销协议（${agreement.version}）`), el('p', agreement.content));
  const feedback = el('p'); feedback.dataset.distributionMessage = ''; feedback.className = 'distribution-message';
  card.append(agreementDetails, agree, action('同意并注册', async () => { if (!check.checked) { message('请先同意当前分销协议。', true); return; } try { const result = await request('/api/v1/distribution/registration', { method: 'POST', headers: mutationHeaders('registration'), body: JSON.stringify({ agreement_version: me!.currentAgreementVersion }) }); me = parseMe(result); await reload(); } catch (error) { message(error instanceof Error ? error.message : '注册失败', true); } }), feedback); root.append(card); }
function render(): void {
  if (!me) return; if (me.registrationRequired) { renderRegistration(); return; }
  root.replaceChildren(); const head = el('header'); head.className = 'distribution-head'; const identity = el('div'); identity.append(el('h1', '分销中心'), el('p', `分销员编号：${me.distributor?.publicNo || '待确认'} · 协议：${me.distributor?.agreementVersion || '—'}`)); head.append(identity, status(me.distributor?.enabled ? '已启用' : '已停用'));
  const readiness = el('div'); readiness.className = 'distribution-readiness'; readiness.append(el('strong', me.readiness.ready ? '微信收款准备完成' : '微信收款准备未完成'), el('span', me.readiness.reason || (me.readiness.ready ? '可生成有效推广入口' : '请完成微信收款准备'))); if (!me.readiness.ready) readiness.append(action('完成收款准备', prepareReceiver)); head.append(readiness); root.append(head);
  const tabs = el('nav'); tabs.className = 'distribution-tabs'; for (const [key, label] of [['products', '推广商品'], ['earnings', '我的收益']] as const) { const button = action(label, () => { tab = key; render(); }, `distribution-tab${tab === key ? ' active' : ''}`); button.setAttribute('aria-current', tab === key ? 'page' : 'false'); tabs.append(button); } root.append(tabs);
  root.append(tab === 'products' ? productView() : earningsView()); const notice = el('p'); notice.dataset.distributionMessage = ''; notice.className = 'distribution-message'; root.append(notice);
}
function productView(): HTMLElement { const section = el('section'); section.className = 'distribution-grid'; if (!products.length) { section.append(el('p', '当前没有符合资格的可推广商品。完成同一商品的有效购买后，可从商品页进入购买。')); return section; } for (const item of products) { const card = el('article'); card.className = 'distribution-card distribution-product'; if (item.coverURL) { const image = document.createElement('img'); image.src = item.coverURL; image.alt = ''; card.append(image); } const body = el('div'); body.append(el('h2', item.name), el('p', `${money(item.priceMinor, item.currency)} · 佣金 ${(item.rate / 100).toFixed(2)}% · 预计 ${money(item.estimatedMinor, item.currency)}`), el('p', `支付确认后等待 ${item.waitDays} 天复核退款状态。`)); const reason = item.ready ? '' : item.blockReason || '微信收款准备未完成'; if (!item.ready) body.append(el('p', `暂不能推广：${reason}`)); const purchase = el('a', '查看商品并购买'); purchase.href = item.purchaseURL; purchase.className = 'distribution-button'; body.append(purchase, action(item.ready ? '生成推广入口' : '完善收款准备', async () => { if (!item.ready) { await prepareReceiver(); return; } await createCredential(item); }, item.ready ? 'distribution-button primary' : 'distribution-button')); card.append(body); section.append(card); } if (productCursor) { const more = action('加载更多商品', async () => { try { const raw = await request(`/api/v1/distribution/products?limit=50&cursor=${encodeURIComponent(productCursor)}`); products.push(...parseProducts(raw)); productCursor = string(obj(raw).next_cursor, 'next_cursor', true); render(); } catch (error) { message(error instanceof Error ? error.message : '推广商品读取失败', true); } }); section.append(more); } return section; }
async function prepareReceiver(): Promise<void> {
  try {
    const prepared = parseReceiverPreparation(await request('/api/v1/distribution/receiver-preparation', { method: 'POST', headers: mutationHeaders('receiver-preparation') }));
    if (prepared.state === 'requires_wechat_session') { location.assign('/auth/wechat/start?next=%2Fdistribution'); return; }
    if (prepared.state === 'processing') { message(`收款准备处理中，请在 ${prepared.retryAfterSeconds} 秒后刷新状态。`); return; }
    if (prepared.state === 'unavailable') { message(prepared.readiness.reason || '收款准备暂不可用，请稍后重试。', true); return; }
    await reload();
  } catch (error) { message(error instanceof Error ? error.message : '收款准备失败', true); }
}
async function createCredential(item: PromotionProduct): Promise<void> { try { const result = obj(await request(`/api/v1/distribution/products/${item.id}/promotion-credentials`, { method: 'POST', headers: mutationHeaders(`promotion-credential:${item.id}`), body: JSON.stringify({ product_type: item.type }) })); const url = string(result.promotion_url, 'promotion_url'); const parsed = new URL(url, location.origin); if (parsed.origin !== location.origin || !/^\/d\/[A-Za-z0-9_-]{16,200}$/.test(parsed.pathname) || parsed.search || parsed.hash) throw new Error('推广入口不是当前站点的受控路径'); await showPromotion(parsed.toString(), string(result.credential_expires_at, 'credential_expires_at')); } catch (error) { await reload(); message(error instanceof Error ? error.message : '推广入口生成失败', true); } }
async function showPromotion(url: string, expiresAt: string): Promise<void> { const dialog = document.createElement('dialog'); dialog.className = 'distribution-dialog'; const card = el('section'); card.className = 'distribution-card'; card.append(el('h2', '专属推广入口'), el('p', `有效期至：${time(expiresAt)}`)); const qr = el('div'); qr.className = 'distribution-qr'; const { renderQr } = await import('../src/admin/sections/qr'); renderQr(qr, url, '推广入口'); const link = el('input') as HTMLInputElement; link.value = url; link.readOnly = true; card.append(qr, link, action('复制链接', async () => { if (!navigator.clipboard?.writeText) { link.focus(); link.select(); message('当前环境不支持自动复制，请复制页面中的推广链接。', true); return; } try { await navigator.clipboard.writeText(url); message('推广链接已复制。'); dialog.close(); } catch { link.focus(); link.select(); message('未能自动复制，请复制页面中的推广链接。', true); } }), action('关闭', () => dialog.close())); dialog.append(card); dialog.addEventListener('close', () => dialog.remove()); document.body.append(dialog); dialog.showModal(); }
function earningsView(): HTMLElement { const section = el('section'); if (!earnings) { section.append(el('p', '收益汇总读取失败。')); return section; } const cards = el('div'); cards.className = 'distribution-metrics'; const entries: Array<[string, string, string]> = [['累计推广成交额', money(earnings.gross, earnings.currency), `退款另列 ${money(earnings.refunds, earnings.currency)}`], ['累计产生佣金', money(earnings.initial, earnings.currency), `调整另列 ${money(earnings.adjustments, earnings.currency)}`], ['未结算佣金', money(earnings.unsettled, earnings.currency), '含暂缓及异常待付'], ['已到账佣金', money(earnings.paid, earnings.currency), `追回另列 ${money(earnings.recovered, earnings.currency)}`]]; for (const [label, value, note] of entries) { const card = el('article'); card.className = 'distribution-card'; card.append(el('span', label), el('strong', value), el('small', note)); cards.append(card); } section.append(cards);
  const filters = el('div'); filters.className = 'distribution-filters'; for (const [value, label] of [['', '全部'], ['pending', '待结算'], ['held', '暂缓'], ['settling', '结算中'], ['paid', '已到账'], ['cancelled', '已取消'], ['exception', '异常']] as const) filters.append(action(label, async () => { try { commissionStatus = value; const query = value ? `?status=${encodeURIComponent(value)}&limit=50` : '?limit=50'; const raw = await request(`/api/v1/distribution/commissions${query}`); commissions = parseCommissions(raw); commissionCursor = string(obj(raw).next_cursor, 'next_cursor', true); render(); } catch (error) { message(error instanceof Error ? error.message : '佣金明细读取失败', true); } }, `distribution-tab${commissionStatus === value ? ' active' : ''}`)); section.append(filters);
  const list = el('div'); list.className = 'distribution-list'; for (const row of commissions.filter((item) => !commissionStatus || item.status === commissionStatus)) { const item = el('article'); item.className = 'distribution-card'; item.append(el('h3', row.product), el('p', `订单 ${row.order} · ${commissionStatusLabel(row.status)}`), el('p', `初始 ${money(row.initial, row.currency)} · 当前应付 ${money(row.payable, row.currency)} · 已到账 ${money(row.paid, row.currency)}`), el('small', `支付时间 ${time(row.paidConfirmedAt)} · 预计结算 ${time(row.dueAt)}${row.paidAt ? ` · 到账时间 ${time(row.paidAt)}` : ''}`), el('small', row.holdReason || row.cancelReason || row.exceptionReason || '暂无补充说明')); list.append(item); } if (!list.childElementCount) list.append(el('p', '暂无该状态的佣金记录。')); section.append(list); if (commissionCursor) section.append(action('加载更多明细', async () => { try { const params = new URLSearchParams({ limit: '50', cursor: commissionCursor }); if (commissionStatus) params.set('status', commissionStatus); const raw = await request(`/api/v1/distribution/commissions?${params}`); commissions.push(...parseCommissions(raw)); commissionCursor = string(obj(raw).next_cursor, 'next_cursor', true); render(); } catch (error) { message(error instanceof Error ? error.message : '佣金明细读取失败', true); } })); return section; }
function commissionStatusLabel(value: string): string { return ({ pending: '待结算', held: '暂缓', settling: '结算中', paid: '已到账', cancelled: '已取消', exception: '异常', zero_commission: '零佣金成交' } as Record<string, string>)[value] || '状态待确认'; }

void reload();
