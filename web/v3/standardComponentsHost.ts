import { installCommittedTextSearch, replayCommittedTextSearch, resetCommittedTextSearch } from './shared/ui/committedTextSearch';

export {};

// Release-only loader for the byte-frozen dd8 selection components.  It does
// adapt selection data: each page Host remains responsible for scoped reads.
// It supplies the V3 envelope for the shared directory refresh command and
// guarantees dependency order and one evaluation per page.
declare global {
  interface Window {
    AICRMStandardComponents?: {
      ready(): Promise<void>;
      readyFor(capabilities: readonly OnDemandStandardComponentCapability[]): Promise<void>;
    };
    AICRMWeComTagPicker?: unknown;
  }
}

type StandardComponentCapability = 'operationMembers' | 'groupChats' | 'materials' | 'sendContent' | 'tags';
type OnDemandStandardComponentCapability = 'tags';

type OperationMemberPickerOptions = Record<string, unknown>;
type OperationMemberPicker = { open(options: OperationMemberPickerOptions): unknown };
type OperationMemberPickerWindow = { OperationMemberPicker?: OperationMemberPicker };

function operationMemberPicker(): OperationMemberPicker | undefined {
  return (window as unknown as OperationMemberPickerWindow).OperationMemberPicker;
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
  if (!clear) return;
  const input = clear.closest<HTMLElement>('[data-operation-member-picker]')?.querySelector<HTMLInputElement>('[data-operation-member-search]');
  if (!input) return;
  // The frozen clear action reloads immediately. Mark its empty value as the
  // committed query first, so a later refresh cannot revive an old draft.
  input.value = '';
  resetCommittedTextSearch(input);
}, true);

document.addEventListener('click', (event) => {
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

const components: ReadonlyArray<{ capability: StandardComponentCapability; source: string; ready: () => boolean }> = [
  { capability: 'operationMembers', source: '/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).OperationMemberPicker?.open === 'function' },
  { capability: 'groupChats', source: '/assets/standard-components/group_chat_picker.js', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).AICRMGroupChatPicker?.open === 'function' },
  { capability: 'materials', source: '/assets/standard-components/material_picker.js', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).AICRMMaterialPicker?.open === 'function' },
  { capability: 'sendContent', source: '/assets/standard-components/send_content_composer.js', ready: () => {
    const composer = (window as unknown as Record<string, { open?: unknown; mount?: unknown }>).AICRMSendContentComposer;
    return typeof composer?.open === 'function' && typeof composer.mount === 'function';
  } },
  { capability: 'tags', source: '/assets/standard-components/wecom_tag_picker.js', ready: () => typeof (window as unknown as Record<string, { open?: unknown }>).AICRMWeComTagPicker?.open === 'function' },
];

const componentByCapability = new Map(components.map((component) => [component.capability, component]));
const componentLoads = new Map<StandardComponentCapability, Promise<void>>();
const readyComponents = new Set<StandardComponentCapability>();
let tagPickerLocked = false;
const operationMemberSearchLifecycleInstalled = new WeakSet<object>();

function installOperationMemberSearchLifecycle(): void {
  const picker = operationMemberPicker();
  if (!picker || operationMemberSearchLifecycleInstalled.has(picker)) return;
  operationMemberSearchLifecycleInstalled.add(picker);
  const open = picker.open.bind(picker);
  picker.open = (options: OperationMemberPickerOptions) => {
    const result = open(options);
    const input = document.querySelector<HTMLInputElement>('[data-operation-member-picker] [data-operation-member-search]');
    if (input) resetCommittedTextSearch(input);
    return result;
  };
}

// Frozen scripts assign this global after the Host. Observe that assignment
// once so every actual picker session starts with a committed empty query.
function observeOperationMemberPicker(): void {
  const descriptor = Object.getOwnPropertyDescriptor(window, 'OperationMemberPicker');
  if (descriptor && !descriptor.configurable) {
    installOperationMemberSearchLifecycle();
    return;
  }
  let current = operationMemberPicker();
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

function matchingScript(source: string): HTMLScriptElement | undefined {
  return [...document.querySelectorAll<HTMLScriptElement>('script[data-aicrm-standard-component]')]
    .find((script) => script.dataset.aicrmStandardComponent === source && script.src === new URL(source, document.baseURI).href);
}

function load(component: { capability: StandardComponentCapability; source: string; ready: () => boolean }): Promise<void> {
  if (readyComponents.has(component.capability)) return Promise.resolve();
  const pending = componentLoads.get(component.capability);
  if (pending) return pending;

  let startRequest: (() => void) | undefined;
  const request = new Promise<void>((resolve, reject) => {
    let script = matchingScript(component.source);
    let appendScript = false;
    let settled = false;
    const fail = () => {
      if (settled) return;
      settled = true;
      script?.remove();
      componentLoads.delete(component.capability);
      reject(new Error('标准选择组件加载失败，请刷新页面后重试'));
    };
    const succeed = () => {
      if (settled) return;
      if (!component.ready()) {
        fail();
        return;
      }
      settled = true;
      readyComponents.add(component.capability);
      if (component.capability === 'tags') lockOriginalTagPicker();
      if (component.capability === 'operationMembers') installOperationMemberSearchLifecycle();
      resolve();
    };

    if (!script) {
      script = document.createElement('script');
      script.defer = true;
      script.src = component.source;
      script.dataset.aicrmStandardComponent = component.source;
      script.dataset.aicrmStandardComponentProvenance = 'v3-standard-components-host';
      script.dataset.aicrmStandardComponentState = 'pending';
      appendScript = true;
    }
    script.addEventListener('load', () => {
      script!.dataset.aicrmStandardComponentState = 'loaded';
      succeed();
    }, { once: true });
    script.addEventListener('error', fail, { once: true });
    if (appendScript) startRequest = () => { if (!settled) document.head.append(script!); };
    else if (script.dataset.aicrmStandardComponentState === 'loaded') queueMicrotask(succeed);
  });
  componentLoads.set(component.capability, request);
  // Register the source-level single-flight before a loader can dispatch an
  // immediate load/error event.
  startRequest?.();
  return request;
}

function readyFor(capabilities: readonly OnDemandStandardComponentCapability[]): Promise<void> {
  const requested = [...new Set(capabilities)];
  const selected = requested.map((capability) => {
    if (capability !== 'tags') throw new Error(`未知标准选择组件：${capability}`);
    const component = componentByCapability.get(capability);
    if (!component) throw new Error(`未知标准选择组件：${capability}`);
    return component;
  });
  return Promise.all(selected.map(load)).then(() => undefined);
}

window.AICRMStandardComponents = {
  ready(): Promise<void> {
    return components.reduce(async (previous, component) => {
      await previous;
      await load(component);
    }, Promise.resolve());
  },
  readyFor,
};
installCommittedTextSearch();
observeOperationMemberPicker();

const autoStart = document.querySelector('[data-customer-directory-root]')
  ? window.AICRMStandardComponents.readyFor(['tags'])
  : window.AICRMStandardComponents.ready();
// The automatic preload has no page-level error surface. Explicit callers keep
// the original rejected promise so their local UI can explain and retry it.
void autoStart.catch(() => undefined);
