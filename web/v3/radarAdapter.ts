export {};

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
