/*
 * V3 host binding for the frozen Agent editor. The donor template renders
 * readonly="false" for a new code, which HTML correctly treats as readonly.
 * Supplying a valid code before the unchanged donor save handler runs retains
 * the donor DOM, controller, API URL, DTO, and interaction sequence verbatim.
 */
(() => {
  const doc = document;
  const pageWindow = window;
  const body = () => doc.body;
  const code = () => body()?.dataset.automationCreateCode || "";
  const legal = (value) => /^agent_[a-f0-9]{32}$/.test(value);
  const existingRecord = () => new URLSearchParams(pageWindow.location.search).has("id");
  const bind = () => {
    try {
      // This script intentionally runs in <head>, before the body and frozen
      // controller template exist. Keep observing until both are present.
      if (!body()) return false;
      const createCode = code();
      const input = doc.getElementById("agentCode");
      if (existingRecord() || !legal(createCode)) return true;
      if (!(input instanceof HTMLInputElement)) return false;
      if (!input.value.trim()) {
        input.value = createCode;
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
      return true;
    } catch (_) {
      // A document can be torn down while an observer is queued; it is no
      // longer a page where a create binding can be applied.
      return true;
    }
  };
  if (bind()) return;
  const observer = new MutationObserver(() => {
    if (bind()) observer.disconnect();
  });
  observer.observe(doc.documentElement, { childList: true, subtree: true });
  pageWindow.setTimeout(() => observer.disconnect(), 10_000);
})();
