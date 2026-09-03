(() => {
  "use strict";

  const statusURL = "/api/admin/wecom/tags/sync-status";
  const successKey = "aicrm.tag-sync.completed";
  let active = false;
  let state = "idle";
  let trackedReceipt = 0;
  let acceptanceGraceUntil = 0;
  let timer = 0;

  const syncButton = () => {
    if (typeof document === "undefined" || !document?.querySelectorAll)
      return null;
    return Array.from(document.querySelectorAll("button")).find(
      (button) =>
        button.textContent.trim() === "同步企微标签" ||
        button.dataset.tagSyncButton === "1",
    );
  };

  const paint = () => {
    const button = syncButton();
    if (!button) return;
    button.dataset.tagSyncButton = "1";
    button.disabled = active;
    const label = !active
      ? "同步企微标签"
      : state === "outcome_unknown"
        ? "同步结果待对账"
        : state === "retryable_failed"
          ? "同步待重试"
          : "同步中…";
    if (button.textContent !== label) button.textContent = label;
    button.style.cursor = active ? "not-allowed" : "pointer";
    button.style.opacity = active ? "0.65" : "1";
    button.setAttribute("aria-busy", active ? "true" : "false");
  };

  const notice = (message, error = false) => {
    const node = document.createElement("div");
    node.setAttribute("role", error ? "alert" : "status");
    node.textContent = message;
    Object.assign(node.style, {
      position: "fixed",
      right: "24px",
      bottom: "24px",
      zIndex: "99999",
      maxWidth: "460px",
      padding: "12px 18px",
      borderRadius: "8px",
      color: "#fff",
      background: error ? "#d83931" : "#1f2329",
      boxShadow: "0 8px 28px rgba(0,0,0,.18)",
      fontSize: "14px",
    });
    document.body.appendChild(node);
    window.setTimeout(() => node.remove(), 5000);
  };

  const schedule = (delay = 800) => {
    window.clearTimeout(timer);
    timer = window.setTimeout(poll, delay);
  };

  const poll = async () => {
    try {
      const response = await fetch(statusURL, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error(`status ${response.status}`);
      const payload = await response.json();
      const sync = payload && payload.sync;
      if (!sync || typeof sync.state !== "string")
        throw new Error("invalid sync status");
      const receipt = Number(sync.receipt_id || 0);
      const isActive = Boolean(sync.active);
      if (isActive) {
        active = true;
        state = sync.state;
        trackedReceipt = receipt;
        paint();
        schedule();
        return;
      }
      if (
        Date.now() < acceptanceGraceUntil &&
        (trackedReceipt === 0 || !receipt || receipt === trackedReceipt)
      ) {
        active = true;
        paint();
        schedule(250);
        return;
      }
      const completedTracked = trackedReceipt > 0 && receipt === trackedReceipt;
      active = false;
      state = sync.state;
      paint();
      if (completedTracked && sync.state === "executed") {
        try {
          sessionStorage.setItem(
            successKey,
            JSON.stringify({
              groups: sync.group_count || 0,
              tags: sync.tag_count || 0,
            }),
          );
        } catch (_error) {}
        location.reload();
        return;
      }
      if (
        completedTracked &&
        ["final_failed", "cancelled", "reconciled"].includes(sync.state)
      ) {
        notice(`标签同步未完成（${sync.state}），已允许重新发起`, true);
      }
    } catch (_error) {
      if (active) schedule(1500);
    }
  };

  document.addEventListener(
    "click",
    (event) => {
      const button =
        event.target instanceof Element ? event.target.closest("button") : null;
      if (
        !button ||
        (button.textContent.trim() !== "同步企微标签" &&
          button.dataset.tagSyncButton !== "1")
      )
        return;
      if (active) {
        event.preventDefault();
        event.stopImmediatePropagation();
        return;
      }
      active = true;
      state = "queued";
      trackedReceipt = 0;
      acceptanceGraceUntil = Date.now() + 3000;
      paint();
      schedule(250);
    },
    true,
  );

  new MutationObserver(paint).observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
  document.addEventListener("DOMContentLoaded", () => {
    let raw = null;
    try {
      raw = sessionStorage.getItem(successKey);
    } catch (_error) {}
    if (raw) {
      try {
        sessionStorage.removeItem(successKey);
      } catch (_error) {}
      try {
        const result = JSON.parse(raw);
        notice(
          `标签同步完成：${result.groups} 个标签组，${result.tags} 个标签`,
        );
      } catch (_error) {
        notice("标签同步完成");
      }
    }
    paint();
    void poll();
  });
})();
