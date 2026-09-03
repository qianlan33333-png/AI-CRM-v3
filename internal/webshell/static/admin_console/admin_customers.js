(function () {
  "use strict";

  const root = document.querySelector("[data-customer-directory-root]");
  if (!root) return;

  const api = { customers: root.dataset.customersUrl, sync: root.dataset.syncUrl };
  const $ = (id) => document.getElementById(id);
  const el = {
    alert: $("customer-page-alert"),
    syncSummary: $("customer-sync-summary"),
    syncMetrics: $("customer-sync-metrics"),
    syncStart: $("customer-sync-start"),
    filters: $("customer-list-filters"),
    refresh: $("customer-list-refresh"),
    summary: $("customer-list-summary"),
    state: $("customer-list-state"),
    wrap: $("customer-list-table-wrap"),
    body: $("customer-list-body"),
    next: $("customer-next-page"),
    detailCard: $("customer-detail-card"),
    detailState: $("customer-detail-state"),
    detailContent: $("customer-detail-content"),
    detailFields: $("customer-detail-fields"),
    identities: $("customer-identity-list"),
    phones: $("customer-phone-list"),
    reveal: $("customer-phone-reveal"),
    ephemeral: $("customer-phone-ephemeral"),
  };
  let nextCursor = "";
  let activeQuery = "";
  let detailID = "";
  let clearPhoneTimer = 0;

  function csrf() {
    const name = "aicrm_admin_csrf=";
    for (const part of document.cookie.split(";")) {
      const item = part.trim();
      if (item.startsWith(name)) return decodeURIComponent(item.slice(name.length));
    }
    return "";
  }

  async function request(url, options) {
    const config = options || {};
    const headers = new Headers(config.headers || {});
    headers.set("Accept", "application/json");
    if (config.body !== undefined) headers.set("Content-Type", "application/json");
    const response = await fetch(url, { ...config, headers, credentials:"same-origin", cache:"no-store" });
    let payload = {};
    try {
      payload = await response.json();
    } catch (_error) {}
    if (!response.ok || payload.ok === false) {
      const failure = new Error(payload.error || "request_failed");
      failure.status = response.status;
      throw failure;
    }
    return payload;
  }

  function alert(message, tone) {
    el.alert.textContent = message || "";
    el.alert.className = "admin-alert " + (tone === "success" ? "admin-alert--success" : "admin-alert--error");
    el.alert.hidden = !message;
  }

  function date(value) {
    const item = new Date(value || "");
    return Number.isNaN(item.getTime()) ? "—" : item.toLocaleString("zh-CN");
  }

  function label(value) {
    return ({
      queued: "排队中",
      listing_staff: "读取成员",
      fetching_profiles: "拉取资料",
      ingesting: "入库中",
      reconciling: "对账中",
      succeeded: "已完成",
      failed_retryable: "可恢复失败",
      failed_terminal: "终止失败",
    })[value] || value || "—";
  }

  function localPhone(value) {
    const phone = String(value || "");
    return phone.startsWith("+86") ? phone.slice(3) : phone;
  }

  function metric(name, value) {
    const node = document.createElement("div");
    node.className = "customer-metric";
    const span = document.createElement("span");
    span.textContent = name;
    const strong = document.createElement("strong");
    strong.textContent = String(value ?? "—");
    node.append(span, strong);
    return node;
  }

  async function loadSync() {
    try {
      const data = await request(api.sync + "?limit=1");
      const run = (data.items || [])[0];
      el.syncMetrics.replaceChildren();
      if (!run) {
        el.syncSummary.textContent = "尚无企微全量同步轮次。";
        return;
      }
      el.syncSummary.textContent = "最近轮次 #" + run.run_id + "：" + label(run.status) + "，开始 " + date(run.started_at || run.created_at) + (run.completed_at ? "，完成 " + date(run.completed_at) : "");
      el.syncMetrics.append(
        metric("发现", run.discovered),
        metric("新激活", run.activated),
        metric("已绑定", run.already_linked),
        metric("冲突", run.conflict),
        metric("终止失败", run.terminal_failed),
        metric("已投影", run.projected),
      );
    } catch (error) {
      el.syncSummary.textContent = error.status === 503 ? "企微客户同步当前未启用。" : "同步状态暂时不可用。";
    }
  }

  function queryFromForm() {
    const data = new FormData(el.filters);
    const params = new URLSearchParams();
    for (const key of ["keyword", "phone", "status"]) {
      const value = String(data.get(key) || "").trim();
      if (value) params.set(key, value);
    }
    params.set("limit", "50");
    return params;
  }

  function state(title, detail, error) {
    el.state.replaceChildren();
    const strong = document.createElement("strong");
    strong.textContent = title;
    const span = document.createElement("span");
    span.textContent = detail;
    el.state.append(strong, span);
    el.state.className = "admin-state" + (error ? " admin-state--error" : "");
    el.state.hidden = false;
    el.wrap.hidden = true;
  }

  function row(item) {
    const tr = document.createElement("tr");
    const person = document.createElement("td");
    const copy = document.createElement("div");
    copy.className = "customer-person";
    const name = document.createElement("strong");
    const id = document.createElement("small");
    name.textContent = item.display_name || "未命名客户";
    id.textContent = "Customer #" + item.customer_id;
    copy.append(name, id);
    person.append(copy);
    tr.append(person);

    for (const value of [item.oneid, item.phone_masked ? localPhone(item.phone_masked) : "未绑定", date(item.last_synced_at)]) {
      const td = document.createElement("td");
      td.textContent = value || "—";
      tr.append(td);
    }
    const action = document.createElement("td");
    const link = document.createElement("a");
    link.className = "admin-button admin-button--ghost";
    link.href = "/admin/customers/" + item.customer_id;
    link.textContent = "查看详情";
    action.append(link);
    tr.append(action);
    return tr;
  }

  async function loadList(cursor) {
    state("正在加载", "读取 v3 客户目录。", false);
    try {
      const params = cursor ? new URLSearchParams(activeQuery) : queryFromForm();
      if (cursor) params.set("cursor", cursor);
      else {
        activeQuery = params.toString();
        nextCursor = "";
      }
      const data = await request(api.customers + "?" + params.toString());
      el.body.replaceChildren();
      for (const item of data.items || []) el.body.append(row(item));
      el.summary.textContent = (data.total_is_estimate ? "至少 " : "") + String(data.total || 0) + " 位客户";
      el.state.hidden = (data.items || []).length > 0;
      el.wrap.hidden = (data.items || []).length === 0;
      if ((data.items || []).length === 0) state("暂无客户", "没有匹配的客户。", false);
      nextCursor = data.next_cursor || "";
      el.next.hidden = !nextCursor;
    } catch (error) {
      if (error.status === 401) state("登录已失效", "请重新登录后查询。", true);
      else if (error.status === 400 && error.message === "invalid_request") state("手机号格式不正确", "请输入11位中国大陆手机号。", true);
      else state("客户列表暂时不可用", "请稍后重试。", true);
    }
  }

  function field(name, value) {
    const node = document.createElement("div");
    node.className = "customer-detail-field";
    const span = document.createElement("span");
    span.textContent = name;
    const strong = document.createElement("strong");
    strong.textContent = String(value || "—");
    node.append(span, strong);
    return node;
  }

  function token(value) {
    const node = document.createElement("span");
    node.className = "customer-token";
    node.textContent = value;
    return node;
  }

  async function loadDetail(id) {
    el.detailCard.hidden = false;
    el.detailState.hidden = false;
    el.detailContent.hidden = true;
    try {
      const data = await request(api.customers + "/" + id);
      const item = data.customer || {};
      el.detailFields.replaceChildren(
        field("Customer ID", item.customer_id),
        field("姓名", item.display_name || "未命名"),
        field("OneID", item.oneid),
        field("企业", item.corp_name),
        field("类型", item.contact_type),
        field("数据源", item.source),
        field("最后同步", date(item.last_synced_at)),
      );
      el.identities.replaceChildren();
      for (const identity of data.identities || []) {
        if (identity.kind === "phone") continue;
        el.identities.append(token(identity.kind + " · " + identity.scope + " · " + identity.assurance + " · " + identity.status));
      }
      el.phones.replaceChildren();
      for (const phone of data.phones || []) el.phones.append(token(localPhone(phone.masked)));
      if (!(data.phones || []).length) el.phones.append(token("未绑定"));
      el.reveal.hidden = !(data.phones || []).length;
      el.detailState.hidden = true;
      el.detailContent.hidden = false;
      detailID = String(id);
    } catch (error) {
      el.detailState.className = "admin-state admin-state--error";
      el.detailState.textContent = error.status === 404 ? "客户不存在。" : "客户详情暂时不可用。";
    }
  }

  async function startSync() {
    el.syncStart.disabled = true;
    try {
      await request(api.sync, { method: "POST", headers: { "X-CSRF-Token": csrf(), "Idempotency-Key": "manual-ui-" + crypto.randomUUID() }, body: undefined });
      alert("已创建企微客户同步轮次。", "success");
      await loadSync();
    } catch (error) {
      alert(error.status === 403 ? "仅 SuperAdmin 可重拉企微客户。" : error.status === 503 ? "企微客户同步未启用或凭据未就绪。" : "无法创建同步轮次。", "error");
    } finally {
      el.syncStart.disabled = false;
    }
  }

  async function reveal(event) {
    event.preventDefault();
    try {
      const data = await request(api.customers + "/" + detailID + "/phone-reveal", { method: "POST", headers: { "X-CSRF-Token": csrf() } });
      el.ephemeral.textContent = "手机号：" + localPhone(data.phone);
      el.ephemeral.hidden = false;
      window.clearTimeout(clearPhoneTimer);
      clearPhoneTimer = window.setTimeout(function () {
        el.ephemeral.textContent = "";
        el.ephemeral.hidden = true;
      }, 30000);
    } catch (error) {
      alert(error.status === 403 ? "当前账号无权查询手机号，或 CSRF 验证失败。" : "手机号查询失败。", "error");
    }
  }

  el.filters.addEventListener("submit", function (event) {
    event.preventDefault();
    loadList("");
  });
  el.refresh.addEventListener("click", function () { loadList(""); });
  el.next.addEventListener("click", function () { if (nextCursor) loadList(nextCursor); });
  el.syncStart.addEventListener("click", startSync);
  el.reveal.addEventListener("submit", reveal);
  const match = location.pathname.match(/^\/admin\/customers\/([1-9][0-9]*)$/);
  loadSync();
  if (match) loadDetail(match[1]);
  else loadList("");
})();
