// The unified material workspace is presentation-only. It joins the three
// existing Media pages through one shell route without taking ownership of
// their HTTP reads, mutations, private URLs, or frozen donor callbacks.
import {
  mountPageHeaderActionElements,
  pageHeaderActionElementsHaveConnectedOrigins,
} from './shared/ui/pageHeaderActions';
import { installCommittedTextSearch } from './shared/ui/committedTextSearch';
import { formatShanghaiDateTime } from './adminDateTime';
import { listLegacyAttachments, listLegacyMiniPrograms } from '../src/api/generated/p4-media-compat/p4-media-compat';
import { ApiError, apiRequestOptions, unwrapGenerated } from '../src/api/transport';
import type { LegacyAttachmentItem, LegacyAttachmentListSuccess, LegacyMiniProgram, LegacyMiniProgramListResponse } from '../src/api/generated/health.schemas';

installCommittedTextSearch();

type MaterialPage = 'attach' | 'mpLib';

type PageConfig = {
  tab: 'attachments' | 'miniprograms';
  label: string;
  actionLabel: string;
  querySelector: string;
  queryMode: 'attachment' | 'miniprogram';
};

const pages: Record<MaterialPage, PageConfig> = {
  attach: {
    tab: 'attachments', label: '附件', actionLabel: '上传附件',
    querySelector: 'input[placeholder="搜索附件名"]', queryMode: 'attachment',
  },
  mpLib: {
    tab: 'miniprograms', label: '小程序', actionLabel: '新建小程序卡片',
    querySelector: '#fMpQuery', queryMode: 'miniprogram',
  },
};

const tabItems = [
  { value: 'images', label: '图片' },
  { value: 'attachments', label: '附件' },
  { value: 'miniprograms', label: '小程序' },
] as const;

function donorHeader(control: HTMLElement): HTMLElement | undefined {
  for (let current: HTMLElement | null = control.parentElement; current; current = current.parentElement) {
    if (current.tagName === 'DIV' && current.style.height === '52px') return current;
  }
  return undefined;
}

function hideDonorHeader(header: HTMLElement): void {
  if (header.dataset.materialLibraryHeaderHidden === 'true') return;
  header.dataset.materialLibraryHeaderHidden = 'true';
  // The frozen header has an inline display declaration. `hidden` alone does
  // not defeat that authored display in every browser, so keep it scoped to
  // this shell-only duplicate title.
  header.setAttribute('hidden', '');
  header.style.setProperty('display', 'none', 'important');
}

export function mountMaterialLibraryTabs(stage: HTMLElement, active: string): HTMLElement {
  const existing = stage.querySelector<HTMLElement>(':scope > [data-material-library-tabs]');
  if (existing) return existing;
  const nav = document.createElement('nav');
  nav.dataset.materialLibraryTabs = 'true';
  nav.setAttribute('aria-label', '素材类型');
  nav.style.cssText = 'display:flex;align-items:center;gap:4px;padding:10px 20px 0;background:#fff;border-bottom:1px solid #EFF0F1';
  for (const item of tabItems) {
    const link = document.createElement('a');
    link.href = `/admin/materials?tab=${item.value}`;
    link.textContent = item.label;
    link.dataset.materialLibraryTab = item.value;
    const selected = item.value === active;
    if (selected) link.setAttribute('aria-current', 'page');
    link.style.cssText = `height:30px;padding:0 12px;display:inline-flex;align-items:center;border-radius:6px 6px 0 0;text-decoration:none;font-size:13px;font-weight:${selected ? '600' : '400'};color:${selected ? '#245BDB' : '#646A73'};background:${selected ? '#EFF4FF' : 'transparent'}`;
    nav.append(link);
  }
  stage.prepend(nav);
  return nav;
}

