(function (window, document) {
  "use strict";
  const versionByMember = new Map();
  const unavailableRenewalByMember = new Set();
  const cookie = (name) => document.cookie.split(";").map((value) => value.trim()).find((value) => value.startsWith(`${name}=`))?.slice(name.length + 1) || "";
  const key = () => window.crypto?.randomUUID?.() || `member-grid-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const remember = (payload) => {
    if (!payload || !Array.isArray(payload.rows)) return payload;
    payload.rows.forEach((row) => {
      if (!row || !row.unionid) return;
      const member = String(row.unionid);
      if (Number.isInteger(Number(row.version))) versionByMember.set(member, Number(row.version));
      if (row.values && row.values.renewal_count_unavailable === true) unavailableRenewalByMember.add(member);
      else unavailableRenewalByMember.delete(member);
    });
    return payload;
  };
  // Public-grid requests bypass AdminApi. Observe their cloned response before
  // the frozen renderer consumes it, so both internal and public pages retain
  // the explicit unavailable renewal marker.
  const nativeFetch = typeof window.fetch === "function" ? window.fetch.bind(window) : null;
  if (nativeFetch) {
    window.fetch = async (...args) => {
      const response = await nativeFetch(...args);
      if (response.ok) {
        try { remember(await response.clone().json()); } catch (_error) { /* non-JSON response */ }
      }
      return response;
    };
  }
  const requestJson = async (path, options) => {
    const settings = options || {};
    const method = String(settings.method || "GET").toUpperCase();
    const headers = {Accept: "application/json", ...(settings.headers || {})};
    let body = settings.body;
    if (method !== "GET") {
      const member = /^\/api\/admin\/service-period-products\/[^/]+\/members\/([^/]+)\/remark$/.exec(path);
      if (member && body && body.version == null) body = {...body, version: versionByMember.get(decodeURIComponent(member[1]))};
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = cookie("aicrm_admin_csrf");
      headers["Idempotency-Key"] = key();
    }
    const response = await window.fetch(path, {method, headers, credentials: "same-origin", cache: "no-store", body: method === "GET" ? undefined : JSON.stringify(body || {})});
    const payload = remember(await response.json().catch(() => ({})));
    if (!response.ok) { const error = new Error(String(payload.message || "操作失败")); error.status = response.status; error.payload = payload; throw error; }
    if (payload?.external_share?.url?.startsWith("/")) payload.external_share.url = new URL(payload.external_share.url, window.location.origin).toString();
    return payload;
  };
  window.AdminApi = {
    requestJson,
    escapeHtml(value) { const node = document.createElement("span"); node.textContent = String(value ?? ""); return node.innerHTML; },
    errorMessage(error, fallback) { return String(error?.payload?.message || error?.message || fallback || "操作失败"); },
  };
  // The frozen dd8 renderer coerces a missing renewal count to 0. V3 has no
  // Order-owned renewal-count projection, so only rows explicitly marked
  // unavailable become “—”; an actual zero remains a truthful zero.
  const renderUnavailableRenewals = () => document.querySelectorAll("td.sp-col-renewal_count").forEach((cell) => {
    const recordID = String(cell.closest("tr")?.dataset?.recordId || "");
    if (recordID && unavailableRenewalByMember.has(recordID) && cell.textContent.trim() === "0") cell.textContent = "—";
  });
  new MutationObserver(renderUnavailableRenewals).observe(document.documentElement, {childList:true, subtree:true});
  window.OperationMemberPicker = {
    async open(options) {
      const root = document.getElementById("spMemberGrid"); const productID = String(root?.dataset?.serviceProductId || "");
      if (!/^[1-9][0-9]*$/.test(productID)) throw new Error("周期商品不存在");
      const payload = await requestJson(`/api/admin/service-period-products/${encodeURIComponent(productID)}/member-grid/staff`);
      const disabled = new Set((options?.disabledUserIds || []).map(String));
      const dialog = document.createElement("dialog"); dialog.className = "sp-dialog";
      dialog.innerHTML = '<form method="dialog" class="sp-dialog__surface"><header><h2>选择企微员工</h2></header><div class="sp-picker-list"></div><footer><button class="sp-secondary-button" value="cancel">取消</button></footer></form>';
      const list = dialog.querySelector(".sp-picker-list");
      (Array.isArray(payload.items) ? payload.items : []).filter((member) => !disabled.has(String(member.user_id || ""))).forEach((member) => {
        const button = document.createElement("button"); button.type = "button"; button.className = "sp-menu-item"; button.textContent = String(member.display_name || member.user_id || "员工");
        button.addEventListener("click", async () => { dialog.close(); dialog.remove(); await options?.onSelect?.(member); }); list.appendChild(button);
      });
      document.body.appendChild(dialog); dialog.addEventListener("close", () => dialog.remove(), {once:true}); dialog.showModal();
    },
  };
})(window, document);
