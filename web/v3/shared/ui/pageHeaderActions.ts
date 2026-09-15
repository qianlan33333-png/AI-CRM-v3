// V3-owned client actions for the existing admin topbar. Server PageAction is
// deliberately link-only; this helper gives a page a reusable mount point for
// already-authorized client commands without creating a second page header.

export type PageHeaderAction = {
  label: string;
  variant?: 'primary' | 'secondary' | 'ghost';
  disabled?: boolean;
  onClick?: () => void | Promise<void>;
  onError?: (error: unknown) => void;
  href?: string;
  target?: string;
};

function buttonClass(variant: PageHeaderAction['variant']): string {
  return `admin-button${variant ? ` admin-button--${variant}` : ''}`;
}

function report(action: PageHeaderAction, error: unknown): void {
  try { action.onError?.(error); } catch { /* a page-level feedback hook cannot break the topbar */ }
}

function actionElement(action: PageHeaderAction): HTMLElement {
  if (action.href) {
    const link = document.createElement('a');
    link.href = action.href;
    link.className = buttonClass(action.variant);
    if (action.target) {
      link.target = action.target;
      if (action.target === '_blank') link.rel = 'noopener';
    }
    link.textContent = action.label;
    return link;
  }
  const control = document.createElement('button');
  control.type = 'button';
  control.className = buttonClass(action.variant);
  control.textContent = action.label;
  control.disabled = action.disabled === true;
  if (control.disabled) control.setAttribute('aria-disabled', 'true');
  const reset = () => {
    if (!control.isConnected) return;
    control.disabled = false;
    control.removeAttribute('aria-busy');
    control.removeAttribute('aria-disabled');
  };
  control.addEventListener('click', () => {
    if (control.disabled || !action.onClick) return;
    // Set busy before invoking page code so a synchronous reentrant click
    // cannot issue the same authorized command twice.
    control.disabled = true;
    control.setAttribute('aria-busy', 'true');
    let pending: void | Promise<void>;
    try {
      pending = action.onClick();
    } catch (error) {
      report(action, error);
      reset();
      return;
    }
    if (!pending || typeof (pending as Promise<void>).then !== 'function') {
      reset();
      return;
    }
    void Promise.resolve(pending).then(
      () => reset(),
      (error) => { report(action, error); reset(); },
    );
  });
  return control;
}

/**
 * Mounts page-owned actions in the one existing `admin-topbar`. Repeated calls
 * replace only this owner’s actions, leaving SSR tabs and link actions intact.
 * It returns a cleanup for hosts which unmount inside a still-live document.
 */
export function mountPageHeaderActions(owner: string, actions: readonly PageHeaderAction[]): () => void {
  const topbar = document.querySelector<HTMLElement>('.admin-topbar');
  if (!topbar) return () => {};
  let meta = topbar.querySelector<HTMLElement>(':scope > .admin-topbar-meta');
  if (!meta) {
    meta = document.createElement('div');
    meta.className = 'admin-topbar-meta';
    meta.dataset.pageHeaderActionsMeta = 'created';
    topbar.append(meta);
  }
  let host = Array.from(meta.querySelectorAll<HTMLElement>(':scope > [data-page-header-actions]'))
    .find((candidate) => candidate.dataset.pageHeaderActions === owner);
  if (!host) {
    host = document.createElement('div');
    host.className = 'page-header-actions';
    host.dataset.pageHeaderActions = owner;
    meta.append(host);
  }
  const revision = crypto.randomUUID();
  host.dataset.pageHeaderActionsRevision = revision;
  host.replaceChildren(...actions.map(actionElement));
  return () => {
    if (host?.dataset.pageHeaderActionsRevision !== revision) return;
    host.remove();
    // Remove only a container created by this helper and only when no other
    // page actions, SSR tabs or link actions occupy it.
    if (meta?.dataset.pageHeaderActionsMeta === 'created' && meta.childElementCount === 0) meta.remove();
  };
}
