export {};

// Release-only loader for the byte-frozen dd8 selection components.  It does
// adapt selection data: each page Host remains responsible for scoped reads.
// It supplies the V3 envelope for the shared directory refresh command and
// guarantees dependency order and one evaluation per page.
import { installTagPickerAdapter } from './shared/ui/tagPickerAdapter';
import { installStaffPickerAdapter } from './shared/ui/staffPickerAdapter';
declare global {
  interface Window {
    AICRMStandardComponents?: { ready(): Promise<void> };
    AICRMWeComTagPicker?: unknown;
  }
}

const scripts = [
  '/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948',
  '/assets/standard-components/group_chat_picker.js',
  '/assets/standard-components/material_picker.js',
  '/assets/standard-components/send_content_composer.js',
  '/assets/standard-components/wecom_tag_picker.js',
];

let loading: Promise<void> | undefined;
let tagPickerLocked = false;

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
    }, Promise.resolve()).then(() => { lockOriginalTagPicker(); });
    return loading;
  },
};
// This is a separate V3 API. Keep the byte-frozen tag global available for
// pages that still need it while V3-owned callers adopt the shared session.
installTagPickerAdapter();
// A separate V3 API. It never changes the frozen OperationMemberPicker's
// request scope or refresh command: every migrated Host supplies its own
// authorised read and optional refresh contract.
installStaffPickerAdapter();
void window.AICRMStandardComponents.ready();
