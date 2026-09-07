// V3 owns this narrow bridge between the frozen questionnaire operations page
// and Composition's protected target whitelist. The donor page still renders
// the surrounding controls; this Host replaces only its free-form reference
// field with an opaque-reference selector.
type CatalogResponse = {
  available_configuration_references?: unknown;
  target_catalog_available?: unknown;
};

const opaqueReference = /^[A-Za-z0-9._:-]{1,128}$/;

function questionnaireID(): number | undefined {
  if (!location.pathname.endsWith('/admin/questionnaireOps.html')) return undefined;
  const raw = new URLSearchParams(location.search).get('id') || '';
  if (!/^[1-9][0-9]*$/.test(raw)) return undefined;
  const id = Number(raw);
  return Number.isSafeInteger(id) ? id : undefined;
}

function targetReferences(value: CatalogResponse): string[] {
  if (value.target_catalog_available !== true || !Array.isArray(value.available_configuration_references)) return [];
  return [...new Set(value.available_configuration_references.filter((item): item is string => typeof item === 'string' && opaqueReference.test(item)))].sort();
}

function option(document: Document, value: string, label: string, disabled = false): HTMLOptionElement {
  const item = document.createElement('option');
  item.value = value;
  item.textContent = label;
  item.disabled = disabled;
  return item;
}

function mountTargetSelector(references: readonly string[]): boolean {
  const field = document.querySelector<HTMLInputElement | HTMLSelectElement>('#opsConfigurationReference');
  if (!field) return false;
  const selected = field.value.trim();
  const catalogVersion = references.join('\n');
  const existingSelect = field instanceof HTMLSelectElement;
  if (existingSelect && field.dataset.surveyTargetCatalog === catalogVersion) return true;
  const select: HTMLSelectElement = existingSelect ? field : document.createElement('select');
  select.replaceChildren();
  select.dataset.surveyTargetCatalog = catalogVersion;
  select.id = field.id;
  select.name = field.name;
  select.style.cssText = field.style.cssText;
  select.setAttribute('aria-label', '推送配置引用');
  select.appendChild(option(document, '', references.length ? '请选择已部署的推送目标' : '正在读取已部署的推送目标', true));
  for (const reference of references) select.appendChild(option(document, reference, reference));
  if (selected && !references.includes(selected)) select.appendChild(option(document, selected, '当前绑定的目标已不可用，请重新选择', true));
  select.value = selected;
  select.disabled = !references.length;
  if (existingSelect) return true;
  field.replaceWith(select);
  return true;
}

async function loadAndInstall(): Promise<void> {
  const id = questionnaireID();
  if (!id) return;
  let references: string[] = [];
  let observer: MutationObserver | undefined;
  const install = () => {
    // jsdom and browser teardown can deliver a final queued mutation after
    // their document global has gone away. It is not a page rerender and must
    // not turn into an asynchronous uncaught exception.
    if (!globalThis.document || !globalThis.document.documentElement) {
      observer?.disconnect();
      observer = undefined;
      return;
    }
    // The frozen controller replaces this field whenever it rerenders after a
    // toggle or save. Keep the bridge for the document lifetime so a later
    // free-form input never leaks back into the enabled panel.
    void mountTargetSelector(references);
  };
  observer = new MutationObserver(install);
  observer.observe(document, { childList: true, subtree: true });
  // The frozen toggle renders during its target click handler. Observing the
  // mutation remains the primary path; this event-bound rescan covers that
  // handler's replacement boundary after its bubble phase without polling or
  // changing the frozen page's state machine.
  const afterClick = () => window.setTimeout(install, 0);
  document.addEventListener('click', afterClick, true);
  window.addEventListener('pagehide', () => {
    observer?.disconnect();
    observer = undefined;
    document.removeEventListener('click', afterClick, true);
  }, { once: true });
  install();
  const response = await fetch(`/api/admin/questionnaires/${id}/operations`, {
    method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' },
  });
  if (!response.ok) return;
  const payload = await response.json() as CatalogResponse;
  references = targetReferences(payload);
  install();
}

void loadAndInstall().catch(() => undefined);

// Install the selector bridge before the byte-frozen page renders and reads
// the current operations response.
// @ts-expect-error The donor entry is a side-effect-only script.
void import('../src/admin/main');
