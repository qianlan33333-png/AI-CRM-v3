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
  const memberGridRoot = () => document.getElementById("spMemberGrid");
  const memberGridStaffURL = () => {
    const productID = String(memberGridRoot()?.dataset?.serviceProductId || "");
    if (!/^[1-9][0-9]*$/.test(productID)) return null;
    return `/api/admin/service-period-products/${encodeURIComponent(productID)}/member-grid/staff`;
  };
  if (nativeFetch) {
    window.fetch = async (...args) => {
      const raw = typeof window.Request === "function" && args[0] instanceof window.Request ? args[0].url : String(args[0]);
      const target = new URL(raw, window.location.origin);
      // Map the original component's common read to this product's Access-scoped staff directory.
      if (target.pathname === "/api/admin/common/operation-members") {
        const scoped = memberGridStaffURL();
        if (!scoped) return new Response(JSON.stringify({items: []}), {status: 400, headers: {"Content-Type": "application/json"}});
        const response = await nativeFetch(scoped, {headers: {Accept: "application/json"}, credentials: "same-origin", cache: "no-store"});
        if (response.ok) {
          try { remember(await response.clone().json()); } catch (_error) { /* non-JSON response */ }
        }
        return response;
      }
      const response = await nativeFetch(...args);
      if (response.ok) {
        try { remember(await response.clone().json()); } catch (_error) { /* non-JSON response */ }
      }
      return response;
    };
  }
  // Preserve the original component; only suppress its unrelated global refresh action.
  if (window.OperationMemberPicker?.open) {
    const standardPicker = window.OperationMemberPicker;
    window.OperationMemberPicker = {...standardPicker, open(options = {}) { return standardPicker.open({...options, allowRefresh: false}); }};
  }
  const requestJson = async (path, options) => {
    const settings = options || {};
    const method = String(settings.method || "GET").toUpperCase();
    const headers = {Accept: "application/json", ...(settings.headers || {})};
    let body = settings.body;
    if (method !== "GET") {
      const member = /^\/api\/admin\/service-period-products\/[^/]+\/members\/([^/]+)\/(?:remark|alliance)$/.exec(path);
      if (member && body && body.version == null) body = {...body, version: versionByMember.get(decodeURIComponent(member[1]))};
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = cookie("aicrm_admin_csrf");
      headers["Idempotency-Key"] = key();
    }
    const response = await window.fetch(path, {method, headers, credentials: "same-origin", cache: "no-store", body: method === "GET" ? undefined : JSON.stringify(body || {})});
    const payload = remember(await response.json().catch(() => ({})));
    // Inline edits use the opaque member reference and a row-version CAS.
    // Remember the acknowledged version immediately: waiting for the next
    // query would send a stale version if the user edits again before it runs.
    const member = /^\/api\/admin\/service-period-products\/[^/]+\/members\/([^/]+)\/(?:remark|alliance)$/.exec(path);
    if (member && payload?.member_ref && Number.isInteger(Number(payload.version))) {
      versionByMember.set(String(payload.member_ref), Number(payload.version));
    }
    if (!response.ok) {
      const message = response.status === 410 ? "分享链接已关闭或已更新" : String(payload.message || "操作失败");
      const error = new Error(message); error.status = response.status; error.payload = payload; throw error;
    }
    if (payload?.external_share?.url?.startsWith("/")) payload.external_share.url = new URL(payload.external_share.url, window.location.origin).toString();
    return payload;
  };
  window.AdminApi = {
    requestJson,
    escapeHtml(value) { const node = document.createElement("span"); node.textContent = String(value ?? ""); return node.innerHTML; },
    errorMessage(error, fallback) {
      if (error?.payload?.error === "share_gone" || error?.payload?.code === "SHARE_GONE") return "分享链接已关闭或已更新";
      return String(error?.payload?.message || error?.message || fallback || "操作失败");
    },
  };
  // The frozen dd8 renderer coerces a missing renewal count to 0. V3 has no
  // Order-owned renewal-count projection, so only rows explicitly marked
  // unavailable become “—”; an actual zero remains a truthful zero.
  const renderUnavailableRenewals = () => document.querySelectorAll("td.sp-col-renewal_count").forEach((cell) => {
    const recordID = String(cell.closest("tr")?.dataset?.recordId || "");
    if (recordID && unavailableRenewalByMember.has(recordID) && cell.textContent.trim() === "0") cell.textContent = "—";
  });
  new MutationObserver(renderUnavailableRenewals).observe(document.documentElement, {childList:true, subtree:true});

})(window, document);
