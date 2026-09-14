import { installCommittedTextSearch, replayCommittedTextSearch, resetCommittedTextSearch } from './shared/ui/committedTextSearch';

export {};

// Release-only loader for the byte-frozen dd8 selection components.  It does
// adapt selection data: each page Host remains responsible for scoped reads.
// It supplies the V3 envelope for the shared directory refresh command and
// guarantees dependency order and one evaluation per page.
declare global {
  interface Window {
    AICRMStandardComponents?: { ready(): Promise<void> };
    AICRMWeComTagPicker?: unknown;
  }
}

// The frozen picker refreshes the common saved staff-profile projection.
// This is a Provider read plus a local projection update, never a message send.
// Explicitly configured V3 requests keep their own envelope.
const componentFetch = window.fetch.bind(window);
window.fetch = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const request = typeof input === 'string' || input instanceof URL ? undefined : input;
  const url = new URL(request ? request.url : String(input), location.href);
  const method = String(init?.method || request?.method || 'GET').toUpperCase();
  if (url.origin !== location.origin || url.pathname !== '/api/admin/common/operation-members/sync' || method !== 'POST' || init?.body != null || request?.body != null) return componentFetch(input, init);
  const headers = new Headers(init?.headers || request?.headers);
  headers.set('Accept', 'application/json');
  headers.set('Content-Type', 'application/json');
  if (!headers.has('Idempotency-Key')) headers.set('Idempotency-Key', `operation-members-${crypto.randomUUID()}`);
  const token = document.cookie.split(';').map((part) => part.trim()).find((part) => part.startsWith('aicrm_admin_csrf=') || part.startsWith('aicrm_csrf='));
  if (token && !headers.has('X-CSRF-Token')) headers.set('X-CSRF-Token', decodeURIComponent(token.slice(token.indexOf('=') + 1)));
  return componentFetch(input, { ...init, method, headers, credentials: 'same-origin', body: JSON.stringify({ scope: 'group_ops', page_size: 100 }) });
};

// Keep the standard selector's current rows and selections while refreshing.
// Its historical refresh handler clears all candidates on an HTTP error; the
// V3 Host handles only this command and uses the existing search reload on success.
let directoryRefreshPending = false;
document.addEventListener('click', (event) => {
  const clear = (event.target as Element | null)?.closest<HTMLButtonElement>('[data-operation-member-clear]');
  if (clear) {
    const input = clear.closest<HTMLElement>('[data-operation-member-picker]')?.querySelector<HTMLInputElement>('[data-operation-member-search]');
    if (input) {
      // The frozen clear handler reloads immediately. Mark its empty value as
      // committed first so a later directory refresh cannot resurrect a
      // closed picker session's old query.
      input.value = '';
      resetCommittedTextSearch(input);
    }
    return;
  }
  const button = (event.target as Element | null)?.closest<HTMLButtonElement>('[data-operation-member-refresh]');
  if (!button) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  if (directoryRefreshPending) return;
  const modal = button.closest<HTMLElement>('[data-operation-member-picker]');
  if (!modal) return;
  directoryRefreshPending = true;
  button.disabled = true;
  button.textContent = '刷新中';
  modal.querySelector('[data-v3-directory-refresh-error]')?.remove();
  void (async () => {
    try {
      const response = await window.fetch('/api/admin/common/operation-members/sync', { method: 'POST', credentials: 'same-origin', headers: { Accept: 'application/json' } });
      const data = await response.clone().json().catch(() => ({})) as { ok?: boolean };
      if (!response.ok || data.ok === false) throw new Error(`刷新客服失败（HTTP ${response.status}），已保留当前列表和选择。`);
      const search = modal.querySelector<HTMLInputElement>('[data-operation-member-search]');
      if (search) replayCommittedTextSearch(search);
    } catch (error) {
      const notice = document.createElement('div');
      notice.dataset.v3DirectoryRefreshError = '1';
      notice.setAttribute('role', 'alert');
      notice.textContent = error instanceof Error ? error.message : '刷新客服失败，已保留当前列表和选择。';
      modal.querySelector('[data-operation-member-list]')?.before(notice);
    } finally {
      directoryRefreshPending = false;
      button.disabled = false;
      button.textContent = '刷新客服';
    }
  })();
}, true);

const scripts = [
  '/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948',
  '/assets/standard-components/group_chat_picker.js',
  '/assets/standard-components/material_picker.js',
  '/assets/standard-components/send_content_composer.js',
  '/assets/standard-components/wecom_tag_picker.js',
];

let loading: Promise<void> | undefined;
let tagPickerLocked = false;
const operationMemberSearchLifecycleInstalled = new WeakSet<object>();

function installOperationMemberSearchLifecycle(): void {
  if (!window.OperationMemberPicker || operationMemberSearchLifecycleInstalled.has(window.OperationMemberPicker)) return;
  const picker = window.OperationMemberPicker;
  operationMemberSearchLifecycleInstalled.add(picker);
  const open = picker.open.bind(picker);
  picker.open = (options) => {
    const result = open(options);
    const input = document.querySelector<HTMLInputElement>('[data-operation-member-picker] [data-operation-member-search]');
    if (input) resetCommittedTextSearch(input);
    return result;
  };
}

// The staff picker is evaluated as an external frozen script after this Host.
// Preserve its global API while wrapping each actual assignment exactly once;
// no DOM focus heuristic is used to guess whether an operator committed a
// draft query.
function observeOperationMemberPicker(): void {
  const descriptor = Object.getOwnPropertyDescriptor(window, 'OperationMemberPicker');
  if (descriptor && !descriptor.configurable) {
    installOperationMemberSearchLifecycle();
    return;
  }
  let current = window.OperationMemberPicker;
  Object.defineProperty(window, 'OperationMemberPicker', {
    configurable: true,
    get: () => current,
    set: (value) => {
      current = value;
      installOperationMemberSearchLifecycle();
    },
  });
  installOperationMemberSearchLifecycle();
}

observeOperationMemberPicker();

function lockOriginalTagPicker(): void {
  if (tagPickerLocked || !window.AICRMWeComTagPicker) return;
  tagPickerLocked = true;
  const original = window.AICRMWeComTagPicker;
  Object.defineProperty(window, 'AICRMWeComTagPicker', {
    configurable: true,
    get: () => original,
    // Frozen page bundles may still assign their historical reimplementation.
    // Preserve the loaded standard global without changing either donor file.
    set: () => undefined,
  });
}

function load(source: string): Promise<void> {
  if (document.querySelector(`script[data-aicrm-standard-component="${source}"]`)) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.defer = true;
    script.src = source;
    script.dataset.aicrmStandardComponent = source;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error('标准选择组件加载失败，请刷新页面后重试'));
    document.head.append(script);
  });
}

window.AICRMStandardComponents = {
  ready(): Promise<void> {
    loading ||= scripts.reduce(async (previous, source) => {
      await previous;
      await load(source);
    }, Promise.resolve()).then(() => {
      lockOriginalTagPicker();
      installOperationMemberSearchLifecycle();
    });
    return loading;
  },
};
void window.AICRMStandardComponents.ready();
installCommittedTextSearch();
