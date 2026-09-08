(() => {
  "use strict";

  const statusURL = "/api/admin/wecom/tags/sync-status";
  const catalogURL = "/api/admin/wecom/tags";
  const successKey = "aicrm.tag-sync.completed";
  let active = false;
  let state = "idle";
  let trackedReceipt = 0;
  let acceptanceGraceUntil = 0;
  let timer = 0;
  let archiveTimer = 0;
  let archiveRefreshing = false;
  let archiveSignature = "";
  let catalogWriteSignature = "";

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

  const archiveStatusLabel = (value) => {
    switch (value) {
      case "queued":
        return "已受理，等待企微执行";
      case "outcome_unknown":
        return "企微结果待确认";
      case "retryable_failed":
        return "本次写入失败，等待原任务处理";
      case "final_failed":
        return "企微写入未完成，需要核对";
      case "cancelled":
        return "任务已取消，需要核对";
      default:
        return value ? `状态：${value}` : "状态待确认";
    }
  };

  const catalogWriteStatusLabel = (value, readbackAt) => {
    switch (value) {
      case "queued":
        return "本地已受理，等待企微执行";
      case "attempted":
        return "企微调用已尝试，结果待确认";
      case "outcome_unknown":
        return "企微结果待确认";
      case "retryable_failed":
        return "本次企微写入失败，等待原任务处理";
      case "final_failed":
        return "企微写入未完成，需要核对";
      case "cancelled":
        return "任务已取消，需要核对";
      case "executed":
      case "reconciled":
        return readbackAt
          ? `企微已读回：${readbackAt}`
          : "企微执行状态已记录，尚未获得读回时间";
      default:
        return value ? `状态：${value}` : "状态待确认";
    }
  };

  const removeArchiveNotice = () => {
    document.querySelector("[data-tag-archive-outcomes]")?.remove();
  };

  const removeCatalogWriteNotice = () => {
    document.querySelector("[data-tag-catalog-write-status]")?.remove();
  };

  const catalogWriteRows = (payload) => {
    const rows = [];
    const append = (kind, value) => {
      if (!value || typeof value !== "object") return;
      const localID = Number(
        kind === "标签组" ? value.group_id : value.tag_id || value.id,
      );
      const state = String(value.provider_write_state || "");
      if (!localID || !state) return;
      rows.push({
        kind,
        localID,
        name: String(
          kind === "标签组"
            ? value.group_name || value.name || ""
            : value.tag_name || value.name || "",
        ),
        state,
        readbackAt: value.synced_at || value.provider_readback_at || "",
      });
    };
    const groups = Array.isArray(payload?.groups) ? payload.groups : [];
    const tags = Array.isArray(payload?.tags)
      ? payload.tags
      : Array.isArray(payload?.items)
        ? payload.items
        : [];
    groups.forEach((value) => append("标签组", value));
    tags.forEach((value) => append("标签", value));
    return rows.sort(
      (left, right) =>
        left.kind.localeCompare(right.kind) || left.localID - right.localID,
    );
  };

  // New and renamed rows remain visible locally while their Provider write is
  // pending. The frozen catalog does not render its receipt fields, so retain
  // their actual persisted state and readback time in this small Host notice.
  // It has no action: outcome_unknown must continue with its original receipt.
  const paintCatalogWriteStates = (payload) => {
    const rows = catalogWriteRows(payload);
    const signature = JSON.stringify(rows);
    if (
      signature === catalogWriteSignature &&
      document.querySelector("[data-tag-catalog-write-status]")
    )
      return;
    catalogWriteSignature = signature;
    removeCatalogWriteNotice();
    if (!rows.length) return;
    const stage = document.getElementById("stage");
    if (!stage) return;
    const unsettled = rows.some(
      (row) =>
        !["executed", "reconciled"].includes(row.state) || !row.readbackAt,
    );
    const panel = document.createElement("section");
    panel.dataset.tagCatalogWriteStatus = "1";
    panel.setAttribute("role", "status");
    Object.assign(panel.style, {
      margin: "12px 0 16px",
      padding: "12px 14px",
      border: "1px solid #91caff",
      borderRadius: "8px",
      background: "#e6f4ff",
      color: "#003a8c",
      fontSize: "13px",
      lineHeight: "1.6",
    });
    const heading = document.createElement("strong");
    heading.textContent = unsettled
      ? "标签目录已保存，部分企微写入尚未确认"
      : "标签目录企微状态已读回";
    panel.appendChild(heading);
    const list = document.createElement("ul");
    Object.assign(list.style, { margin: "6px 0 0", paddingLeft: "18px" });
    for (const item of rows) {
      const row = document.createElement("li");
      const label = item.name ? `「${item.name}」` : `#${item.localID}`;
      row.textContent = `${item.kind}${label}：${catalogWriteStatusLabel(
        item.state,
        item.readbackAt,
      )}`;
      list.appendChild(row);
    }
    panel.appendChild(list);
    stage.prepend(panel);
  };

  // Archive hides the catalog row locally before the Provider outcome is
  // known. Render the Tag-owned durable operation record in the V3 Host so a
  // successful local response is never mistaken for a completed WeCom write.
  // This panel intentionally has no retry action: an unknown outcome must use
  // the original external-effect receipt and cannot safely mint a new key.
  const paintArchiveOperations = (operations) => {
    const normalized = Array.isArray(operations)
      ? operations
          .filter((value) => value && typeof value === "object")
          .map((value) => ({
            operation: String(value.operation || ""),
            localID: Number(value.local_id || 0),
            state: String(value.state || value.provider_write_state || ""),
          }))
          .filter((value) => value.localID > 0 && value.operation)
      : [];
    const signature = JSON.stringify(normalized);
    // The frozen controller can replace #stage after this Host has already
    // read the exact same durable record. Keep its signature for comparison,
    // but do not let it suppress restoration of a panel the redraw removed.
    if (
      signature === archiveSignature &&
      document.querySelector("[data-tag-archive-outcomes]")
    )
      return;
    archiveSignature = signature;
    removeArchiveNotice();
    if (!normalized.length) return;
    const stage = document.getElementById("stage");
    if (!stage) return;
    const panel = document.createElement("section");
    panel.dataset.tagArchiveOutcomes = "1";
    panel.setAttribute("role", "status");
    Object.assign(panel.style, {
      margin: "12px 0 16px",
      padding: "12px 14px",
      border: "1px solid #f2c94c",
      borderRadius: "8px",
      background: "#fffbe6",
      color: "#614700",
      fontSize: "13px",
      lineHeight: "1.6",
    });
    const heading = document.createElement("strong");
    heading.textContent = "部分标签已本地归档，企微写入尚未确认";
    panel.appendChild(heading);
    const list = document.createElement("ul");
    Object.assign(list.style, { margin: "6px 0 0", paddingLeft: "18px" });
    for (const item of normalized) {
      const row = document.createElement("li");
      const kind = item.operation === "group_archive" ? "标签组" : "标签";
      row.textContent = `${kind} #${item.localID}：${archiveStatusLabel(item.state)}`;
      list.appendChild(row);
    }
    panel.appendChild(list);
    stage.prepend(panel);
  };

  const refreshArchiveOperations = async () => {
    if (archiveRefreshing) return;
    archiveRefreshing = true;
    try {
      const response = await fetch(catalogURL, {
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "application/json" },
      });
      if (!response.ok) return;
      const payload = await response.json();
      paintCatalogWriteStates(payload);
      if (payload?.archive_operations_status === "ready") {
        paintArchiveOperations(payload.archive_operations);
      }
    } catch (_error) {
      // The frozen catalog keeps rendering. A transient read failure must not
      // replace its data with a fabricated archive outcome.
    } finally {
      archiveRefreshing = false;
    }
  };

  const scheduleArchiveRefresh = (delay = 300) => {
    window.clearTimeout(archiveTimer);
    archiveTimer = window.setTimeout(() => void refreshArchiveOperations(), delay);
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

  document.addEventListener(
    "click",
    (event) => {
      const button =
        event.target instanceof Element ? event.target.closest("button") : null;
      if (button?.textContent.trim() === "删除") {
        // The frozen controller archives a row asynchronously. Refresh twice
        // after the command settles so the durable accepted/unknown receipt is
        // shown even though the archived row has disappeared from the table.
        scheduleArchiveRefresh(300);
        window.setTimeout(() => scheduleArchiveRefresh(300), 1200);
      }
    },
    true,
  );

  new MutationObserver(() => {
    paint();
    scheduleArchiveRefresh(500);
  }).observe(document.documentElement, { childList: true, subtree: true });
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
    void refreshArchiveOperations();
  });
})();
