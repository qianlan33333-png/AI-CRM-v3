import { openGroupPicker, type GroupPickerRecord } from './shared/ui/groupPickerAdapter';
import { installMaterialPickerAdapter, type MaterialPickerRecord } from './shared/ui/materialPickerAdapter';
import { installSelectionDialog } from './shared/ui/selectionDialog';
import { SelectionSession, selectionKey, type SelectionItem, type SelectionLoader } from './shared/ui/selectionSession';

type MaterialPicker = { open(options?: Record<string, unknown>): unknown };
type ComponentStatesWindow = Window & { AICRMMaterialPicker?: MaterialPicker };
type DemoChoice = { id: string; label: string };
type DemoMode = 'ready' | 'loading' | 'empty' | 'error' | 'forbidden' | 'readonly' | 'invalid';

const groupRecords: GroupPickerRecord[] = [
  { chat_reference: 'demo-chat-north', display_name: '北区新品体验群', owner_staff_id: 101, member_count: 128 },
  { chat_reference: 'demo-chat-growth', display_name: '增长社群', owner_staff_id: 101, member_count: 86 },
  { chat_reference: 'demo-chat-readonly', display_name: '归档群（不可编辑）', owner_staff_id: 101, member_count: 41, unavailable_reason: '示例：该群当前不可编辑' },
];

const materialRecords: MaterialPickerRecord[] = [
  { library_id: 101, type: 'image', title: '秋日活动封面', subtitle: 'PNG · 明确示例数据', thumbnail_url: '', enabled: true, selectable: true },
  { library_id: 102, type: 'image', title: '权益说明配图', subtitle: 'PNG · 明确示例数据', thumbnail_url: '', enabled: true, selectable: true },
  { library_id: 103, type: 'image', title: '已归档素材', subtitle: '不可选择的示例状态', thumbnail_url: '', enabled: false, selectable: false, unavailable_reason: '示例：素材已归档' },
];

const demoChoices: DemoChoice[] = [
  { id: 'review', label: '审核说明' },
  { id: 'handoff', label: '交接备注' },
  { id: 'follow-up', label: '跟进计划' },
];

function escape(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character] || character);
}

function matching<T>(items: T[], query: string, label: (item: T) => string): T[] {
  const needle = query.trim().toLocaleLowerCase();
  return needle ? items.filter((item) => label(item).toLocaleLowerCase().includes(needle)) : items;
}

function selectedLabels<T>(items: T[], label: (item: T) => string): string {
  return items.length ? items.map(label).join('、') : '尚未选择（示例）';
}

function stateCard(name: string, state: string, detail: string, extra = ''): string {
  const label = state === 'error' ? '错误' : state === 'empty' ? '空数据' : state === 'forbidden' ? '无权限' : state === 'readonly' ? '只读' : state === 'invalid' ? '选择无效' : state === 'ime' ? '输入法候选' : '加载中';
  return `<article class="component-states__card" data-state="${state}"><span class="component-states__status-label component-states__status-label--${state}">${extra}${label}</span><h4>${name}</h4><p>${detail}</p></article>`;
}

function modeLabel(mode: DemoMode): string {
  return { ready: '正常目录', loading: '加载中', empty: '空数据', error: '失败可重试', forbidden: '403 只读', readonly: '显式只读', invalid: '失效初选' }[mode];
}

