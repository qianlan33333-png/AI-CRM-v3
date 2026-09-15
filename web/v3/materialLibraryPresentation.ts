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

type NodePresentation = { style: string | null; role: string | null; children: Node[] };
type AttachmentRowSource = { cells: NodePresentation[] };
type MiniCardSource = { card: NodePresentation; cover: NodePresentation; name: NodePresentation; thumbnail: NodePresentation; enabled: NodePresentation; actions: NodePresentation; inner: HTMLElement; body: HTMLElement; coverNode: HTMLElement; nameNode: HTMLElement; thumbnailNode: HTMLElement; enabledNode: HTMLElement; actionsNode: HTMLElement };

function snapshotNode(node: HTMLElement): NodePresentation {
  return { style: node.getAttribute('style'), role: node.getAttribute('role'), children: Array.from(node.childNodes) };
}

function restoreNode(node: HTMLElement, snapshot: NodePresentation): void {
  node.replaceChildren(...snapshot.children);
  if (snapshot.style === null) node.removeAttribute('style'); else node.setAttribute('style', snapshot.style);
  if (snapshot.role === null) node.removeAttribute('role'); else node.setAttribute('role', snapshot.role);
}

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
  private metadataReadFailed = false;
  private readonly attachmentRows = new WeakMap<HTMLTableRowElement, AttachmentRowSource>();
  private attachmentHeader?: NodePresentation[];
  private readonly miniCards = new WeakMap<HTMLElement, MiniCardSource>();
  private readonly authorizationDisabled = new Map<HTMLButtonElement, { disabled: boolean; ariaDisabled: string | null }>();

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
      this.metadataReadFailed = false;
      this.setMutationReadOnly(false);
      this.applyCachedMetadata();
    } catch (error) {
      if (generation !== this.readGeneration || (error instanceof DOMException && error.name === 'AbortError')) return;
      this.metadataReadFailed = true;
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
        this.attachments = [];
        this.miniPrograms = [];
        this.metadataQuery = '';
        this.clearEnrichedMetadata();
        this.setMutationReadOnly(true);
      }
      this.renderMetadataError(error);
    }
  }

  private hasUsableMetadata(): boolean {
    return !this.metadataReadFailed && (this.page === 'attach' ? this.attachments.length > 0 : this.miniPrograms.length > 0);
  }

  private setMutationReadOnly(readonly: boolean): void {
    if (!readonly) {
      for (const [control, prior] of this.authorizationDisabled) {
        control.disabled = prior.disabled;
        if (prior.ariaDisabled === null) control.removeAttribute('aria-disabled'); else control.setAttribute('aria-disabled', prior.ariaDisabled);
      }
      this.authorizationDisabled.clear();
      this.stage.removeAttribute('data-material-library-readonly');
      return;
    }
    this.stage.dataset.materialLibraryReadonly = 'true';
    const controls = new Set<HTMLButtonElement>();
    if (this.action instanceof HTMLButtonElement) controls.add(this.action);
    for (const control of this.stage.querySelectorAll<HTMLButtonElement>('button')) {
      const label = control.textContent?.trim() || '';
      if (label === '编辑' || label === '删除' || label === this.config.actionLabel) controls.add(control);
    }
    for (const control of controls) {
      if (!this.authorizationDisabled.has(control)) this.authorizationDisabled.set(control, { disabled: control.disabled, ariaDisabled: control.getAttribute('aria-disabled') });
      control.disabled = true;
      control.setAttribute('aria-disabled', 'true');
    }
  }

  private clearEnrichedMetadata(): void {
    const table = this.attachmentTable();
    if (table && this.attachmentHeader) {
      const header = table.querySelector<HTMLTableRowElement>('thead tr');
      if (header) {
        while (header.cells.length > this.attachmentHeader.length) header.deleteCell(5);
        this.attachmentHeader.forEach((snapshot, index) => restoreNode(header.cells[index]!, snapshot));
      }
      for (const row of table.querySelectorAll<HTMLTableRowElement>('tbody tr')) {
        const source = this.attachmentRows.get(row);
        if (!source) continue;
        while (row.cells.length > source.cells.length) row.deleteCell(5);
        source.cells.forEach((snapshot, index) => restoreNode(row.cells[index]!, snapshot));
        delete row.dataset.materialLibraryMetadataVersion;
      }
      table.removeAttribute('data-material-library-attachment-table');
    }
    for (const card of this.stage.querySelectorAll<HTMLElement>('[data-material-library-mini-directory] > [data-material-library-metadata-version]')) {
      const source = this.miniCards.get(card);
      if (!source) continue;
      restoreNode(source.coverNode, source.cover);
      restoreNode(source.nameNode, source.name);
      restoreNode(source.thumbnailNode, source.thumbnail);
      restoreNode(source.enabledNode, source.enabled);
      restoreNode(source.actionsNode, source.actions);
      source.body.replaceChildren(source.nameNode, source.thumbnailNode, source.enabledNode, source.actionsNode);
      source.inner.replaceChildren(source.coverNode, source.body);
      restoreNode(card, source.card);
      card.replaceChildren(source.inner);
      delete card.dataset.materialLibraryMetadataVersion;
    }
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
    if (!rows.length) return;
    if (matched.some((item) => !item)) {
      // The frozen attachment rows expose no stable entity ID. A duplicate
      // display name therefore cannot be joined safely to a typed DTO. Keep
      // the original row and its bound callbacks intact instead of applying a
      // neighboring record's metadata.
      table.dataset.materialLibraryAttachmentTable = 'unresolved';
      let notice = table.parentElement?.querySelector<HTMLElement>('[data-material-library-identity-notice="attachment"]');
      if (!notice) {
        notice = document.createElement('p');
        notice.dataset.materialLibraryIdentityNotice = 'attachment';
        notice.style.cssText = 'margin:8px 12px;color:#646A73;font-size:12px;line-height:18px';
        table.before(notice);
      }
      notice.textContent = '存在同名附件，补充信息待确认；原有记录和操作保持不变。';
      return;
    }
    table.dataset.materialLibraryAttachmentTable = 'true';
    const header = table.querySelector<HTMLTableRowElement>('thead tr');
    if (header && !this.attachmentHeader) this.attachmentHeader = Array.from(header.cells).map((cell) => snapshotNode(cell));
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
      if (!this.attachmentRows.has(row)) this.attachmentRows.set(row, { cells: Array.from(row.cells).map((cell) => snapshotNode(cell)) });
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
    const knownNames = new Set(this.miniPrograms.map((item) => item.name));
    const byName = new Map<string, LegacyMiniProgram>();
    const duplicated = new Set<string>();
    for (const item of this.miniPrograms) {
      if (byName.has(item.name)) duplicated.add(item.name);
      else byName.set(item.name, item);
    }
    grid.dataset.materialLibraryMiniDirectory = 'true';
    grid.setAttribute('role', 'table');
    grid.setAttribute('aria-label', '小程序素材目录');
    grid.style.cssText = 'display:grid;grid-template-columns:minmax(0,1fr);gap:0;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden;background:#fff';
    let header = grid.querySelector<HTMLElement>(':scope > [data-material-library-mini-header]');
    if (!header) {
      header = document.createElement('div');
      header.dataset.materialLibraryMiniHeader = 'true';
      header.setAttribute('role', 'row');
      header.style.cssText = 'display:grid;grid-template-columns:72px minmax(150px,1.2fr) minmax(112px,1fr) minmax(128px,1.2fr) minmax(122px,1fr) minmax(110px,1fr) 88px;gap:10px;align-items:center;padding:9px 12px;background:#FAFAFB;border-bottom:1px solid #DEE0E3;color:#8F959E;font-size:12px;font-weight:500';
      ['封面', '名称 / 标题', 'AppID', '页面', '状态 / 封面', '更新时间', '操作'].forEach((label) => {
        const cell = document.createElement('span'); cell.setAttribute('role', 'columnheader'); cell.textContent = label; header!.append(cell);
      });
      grid.prepend(header);
    }
    const cards = Array.from(grid.children).filter((node): node is HTMLElement => node instanceof HTMLElement && node.dataset.materialLibraryMiniHeader !== 'true');
    cards.forEach((card, index) => {
      const sourceName = card.dataset.materialLibrarySourceName || Array.from(card.querySelectorAll<HTMLElement>('div'))
        .map((node) => node.textContent?.trim() || '')
        .find((value) => knownNames.has(value)) || `未识别素材 ${index + 1}`;
      card.dataset.materialLibrarySourceName = sourceName;
      // Names are display values, not identities. Only a uniquely named typed
      // record may enrich the donor row. Ambiguous rows use the visible owner
      // fields and explicit unknown values, never a guessed neighbor record.
      const item = !duplicated.has(sourceName) ? byName.get(sourceName) : undefined;
      const metadataVersion = item ? `${item.id}:${item.version}` : `unresolved:${sourceName}`;
      if (card.dataset.materialLibraryMetadataVersion === metadataVersion) return;
      const inner = card.firstElementChild as HTMLElement | null;
      const cover = inner?.firstElementChild as HTMLElement | null;
      const body = inner?.lastElementChild as HTMLElement | null;
      if (!inner || !cover || !body) return;
      const [nameNode, thumbnailStatus, enabledNode, actions] = Array.from(body.children) as HTMLElement[];
      if (!nameNode || !thumbnailStatus || !enabledNode || !actions) return;
      if (!this.miniCards.has(card)) this.miniCards.set(card, {
        card: snapshotNode(card), cover: snapshotNode(cover), name: snapshotNode(nameNode), thumbnail: snapshotNode(thumbnailStatus), enabled: snapshotNode(enabledNode), actions: snapshotNode(actions),
        inner, body, coverNode: cover, nameNode, thumbnailNode: thumbnailStatus, enabledNode, actionsNode: actions,
      });
      card.setAttribute('role', 'row');
      card.style.cssText = 'display:grid;grid-template-columns:72px minmax(150px,1.2fr) minmax(112px,1fr) minmax(128px,1.2fr) minmax(122px,1fr) minmax(110px,1fr) 88px;gap:10px;align-items:center;min-width:0;padding:9px 12px;border:0;border-bottom:1px solid #F2F3F5;border-radius:0;overflow:visible;background:#fff';
      cover.setAttribute('role', 'cell');
      cover.style.cssText = 'height:56px;border-radius:5px;grid-column:1;cursor:pointer;background:#EFF4FF';
      nameNode.setAttribute('role', 'cell');
      nameNode.style.cssText = 'grid-column:2;min-width:0;font-size:13px;font-weight:500;color:#1F2329;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
      const title = document.createElement('small'); title.textContent = item?.title || '信息待确认'; title.style.cssText = 'display:block;margin-top:3px;color:#8F959E;font-size:12px;font-weight:400;white-space:nowrap;overflow:hidden;text-overflow:ellipsis';
      nameNode.append(title);
      const appid = document.createElement('span'); appid.setAttribute('role', 'cell'); appid.textContent = item?.appid || '—'; appid.style.cssText = 'grid-column:3;min-width:0;color:#646A73;font-size:12px;overflow-wrap:anywhere';
      const page = document.createElement('span'); page.setAttribute('role', 'cell'); page.textContent = item?.page_path || item?.pagepath || '—'; page.style.cssText = 'grid-column:4;min-width:0;color:#646A73;font-size:12px;overflow-wrap:anywhere';
      const sourceThumbnailStatus = thumbnailStatus.textContent?.replace(/^\s*[●•]\s*/, '').trim() || '';
      const thumbnailText = sourceThumbnailStatus === 'ready' || sourceThumbnailStatus === '可用' ? '封面可用' : sourceThumbnailStatus === 'not_available' ? '封面暂不可用' : sourceThumbnailStatus || '封面状态未知';
      const enabledText = item ? (item.enabled ? '已启用' : '已停用') : (enabledNode.textContent?.trim() || '状态待确认');
      thumbnailStatus.textContent = `${enabledText} · ${thumbnailText}`;
      thumbnailStatus.setAttribute('role', 'cell');
      thumbnailStatus.style.cssText = `grid-column:5;min-width:0;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:${thumbnailText === '封面可用' ? '#237804' : '#B54708'}`;
      enabledNode.style.display = 'none';
      const updated = document.createElement('span'); updated.setAttribute('role', 'cell'); updated.textContent = item ? `${formatTime(item.updated_at)} · v${item.version}` : '信息待确认'; updated.style.cssText = 'grid-column:6;min-width:0;color:#646A73;font-size:12px;line-height:18px';
      actions.setAttribute('role', 'cell');
      actions.style.cssText = 'grid-column:7;display:flex;align-items:center;justify-content:flex-end;gap:2px;white-space:nowrap';
      // Keep the original elements in the row so the donor's direct event
      // handlers continue to target their own entity, including duplicates.
      card.replaceChildren(cover, nameNode, appid, page, thumbnailStatus, updated, actions);
      card.dataset.materialLibraryMetadataVersion = metadataVersion;
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