class FrozenMaterialPresentation {
  private readonly page: MaterialPage;
  private readonly config: PageConfig;
  private readonly stage: HTMLElement;
  private action?: HTMLElement;
  private releaseAction?: () => void;
  private observer?: MutationObserver;
  private attachmentQuery?: HTMLInputElement;
  private readAbort?: AbortController;
  private readGeneration = 0;
  private metadataQuery = '';
  private attachments: LegacyAttachmentItem[] = [];
  private miniPrograms: LegacyMiniProgram[] = [];
  private listSignature = '';

  constructor(page: MaterialPage, stage: HTMLElement) {
    this.page = page;
    this.config = pages[page];
    this.stage = stage;
  }

  start(): void {
    this.sync();
    this.observer = new MutationObserver(() => this.sync());
    this.observer.observe(this.stage, { childList: true, subtree: true });
  }

  private sync(): void {
    this.ensureTabs();
    this.installCommittedSearch();
    this.applyCachedMetadata();
    const action = Array.from(this.stage.querySelectorAll<HTMLButtonElement>('button'))
      .find((candidate) => candidate.textContent?.trim() === this.config.actionLabel);
    if (!action) {
      // The action is intentionally no longer in `stage` after it is moved to
      // the topbar. Only a disconnected source marker means the donor redrew
      // without a replacement; do not bounce a live control back and forth on
      // every observer notification.
      if (this.action && !pageHeaderActionElementsHaveConnectedOrigins(`material-library-${this.page}`, [this.action])) {
        this.releaseAction?.();
        this.releaseAction = undefined;
        this.action = undefined;
      }
      return;
    }
    if (action === this.action && pageHeaderActionElementsHaveConnectedOrigins(`material-library-${this.page}`, [action])) return;
    this.releaseAction?.();
    this.action = action;
    const header = donorHeader(action);
    if (header) hideDonorHeader(header);
    this.releaseAction = mountPageHeaderActionElements(`material-library-${this.page}`, [action]);
  }

  private ensureTabs(): void {
    mountMaterialLibraryTabs(this.stage, this.config.tab);
  }

  private installCommittedSearch(): void {
    const input = this.stage.querySelector<HTMLInputElement>(this.config.querySelector);
    if (!input || input.dataset.materialLibraryQuery === this.config.queryMode) return;
    input.dataset.materialLibraryQuery = this.config.queryMode;
    if (this.config.queryMode === 'miniprogram') {
      input.addEventListener('keydown', (event) => {
        if (event.key !== 'Enter' || event.isComposing || event.keyCode === 229) return;
        event.preventDefault();
        this.stage.querySelector<HTMLButtonElement>('#mpSearch')?.click();
        void this.readMetadata(input.value);
      });
      void this.readMetadata(input.value);
      return;
    }
    this.attachmentQuery = input;
    input.addEventListener('input', () => {
      this.filterVisibleAttachments();
      void this.readMetadata(input.value);
    });
    void this.readMetadata(input.value);
  }

  private filterVisibleAttachments(): void {
    const query = this.attachmentQuery?.value.trim().toLocaleLowerCase() || '';
    // Metadata upgrades the frozen heading from "附件名" to "名称". Keep using
    // the marked owner table after that upgrade so committed searches continue
    // to filter only actual attachment rows, never the refresh diagnostics.
    const table = this.attachmentTable();
    if (!table) return;
    for (const row of table.querySelectorAll<HTMLTableRowElement>('tbody tr')) {
      const name = row.cells[0]?.querySelector('span:last-child')?.textContent?.trim().toLocaleLowerCase() || '';
      const match = !query || name.includes(query);
      row.hidden = !match;
      if (match) row.style.removeProperty('display');
      else row.style.setProperty('display', 'none', 'important');
    }
  }

