import { mountPageHeaderActionElements } from './shared/ui/pageHeaderActions';

function byText<T extends HTMLElement>(root: ParentNode, selector: string, label: string): T | undefined {
  return Array.from(root.querySelectorAll<T>(selector)).find((element) => element.textContent?.trim() === label);
}

type Mounted = { owner: string; elements: readonly HTMLElement[] };

function sameElements(previous: Mounted | undefined, owner: string, next: readonly HTMLElement[]): boolean {
  return previous?.owner === owner && previous.elements.length === next.length && previous.elements.every((element, index) => element === next[index]);
}

function mountTagActions(previous: Mounted | undefined): Mounted | undefined {
  const stage = document.querySelector<HTMLElement>('#stage');
  if (!stage || !document.body.matches('[data-page="tags"]')) return undefined;
  const sync = byText<HTMLButtonElement>(stage, 'button', '同步企微标签');
  const createGroup = byText<HTMLButtonElement>(stage, 'button', '新增标签组');
  const createTag = byText<HTMLButtonElement>(stage, 'button', '新增标签');
  if (!sync || !createGroup || !createTag) return undefined;
  const elements = [sync, createGroup, createTag];
  if (!sameElements(previous, 'wecom-tags', elements)) mountPageHeaderActionElements('wecom-tags', elements);
  // The frozen document owns this former local title row. The V3 shell now
  // supplies the one visible page title, so hide the duplicated visual row
  // without changing donor markup or its controller.
  const localTitle = Array.from(stage.children).find((element) => element.textContent?.includes('企微标签管理'));
  if (localTitle instanceof HTMLElement) localTitle.hidden = true;
  return { owner: 'wecom-tags', elements };
}

function mountPlanDetailActions(previous: Mounted | undefined): Mounted | undefined {
  if (!document.body.matches('[data-page="ai-assistant"]')) return undefined;
  const root = document.querySelector<HTMLElement>('[data-cloud-plan-root][data-page-mode="detail"]');
  const approve = root?.querySelector<HTMLButtonElement>('[data-plan-approve]');
  const reject = root?.querySelector<HTMLButtonElement>('[data-plan-reject]');
  const back = root?.querySelector<HTMLAnchorElement>('a[href="/admin/cloud-orchestrator/plans"]');
  if (!root || !approve || !reject || !back) return undefined;
  const source = approve.closest<HTMLElement>('.cloud-plan-actions');
  const elements = [back, reject, approve];
  if (!sameElements(previous, 'ai-plan-detail', elements)) mountPageHeaderActionElements('ai-plan-detail', elements);
  if (source) source.hidden = true;
  return { owner: 'ai-plan-detail', elements };
}

function mountWhenReady(): void {
  let mounted: Mounted | undefined;
  let scheduled = false;
  let observer: MutationObserver | undefined;
  const mount = () => {
    scheduled = false;
    const next = mountTagActions(mounted) || mountPlanDetailActions(mounted);
    // A donor redraw replaces these controls. Mount only a newly rendered set
    // of original nodes; unrelated DOM changes must not restore/reinsert a
    // focused header action or replace its domain-owned busy state.
    if (next) mounted = next;
  };
  const queueMount = () => {
    if (typeof document === 'undefined' || !document.documentElement) {
      observer?.disconnect();
      return;
    }
    if (scheduled) return;
    scheduled = true;
    queueMicrotask(mount);
  };
  observer = new MutationObserver(queueMount);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  mount();
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', mountWhenReady, { once: true });
else mountWhenReady();