function pageMarkup(groupSelected: GroupPickerRecord[], materialSelected: MaterialPickerRecord[], mode: DemoMode): string {
  return `<section class="component-states" aria-label="共享组件状态示例">
    <header class="component-states__intro">
      <div><h2>共享组件状态示例</h2><p>本页只使用明确的本地示例数据。不会发送、保存，也不会调用 Provider。</p></div>
      <span class="component-states__demo-badge">仅供后台组件验收</span>
    </header>
    <section class="component-states__panel" aria-labelledby="component-states-catalog-title">
      <div class="component-states__panel-head"><h3 id="component-states-catalog-title">可观察状态</h3><p>各状态均独立说明，不能由加载或空数据推断业务成功。</p></div>
      <div class="component-states__state-grid">
        ${stateCard('目录读取', 'loading', '读取中的结果不会覆盖已确认选择。', '<span class="component-states__spinner" aria-hidden="true"></span>')}
        ${stateCard('无匹配结果', 'empty', '关键词没有匹配项时保留查询入口和重新读取动作。')}
        ${stateCard('目录暂不可用', 'error', '首读失败后可在同一弹窗刷新；组件不把错误写成成功。')}
        ${stateCard('访问范围变化', 'forbidden', '403 后保留草稿并锁定编辑，不静默移除既有选择。')}
        ${stateCard('历史只读', 'readonly', '只读保留已选记录和不可编辑原因。')}
        ${stateCard('已选记录失效', 'invalid', '目录回显前不会假定记录可用；失效原因单独显示。')}
        ${stateCard('中文输入法', 'ime', '候选词确认的 Enter 留给输入法；普通 Enter 才提交查询。')}
      </div>
    </section>
    <section class="component-states__panel" aria-labelledby="component-states-live-title">
      <div class="component-states__panel-head"><h3 id="component-states-live-title">真实共享会话</h3><p>按钮调用 V3 SelectionSession 与 dialog；错误模式首读失败，点击“刷新”后使用第二次本地成功结果。</p></div>
      <div class="component-states__actions" aria-label="本地示例状态开关">
        ${(['ready', 'loading', 'empty', 'error', 'forbidden', 'readonly', 'invalid'] as DemoMode[]).map((name) => `<button class="component-states__button" type="button" data-component-states-mode="${name}" aria-pressed="${mode === name}">${modeLabel(name)}</button>`).join('')}
      </div>
      <p class="component-states__panel-note" data-component-states-mode-note>当前模式：${modeLabel(mode)}。此模式会由两个真实 picker 的本地 loader 产生。</p>
      <div class="component-states__actions">
        <button class="component-states__button component-states__button--primary" type="button" data-component-states-group-open>打开群聊示例</button>
        <button class="component-states__button component-states__button--primary" type="button" data-component-states-material-open>打开素材示例</button>
        <button class="component-states__button" type="button" data-component-states-form-open>打开表单焦点示例</button>
      </div>
      <div class="component-states__selection-summary" aria-live="polite">
        <div class="component-states__selection-value"><strong>群聊示例的当前选择</strong><span data-component-states-group-result>${escape(selectedLabels(groupSelected, (item) => item.display_name))}</span></div>
        <div class="component-states__selection-value"><strong>素材示例的当前选择</strong><span data-component-states-material-result>${escape(selectedLabels(materialSelected, (item) => String(item.title || item.library_id)))}</span></div>
      </div>
    </section>
  </section>`;
}

function demoFailure(message: string, status?: number): Error & { status?: number } {
  const error = new Error(message) as Error & { status?: number };
  error.status = status;
  return error;
}

function waitForAbort(signal: AbortSignal): Promise<never> {
  return new Promise((_resolve, reject) => {
    if (signal.aborted) { reject(new DOMException('示例读取已取消', 'AbortError')); return; }
    signal.addEventListener('abort', () => reject(new DOMException('示例读取已取消', 'AbortError')), { once: true });
  });
}

function item(choice: DemoChoice): SelectionItem<DemoChoice> {
  return { kind: 'component-state.choice', source: 'component-states-demo', id: choice.id, label: choice.label, value: choice };
}