  private async readMetadata(value: string): Promise<void> {
    const query = value.trim();
    if (query === this.metadataQuery && this.hasUsableMetadata()) return;
    this.metadataQuery = query;
    this.readAbort?.abort();
    const abort = new AbortController();
    this.readAbort = abort;
    const generation = ++this.readGeneration;
    try {
      if (this.page === 'attach') {
        const response = unwrapGenerated(await listLegacyAttachments({
          limit: '100', offset: '0', enabled_only: 'false', ...(query ? { q: query } : {}),
        }, apiRequestOptions({ signal: abort.signal }))) as LegacyAttachmentListSuccess;
        if (generation !== this.readGeneration) return;
        this.attachments = Array.isArray(response.items) ? response.items : [];
      } else {
        const response = unwrapGenerated(await listLegacyMiniPrograms({
          limit: 100, offset: 0, enabled_only: false, ...(query ? { q: query } : {}),
        }, apiRequestOptions({ signal: abort.signal }))) as LegacyMiniProgramListResponse;
        if (generation !== this.readGeneration) return;
        this.miniPrograms = Array.isArray(response.items) ? response.items : [];
      }
      this.applyCachedMetadata();
    } catch (error) {
      if (generation !== this.readGeneration || (error instanceof DOMException && error.name === 'AbortError')) return;
      this.renderMetadataError(error);
    }
  }

  private hasUsableMetadata(): boolean {
    return this.page === 'attach' ? this.attachments.length > 0 : this.miniPrograms.length > 0;
  }

  private applyCachedMetadata(): void {
    if (this.page === 'attach') this.annotateAttachments();
    else this.annotateMiniPrograms();
  }

  private attachmentTable(): HTMLTableElement | undefined {
    return Array.from(this.stage.querySelectorAll<HTMLTableElement>('table'))
      .find((candidate) => candidate.querySelector('thead th')?.textContent?.trim() === '附件名' || candidate.dataset.materialLibraryAttachmentTable === 'true');
  }

  private annotateAttachments(): void {
    const table = this.attachmentTable();
    if (!table || !this.attachments.length) return;
    const rows = Array.from(table.querySelectorAll<HTMLTableRowElement>('tbody tr'));
    const byName = new Map<string, LegacyAttachmentItem>();
    const duplicated = new Set<string>();
    for (const item of this.attachments) {
      if (byName.has(item.name)) duplicated.add(item.name);
      else byName.set(item.name, item);
    }
    const matched = rows.map((row) => {
      const name = row.cells[0]?.querySelector('span:last-child')?.textContent?.trim() || '';
      return !duplicated.has(name) ? byName.get(name) : undefined;
    });
    // A donor row does not expose its typed ID. Do not guess from position or
    // duplicate names: retain its original presentation until every visible
    // row can be matched to the same Media-owned DTO by a unique name.
    if (!rows.length || matched.some((item) => !item)) return;
    table.dataset.materialLibraryAttachmentTable = 'true';
    const header = table.querySelector<HTMLTableRowElement>('thead tr');
    if (header && header.cells.length === 6) {
      header.cells[0].textContent = '名称';
      header.cells[4].textContent = '创建时间';
      const status = document.createElement('th'); status.textContent = '启用状态';
      const version = document.createElement('th'); version.textContent = '版本';
      for (const cell of [status, version]) cell.style.cssText = header.cells[4].style.cssText;
      header.insertBefore(status, header.cells[5]);
      header.insertBefore(version, header.cells[6]);
    }
    rows.forEach((row, index) => {
      const item = matched[index]!;
      const metadataVersion = `${item.id}:${item.version}`;
      if (row.dataset.materialLibraryMetadataVersion === metadataVersion) return;
      row.cells[3].textContent = formatSize(item.file_size);
      row.cells[4].textContent = formatTime(item.created_at);
      if (row.cells.length === 6) {
        const status = document.createElement('td');
        const version = document.createElement('td');
        for (const cell of [status, version]) cell.style.cssText = row.cells[4].style.cssText;
        row.insertBefore(status, row.cells[5]);
        row.insertBefore(version, row.cells[6]);
      }
      row.cells[5].textContent = item.enabled ? '启用' : '已停用';
      row.cells[6].textContent = `v${item.version}`;
      // The MIME type already has its own column. Leave the first cell for a
      // scannable display name instead of spending half the row on a second
      // long `application/pdf` badge.
      const duplicateType = row.cells[0]?.querySelector<HTMLElement>('span:first-child');
      if (duplicateType) duplicateType.style.display = 'none';
      const operations = row.cells[7]?.querySelector<HTMLElement>('div');
      if (operations) operations.style.cssText = 'display:flex;flex-wrap:nowrap;gap:2px;justify-content:flex-end;white-space:nowrap';
      row.dataset.materialLibraryMetadataVersion = metadataVersion;
    });
  }

