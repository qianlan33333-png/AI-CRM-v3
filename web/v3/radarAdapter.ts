export {};
import { api } from '../src/shared/api/client';
import { rememberActionInputs, runAction } from './actionFeedback';
import { request as authenticatedRequest } from '../src/api/transport';
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

type RadarVisitor = {
  nickname?: string;
  externalContactID?: string;
  externalContactStatus: 'available' | 'missing' | 'ambiguous' | 'unavailable';
  oneID?: string;
  openedAt: string;
  attributionStatus: string;
};
type RadarVisitorPage = { items: RadarVisitor[]; total: number; limit: number; offset: number; hasMore: boolean };
type RadarVisitorFilters = { search: string; startAt?: string; endAt?: string };

const radarVisitorPageLimit = 100;

function radarRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function radarDisplayTime(value: string): string {
  const formatted = formatShanghaiDateTime(value);
  return formatted === '未提供' ? '时间暂时无法显示' : formatted;
}

function radarAttributionLabel(value: string): string {
  return ({
    resolved: '已识别客户',
    anonymous: '未识别访客',
    pending: '身份待确认',
    conflict: '身份冲突待确认',
    failed: '身份关联暂不可用',
  } as Record<string, string>)[value] || '身份状态待确认';
}

function visitorName(visitor: RadarVisitor): string {
  return visitor.attributionStatus === 'resolved' ? visitor.nickname || '姓名暂缺' : radarAttributionLabel(visitor.attributionStatus);
}

function visitorExternalContact(visitor: RadarVisitor): string {
  if (visitor.attributionStatus !== 'resolved') return '未关联';
  if (visitor.externalContactStatus === 'available') return visitor.externalContactID || '外部联系人 ID 暂不可用';
  return ({
    missing: '外部联系人 ID 暂缺',
    ambiguous: '外部联系人 ID 待确认',
    unavailable: '外部联系人 ID 暂不可用',
  } as Record<string, string>)[visitor.externalContactStatus] || '外部联系人 ID 暂不可用';
}

function visitorOneID(visitor: RadarVisitor): string {
  if (visitor.attributionStatus === 'resolved') return visitor.oneID || 'OneID 暂缺';
  return visitor.attributionStatus === 'anonymous' ? '未关联' : radarAttributionLabel(visitor.attributionStatus);
}

function radarErrorStatus(error: unknown): number {
  const status = radarRecord(error)?.status;
  return typeof status === 'number' && Number.isSafeInteger(status) ? status : 0;
}

function radarErrorMessage(error: unknown, operation: 'read' | 'export'): string {
  const code = radarErrorStatus(error);
  if (code === 401) return operation === 'read' ? '登录状态已失效，请重新登录后查看访客明细。' : '登录状态已失效，请重新登录后导出访客明细。';
  if (code === 403) return operation === 'read' ? '没有查看访客明细的权限。' : '没有导出访客明细的权限。';
  if (code === 404) return '该雷达链接不存在或已不可用。';
  if (code === 409) return operation === 'read' ? '搜索候选范围过大，请缩小搜索条件或时间范围后查询。' : '筛选结果超过 500 条，请缩小搜索条件或时间范围后导出。';
  if (code === 400) return '筛选条件无效，请填写有效时间后查询。';
  if (code === 503) return operation === 'read' ? '访客身份资料暂时无法读取，请重试。' : '访客身份资料暂时无法导出，请重试。';
  return operation === 'read' ? '访客明细暂时无法读取，请检查网络后重试。' : '访客明细暂时无法导出，请检查网络后重试。';
}

function sameRadarFilters(left: RadarVisitorFilters, right: RadarVisitorFilters): boolean {
  return left.search === right.search && left.startAt === right.startAt && left.endAt === right.endAt;
}

