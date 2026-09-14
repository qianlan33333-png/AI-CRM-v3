// Shared, V3-owned search interaction contract for the byte-frozen page and
// picker surfaces. It deliberately intercepts only the registered search
// controls below: ordinary form fields and text composers keep their native
// input semantics.
//
// A committed query is a read concern. It does not resolve an identity,
// persist data, or submit an external effect.

type SearchTrigger = 'input' | 'keydown';

type RegisteredSearch = {
  selector: string;
  trigger: SearchTrigger;
};

const registeredSearches: RegisteredSearch[] = [
  // The byte-frozen Channels template uses an `onInput` handler that replaces
  // the list on every key. Its accessible name is the stable donor seam.
  { selector: 'input[aria-label="搜索渠道名称"]', trigger: 'input' },
  { selector: 'input[placeholder="搜索优惠券名称"]', trigger: 'input' },
  { selector: 'input[placeholder="按问卷名称或 ID 搜索"]', trigger: 'input' },
  { selector: 'input[placeholder="搜索标签组 / 标签 / tag_id"]', trigger: 'input' },
  { selector: '#list-search', trigger: 'input' },
  { selector: 'input[placeholder="搜索计划名称、发送人"]:not(:disabled)', trigger: 'input' },
  { selector: 'input[placeholder="按名称、链接、文件名搜索"]', trigger: 'input' },
  { selector: 'input[data-image-library-query]', trigger: 'input' },
  { selector: 'input[data-open-platform-doc-search]', trigger: 'input' },
  { selector: 'input[data-field-mapping-variable-search]', trigger: 'input' },
  { selector: '#group-ops-app input[name="keyword"][data-filter]', trigger: 'keydown' },
  { selector: '.aicrm-group-chat-picker-mask [data-group-picker-search]', trigger: 'input' },
  { selector: '.aicrm-tag-picker [data-role="search"]', trigger: 'input' },
  { selector: '[data-operation-member-picker] [data-operation-member-search]', trigger: 'keydown' },
  { selector: '.aicrm-material-picker-mask [data-picker-search]', trigger: 'keydown' },
];

type SearchState = {
  forwarded: WeakSet<Event>;
  composing: WeakSet<HTMLInputElement>;
  compositionJustEnded: WeakSet<HTMLInputElement>;
  committedQueries: WeakMap<HTMLInputElement, string>;
  installed: boolean;
};

const stateKey = Symbol.for('aicrm.v3.committed-text-search.state');

function state(): SearchState {
  const holder = document as Document & { [stateKey]?: SearchState };
  if (!holder[stateKey]) {
    holder[stateKey] = {
      forwarded: new WeakSet<Event>(),
      composing: new WeakSet<HTMLInputElement>(),
      compositionJustEnded: new WeakSet<HTMLInputElement>(),
      committedQueries: new WeakMap<HTMLInputElement, string>(),
      installed: false,
    };
  }
  return holder[stateKey];
}

function registered(input: HTMLInputElement): RegisteredSearch | undefined {
  return registeredSearches.find(({ selector }) => input.matches(selector));
}

function inputFrom(event: Event): HTMLInputElement | null {
  return event.target instanceof HTMLInputElement ? event.target : null;
}

function preserveSelection(input: HTMLInputElement, selector: string): () => void {
  const start = input.selectionStart;
  const end = input.selectionEnd;
  return () => {
    const current = input.isConnected ? input : document.querySelector<HTMLInputElement>(selector);
    if (!current) return;
    current.focus({ preventScroll: true });
    if (start !== null && end !== null) {
      const length = current.value.length;
      current.setSelectionRange(Math.min(start, length), Math.min(end, length));
    }
  };
}

function dispatchLegacySearch(input: HTMLInputElement, search: RegisteredSearch): void {
  const shared = state();
  const event = search.trigger === 'input'
    ? new Event('input', { bubbles: true })
    : new KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' });
  shared.forwarded.add(event);
  input.dispatchEvent(event);
}

function forward(input: HTMLInputElement, search: RegisteredSearch): void {
  const restoreSelection = preserveSelection(input, search.selector);
  state().committedQueries.set(input, input.value);
  dispatchLegacySearch(input, search);
  restoreSelection();
  queueMicrotask(restoreSelection);
  if (typeof window.requestAnimationFrame === 'function') window.requestAnimationFrame(restoreSelection);
}

function deferCompositionEnd(input: HTMLInputElement): void {
  const shared = state();
  shared.compositionJustEnded.add(input);
  window.setTimeout(() => shared.compositionJustEnded.delete(input), 0);
}

/**
 * Installs the only text-search policy shared by V3 Hosts. The existing
 * frozen handlers remain the authoritative list/fetch implementation; this
 * adapter blocks their per-keystroke invocation and forwards one event only
 * after a deliberate Enter. It is safe to call before or after a donor opens
 * its dynamic picker.
 */
export function installCommittedTextSearch(): void {
  const shared = state();
  if (shared.installed) return;
  shared.installed = true;

  document.addEventListener('compositionstart', (event) => {
    const input = inputFrom(event);
    if (input && registered(input)) state().composing.add(input);
  }, true);

  document.addEventListener('compositionupdate', (event) => {
    const input = inputFrom(event);
    if (input && registered(input)) state().composing.add(input);
  }, true);

  document.addEventListener('compositionend', (event) => {
    const input = inputFrom(event);
    if (!input || !registered(input)) return;
    state().composing.delete(input);
    // Composition completion only settles the draft. A following Enter used
    // to choose an IME candidate must not submit the search as well.
    deferCompositionEnd(input);
  }, true);

  document.addEventListener('input', (event) => {
    if (state().forwarded.has(event)) {
      state().forwarded.delete(event);
      return;
    }
    const input = inputFrom(event);
    if (!input || !registered(input)) return;
    // Keep the browser-owned draft and selection. The frozen handler is
    // intentionally prevented from filtering or fetching on every key.
    event.stopImmediatePropagation();
  }, true);

  document.addEventListener('keydown', (event) => {
    if (state().forwarded.has(event)) {
      state().forwarded.delete(event);
      return;
    }
    const input = inputFrom(event);
    const search = input && registered(input);
    if (!input || !search || event.key !== 'Enter') return;

    if (event.isComposing || event.keyCode === 229 || state().composing.has(input) || state().compositionJustEnded.has(input)) {
      // Do not prevent the browser's IME commit. Stopping propagation alone
      // protects the donor handler from treating the candidate Enter as a
      // business search submission.
      event.stopImmediatePropagation();
      return;
    }

    event.preventDefault();
    event.stopImmediatePropagation();
    forward(input, search);
  }, true);
}

/**
 * Repeats the last explicit query after an unrelated refresh. The refresh
 * button must not accidentally submit a newer IME/text draft merely because
 * the frozen picker reads its input value when it starts a reload.
 */
export function replayCommittedTextSearch(input: HTMLInputElement): void {
  const search = registered(input);
  if (!search) return;
  const shared = state();
  const draft = input.value;
  const selectionStart = input.selectionStart;
  const selectionEnd = input.selectionEnd;
  input.value = shared.committedQueries.get(input) ?? '';
  dispatchLegacySearch(input, search);
  input.value = draft;
  if (selectionStart !== null && selectionEnd !== null) input.setSelectionRange(selectionStart, selectionEnd);
}

/** Mark a programmatic clear/open value as the new committed query. */
export function resetCommittedTextSearch(input: HTMLInputElement): void {
  const search = registered(input);
  if (!search) return;
  state().committedQueries.set(input, input.value);
}