function openFormSession(): void {
  const mask = document.createElement('div');
  mask.className = 'component-states-mask';
  mask.dataset.v3SelectionSession = 'component-states';
  mask.innerHTML = `<section class="component-states-dialog" role="dialog" aria-modal="true" aria-labelledby="component-states-dialog-title">
    <header class="component-states-dialog__head"><h3 id="component-states-dialog-title">表单焦点与输入法示例</h3><button class="component-states__button" type="button" data-component-states-close>取消</button></header>
    <div class="component-states-dialog__body">
      <label class="component-states-dialog__field">搜索示例<input data-v3-picker-search-input data-component-states-ime-input aria-label="搜索示例" placeholder="输入关键词后按 Enter"></label>
      <label class="component-states-dialog__field">选择类型<select data-component-states-form-select><option>普通说明</option><option>需复核</option></select></label>
      <label class="component-states-dialog__field">备注<textarea data-component-states-form-textarea>此文本不会保存。</textarea></label>
      <label class="component-states-dialog__field">可编辑说明<div contenteditable="true" tabindex="0" data-component-states-form-editable>此区域验证 dialog 中的真实焦点顺序。</div></label>
      <p class="component-states-dialog__status" data-component-states-form-status role="status">正在准备本地示例目录…</p>
      <div class="component-states-dialog__list" data-component-states-form-list></div>
    </div>
    <footer class="component-states-dialog__foot"><button class="component-states__button" type="button" data-component-states-close>取消</button><button class="component-states__button component-states__button--primary" type="button" data-component-states-confirm>确认示例选择</button></footer>
  </section>`;
  document.body.append(mask);

  const dialog = mask.querySelector<HTMLElement>('.component-states-dialog')!;
  const search = mask.querySelector<HTMLInputElement>('[data-v3-picker-search-input]')!;
  const list = mask.querySelector<HTMLElement>('[data-component-states-form-list]')!;
  const status = mask.querySelector<HTMLElement>('[data-component-states-form-status]')!;
  const session = new SelectionSession<DemoChoice>([], { mode: 'multiple', limit: 2 });
  const loader: SelectionLoader<DemoChoice> = async ({ query, signal }) => {
    if (signal.aborted) throw new DOMException('示例目录读取已替换', 'AbortError');
    return { items: matching(demoChoices, query, (choice) => choice.label).map(item) };
  };
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    unsubscribe();
    session.cancel();
    session.dispose();
    control.dispose();
    mask.remove();
  };
  const render = () => {
    if (closed) return;
    const snapshot = session.snapshot();
    if (document.activeElement !== search) search.value = snapshot.query.draft;
    status.textContent = snapshot.error || snapshot.notice || (snapshot.loading ? '正在读取本地示例目录…' : `已暂选 ${snapshot.draft.length} 项；确认不会保存。`);
    list.innerHTML = snapshot.items.map((choice) => {
      const key = selectionKey(choice.kind, choice.source, choice.id);
      const selected = session.isDraftSelected(key);
      return `<button class="component-states__button component-states-dialog__item${selected ? ' component-states__button--primary' : ''}" type="button" data-component-states-choice="${escape(key)}" aria-pressed="${selected}">${escape(choice.label)}</button>`;
    }).join('') || '<p class="component-states-dialog__status">没有匹配的本地示例。</p>';
  };
  const unsubscribe = session.subscribe(render);
  const submit = () => { session.setDraftQuery(search.value); void session.submitSearch(loader); };
  const control = installSelectionDialog({ dialog, search, close, submit });
  mask.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    if (target === mask || target.closest('[data-component-states-close]')) { close(); return; }
    const choice = target.closest<HTMLElement>('[data-component-states-choice]');
    if (choice) { session.toggle(choice.dataset.componentStatesChoice || ''); return; }
    if (target.closest('[data-component-states-confirm]')) {
      const current = session.previewCommit().selected.map((selected) => selected.label).join('、') || '尚未选择';
      session.commit();
      status.textContent = `已提交本地示例会话：${current}。没有保存或外部调用。`;
    }
  });
  search.addEventListener('input', () => session.setDraftQuery(search.value, { silent: true }));
  void session.submitSearch(loader);
}