class RadarDetailVisitorsHost {
  private readonly controller = new AbortController();
  private readonly host = document.createElement('section');
  private readonly search = document.createElement('input');
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
  private page: RadarVisitorPage | undefined;
  private applied: RadarVisitorFilters = { search: '' };
  private loading = false;
  private generation = 0;
  private activeRequest: AbortController | undefined;
  private activeExport: AbortController | undefined;
  private retryOffset: number | undefined;
  private initialReadFailed = false;

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
    this.activeExport?.abort();
    this.controller.abort();
  }

  private prepareHost(): void {
    this.host.dataset.v3RadarVisitorHost = '';
    this.host.append(this.controls(), this.results());
    [this.search, this.start, this.end].forEach((input) => input.addEventListener('input', () => this.invalidateForFilterEdit(), { signal: this.controller.signal }));
    this.queryButton.addEventListener('click', () => { void this.load(0); }, { signal: this.controller.signal });
    this.resetButton.addEventListener('click', () => {
      this.search.value = '';
      this.start.value = '';
      this.end.value = '';
      this.invalidateForFilterEdit();
      void this.load(0);
    }, { signal: this.controller.signal });
    this.retryButton.addEventListener('click', () => { void this.load(this.retryOffset ?? this.page?.offset ?? 0); }, { signal: this.controller.signal });
    this.previousButton.addEventListener('click', () => { void this.load(Math.max(0, (this.page?.offset || 0) - radarVisitorPageLimit)); }, { signal: this.controller.signal });
    this.nextButton.addEventListener('click', () => { void this.load((this.page?.offset || 0) + (this.page?.limit || radarVisitorPageLimit)); }, { signal: this.controller.signal });
  }

  private invalidateForFilterEdit(): void {
    this.activeRequest?.abort();
    this.activeRequest = undefined;
    this.activeExport?.abort();
    this.activeExport = undefined;
    this.generation += 1;
    this.loading = false;
    this.retryOffset = undefined;
    this.render();
  }

  private controls(): HTMLElement {
    const card = document.createElement('div');
    card.className = 'card filter-bar';
    const search = this.field('搜索访问者', this.search);
    this.search.className = 'input';
    this.search.placeholder = '搜索昵称、外部联系人 ID 或 OneID';
    search.style.flex = '1';
    search.style.minWidth = '220px';
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
    card.append(search, this.field('开始时间', this.start), this.field('结束时间', this.end), this.queryButton, this.resetButton);
    return card;
  }

  private results(): HTMLElement {
    const card = document.createElement('div');
    card.className = 'card';
    const status = document.createElement('div');
    status.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px 0;flex-wrap:wrap';
    this.summary.className = 'muted';
    this.feedback.dataset.radarVisitorFeedback = '';
    this.feedback.setAttribute('role', 'status');
    this.feedback.style.cssText = 'margin:0;color:#8F959E;font-size:13px;line-height:20px';
    this.retryButton.className = 'btn';
    this.retryButton.type = 'button';
    this.retryButton.textContent = '重试';
    this.retryButton.hidden = true;
    const exportScope = document.createElement('small');
    exportScope.className = 'muted';
    exportScope.textContent = '导出与当前已查询的搜索和时间条件一致。';
    status.append(this.summary, this.feedback, this.retryButton, exportScope);
    const overflow = document.createElement('div');
    overflow.dataset.radarVisitorTableOverflow = '';
    overflow.style.overflowX = 'auto';
    const table = document.createElement('table');
    table.className = 'tbl';
    table.style.minWidth = '680px';
    const head = document.createElement('thead');
    const header = document.createElement('tr');
    ['昵称', '外部联系人 ID', 'OneID', '打开时间'].forEach((value) => {
      const cell = document.createElement('th');
      cell.style.whiteSpace = 'nowrap';
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

  private currentFilters(): RadarVisitorFilters | undefined {
    const convert = (value: string): string | undefined => value ? shanghaiDateTimeLocalToRFC3339(value) : undefined;
    const startAt = convert(this.start.value);
    const endAt = convert(this.end.value);
    if ((this.start.value && !startAt) || (this.end.value && !endAt)) {
      this.showError('请填写有效时间后查询。');
      return undefined;
    }
    if (startAt && endAt && startAt >= endAt) {
      this.showError('开始时间必须早于结束时间。');
      return undefined;
    }
    return { search: this.search.value.trim(), startAt, endAt };
  }

  private currentFiltersForRender(): RadarVisitorFilters {
    return {
      search: this.search.value.trim(),
      startAt: this.start.value ? shanghaiDateTimeLocalToRFC3339(this.start.value) : undefined,
      endAt: this.end.value ? shanghaiDateTimeLocalToRFC3339(this.end.value) : undefined,
    };
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
      this.initialReadFailed = false;
      this.feedback.textContent = '';
      this.feedback.setAttribute('role', 'status');
      this.retryButton.hidden = true;
      this.render();
    } catch (error) {
      if (request.signal.aborted || generation !== this.generation) return;
      if ([401, 403].includes(radarErrorStatus(error))) {
        // A role/session change must remove the previously authorized identity
        // projection before showing the access failure.
        this.clearUnauthorizedVisitors();
      } else {
        this.retryOffset = offset;
        this.initialReadFailed = !this.page;
      }
      this.showError(radarErrorMessage(error, 'read'));
    } finally {
      if (generation === this.generation) this.setLoading(false);
    }
  }

  private async readPage(filters: RadarVisitorFilters, offset: number, signal: AbortSignal): Promise<RadarVisitorPage> {
    const url = new URL(`/api/admin/radar-links/${this.linkID}/visitors`, location.origin);
    url.searchParams.set('limit', String(radarVisitorPageLimit));
    url.searchParams.set('offset', String(offset));
    if (filters.search) url.searchParams.set('search', filters.search);
    if (filters.startAt) url.searchParams.set('start_at', filters.startAt);
    if (filters.endAt) url.searchParams.set('end_at', filters.endAt);
    const response = await authenticatedRequest(url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' }, signal });
    const payload = radarRecord(await response.json());
    const sourceItems = Array.isArray(payload?.items) ? payload.items : undefined;
    const total = payload?.total;
    const limit = payload?.limit;
    const returnedOffset = payload?.offset;
    const hasMore = payload?.has_more;
    if (!payload || !sourceItems || typeof total !== 'number' || !Number.isSafeInteger(total) || total < 0 || typeof limit !== 'number' || !Number.isSafeInteger(limit) || limit < 1 || limit > 500 || returnedOffset !== offset || typeof hasMore !== 'boolean') throw new Error('invalid radar visitor page');
    const items = sourceItems.map((item): RadarVisitor => {
      const source = radarRecord(item);
      const nickname = typeof source?.nickname === 'string' && source.nickname.trim() ? source.nickname : undefined;
      const externalContactID = typeof source?.external_contact_id === 'string' && source.external_contact_id.trim() ? source.external_contact_id : undefined;
      const externalContactStatus = typeof source?.external_contact_status === 'string' ? source.external_contact_status : '';
      const oneID = typeof source?.oneid === 'string' && source.oneid.trim() ? source.oneid : undefined;
      const openedAt = typeof source?.opened_at === 'string' ? source.opened_at : '';
      const attributionStatus = typeof source?.attribution_status === 'string' ? source.attribution_status : '';
      if (!['available', 'missing', 'ambiguous', 'unavailable'].includes(externalContactStatus) || !['resolved', 'anonymous', 'pending', 'conflict', 'failed'].includes(attributionStatus) || (externalContactStatus === 'available') !== Boolean(externalContactID) || (attributionStatus !== 'resolved' && Boolean(nickname || externalContactID || oneID)) || radarDisplayTime(openedAt) === '时间暂时无法显示') throw new Error('invalid radar visitor item');
      return { nickname, externalContactID, externalContactStatus: externalContactStatus as RadarVisitor['externalContactStatus'], oneID, openedAt, attributionStatus };
    });
    if (hasMore && items.length === 0) throw new Error('invalid radar visitor page');
    return { items, total, limit, offset, hasMore };
  }

  private render(): void {
    const page = this.page;
    const stale = Boolean(page && !sameRadarFilters(this.currentFiltersForRender(), this.applied));
    this.rows.replaceChildren();
    if (page?.items.length) {
      page.items.forEach((item) => {
        const row = document.createElement('tr');
        for (const [value, className] of [[visitorName(item), ''], [visitorExternalContact(item), 'mono'], [visitorOneID(item), 'mono'], [radarDisplayTime(item.openedAt), '']] as const) {
          const cell = document.createElement('td');
          if (className) cell.className = className;
          cell.style.whiteSpace = 'nowrap';
          cell.textContent = value;
          row.append(cell);
        }
        this.rows.append(row);
      });
    } else {
      const row = document.createElement('tr');
      const cell = document.createElement('td');
      cell.colSpan = 4;
      cell.style.cssText = 'text-align:center;padding:36px;color:#8F959E';
      cell.textContent = page ? '暂无符合条件的访问者' : this.initialReadFailed ? '暂时无法读取访客明细，请重试。' : '正在读取访客明细…';
      row.append(cell);
      this.rows.append(row);
    }
    if (!page) this.summary.textContent = this.initialReadFailed ? '访客明细未能读取，请重试。' : '正在读取访客明细…';
    else if (stale) this.summary.textContent = '筛选已变更，下面仍显示上次成功查询的结果。请点击“查询”。';
    else {
      const from = page.total === 0 ? 0 : page.offset + 1;
      const to = page.offset + page.items.length;
      this.summary.textContent = `共 ${page.total} 条，第 ${from}–${to} 条`;
    }
    this.updateControls(stale);
  }

  private setLoading(loading: boolean): void {
    this.loading = loading;
    if (loading) {
      this.feedback.textContent = '正在读取访客明细…';
      this.feedback.setAttribute('role', 'status');
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

  private clearUnauthorizedVisitors(): void {
    this.page = undefined;
    this.retryOffset = undefined;
    this.initialReadFailed = true;
  }

  private showError(message: string): void {
    const stale = this.resultIsStale();
    this.feedback.textContent = stale ? `${message.replace(/[。；]+$/, '')}；当前筛选尚未查询，下面仍显示上次成功查询的结果。请点击“查询”。` : message;
    this.feedback.setAttribute('role', 'alert');
    this.retryButton.hidden = stale || this.retryOffset === undefined;
    this.setLoading(false);
    this.render();
  }

  private async exportCSV(): Promise<void> {
    const current = this.currentFilters();
    if (!current || !this.page || !sameRadarFilters(current, this.applied)) {
      if (current) this.showError('请先按当前搜索和时间条件查询，再导出。');
      return;
    }
    const operationGeneration = this.generation;
    this.activeExport?.abort();
    const request = new AbortController();
    this.activeExport = request;
    this.exportButton.disabled = true;
    try {
      const url = new URL(`/api/admin/radar-links/${this.linkID}/visitors/export`, location.origin);
      if (this.applied.search) url.searchParams.set('search', this.applied.search);
      if (this.applied.startAt) url.searchParams.set('start_at', this.applied.startAt);
      if (this.applied.endAt) url.searchParams.set('end_at', this.applied.endAt);
      const response = await authenticatedRequest(url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'text/csv' }, signal: request.signal });
      if (request.signal.aborted || operationGeneration !== this.generation) return;
      if (!/^text\/csv(?:;|$)/i.test(response.headers.get('Content-Type') || '')) throw new Error('invalid radar visitor export response');
      const csv = await response.text();
      if (request.signal.aborted || operationGeneration !== this.generation) return;
      const href = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
      const link = document.createElement('a');
      link.href = href;
      link.download = 'radar-visitors.csv';
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(href), 1000);
      this.feedback.textContent = '已导出 CSV。';
      this.feedback.setAttribute('role', 'status');
    } catch (error) {
      if (!request.signal.aborted && operationGeneration === this.generation) {
        if ([401, 403].includes(radarErrorStatus(error))) this.clearUnauthorizedVisitors();
        this.showError(radarErrorMessage(error, 'export'));
      }
    } finally {
      if (this.activeExport === request) this.activeExport = undefined;
      this.updateControls();
    }
  }
}

function projectRadarPresentation(root: HTMLElement): void {
  const shareQR = root.querySelector<HTMLElement>('#shareQr');
  if (shareQR?.textContent?.includes('backend_blocked')) {
    shareQR.textContent = '分享链接暂不可用，请稍后重试。';
    shareQR.dataset.v3RadarShareState = 'unavailable';
  }
  const disabledDetailCopy = root.querySelector<HTMLButtonElement>('#dCopyInline[disabled]');
  const detailShareNotice = disabledDetailCopy?.previousElementSibling;
  if (detailShareNotice instanceof HTMLElement && detailShareNotice.textContent.includes('backend_blocked')) {
    detailShareNotice.textContent = '分享链接暂不可用，请稍后重试。';
    detailShareNotice.setAttribute('role', 'alert');
    detailShareNotice.dataset.v3RadarShareState = 'unavailable';
  }
  root.querySelectorAll<HTMLElement>('.stat-row .stat').forEach((card) => {
    const label = card.querySelector<HTMLElement>('.stat-l');
    const detail = card.querySelector<HTMLElement>('.stat-s');
    if (label?.textContent?.trim() === 'PV · 中转页到达') label.textContent = '访问次数 · 中转页到达';
    if (detail?.textContent?.trim() === 'wrapper 页加载次数') detail.textContent = '中转页加载次数';
  });
}

function installRadarPresentationProjection(): void {
  if (!['radar', 'radarDetail'].includes(document.body.dataset.page || '')) return;
  const render = (): void => {
    const root = document.querySelector<HTMLElement>('#stage.sec-radar');
    if (root) projectRadarPresentation(root);
  };
  const observer = new MutationObserver(render);
  observer.observe(document.documentElement, { childList: true, subtree: true, characterData: true });
  window.addEventListener('pagehide', () => observer.disconnect(), { once: true });
  render();
}

function installRadarDetailVisitorsHost(): void {
  if (document.body.dataset.page !== 'radarDetail') return;
  let host: RadarDetailVisitorsHost | undefined;
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
    host = new RadarDetailVisitorsHost(linkID, exportButton);
    host.mount(filterCard, tableCard);
    observer.disconnect();
  };
  const observer = new MutationObserver(attemptMount);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('pagehide', () => { observer.disconnect(); host?.destroy(); }, { once: true });
  attemptMount();
}

installRadarPresentationProjection();
installRadarDetailVisitorsHost();
