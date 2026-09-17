import { request } from '../src/api/transport';

type Page = 'attach' | 'mpLib';
const paths: Record<Page, string> = {
  attach: '/api/admin/attachment-library',
  mpLib: '/api/admin/miniprogram-library',
};

// Extend the existing Media transport seam only for the current library list.
// Picker reads, mutations and other pages keep their original request.
export function materialGroupRequest(
  input: RequestInfo | URL,
  method: string,
): RequestInfo | URL {
  const page = document.body?.dataset.page as Page;
  if (!paths[page] || method !== 'GET') return input;
  const current = new URL(location.href);
  if (!current.searchParams.has('material_group')) return input;
  const url = new URL(
    input instanceof Request ? input.url : String(input),
    location.origin,
  );
  if (url.origin !== location.origin || url.pathname !== paths[page])
    return input;
  url.searchParams.set(
    'category',
    current.searchParams.get('material_group') || '',
  );
  return input instanceof Request ? new Request(url, input) : url;
}
export function mountMaterialGroups(stage: HTMLElement, page: Page): void {
  if (stage.querySelector('[data-material-groups]')) return;
  const bar = document.createElement('div');
  bar.dataset.materialGroups = 'true';
  bar.style.cssText =
    'display:flex;gap:12px;align-items:center;padding:12px 20px;background:white';
  const label = document.createElement('label');
  label.textContent = '组别 ';
  const select = document.createElement('select');
  select.setAttribute('aria-label', '组别');
  select.disabled = true;
  const all = new Option('全部分组', 'all');
  select.add(all);
  const refresh = document.createElement('button');
  refresh.type = 'button';
  refresh.textContent = '刷新';
  refresh.className = 'admin-button';
  const status = document.createElement('span');
  status.setAttribute('role', 'status');
  label.append(select);
  bar.append(label, refresh, status);
  const tabs = stage.querySelector('[data-material-library-tabs]');
  if (tabs) tabs.after(bar);
  else stage.prepend(bar);
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 10000);
  void request(paths[page] + '/groups', { signal: controller.signal })
    .then(async (response) => {
      if (!response.ok) throw new Error('read failed');
      const data = await response.json();
      if (!Array.isArray(data.items)) throw new Error('invalid groups');
      const entries = data.items as { name: string; count: number }[];
      select.add(
        new Option(
          `未分组 (${entries.find((x) => x.name === '')?.count || 0})`,
          'group:',
        ),
      );
      for (const item of entries) {
        if (item.name)
          select.add(
            new Option(`${item.name} (${item.count})`, 'group:' + item.name),
          );
      }
      const url = new URL(location.href);
      if (url.searchParams.has('material_group'))
        select.value =
          'group:' + (url.searchParams.get('material_group') || '');
      select.disabled = false;
    })
    .catch(() => {
      status.textContent = '分组加载失败，请刷新重试。';
    })
    .finally(() => clearTimeout(timer));
  select.addEventListener('change', () => {
    const url = new URL(location.href);
    if (select.value === 'all') url.searchParams.delete('material_group');
    else url.searchParams.set('material_group', select.value.slice(6));
    url.searchParams.delete('offset');
    location.assign(url.href);
  });
  refresh.addEventListener('click', () => location.reload());
}
export function mountMaterialGroupControl(
  container: HTMLElement,
  page: Page,
  item: { id: number; version: number; category?: unknown },
): void {
  container.querySelector('[data-material-group-edit]')?.remove();
  const button = document.createElement('button');
  button.type = 'button';
  button.dataset.materialGroupEdit = 'true';
  button.className = 'admin-button admin-button--ghost';
  const category = typeof item.category === 'string' ? item.category : '';
  button.textContent = category || '未分组';
  button.title = '修改组别';
  button.setAttribute('aria-label', '修改组别：' + (category || '未分组'));
  button.addEventListener('click', () => {
    const dialog = document.createElement('dialog');
    dialog.style.cssText =
      'border:1px solid #DEE0E3;border-radius:10px;padding:24px;min-width:300px';
    const form = document.createElement('form');
    const title = document.createElement('h3');
    title.textContent = '素材组别';
    const label = document.createElement('label');
    label.textContent = '组别';
    const field = document.createElement('input');
    field.value = category;
    field.maxLength = 100;
    field.placeholder = '留空为未分组';
    label.append(field);
    const error = document.createElement('p');
    error.setAttribute('role', 'alert');
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.textContent = '取消';
    const save = document.createElement('button');
    save.type = 'submit';
    save.textContent = '保存';
    save.className = 'admin-button admin-button--primary';
    const key = crypto.randomUUID();
    form.append(title, label, error, cancel, save);
    dialog.append(form);
    document.body.append(dialog);
    dialog.addEventListener('close', () => dialog.remove());
    cancel.addEventListener('click', () => dialog.close());
    dialog.showModal();
    field.focus();
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      if (save.disabled) return;
      save.disabled = true;
      field.disabled = true;
      error.textContent = '';
      try {
        await request(paths[page] + '/' + item.id + '/group', {
          method: 'PUT',
          headers: {
            'Content-Type': 'application/json',
            'Idempotency-Key': key,
          },
          body: JSON.stringify({
            category: field.value.trim(),
            expected_version: item.version,
          }),
        });
        location.reload();
      } catch {
        error.textContent = '保存未完成，请刷新核对后重试。';
      } finally {
        save.disabled = false; /* Freeze the command after an uncertain outcome; same key retries only the same data. */
      }
    });
  });
  container.append(button);
}
