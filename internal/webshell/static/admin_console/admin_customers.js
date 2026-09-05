(function () {
  "use strict";

  const root = document.querySelector("[data-customer-directory-root]");
  if (!root) return;

  const api = { customers: root.dataset.customersUrl, sync: root.dataset.syncUrl };
  const byID = (id) => document.getElementById(id);
  const el = {
    alert: byID("customer-page-alert"),
    syncSummary: byID("customer-sync-summary"),
    syncMetrics: byID("customer-sync-metrics"),
    syncStart: byID("customer-sync-start"),
    filters: byID("customer-list-filters"),
    clear: byID("customer-list-clear"),
    refresh: byID("customer-list-refresh"),
    summary: byID("customer-list-summary"),
    state: byID("customer-list-state"),
    wrap: byID("customer-list-table-wrap"),
    body: byID("customer-list-body"),
    previous: byID("customer-prev-page"),
    next: byID("customer-next-page"),
    profileName: byID("customer-profile-name"),
    detailState: byID("customer-detail-state"),
    detailContent: byID("customer-detail-content"),
    detailFields: byID("customer-detail-fields"),
    profileMeta: byID("customer-profile-meta"),
    ephemeral: byID("customer-phone-ephemeral"),
	sections360: byID("customer-360-sections"),
	main360: byID("customer-360-main"),
	sidebar360: byID("customer-360-sidebar"),
  };

  let activeQuery = "";
  let nextCursor = "";
  let pageIndex = 0;
  let pageCursors = [""];
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
    const response = await fetch(url, { ...config, headers, credentials: "same-origin", cache: "no-store" });
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

  function showAlert(message, success) {
    if (!el.alert) return;
    el.alert.textContent = message || "";
    el.alert.className = "admin-alert " + (success ? "admin-alert--success" : "admin-alert--error");
    el.alert.hidden = !message;
  }

  function date(value) {
    const item = new Date(value || "");
    return Number.isNaN(item.getTime()) ? "—" : item.toLocaleString("zh-CN");
  }

  function syncLabel(value) {
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

  function syncFailureDetail(code) {
    if (String(code || "").startsWith("retry_exhausted:")) {
      return "自动恢复次数已用尽；已保留已提交进度，等待管理员处理。";
    }
    return ({
      provider_disabled: "企微目录读取未启用。",
      provider_permission_denied: "企微目录读取权限不足。",
      provider_credentials_invalid: "企微目录凭据无效或持续失效。",
      provider_rate_limited: "企微接口限流，系统将按原轮次恢复。",
      provider_unavailable: "企微服务暂时不可用，系统将按原轮次恢复。",
      provider_response_invalid: "企微返回资料无效，已保留已提交进度。",
    })[code] || "同步未完成，已保留已提交进度。";
  }

  function localPhone(value) {
    const phone = String(value || "");
    return phone.startsWith("+86") ? phone.slice(3) : phone;
  }

  function syncMetric(name, value) {
    const node = document.createElement("span");
    node.className = "customer-sync-metric";
    node.append(document.createTextNode(name));
    const strong = document.createElement("strong");
    strong.textContent = String(value ?? "—");
    node.append(strong);
    return node;
  }

  async function loadSync() {
    if (!el.syncSummary || !el.syncMetrics) return;
    try {
      const data = await request(api.sync + "?limit=1");
      const run = (data.items || [])[0];
      el.syncMetrics.replaceChildren();
      if (!run) {
        el.syncSummary.textContent = "尚无企微全量同步轮次。";
        return;
      }
      const failure = run.status === "failed_retryable" || run.status === "failed_terminal";
      el.syncSummary.textContent = "最近轮次 #" + run.run_id + "：" + syncLabel(run.status) + "，开始 " + date(run.started_at || run.created_at) + (run.completed_at ? "，完成 " + date(run.completed_at) : "") + (failure ? "。" + syncFailureDetail(run.last_error_code) : "");
      el.syncMetrics.append(
        syncMetric("发现", run.discovered),
        syncMetric("新激活", run.activated),
        syncMetric("已绑定", run.already_linked),
        syncMetric("冲突", run.conflict),
        syncMetric("终止失败", run.terminal_failed),
        syncMetric("已投影", run.projected),
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

  function listState(title, detail, error) {
    el.state.replaceChildren();
    const strong = document.createElement("strong");
    const span = document.createElement("span");
    strong.textContent = title;
    span.textContent = detail;
    el.state.append(strong, span);
    el.state.className = "admin-state admin-state--inline" + (error ? " admin-state--error" : "");
    el.state.hidden = false;
    el.wrap.hidden = true;
  }

  function listRow(item) {
    const row = document.createElement("tr");
    const customer = document.createElement("td");
    const cell = document.createElement("div");
    cell.className = "admin-customer-cell";
    const name = document.createElement("div");
    name.className = "admin-customer-name";
    name.textContent = item.display_name || "未命名客户";
    const subtext = document.createElement("div");
    subtext.className = "admin-customer-subtext";
    subtext.textContent = "Customer #" + item.customer_id;
    cell.append(name, subtext);
    customer.append(cell);
    row.append(customer);

    for (const value of [item.oneid, item.phone_masked ? localPhone(item.phone_masked) : "未填写", date(item.last_synced_at)]) {
      const td = document.createElement("td");
      td.textContent = value || "—";
      row.append(td);
    }
    const action = document.createElement("td");
    const link = document.createElement("a");
    link.className = "admin-button admin-button--secondary";
    link.href = "/admin/customers/" + item.customer_id;
    link.textContent = "查看档案";
    action.append(link);
    row.append(action);
    return row;
  }

  async function loadList(cursor, navigation) {
    listState("正在加载客户", "按当前筛选读取客户目录。", false);
    try {
      let params;
      if (navigation === "reset") {
        params = queryFromForm();
        activeQuery = params.toString();
      } else {
        params = new URLSearchParams(activeQuery);
      }
      if (cursor) params.set("cursor", cursor);
      const data = await request(api.customers + "?" + params.toString());
      if (navigation === "reset") {
        pageIndex = 0;
        pageCursors = [""];
      } else if (navigation === "next") {
        pageIndex += 1;
        pageCursors = pageCursors.slice(0, pageIndex);
        pageCursors[pageIndex] = cursor;
      } else if (navigation === "previous") {
        pageIndex -= 1;
      }
      el.body.replaceChildren();
      for (const item of data.items || []) el.body.append(listRow(item));
      el.summary.textContent = (data.total_is_estimate ? "至少 " : "共 ") + String(data.total || 0) + " 位客户";
      const hasItems = (data.items || []).length > 0;
      el.state.hidden = hasItems;
      el.wrap.hidden = !hasItems;
      if (!hasItems) listState("当前没有匹配客户", "请调整关键词、手机号或客户状态后重试。", false);
      nextCursor = data.next_cursor || "";
      el.previous.hidden = pageIndex === 0;
      el.next.hidden = !nextCursor;
    } catch (error) {
      if (error.status === 401) listState("登录已失效", "请重新登录后查询。", true);
      else if (error.status === 400 && error.message === "invalid_request") listState("手机号格式不正确", "请输入11位中国大陆手机号。", true);
      else listState("客户列表暂时不可用", "请稍后重试。", true);
    }
  }

  function profileField(label, value) {
    const field = document.createElement("div");
    field.className = "admin-profile-field";
    const span = document.createElement("span");
    const strong = document.createElement("strong");
    span.textContent = label;
    if (value instanceof Node) strong.append(value);
    else strong.textContent = String(value || "—");
    field.append(span, strong);
    return field;
  }

  function metaItem(label, value) {
    const item = document.createElement("span");
    item.append(document.createTextNode(label));
    const strong = document.createElement("strong");
    strong.textContent = String(value || "—");
    item.append(strong);
    return item;
  }

  function phoneField(masked) {
    const line = document.createElement("span");
    line.className = "customer-phone-line";
    const value = document.createElement("span");
    value.textContent = masked || "未填写";
    line.append(value);
    if (masked) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "admin-button admin-button--secondary";
      button.textContent = "查询";
      button.addEventListener("click", revealPhone);
      line.append(button);
    }
    return line;
  }

  function sectionCard(title, section, render) {
    const card = document.createElement("section");
    card.className = "admin-card";
    const heading = document.createElement("h2");
    heading.textContent = title;
    card.append(heading);
    if (!section || section.status !== "ready") {
      const state = document.createElement("div");
      state.className = "admin-state admin-state--inline admin-state--error";
      state.textContent = "该分区暂时不可用，其他客户信息不受影响。";
      card.append(state);
      return card;
    }
    render(card, section.data || {});
    return card;
  }

  function line(target, value) {
    const node = document.createElement("div");
    node.className = "admin-profile-message";
    node.textContent = value;
    target.append(node);
  }

  async function loadDetail(id) {
    detailID = String(id);
    try {
	  const data = await request(api.customers + "/" + id + "/360");
	  const item = (data.profile && data.profile.data) || {};
	  const identity = (data.identity_summary && data.identity_summary.data) || {};
	  const identities = (identity.identities || []).map((value) => value.summary).filter(Boolean);
      el.profileName.textContent = item.display_name || "未命名客户";
      el.detailFields.replaceChildren(
        profileField("姓名", item.display_name || "未命名客户"),
		profileField("手机号", phoneField((identity.phones || [])[0] ? localPhone(identity.phones[0].masked) : "")),
        profileField("Customer ID", item.customer_id),
        profileField("OneID", [item.oneid, ...identities].filter(Boolean).join(" · ")),
      );
      el.profileMeta.replaceChildren(
        metaItem("客户状态", item.status),
        metaItem("企业", item.corp_name),
        metaItem("客户类型", item.contact_type),
        metaItem("数据来源", item.source),
        metaItem("最后同步", date(item.last_synced_at)),
      );
      el.detailState.hidden = true;
      el.detailContent.hidden = false;
	  el.main360.replaceChildren(
		sectionCard("订单统计", data.order_summary, function (target, value) { line(target, "订单总数：" + (value.total || 0)); line(target, "已支付：" + (value.paid || 0) + "，退款相关：" + (value.refunded || 0) + "，支付失败：" + (value.failed || 0)); (value.recent || []).slice(0, 10).forEach(function (order) { line(target, (order.merchant_order_no || "订单 #" + order.id) + " · " + (order.status || "")); }); }),
		sectionCard("问卷统计", data.questionnaire_summary, function (target, value) { line(target, "问卷记录：" + (value.total || 0)); (value.recent || []).forEach(function (survey) { line(target, (survey.title || "问卷") + " · " + date(survey.submitted_at)); }); })
	  );
	  el.sidebar360.replaceChildren(
		sectionCard("风险摘要", data.risk, function (target, value) { line(target, "风险等级：" + (value.level || "unknown")); (value.reasons || []).forEach(function (reason) { line(target, reason); }); }),
		sectionCard("最近触点", data.recent_touchpoints, function (target, value) { (Array.isArray(value) ? value : []).forEach(function (event) { line(target, (event.title || event.event_type || "客户事件") + " · " + date(event.occurred_at)); }); })
	  );
	  el.sections360.hidden = false;
    } catch (error) {
      el.detailState.className = "admin-state admin-state--inline admin-state--error";
      el.detailState.replaceChildren();
      const strong = document.createElement("strong");
      const span = document.createElement("span");
      strong.textContent = error.status === 404 ? "客户不存在" : "当前无法加载";
      span.textContent = error.status === 404 ? "请返回客户列表重新选择。" : "客户基础档案暂时不可用。";
      el.detailState.append(strong, span);
    }
  }

  async function startSync() {
    el.syncStart.disabled = true;
    try {
      await request(api.sync, { method: "POST", headers: { "X-CSRF-Token": csrf(), "Idempotency-Key": "manual-ui-" + crypto.randomUUID() } });
      showAlert("已创建企微客户同步轮次。", true);
      await loadSync();
    } catch (error) {
      showAlert(error.status === 403 ? "仅 SuperAdmin 可重拉企微客户。" : error.status === 503 ? "企微客户同步未启用或凭据未就绪。" : "无法创建同步轮次。", false);
    } finally {
      el.syncStart.disabled = false;
    }
  }

  async function revealPhone() {
    try {
      const data = await request(api.customers + "/" + detailID + "/phone-reveal", { method: "POST", headers: { "X-CSRF-Token": csrf() } });
      el.ephemeral.textContent = "手机号：" + localPhone(data.phone) + "（30 秒后自动隐藏）";
      el.ephemeral.className = "admin-alert admin-alert--success customer-phone-ephemeral";
      el.ephemeral.hidden = false;
      window.clearTimeout(clearPhoneTimer);
      clearPhoneTimer = window.setTimeout(function () {
        el.ephemeral.textContent = "";
        el.ephemeral.hidden = true;
      }, 30000);
    } catch (error) {
      el.ephemeral.textContent = error.status === 403 ? "当前账号无权查询手机号，或 CSRF 验证失败。" : "手机号查询失败。";
      el.ephemeral.className = "admin-alert admin-alert--error customer-phone-ephemeral";
      el.ephemeral.hidden = false;
    }
  }

  if (el.filters) el.filters.addEventListener("submit", function (event) { event.preventDefault(); loadList("", "reset"); });
  if (el.clear) el.clear.addEventListener("click", function () { el.filters.reset(); loadList("", "reset"); });
  if (el.refresh) el.refresh.addEventListener("click", function () { loadList(pageCursors[pageIndex], "refresh"); });
  if (el.previous) el.previous.addEventListener("click", function () { if (pageIndex > 0) loadList(pageCursors[pageIndex - 1], "previous"); });
  if (el.next) el.next.addEventListener("click", function () { if (nextCursor) loadList(nextCursor, "next"); });
  if (el.syncStart) el.syncStart.addEventListener("click", startSync);
  const match = location.pathname.match(/^\/admin\/customers\/([1-9][0-9]*)$/);
  if (match) loadDetail(match[1]);
  else {
    loadSync();
    loadList("", "reset");
  }
})();
