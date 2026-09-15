import { installSelectionDialog, type SelectionDialogController } from './selectionDialog';

export type ConfirmationTone = 'default' | 'danger';

export type ConfirmationReason = {
  label?: string;
  placeholder?: string;
  required?: boolean;
  maxLength?: number;
};

export type ConfirmationDialogOptions = {
  title: string;
  description: string;
  confirmLabel: string;
  tone?: ConfirmationTone;
  reason?: ConfirmationReason;
  readonly?: boolean;
  busy?: boolean;
};

export type ConfirmationDialogResult = {
  confirmed: boolean;
  reason?: string;
};

function text(value: string | undefined, fallback: string): string {
  return typeof value === 'string' && value.trim() ? value : fallback;
}

/**
 * Opens a presentation-only confirmation surface. It has no transport, domain
 * data, persistence, or retry logic: a caller receives one result and remains
 * the sole owner of any authorized command it chooses to run afterwards.
 */
export function openConfirmationDialog(options: ConfirmationDialogOptions): Promise<ConfirmationDialogResult> {
  return new Promise((resolve) => {
    const reason = options.reason;
    const readonly = options.readonly === true;
    const initiallyBusy = options.busy === true;
    const dialog = document.createElement('dialog');
    dialog.className = `v3-confirmation-dialog${options.tone === 'danger' ? ' v3-confirmation-dialog--danger' : ''}`;
    dialog.dataset.v3ConfirmationDialog = '';
    dialog.setAttribute('aria-labelledby', 'v3-confirmation-dialog-title');
    dialog.innerHTML = `
      <section class="v3-confirmation-dialog__panel">
        <header class="v3-confirmation-dialog__head">
          <h2 id="v3-confirmation-dialog-title"></h2>
        </header>
        <div class="v3-confirmation-dialog__body">
          <p class="v3-confirmation-dialog__description"></p>
          ${reason ? `<label class="v3-confirmation-dialog__reason"><span></span><textarea rows="3"></textarea></label>` : ''}
          <p class="v3-confirmation-dialog__status" data-v3-confirmation-status role="alert" hidden></p>
        </div>
        <footer class="v3-confirmation-dialog__actions">
          <button class="v3-confirmation-dialog__cancel" type="button" data-v3-confirmation-cancel>取消</button>
          <button class="v3-confirmation-dialog__confirm" type="button" data-v3-confirmation-confirm></button>
        </footer>
      </section>`;

    const title = dialog.querySelector<HTMLHeadingElement>('#v3-confirmation-dialog-title')!;
    const description = dialog.querySelector<HTMLParagraphElement>('.v3-confirmation-dialog__description')!;
    const reasonField = dialog.querySelector<HTMLTextAreaElement>('textarea');
    const reasonLabel = dialog.querySelector<HTMLElement>('.v3-confirmation-dialog__reason > span');
    const status = dialog.querySelector<HTMLElement>('[data-v3-confirmation-status]')!;
    const cancel = dialog.querySelector<HTMLButtonElement>('[data-v3-confirmation-cancel]')!;
    const confirm = dialog.querySelector<HTMLButtonElement>('[data-v3-confirmation-confirm]')!;
    title.textContent = options.title;
    description.textContent = options.description;
    confirm.textContent = options.confirmLabel;
    if (reasonField && reasonLabel) {
      reasonLabel.textContent = text(reason?.label, reason?.required ? '请说明原因（必填）' : '补充原因（可选）');
      reasonField.placeholder = text(reason?.placeholder, '请输入原因');
      reasonField.maxLength = reason?.maxLength ?? 280;
      reasonField.readOnly = readonly;
      reasonField.disabled = initiallyBusy;
    }
    confirm.disabled = readonly || initiallyBusy;
    if (readonly) status.textContent = '当前状态为只读，不能确认此操作。';
    if (initiallyBusy) status.textContent = '操作正在处理中，请等待结果。';
    status.hidden = !readonly && !initiallyBusy;

    let settled = false;
    let controller: SelectionDialogController | undefined;
    const finish = (confirmed: boolean) => {
      if (settled) return;
      settled = true;
      controller?.dispose();
      if (dialog.open) dialog.close();
      dialog.remove();
      const value = reasonField?.value.trim();
      resolve(confirmed ? { confirmed: true, ...(value ? { reason: value } : {}) } : { confirmed: false });
    };
    const validate = (): boolean => {
      if (!reason?.required || reasonField?.value.trim()) return true;
      status.textContent = '请填写确认原因后再继续。';
      status.hidden = false;
      reasonField?.setAttribute('aria-invalid', 'true');
      reasonField?.focus({ preventScroll: true });
      return false;
    };
    const confirmOnce = () => {
      if (settled || readonly || initiallyBusy || !validate()) return;
      confirm.disabled = true;
      finish(true);
    };
    cancel.addEventListener('click', () => finish(false));
    confirm.addEventListener('click', confirmOnce);
    reasonField?.addEventListener('input', () => {
      reasonField.removeAttribute('aria-invalid');
      if (!status.hidden && !readonly && !initiallyBusy) status.hidden = true;
    });
    dialog.addEventListener('cancel', (event) => {
      event.preventDefault();
      finish(false);
    });

    document.body.append(dialog);
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    controller = installSelectionDialog({
      dialog,
      initialFocus: reasonField && !readonly && !initiallyBusy ? reasonField : cancel,
      close: () => finish(false),
      submit: confirmOnce,
    });
  });
}
