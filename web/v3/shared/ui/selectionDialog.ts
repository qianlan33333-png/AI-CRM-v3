// Shared V3 dialog mechanics for selection sessions. This stays deliberately
// presentation-only: callers retain their own authorised loaders and commits.

export type SelectionDialogOptions = {
  dialog: HTMLElement;
  search: HTMLInputElement;
  close(): void;
  submit(): void;
  onKeyDown?(event: KeyboardEvent): void;
};

export type SelectionDialogController = {
  dispose(): void;
};

export function focusableElements(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>('button:not([disabled]),input:not([disabled]),[href],[tabindex]:not([tabindex="-1"])'))
    .filter((node) => {
      if (node.tabIndex < 0) return false;
      for (let current: HTMLElement | null = node; current && root.contains(current); current = current.parentElement) {
        const style = window.getComputedStyle(current);
        if (current.hidden || style.display === 'none' || style.visibility === 'hidden') return false;
      }
      return true;
    });
}

/** Preserve a row focus target across an innerHTML refresh. */
export function focusedSelectionKey(root: HTMLElement, selector: string, attribute: string): string | undefined {
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
  if (!focused || !root.contains(focused)) return undefined;
  return focused.closest<HTMLElement>(selector)?.dataset[attribute];
}

export function restoreSelectionFocus(root: HTMLElement, selector: string, attribute: string, key: string | undefined): void {
  if (!key) return;
  Array.from(root.querySelectorAll<HTMLElement>(selector)).find((node) => node.dataset[attribute] === key)?.focus({ preventScroll: true });
}

/**
 * Installs one IME-safe search and focus contract shared by V3 selectors.
 * Composition-end keeps the immediately adjacent Enter in the native IME
 * path; only an explicit non-IME Enter submits the caller-owned loader.
 */
export function installSelectionDialog(options: SelectionDialogOptions): SelectionDialogController {
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
  let composing = false;
  let compositionJustEnded = false;

  const onCompositionStart = () => { composing = true; };
  const onCompositionUpdate = () => { composing = true; };
  const onCompositionEnd = () => {
    composing = false;
    compositionJustEnded = true;
    window.setTimeout(() => { compositionJustEnded = false; }, 0);
  };
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') {
      // Escape belongs to an active IME candidate session. Keep the dialog
      // open and let that native session consume it; plain Escape still exits.
      if (event.isComposing || event.keyCode === 229 || composing) {
        event.stopPropagation();
        return;
      }
      event.preventDefault();
      options.close();
      return;
    }
    if (event.key === 'Tab') {
      const nodes = focusableElements(options.dialog);
      const first = nodes[0];
      const last = nodes.length ? nodes[nodes.length - 1] : undefined;
      if (first && last && (event.shiftKey ? document.activeElement === first : document.activeElement === last)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      }
      return;
    }
    if (event.target === options.search && event.key === 'Enter') {
      if (event.isComposing || event.keyCode === 229 || composing || compositionJustEnded) {
        event.stopPropagation();
        return;
      }
      event.preventDefault();
      options.submit();
      return;
    }
    options.onKeyDown?.(event);
  };

  options.search.addEventListener('compositionstart', onCompositionStart);
  options.search.addEventListener('compositionupdate', onCompositionUpdate);
  options.search.addEventListener('compositionend', onCompositionEnd);
  options.dialog.addEventListener('keydown', onKeyDown);
  window.setTimeout(() => { if (options.search.isConnected) options.search.focus({ preventScroll: true }); }, 0);

  return {
    dispose() {
      options.search.removeEventListener('compositionstart', onCompositionStart);
      options.search.removeEventListener('compositionupdate', onCompositionUpdate);
      options.search.removeEventListener('compositionend', onCompositionEnd);
      options.dialog.removeEventListener('keydown', onKeyDown);
      if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true });
    },
  };
}