export function mountComponentStates(root: HTMLElement): void {
  const view = window as ComponentStatesWindow;
  let groups: GroupPickerRecord[] = [];
  let materials: MaterialPickerRecord[] = [];
  let mode: DemoMode = 'ready';
  let installed = false;
  let groupErrorDelivered = false;
  let materialErrorDelivered = false;

  const render = () => { root.innerHTML = pageMarkup(groups, materials, mode); };
  const updateSummary = () => {
    const groupResult = root.querySelector<HTMLElement>('[data-component-states-group-result]');
    const materialResult = root.querySelector<HTMLElement>('[data-component-states-material-result]');
    if (groupResult) groupResult.textContent = selectedLabels(groups, (record) => record.display_name);
    if (materialResult) materialResult.textContent = selectedLabels(materials, (record) => String(record.title || record.library_id));
  };
  const ensureMaterialDemo = () => {
    if (installed) return;
    view.AICRMMaterialPicker = { open() { return undefined; } };
    installMaterialPickerAdapter({
      source: 'component-states-demo',
      scope: 'local-demo-only',
      loadPage: async ({ query, signal }) => {
        if (mode === 'loading') return waitForAbort(signal);
        if (mode === 'error' && !materialErrorDelivered) {
          materialErrorDelivered = true;
          throw demoFailure('示例：素材目录暂时不可用。点击“刷新当前结果”使用第二次本地成功结果。');
        }
        if (mode === 'forbidden') throw demoFailure('示例：当前账号失去素材目录范围。', 403);
        if (signal.aborted) throw new DOMException('示例素材读取已替换', 'AbortError');
        return { items: mode === 'empty' ? [] : matching(materialRecords, query, (record) => `${record.title || ''} ${record.subtitle || ''}`) };
      },
      accessLossMessage: (error) => (error as { status?: number }).status === 403 ? '示例：目录权限已收回，保留草稿但不能继续编辑。' : undefined,
    });
    installed = true;
  };

  const openGroups = () => {
    const initialGroups = mode === 'invalid'
      ? [{ chat_reference: 'demo-chat-missing', display_name: '等待目录回显的失效初选', unavailable_reason: '示例：群聊已不可管理' }]
      : mode === 'forbidden' || mode === 'readonly'
        ? [groupRecords[0]]
        : groups;
    openGroupPicker({
      source: 'component-states-demo',
      scope: 'local-demo-only',
      selectedRecords: initialGroups,
      limit: 2,
      loadPage: async ({ query, signal }) => {
        if (mode === 'loading') return waitForAbort(signal);
        if (mode === 'error' && !groupErrorDelivered) {
          groupErrorDelivered = true;
          throw demoFailure('示例：群聊目录暂时不可用。点击“刷新”使用第二次本地成功结果。');
        }
        if (mode === 'forbidden') throw demoFailure('示例：当前账号失去群聊目录范围。', 403);
        if (signal.aborted) throw new DOMException('示例群聊读取已替换', 'AbortError');
        return { items: mode === 'empty' ? [] : matching(groupRecords, query, (record) => `${record.display_name} ${record.chat_reference}`) };
      },
      readonly: mode === 'readonly',
      readonlyReason: mode === 'readonly' ? '示例：当前记录处于只读状态。' : undefined,
      accessLossMessage: (error) => (error as { status?: number }).status === 403 ? '示例：目录权限已收回，保留草稿但不能继续编辑。' : undefined,
      onCommit: async ({ selected }) => { groups = selected; updateSummary(); },
    });
  };
  const openMaterials = () => {
    ensureMaterialDemo();
    const initialMaterials = mode === 'invalid'
      ? [{ library_id: 999, type: 'image', title: '待目录确认的失效初选', unavailable_reason: '示例：该素材当前不可用' }]
      : mode === 'forbidden' || mode === 'readonly'
        ? [materialRecords[0]]
        : materials;
    view.AICRMMaterialPicker?.open({
      type: 'image',
      title: '选择示例素材',
      selectedIds: initialMaterials.map((record) => record.library_id),
      selectedRecords: initialMaterials,
      limit: 2,
      readonly: mode === 'readonly',
      onCommit: async ({ selected }: { selected: MaterialPickerRecord[] }) => { materials = selected; updateSummary(); },
    });
  };

  root.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    const modeControl = target.closest<HTMLElement>('[data-component-states-mode]');
    if (modeControl) {
      const next = modeControl.dataset.componentStatesMode;
      if (next === 'ready' || next === 'loading' || next === 'empty' || next === 'error' || next === 'forbidden' || next === 'readonly' || next === 'invalid') {
        mode = next;
        groupErrorDelivered = false;
        materialErrorDelivered = false;
        render();
      }
      return;
    }
    if (target.closest('[data-component-states-group-open]')) { openGroups(); return; }
    if (target.closest('[data-component-states-material-open]')) { openMaterials(); return; }
    if (target.closest('[data-component-states-form-open]')) openFormSession();
  });
  render();
  root.dataset.componentStatesReady = 'true';
}

if (typeof document !== 'undefined') {
  const root = document.querySelector<HTMLElement>('[data-component-states-root]');
  if (root) mountComponentStates(root);
}
