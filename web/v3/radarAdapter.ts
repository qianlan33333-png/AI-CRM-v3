export {};
import { api } from '../src/shared/api/client';
import { rememberActionInputs, runAction } from './actionFeedback';
import { formatShanghaiDateTime, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime';

const takeRadarUploadInput = rememberActionInputs((input) =>
  document.body.dataset.page === 'radarForm' && Boolean(input.files?.length),
);
for (const method of ['uploadRadarImage', 'uploadRadarPdf'] as const) {
  const original = api[method].bind(api);
  api[method] = (file) => runAction(takeRadarUploadInput(), () => original(file), '上传中…');
}

type MaterialItem = { type: 'image' | 'attachment'; library_id: number; title?: string; subtitle?: string; thumbnail_url?: string; metadata?: Record<string, unknown> };
type StandardWindow = Window & {
  AICRMStandardComponents?: { ready(): Promise<void> };
  AdminApi?: { requestJson?: (path: string) => Promise<unknown> };
};

const originalFetch = window.fetch.bind(window);
const record = (value: unknown): Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : [];

async function materialItems(path: string): Promise<unknown> {
  const request = new URL(path, location.origin);
  const type = request.searchParams.get('type') === 'attachment' ? 'attachment' : 'image';
  const endpoint = type === 'image' ? '/api/admin/image-library' : '/api/admin/attachment-library';
  const query = request.searchParams.get('q') || '';
  const values: Record<string, unknown>[] = [];
  for (let offset = 0; ; ) {
    const source = new URL(endpoint, location.origin);
    source.searchParams.set('limit', '100');
    source.searchParams.set('offset', String(offset));
    source.searchParams.set('q', query);
    source.searchParams.set('enabled_only', 'true');
    const response = await originalFetch(source, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
    const payload = record(await response.json().catch(() => ({})));
    if (!response.ok) throw new Error(`素材目录读取失败（HTTP ${response.status}）`);
    values.push(...list(payload.items).map(record));
    const next = Number(payload.next_offset);
    if (payload.has_more !== true || !Number.isSafeInteger(next) || next <= offset) break;
    offset = next;
  }
  return { items: values.map((item): MaterialItem => {
    const id = Number(item.id ?? item.library_id);
    return { type, library_id: id, title: String(item.name ?? item.file_name ?? `素材 ${id}`), subtitle: String(item.description ?? item.category ?? ''), thumbnail_url: String(item.thumb_320_url ?? item.variant_url ?? ''), metadata: item };
  }) };
}

function installMaterialTransport(): void {
  const target = window as StandardWindow;
  const prior = target.AdminApi?.requestJson;
  target.AdminApi ||= {};
  target.AdminApi.requestJson = async (path: string): Promise<unknown> => {
    if (new URL(path, location.origin).pathname === '/api/admin/material-picker/items') return materialItems(path);
    if (prior) return prior(path);
    const response = await originalFetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`请求失败（HTTP ${response.status}）`);
    return response.json();
  };
}

installMaterialTransport();
void (async () => {
  await (window as StandardWindow).AICRMStandardComponents?.ready();
  // @ts-ignore frozen side-effect entry has no module declaration.
  await import('../src/admin/main');
})();

type MaterialPickerWindow = Window & {
  AICRMMaterialPicker?: { open(options: { type: 'image' | 'attachment'; title: string; selectedIds: number[]; limit: number; onConfirm(item: MaterialItem): void; onCancel(): void }): void };
};

let pendingRadarPickerObserver: MutationObserver | undefined;

function relayRadarMaterialSelection(): void {
  document.addEventListener('click', (event) => {
    const button = (event.target as Element | null)?.closest('#btnPick');
    if (!button) return;
    pendingRadarPickerObserver?.disconnect();
    // `openPicker` waits for the frozen page's scoped `loadDb` before it
    // appends `.pk-mask`; observing that append avoids racing the renderer.
    const observer = new MutationObserver((records) => {
      for (const record of records) {
        for (const node of record.addedNodes) {
          if (!(node instanceof HTMLElement) || !node.classList.contains('pk-mask')) continue;
          observer.disconnect();
          if (pendingRadarPickerObserver === observer) pendingRadarPickerObserver = undefined;
          openStandardPicker(node);
          return;
        }
      }
    });
    pendingRadarPickerObserver = observer;
    observer.observe(document.body, { childList: true, subtree: true });

    function openStandardPicker(legacyMask: HTMLElement): void {
      const picker = (window as MaterialPickerWindow).AICRMMaterialPicker;
      if (!picker) {
        observer.disconnect();
        if (pendingRadarPickerObserver === observer) pendingRadarPickerObserver = undefined;
        return;
      }
      // The frozen picker remains alive as the form-state callback channel,
      // but cannot flash through its inline `display:flex` style.
      legacyMask.style.setProperty('display', 'none', 'important');
      legacyMask.setAttribute('aria-hidden', 'true');
      const type = document.querySelector<HTMLElement>('#typeCards .type-card.on')?.dataset.t === 'pdf' ? 'attachment' : 'image';
      picker.open({
        type,
        title: type === 'image' ? '选择图片素材' : '选择 PDF 附件',
        selectedIds: [],
        limit: 1,
        onConfirm(item) {
          const row = Array.from(legacyMask.querySelectorAll<HTMLElement>('[data-pk-id]')).find((candidate) => candidate.dataset.pkId === String(item.library_id));
          if (!row) {
            const help = document.getElementById('mediaHelp');
            if (help) {
              help.textContent = '素材目录已变化，未改动当前草稿；请刷新页面后重新选择。';
              help.setAttribute('role', 'alert');
            }
            legacyMask.querySelector<HTMLElement>('[data-pk="cancel"]')?.click();
            return;
          }
          row.click();
          legacyMask.querySelector<HTMLElement>('[data-pk="ok"]')?.click();
        },
        onCancel() {
          legacyMask.querySelector<HTMLElement>('[data-pk="cancel"]')?.click();
        },
      });
    }
  }, true);
  window.addEventListener('pagehide', () => pendingRadarPickerObserver?.disconnect(), { once: true });
  window.addEventListener('unload', () => pendingRadarPickerObserver?.disconnect(), { once: true });
}

relayRadarMaterialSelection();

type RadarEvent = { receiptID: string; stage: string; createdAt: string };
type RadarEventPage = { items: RadarEvent[]; total: number; limit: number; offset: number; hasMore: boolean };
type RadarTimeFilters = { keyword: string; startAt?: string; endAt?: string };

const radarEventPageLimit = 100;

function radarRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function radarDisplayTime(value: string): string {
  const formatted = formatShanghaiDateTime(value);
  return formatted === '未提供' ? '时间暂时无法显示' : formatted;
}

function radarStageLabel(value: string): string {
  return ({
    landing: '访问落地页',
    oauth_started: '开始授权',
    oauth_verified: '授权已验证',
    identity_resolved: '已关联客户',
    content_opened: '已打开内容',
    redirected: '已完成跳转',
    image_loaded: '图片已加载',
    pdf_opened: '已打开 PDF',
  } as Record<string, string>)[value] || '事件阶段待确认';
}

function radarErrorMessage(error: unknown, operation: 'read' | 'export'): string {
  const status = radarRecord(error)?.status;
  const code = typeof status === 'number' && Number.isSafeInteger(status) ? status : 0;
  if (code === 401 || code === 403) return operation === 'read' ? '没有查看雷达事件的权限。' : '没有导出雷达事件的权限。';
  if (code === 404) return '该雷达链接不存在或已不可用。';
  if (code === 409 && operation === 'export') return '时间范围内记录超过 500 条，请缩小时间范围后导出。';
  if (code === 400) return '筛选条件无效，请填写有效时间后查询。';
  if (code === 503) return operation === 'read' ? '雷达事件暂时无法读取，请重试。' : '雷达事件暂时无法导出，请重试。';
  return operation === 'read' ? '雷达事件暂时无法读取，请检查网络后重试。' : '雷达事件暂时无法导出，请检查网络后重试。';
}

function sameRadarFilters(left: RadarTimeFilters, right: RadarTimeFilters): boolean {
  return left.startAt === right.startAt && left.endAt === right.endAt;
}

class RadarDetailTimeHost {
  private readonly controller = new AbortController();
  private readonly host = document.createElement('section');
  private readonly keyword = document.createElement('input');
  private readonly start = document.createElement('input');
  private readonly end = document.createElement('input');
  private readonly queryButton = document.createElement('button');
  private readonly resetButton = document.createElement('button');
  private readonly retryButton = document.createElement('button');
  private readonly previousButton = document.createElement('button');
  private readonly nextButton = document.createElement('button');
  private readonly feedback = document.createElement('p');
  private readonly summary = document.createElement('span');
  private readonly rows = document.createElement('tbody');
  private readonly exportButton: HTMLButtonElement;
  private page: RadarEventPage | undefined;
  private applied: RadarTimeFilters = { keyword: '' };
  private loading = false;
  private generation = 0;
  private activeRequest: AbortController | undefined;
  private retryOffset: number | undefined;

  constructor(private readonly linkID: number, originalExport: HTMLButtonElement) {
    this.exportButton = originalExport.cloneNode(true) as HTMLButtonElement;
    originalExport.replaceWith(this.exportButton);
    this.exportButton.addEventListener('click', () => { void this.exportCSV(); }, { signal: this.controller.signal });
    this.prepareHost();
  }

  mount(filterCard: HTMLElement, tableCard: HTMLElement): void {
    filterCard.replaceWith(this.host);
    tableCard.remove();
    void this.load(0);
  }

  destroy(): void {
    this.activeRequest?.abort();
    this.controller.abort();
  }

  private prepareHost(): void {
    this.host.dataset.v3RadarEventHost = '';
    this.host.append(this.controls(), this.results());
    this.keyword.addEventListener('input', () => this.render(), { signal: this.controller.signal });
    [this.start, this.end].forEach((input) => input.addEventListener('input', () => this.render(), { signal: this.controller.signal }));
    this.queryButton.addEventListener('click', () => { void this.load(0); }, { signal: this.controller.signal });
    this.resetButton.addEventListener('click', () => {
      this.keyword.value = '';
      this.start.value = '';
      this.end.value = '';
      void this.load(0);
    }, { signal: this.controller.signal });
    this.retryButton.addEventListener('click', () => { void this.load(this.retryOffset ?? this.page?.offset ?? 0); }, { signal: this.controller.signal });
    this.previousButton.addEventListener('click', () => { void this.load(Math.max(0, (this.page?.offset || 0) - radarEventPageLimit)); }, { signal: this.controller.signal });
    this.nextButton.addEventListener('click', () => { void this.load((this.page?.offset || 0) + (this.page?.limit || radarEventPageLimit)); }, { signal: this.controller.signal });
  }

  private controls(): HTMLElement {
    const card = document.createElement('div');
    card.className = 'card filter-bar';
    const keyword = this.field('本页搜索', this.keyword);
    this.keyword.className = 'input';
    this.keyword.placeholder = '仅筛选当前页的回执 ID / 事件阶段';
    keyword.style.flex = '1';
    keyword.style.minWidth = '200px';
    this.start.className = 'input';
    this.start.type = 'datetime-local';
    this.end.className = 'input';
    this.end.type = 'datetime-local';
    this.queryButton.className = 'btn';
    this.queryButton.type = 'button';
    this.queryButton.textContent = '查询';
    this.resetButton.className = 'btn';
    this.resetButton.type = 'button';
    this.resetButton.textContent = '清空筛选';
    card.append(keyword, this.field('开始时间', this.start), this.field('结束时间', this.end), this.queryButton, this.resetButton);
    return card;
  }

  private results(): HTMLElement {
    const card = document.createElement('div');
    card.className = 'card';
    const status = document.createElement('div');
    status.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px 0;flex-wrap:wrap';
    this.summary.className = 'muted';
    this.feedback.dataset.radarEventFeedback = '';
    this.feedback.setAttribute('role', 'status');
    this.feedback.style.cssText = 'margin:0;color:#8F959E;font-size:13px;line-height:20px';
    this.retryButton.className = 'btn';
    this.retryButton.type = 'button';
    this.retryButton.textContent = '重试';
    this.retryButton.hidden = true;
    const exportScope = document.createElement('small');
    exportScope.className = 'muted';
    exportScope.textContent = '导出仅按已查询的时间范围，不包含本页搜索。';
    status.append(this.summary, this.feedback, this.retryButton, exportScope);
    const overflow = document.createElement('div');
    overflow.style.overflowX = 'auto';
    const table = document.createElement('table');
    table.className = 'tbl';
    const head = document.createElement('thead');
    const header = document.createElement('tr');
    ['回执 ID', '事件阶段', '发生时间'].forEach((value) => {
      const cell = document.createElement('th');
      cell.textContent = value;
      header.append(cell);
    });
    head.append(header);
    table.append(head, this.rows);
    overflow.append(table);
    const pagination = document.createElement('div');
    pagination.style.cssText = 'display:flex;justify-content:flex-end;gap:8px;padding:14px 16px';
    this.previousButton.className = 'btn';
    this.previousButton.type = 'button';
    this.previousButton.textContent = '上一页';
    this.nextButton.className = 'btn';
    this.nextButton.type = 'button';
    this.nextButton.textContent = '下一页';
    pagination.append(this.previousButton, this.nextButton);
    card.append(status, overflow, pagination);
    return card;
  }

  private field(label: string, input: HTMLInputElement): HTMLElement {
    const field = document.createElement('div');
    field.className = 'field';
    const caption = document.createElement('label');
    caption.textContent = label;
    field.append(caption, input);
    return field;
  }

  private currentFilters(): RadarTimeFilters | undefined {
    const convert = (value: string): string | undefined => value ? shanghaiDateTimeLocalToRFC3339(value) : undefined;
    const startAt = convert(this.start.value);
    const endAt = convert(this.end.value);
    if ((this.start.value && !startAt) || (this.end.value && !endAt)) {
      this.showError('请填写有效时间后查询。');
      return undefined;
    }
    if (startAt && endAt && startAt > endAt) {
      this.showError('开始时间不能晚于结束时间。');
      return undefined;
    }
    return { keyword: this.keyword.value.trim(), startAt, endAt };
  }

  private async load(offset: number): Promise<void> {
    const filters = this.currentFilters();
    if (!filters) return;
    if (!Number.isSafeInteger(offset) || offset < 0 || offset > 1_000_000) {
      this.showError('页码无效，请重新查询。');
      return;
    }
    this.activeRequest?.abort();
    const request = new AbortController();
    this.activeRequest = request;
    const generation = ++this.generation;
    this.setLoading(true);
    try {
      const page = await this.readPage(filters, offset, request.signal);
      if (generation !== this.generation) return;
      this.page = page;
      this.applied = filters;
      this.retryOffset = undefined;
      this.feedback.textContent = '';
      this.retryButton.hidden = true;
      this.render();
    } catch (error) {
      if (request.signal.aborted || generation !== this.generation) return;
      this.retryOffset = offset;
      this.showError(radarErrorMessage(error, 'read'));
    } finally {
      if (generation === this.generation) this.setLoading(false);
    }
  }

  private async readPage(filters: RadarTimeFilters, offset: number, signal: AbortSignal): Promise<RadarEventPage> {
    const url = new URL(`/api/admin/radar-links/${this.linkID}/events`, location.origin);
    url.searchParams.set('limit', String(radarEventPageLimit));
    url.searchParams.set('offset', String(offset));
    if (filters.startAt) url.searchParams.set('start_at', filters.startAt);
    if (filters.endAt) url.searchParams.set('end_at', filters.endAt);
    const response = await originalFetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal });
    if (!response.ok) throw response;
    const payload = radarRecord(await response.json());
    const sourceItems = Array.isArray(payload?.items) ? payload.items : undefined;
    const total = payload?.total;
    const limit = payload?.limit;
    const returnedOffset = payload?.offset;
    const hasMore = payload?.has_more;
    if (!payload || !sourceItems || typeof total !== 'number' || !Number.isSafeInteger(total) || total < 0 || typeof limit !== 'number' || !Number.isSafeInteger(limit) || limit < 1 || limit > 500 || returnedOffset !== offset || typeof hasMore !== 'boolean' || payload.identity_attributed !== false || payload.real_external_call_executed !== false) throw new Error('invalid radar event page');
    const items = sourceItems.map((item): RadarEvent => {
      const record = radarRecord(item);
      const receiptID = typeof record?.receipt_id === 'string' ? record.receipt_id : '';
      const stage = typeof record?.stage === 'string' ? record.stage : '';
      const createdAt = typeof record?.created_at === 'string' ? record.created_at : '';
      if (!/^rre_[0-9a-f]{32}$/.test(receiptID) || !stage || radarDisplayTime(createdAt) === '时间暂时无法显示') throw new Error('invalid radar event item');
      return { receiptID, stage, createdAt };
    });
    if (hasMore && items.length === 0) throw new Error('invalid radar event page');
    return { items, total, limit, offset, hasMore };
  }

  private visibleItems(): RadarEvent[] {
    const keyword = this.keyword.value.trim().toLowerCase();
    if (!keyword) return this.page?.items || [];
    return (this.page?.items || []).filter((item) => item.receiptID.toLowerCase().includes(keyword) || item.stage.toLowerCase().includes(keyword) || radarStageLabel(item.stage).toLowerCase().includes(keyword));
  }

  private render(): void {
    const page = this.page;
    const current = this.currentFiltersForRender();
    const stale = Boolean(page && !sameRadarFilters(current, this.applied));
    const visible = this.visibleItems();
    this.rows.replaceChildren();
    if (visible.length) {
      visible.forEach((item) => {
        const row = document.createElement('tr');
        for (const [value, className] of [[item.receiptID, 'mono'], [radarStageLabel(item.stage), ''], [radarDisplayTime(item.createdAt), '']] as const) {
          const cell = document.createElement('td');
          if (className) cell.className = className;
          cell.textContent = value;
          row.append(cell);
        }
        this.rows.append(row);
      });
    } else {
      const row = document.createElement('tr');
      const cell = document.createElement('td');
      cell.colSpan = 3;
      cell.style.cssText = 'text-align:center;padding:36px;color:#8F959E';
      cell.textContent = page ? '暂无本地 Radar 事件' : '正在读取雷达事件…';
      row.append(cell);
      this.rows.append(row);
    }
    if (!page) this.summary.textContent = '正在读取事件…';
    else if (stale) this.summary.textContent = '筛选已变更，请查询后查看结果。';
    else {
      const from = page.total === 0 ? 0 : page.offset + 1;
      const to = page.offset + page.items.length;
      const pageSearch = this.keyword.value.trim() ? `，当前页匹配 ${visible.length} 条` : '';
      this.summary.textContent = `共 ${page.total} 条，第 ${from}–${to} 条${pageSearch}`;
    }
    this.updateControls(stale);
  }

  private currentFiltersForRender(): RadarTimeFilters {
    return {
      keyword: this.keyword.value.trim(),
      startAt: this.start.value ? shanghaiDateTimeLocalToRFC3339(this.start.value) : undefined,
      endAt: this.end.value ? shanghaiDateTimeLocalToRFC3339(this.end.value) : undefined,
    };
  }

  private setLoading(loading: boolean): void {
    this.loading = loading;
    if (loading) {
      this.feedback.textContent = '正在读取雷达事件…';
      this.retryButton.hidden = true;
    }
    this.updateControls();
  }

  private updateControls(stale = this.resultIsStale()): void {
    const page = this.page;
    this.queryButton.disabled = this.loading;
    this.resetButton.disabled = this.loading;
    this.previousButton.disabled = this.loading || !page || page.offset === 0 || stale;
    this.nextButton.disabled = this.loading || !page || !page.hasMore || stale;
    this.exportButton.disabled = this.loading || !page || stale;
    this.retryButton.disabled = this.loading || stale || this.retryOffset === undefined;
  }

  private resultIsStale(): boolean {
    return Boolean(this.page && !sameRadarFilters(this.currentFiltersForRender(), this.applied));
  }

  private showError(message: string): void {
    this.feedback.textContent = message;
    this.feedback.setAttribute('role', 'alert');
    this.retryButton.hidden = false;
    this.setLoading(false);
    this.render();
  }

  private async exportCSV(): Promise<void> {
    const current = this.currentFilters();
    if (!current || !this.page || !sameRadarFilters(current, this.applied)) {
      if (current) this.showError('请先按当前时间条件查询，再导出。');
      return;
    }
    const operationGeneration = this.generation;
    this.exportButton.disabled = true;
    try {
      const url = new URL(`/api/admin/radar-links/${this.linkID}/events/export`, location.origin);
      if (this.applied.startAt) url.searchParams.set('start_at', this.applied.startAt);
      if (this.applied.endAt) url.searchParams.set('end_at', this.applied.endAt);
      const response = await originalFetch(url, { credentials: 'same-origin', headers: { Accept: 'text/csv' }, signal: this.controller.signal });
      if (!response.ok) throw response;
      if (!/^text\/csv(?:;|$)/i.test(response.headers.get('Content-Type') || '')) throw new Error('invalid radar export response');
      const csv = await response.text();
      const href = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
      const link = document.createElement('a');
      link.href = href;
      link.download = 'radar-events.csv';
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(href), 1000);
      if (operationGeneration === this.generation) {
        this.feedback.textContent = '已导出 CSV。';
        this.feedback.setAttribute('role', 'status');
      }
    } catch (error) {
      if (!this.controller.signal.aborted && operationGeneration === this.generation) this.showError(radarErrorMessage(error, 'export'));
    } finally {
      this.updateControls();
    }
  }
}

function installRadarDetailTimeHost(): void {
  if (document.body.dataset.page !== 'radarDetail') return;
  let host: RadarDetailTimeHost | undefined;
  const linkID = Number(new URL(location.href).searchParams.get('id'));
  if (!Number.isSafeInteger(linkID) || linkID < 1) return;
  const attemptMount = (): void => {
    if (host) return;
    const root = document.querySelector<HTMLElement>('#stage.sec-radar');
    const start = root?.querySelector<HTMLInputElement>('#dStart');
    const rows = root?.querySelector<HTMLTableSectionElement>('#dRows');
    const exportButton = root?.querySelector<HTMLButtonElement>('#dExport');
    const filterCard = start?.closest<HTMLElement>('.card');
    const tableCard = rows?.closest('table')?.closest<HTMLElement>('.card');
    if (!root || !start || !rows || !exportButton || !filterCard || !tableCard) return;
    host = new RadarDetailTimeHost(linkID, exportButton);
    host.mount(filterCard, tableCard);
    observer.disconnect();
  };
  const observer = new MutationObserver(attemptMount);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('pagehide', () => { observer.disconnect(); host?.destroy(); }, { once: true });
  attemptMount();
}

installRadarDetailTimeHost();
