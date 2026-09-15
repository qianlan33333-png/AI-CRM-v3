// V3-owned client actions for the existing admin topbar. Server PageAction is
// deliberately link-only; this helper gives a page a reusable mount point for
// already-authorized client commands without creating a second page header.

export type PageHeaderAction = {
  // Use a stable id when a mounted button's disabled state changes over time.
  id?: string;
  label: string;
  variant?: 'primary' | 'secondary' | 'ghost';
  disabled?: boolean;
  onClick?: () => void | Promise<void>;
  onError?: (error: unknown) => void;
  href?: string;
  target?: string;
};

function actionID(action: PageHeaderAction): string { return action.id || action.label; }

function buttonClass(variant: PageHeaderAction['variant']): string {
  return `admin-button${variant ? ` admin-button--${variant}` : ''}`;
}

function applyDisabled(control: HTMLButtonElement): void {
  const businessDisabled = control.dataset.pageHeaderActionBusinessDisabled === 'true';
  const busy = control.dataset.pageHeaderActionBusy === 'true';
  control.disabled = businessDisabled || busy;
  if (control.disabled) control.setAttribute('aria-disabled', 'true');
  else control.removeAttribute('aria-disabled');
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
    link.dataset.pageHeaderAction = actionID(action);
    return link;
  }
  const control = document.createElement('button');
  control.type = 'button';
  control.className = buttonClass(action.variant);
  control.textContent = action.label;
  control.dataset.pageHeaderAction = actionID(action);
  control.dataset.pageHeaderActionBusinessDisabled = String(action.disabled === true);
  control.dataset.pageHeaderActionBusy = 'false';
  applyDisabled(control);
  const reset = () => {
    if (!control.isConnected) return;
    control.dataset.pageHeaderActionBusy = 'false';
    control.removeAttribute('aria-busy');
    applyDisabled(control);
  };
  control.addEventListener('click', () => {
    if (control.disabled || !action.onClick) return;
    // Set busy before invoking page code so a synchronous reentrant click
    // cannot issue the same authorized command twice.
    control.dataset.pageHeaderActionBusy = 'true';
    applyDisabled(control);
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
function actionHost(owner: string): HTMLElement | undefined {
  const topbar = document.querySelector<HTMLElement>('.admin-topbar');
  const meta = topbar?.querySelector<HTMLElement>(':scope > .admin-topbar-meta');
  return Array.from(meta?.querySelectorAll<HTMLElement>(':scope > [data-page-header-actions]') || [])
    .find((candidate) => candidate.dataset.pageHeaderActions === owner);
}

/** Updates only an already-mounted button's disabled state without replacing
 * the control, preserving its DOM identity and surrounding header focus. */
export function setPageHeaderActionDisabled(owner: string, action: string, disabled: boolean): boolean {
  const control = Array.from(actionHost(owner)?.querySelectorAll<HTMLElement>(':scope > [data-page-header-action]') || [])
    .find((candidate) => candidate.dataset.pageHeaderAction === action);
  if (!(control instanceof HTMLButtonElement)) return false;
  control.dataset.pageHeaderActionBusinessDisabled = String(disabled);
  applyDisabled(control);
  return true;
}

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