  private annotateMiniPrograms(): void {
    if (!this.miniPrograms.length) return;
    const grid = Array.from(this.stage.querySelectorAll<HTMLElement>('div'))
      .find((candidate) => candidate.dataset.materialLibraryMiniDirectory === 'true' || (candidate.style.display === 'grid' && candidate.style.gridTemplateColumns.includes('repeat(4')));
    if (!grid) return;
    const cards = Array.from(grid.children).filter((node): node is HTMLElement => node instanceof HTMLElement);
    const byName = new Map<string, LegacyMiniProgram>();
    const duplicated = new Set<string>();
    for (const item of this.miniPrograms) {
      if (byName.has(item.name)) duplicated.add(item.name);
      else byName.set(item.name, item);
    }
    const header = grid.parentElement?.querySelector<HTMLElement>('[data-material-library-mini-header]') || document.createElement('div');
    if (!header.dataset.materialLibraryMiniHeader) {
      header.dataset.materialLibraryMiniHeader = 'true';
      header.setAttribute('role', 'row');
      header.style.cssText = 'display:grid;grid-template-columns:72px minmax(150px,1.2fr) minmax(112px,1fr) minmax(128px,1.2fr) minmax(122px,1fr) minmax(110px,1fr) 88px;gap:10px;align-items:center;padding:9px 12px;background:#FAFAFB;border:1px solid #DEE0E3;border-bottom:0;border-radius:8px 8px 0 0;color:#8F959E;font-size:12px;font-weight:500';
      ['封面', '名称 / 标题', 'AppID', '页面', '状态 / 封面', '更新时间', '操作'].forEach((label) => {
        const cell = document.createElement('span'); cell.setAttribute('role', 'columnheader'); cell.textContent = label; header.append(cell);
      });
      grid.before(header);
    }
    grid.dataset.materialLibraryMiniDirectory = 'true';
    grid.setAttribute('role', 'table');
    grid.setAttribute('aria-label', '小程序素材目录');
    grid.style.cssText = 'display:grid;grid-template-columns:minmax(0,1fr);gap:0;border:1px solid #DEE0E3;border-radius:0 0 8px 8px;overflow:hidden;background:#fff';
    cards.forEach((card) => {
      const name = Array.from(card.querySelectorAll<HTMLElement>('div'))
        .map((node) => node.textContent?.trim() || '')
        .find((value) => byName.has(value)) || '';
      const item = !duplicated.has(name) ? byName.get(name) : undefined;
      if (!item || card.dataset.materialLibraryMetadataVersion === `${item.id}:${item.version}`) return;
      const inner = card.firstElementChild as HTMLElement | null;
      const cover = inner?.firstElementChild as HTMLElement | null;
      const body = inner?.lastElementChild as HTMLElement | null;
      if (!inner || !cover || !body) return;
      const [nameNode, thumbnailStatus, enabledNode, actions] = Array.from(body.children) as HTMLElement[];
      if (!nameNode || !thumbnailStatus || !enabledNode || !actions) return;
      card.setAttribute('role', 'row');
      card.style.cssText = 'display:grid;grid-template-columns:72px minmax(150px,1.2fr) minmax(112px,1fr) minmax(128px,1.2fr) minmax(122px,1fr) minmax(110px,1fr) 88px;gap:10px;align-items:center;min-width:0;padding:9px 12px;border:0;border-bottom:1px solid #F2F3F5;border-radius:0;overflow:visible;background:#fff';
      inner.style.display = 'contents';
      cover.setAttribute('role', 'cell');
      cover.style.cssText = 'height:56px;border-radius:5px;grid-column:1;cursor:pointer;background:#EFF4FF';
      body.style.display = 'contents';
      nameNode.setAttribute('role', 'cell');
      nameNode.style.cssText = 'grid-column:2;min-width:0;font-size:13px;font-weight:500;color:#1F2329;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
      const title = document.createElement('small'); title.textContent = item.title || '—'; title.style.cssText = 'display:block;margin-top:3px;color:#8F959E;font-size:12px;font-weight:400;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
      nameNode.append(title);
      const appid = document.createElement('span'); appid.setAttribute('role', 'cell'); appid.textContent = item.appid || '—'; appid.style.cssText = 'grid-column:3;min-width:0;color:#646A73;font-size:12px;overflow-wrap:anywhere';
      const page = document.createElement('span'); page.setAttribute('role', 'cell'); page.textContent = item.page_path || item.pagepath || '—'; page.style.cssText = 'grid-column:4;min-width:0;color:#646A73;font-size:12px;overflow-wrap:anywhere';
      const sourceThumbnailStatus = thumbnailStatus.textContent?.replace(/^\s*[●•]\s*/, '').trim() || '';
      const thumbnailText = sourceThumbnailStatus === 'ready' || sourceThumbnailStatus === '可用' ? '封面可用' : sourceThumbnailStatus === 'not_available' ? '封面暂不可用' : sourceThumbnailStatus || '封面状态未知';
      thumbnailStatus.textContent = `${item.enabled ? '启用' : '已停用'} · ${thumbnailText}`;
      thumbnailStatus.setAttribute('role', 'cell');
      thumbnailStatus.style.cssText = `grid-column:5;min-width:0;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:${thumbnailText === '封面可用' ? '#237804' : '#B54708'}`;
      enabledNode.style.display = 'none';
      const updated = document.createElement('span'); updated.setAttribute('role', 'cell'); updated.textContent = `${formatTime(item.updated_at)} · v${item.version}`; updated.style.cssText = 'grid-column:6;min-width:0;color:#646A73;font-size:12px;line-height:18px';
      actions.setAttribute('role', 'cell');
      actions.style.cssText = 'grid-column:7;display:flex;align-items:center;justify-content:flex-end;gap:2px;white-space:nowrap';
      actions.before(appid, page, updated);
      card.dataset.materialLibraryMetadataVersion = `${item.id}:${item.version}`;
    });
  }

  private renderMetadataError(error: unknown): void {
    const existing = this.stage.querySelector<HTMLElement>('[data-material-library-read-error]');
    const message = error instanceof ApiError && error.status === 401
      ? '登录状态已失效，请重新登录后查看素材。'
      : error instanceof ApiError && error.status === 403
        ? '当前账号无权查看该类素材。'
        : '素材列表暂不可读取，当前内容已保留。';
    if (existing) { existing.textContent = message; return; }
    const node = document.createElement('p');
    node.dataset.materialLibraryReadError = 'true'; node.setAttribute('role', 'alert'); node.textContent = message;
    node.style.cssText = 'margin:0;color:#B42318;font-size:12px;line-height:20px';
    const input = this.stage.querySelector<HTMLElement>(this.config.querySelector);
    input?.closest('div')?.parentElement?.after(node);
  }
}

function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatTime(value: string): string {
  if (!value) return '—';
  try { return formatShanghaiDateTime(value); } catch { return '—'; }
}

function boot(): void {
  if (!document.querySelector('main#stage[data-material-library-workspace="true"]')) return;
  const page = document.body?.dataset.page;
  if (page !== 'attach' && page !== 'mpLib') return;
  const stage = document.getElementById('stage');
  if (!stage || stage.dataset.materialLibraryPresentationMounted === 'true') return;
  stage.dataset.materialLibraryPresentationMounted = 'true';
  new FrozenMaterialPresentation(page, stage).start();
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot, { once: true });
else boot();
