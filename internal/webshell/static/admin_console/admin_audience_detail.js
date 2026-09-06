// v3-owned Automation Operations host adapter. Frozen v2 donor files are not
// imported or mutated; this adapter talks only to authenticated v3 APIs.
(() => {
  "use strict";

  const API = "/api/admin";
  const byID = (id) => document.getElementById(id);
  const text = (value, fallback = "—") => value === null || value === undefined || value === "" ? fallback : String(value);
  const escapeHTML = (value) => text(value, "").replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch]);
  const formatTime = (value) => value ? (window.AdminFmt?.localTime(value) || text(value)) : "—";
  const requestKey = (scope) => `${scope}-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`}`;
  const csrf = () => {
    for (const part of document.cookie.split(";")) {
      const value = part.trim();
      for (const name of ["aicrm_admin_csrf=", "aicrm_csrf="]) {
        if (value.startsWith(name)) return decodeURIComponent(value.slice(name.length));
      }
    }
    return "";
  };

  class APIError extends Error {
    constructor(status, code) {
      super(code || `HTTP ${status}`);
      this.status = status;
      this.code = code || "unknown_error";
    }
  }

  const transientRetryDelays = [150, 600];
  const delay = (milliseconds) => new Promise((resolve) => window.setTimeout(resolve, milliseconds));

  async function request(path, options = {}) {
    const method = options.method || "GET";
    const headers = new Headers(options.headers || {});
    headers.set("Accept", "application/json");
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (options.mutate) {
      headers.set("X-CSRF-Token", csrf());
      headers.set("Idempotency-Key", requestKey(options.scope || "automation-operations"));
    }
    // GETs and explicitly marked read-only commands may be retried across the
    // sub-second service handoff performed by a versioned production deploy.
    // Mutations are deliberately excluded: an interrupted response must stay
    // unknown until the caller reloads the persisted receipt/state.
    const retryDelays = method === "GET" || options.retryTransient ? transientRetryDelays : [];
    for (let attempt = 0; ; attempt += 1) {
      let response;
      try {
        response = await fetch(path, {
          method,
          credentials: "same-origin",
          cache: "no-store",
          headers,
          body: options.body === undefined ? undefined : JSON.stringify(options.body),
        });
      } catch (_error) {
        if (attempt < retryDelays.length) {
          await delay(retryDelays[attempt]);
          continue;
        }
        throw new APIError(0, "network_error");
      }
      const payload = await response.json().catch(() => ({}));
      if (response.ok) return payload;
      const code = typeof payload.error === "string" ? payload.error : "";
      const gatewayUnavailable = response.status === 502 || response.status === 504 || (response.status === 503 && !code);
      if (gatewayUnavailable && attempt < retryDelays.length) {
        await delay(retryDelays[attempt]);
        continue;
      }
      throw new APIError(response.status, code || (gatewayUnavailable ? "gateway_unavailable" : "unknown_error"));
    }
  }

  function errorState(error) {
    if (!(error instanceof APIError)) return { state: "unknown", message: "网络或服务状态未知，请勿将本次操作视为成功。" };
    if (error.code === "network_error") return { state: "unknown", message: "网络连接中断，已停止本次操作；请刷新页面核对实际状态后重试。" };
    if (error.code === "gateway_unavailable") return { state: "not-ready", message: "服务正在发布或短暂不可用，已停止本次操作；请稍后重试。" };
    if (error.status === 401) return { state: "forbidden", message: "登录会话已失效，请重新登录。" };
    if (error.status === 403) return { state: "forbidden", message: error.code === "csrf_required" ? "页面安全令牌已失效，请刷新页面后重试。" : "当前账号没有执行此操作的权限。" };
    if (error.status === 409) return { state: "conflict", message: "服务端版本已经变化。页面将重新读取；请重新预览后确认。" };
    if (error.status === 404) return { state: "empty", message: "记录不存在或尚未生成。" };
    if (error.status === 422 || error.status === 503) return { state: "not-ready", message: "能力尚未满足执行条件；请检查 OneID、快照、固定话术、发送人与 Provider 就绪状态。" };
    return { state: "unknown", message: `请求未完成（${escapeHTML(error.code)}），请勿按成功处理。` };
  }

  const readinessReasonLabels = {
    configuration_missing: "尚未配置人群筛选条件",
    automation_binding_missing: "未绑定已发布的固定话术",
    sender_set_missing: "未配置发送人白名单",
    sender_set_empty: "发送人白名单为空",
    published_snapshot_missing: "尚未发布人群快照",
    published_content_missing: "固定话术尚未发布",
    agent_execution_not_supported: "当前绑定不是固定话术",
    content_not_active: "固定话术尚未激活",
    content_version_drift: "固定话术版本已经变化，需要重新绑定",
    sender_ineligible: "发送人当前不具备企微发送资格",
    sender_version_drift: "发送人资格版本已经变化，需要重新保存",
    provider_disabled: "Automation Provider 尚未获得生产发送授权",
    definition_unsupported: "人群筛选定义不受支持",
    schedule_invalid: "刷新计划无效",
    package_archived: "人群包已归档",
  };

  function readinessMessage(reasons) {
    return (Array.isArray(reasons) ? reasons : []).map((reason) => readinessReasonLabels[reason] || text(reason)).join("；");
  }

  function setStatus(node, message, state = "") {
    if (!node) return;
    node.textContent = message;
    if (state) node.dataset.state = state;
    else delete node.dataset.state;
  }

  function setCapability(message, state) {
    const node = byID("capabilityStatus");
    if (!node) return;
    node.textContent = message;
    node.dataset.capabilityState = state;
  }

  function enable(root = document) {
    root.querySelectorAll("button, input, textarea, select").forEach((control) => {
      control.disabled = false;
      control.removeAttribute("aria-disabled");
    });
  }

  function lifecycleLabel(value) {
    return ({ paused: "已暂停", active: "运行中", archived: "已归档" })[value] || text(value);
  }

  function runStateLabel(value) {
    return ({ accepted: "已接受", queued: "已排队", pending_review: "等待 AI 审阅", executing: "执行中", completed: "已完成", partial: "部分完成", partial_failed: "部分失败", failed: "失败", cancelled: "已取消", outcome_unknown: "结果未知", reconciled: "已对账", provider_accepted: "Provider 已接受", delivery_proven: "已证明送达", retryable_failed: "可重试失败", final_failed: "最终失败" })[value] || text(value);
  }

  async function bootList() {
    if (!byID("audRows")) return;
    const state = { groups: [], packages: [], templates: [], groupID: null, page: 1, pageSize: 20, busy: false };
    const notice = byID("audNotice");

    const showNotice = (message, isError = false) => {
      notice.hidden = !message;
      notice.textContent = message;
      notice.classList.toggle("error", isError);
    };

    const groupPackages = () => state.packages.filter((item) => (item.group_id || null) === state.groupID);
    const render = () => {
      const groups = [{ id: null, name: "未分组", version: 0 }, ...state.groups];
      byID("groupSummary").textContent = `共 ${state.groups.length} 个自定义分组`;
      byID("groupList").innerHTML = groups.map((group) => {
        const count = state.packages.filter((item) => (item.group_id || null) === group.id).length;
        return `<button class="aud-group-item${group.id === state.groupID ? " active" : ""}" type="button" data-group-id="${group.id || ""}"><strong title="${escapeHTML(group.name)}">${escapeHTML(group.name)}</strong><span>${count}</span></button>`;
      }).join("");
      byID("groupList").querySelectorAll("[data-group-id]").forEach((node) => node.addEventListener("click", () => { state.groupID = node.dataset.groupId ? Number(node.dataset.groupId) : null; state.page = 1; render(); }));
      const current = groups.find((item) => item.id === state.groupID) || groups[0];
      const rows = groupPackages();
      const pages = Math.max(1, Math.ceil(rows.length / state.pageSize));
      state.page = Math.min(state.page, pages);
      const visible = rows.slice((state.page - 1) * state.pageSize, state.page * state.pageSize);
      byID("selectedGroupName").textContent = current.name;
      byID("selectedGroupMeta").textContent = `${rows.length} 个人群包 · 每页 ${state.pageSize} 个`;
      byID("groupActions").hidden = state.groupID === null;
      byID("audRows").innerHTML = visible.length ? visible.map((item) => `<tr>
        <td><div class="aud-name-cell"><a class="aud-name" href="/admin/automation-conversion/packages/${item.id}"><span class="aud-dot${item.lifecycle === "active" ? "" : " muted"}"></span>${escapeHTML(item.name)}</a><span class="aud-template-tag">${escapeHTML(item.code)}</span></div></td>
        <td class="aud-strong">${Number(item.member_count || 0)}</td><td>${formatTime(item.published_at)}</td><td><span class="aud-pill${item.lifecycle === "active" ? "" : " gray"}">${lifecycleLabel(item.lifecycle)}</span></td>
        <td><div class="aud-actions" style="justify-content:flex-end"><button class="aud-btn" data-action="${item.lifecycle === "active" ? "pause" : "activate"}" data-package-id="${item.id}">${item.lifecycle === "active" ? "暂停" : "激活"}</button><button class="aud-btn" data-action="copy" data-package-id="${item.id}">复制</button><button class="aud-btn danger" data-action="archive" data-package-id="${item.id}">归档</button></div></td>
      </tr>`).join("") : `<tr><td class="aud-empty" colspan="5">当前分组暂无人群包</td></tr>`;
      byID("pageMeta").textContent = `第 ${state.page} / ${pages} 页，共 ${rows.length} 个`;
      byID("prevBtn").disabled = state.page <= 1;
      byID("nextBtn").disabled = state.page >= pages;
      byID("audRows").querySelectorAll("[data-action]").forEach((node) => node.addEventListener("click", () => mutatePackage(Number(node.dataset.packageId), node.dataset.action)));
    };

    async function load() {
      showNotice("正在读取真实人群配置…");
      try {
        const [groups, packages, templates] = await Promise.all([
          request(`${API}/ai-audience/package-groups`),
          request(`${API}/ai-audience/packages?limit=100&offset=0`),
          request(`${API}/ai-audience/templates`),
        ]);
        state.groups = groups.items || [];
        state.packages = packages.items || [];
        state.templates = (templates.items || []).filter((item) => item.available);
        enable();
        render();
        showNotice(state.packages.length ? "" : "尚未创建人群包。创建后仍需配置快照、固定话术和发送人。", false);
      } catch (error) {
        const detail = errorState(error);
        showNotice(detail.message, true);
      }
    }

    async function mutatePackage(id, action) {
      if (state.busy || !["activate", "pause", "copy", "archive"].includes(action)) return;
      const item = state.packages.find((value) => value.id === id);
      if (!item) return;
      if (action === "archive" && !window.confirm(`归档“${item.name}”？归档后不可编辑。`)) return;
      state.busy = true;
      showNotice("正在提交并等待持久化收据…");
      try {
        if (action === "activate") {
          const checked = await request(`${API}/ai-audience/packages/${id}/precheck`, { method: "POST", body: {}, retryTransient: true });
          if (!checked.precheck?.ready) {
            showNotice(`暂不能激活：${readinessMessage(checked.precheck?.reasons) || "执行条件未满足"}。请点击人群包名称进入配置。`, true);
            return;
          }
        }
        const path = action === "archive" ? `${API}/ai-audience/packages/${id}?expected_version=${item.version}` : `${API}/ai-audience/packages/${id}/${action}`;
        await request(path, { method: action === "archive" ? "DELETE" : "POST", mutate: true, scope: `audience-${action}`, body: action === "copy" ? undefined : { expected_version: item.version } });
        await load();
      } catch (error) {
        const detail = errorState(error);
        showNotice(detail.message, true);
        if (error instanceof APIError && error.status === 409) await load();
      } finally { state.busy = false; }
    }

    const groupModal = byID("groupModal");
    const groupForm = byID("groupForm");
    let editingGroup = null;
    const openGroup = (group = null) => {
      editingGroup = group;
      byID("groupModalTitle").textContent = group ? "编辑分组" : "新增分组";
      byID("groupNameInput").value = group?.name || "";
      groupModal.hidden = false;
      byID("groupNameInput").focus();
    };
    byID("createGroupBtn").addEventListener("click", () => openGroup());
    byID("renameGroupBtn").addEventListener("click", () => openGroup(state.groups.find((item) => item.id === state.groupID)));
    byID("cancelGroupBtn").addEventListener("click", () => { groupModal.hidden = true; });
    groupForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const name = byID("groupNameInput").value.trim();
      if (!name) return;
      try {
        await request(editingGroup ? `${API}/ai-audience/package-groups/${editingGroup.id}` : `${API}/ai-audience/package-groups`, { method: editingGroup ? "PATCH" : "POST", mutate: true, scope: "audience-group", body: editingGroup ? { name, sort_order: editingGroup.sort_order, expected_version: editingGroup.version } : { name, sort_order: state.groups.length + 1 } });
        groupModal.hidden = true;
        await load();
      } catch (error) { const detail = errorState(error); showNotice(detail.message, true); }
    });
    byID("deleteGroupBtn").addEventListener("click", async () => {
      const group = state.groups.find((item) => item.id === state.groupID);
      if (!group || !window.confirm(`删除空分组“${group.name}”？`)) return;
      try { await request(`${API}/ai-audience/package-groups/${group.id}?expected_version=${group.version}`, { method: "DELETE", mutate: true, scope: "audience-group-delete" }); state.groupID = null; await load(); }
      catch (error) { const detail = errorState(error); showNotice(detail.message, true); }
    });
    byID("prevBtn").addEventListener("click", () => { state.page--; render(); });
    byID("nextBtn").addEventListener("click", () => { state.page++; render(); });

    const packageModal = byID("packageModal");
    byID("createPackageBtn").addEventListener("click", () => {
      byID("packageCreateName").value = "";
      byID("packageCreateTemplate").innerHTML = state.templates.map((item) => `<option value="${escapeHTML(item.key)}">${escapeHTML(item.key)}</option>`).join("");
      packageModal.hidden = false;
      byID("packageCreateName").focus();
    });
    byID("cancelPackageBtn").addEventListener("click", () => { packageModal.hidden = true; });
    byID("packageForm").addEventListener("submit", async (event) => {
      event.preventDefault();
      const name = byID("packageCreateName").value.trim();
      const templateKey = byID("packageCreateTemplate").value;
      if (!name || !templateKey) return;
      try {
        const result = await request(`${API}/ai-audience/packages`, { method: "POST", mutate: true, scope: "audience-package-create", body: { name, group_id: state.groupID, template_key: templateKey } });
        window.location.href = `/admin/automation-conversion/packages/${result.package.id}`;
      } catch (error) { const detail = errorState(error); showNotice(detail.message, true); }
    });
    await load();
  }

  async function bootDetail() {
    if (!byID("packageTitle")) return;
    const match = window.location.pathname.match(/^\/admin\/automation-conversion\/packages\/([1-9][0-9]*)$/);
    if (!match) { setCapability("人群包路径无效。", "unknown"); return; }
    const packageID = Number(match[1]);
    const state = { pkg: null, config: null, binding: null, senders: null, snapshot: null, agents: [], policies: [], preview: null, runs: [], dependencyIssues: [], busy: false };
    let currentPanel = "basic";

    const optional = async (path, label = "") => {
      try { return await request(path); }
      catch (error) {
        if (error instanceof APIError && error.status === 404) return null;
        if (label && error instanceof APIError && (error.status === 422 || error.status === 503)) {
          state.dependencyIssues.push(`${label}（${error.code}）`);
          return null;
        }
        throw error;
      }
    };
    const panelButtons = document.querySelectorAll("[data-panel]");
    const showPanel = (key) => {
      currentPanel = key;
      panelButtons.forEach((button) => button.classList.toggle("active", button.dataset.panel === key));
      document.querySelectorAll(".ai-panel").forEach((panel) => panel.classList.toggle("active", panel.id === `panel-${key}`));
      byID("saveCurrentDimensionBtn").textContent = ["members", "records", "policies"].includes(key) ? "刷新列表" : "保存当前维度";
      byID("manualRefreshBtn").hidden = key === "records";
      if (key === "members") void loadMembers();
      if (key === "records") void loadRuns();
      if (key === "policies") void loadPolicies();
    };
    panelButtons.forEach((button) => button.addEventListener("click", () => showPanel(button.dataset.panel)));

    function renderSummary() {
      const pkg = state.pkg;
      byID("packageTitle").textContent = pkg?.name || "人群包配置";
      const memberCount = state.snapshot?.member_count ?? pkg?.member_count;
      const publishedAt = state.snapshot?.published_at || state.snapshot?.reference_time || pkg?.published_at || pkg?.reference_time;
      byID("summaryCount").textContent = memberCount === null || memberCount === undefined ? "尚无快照" : Number(memberCount).toLocaleString("zh-CN");
      byID("summaryRefresh").textContent = formatTime(publishedAt);
      byID("summaryMode").textContent = state.config?.refresh_cron_utc ? `计划 ${state.config.refresh_cron_utc}` : "手动";
      byID("summaryStatus").textContent = lifecycleLabel(pkg?.lifecycle);
      byID("packageNameInput").value = pkg?.name || "";
      byID("packageDefinitionInput").value = state.config?.definition ? JSON.stringify(state.config.definition, null, 2) : "";
      byID("dailySelect").value = state.config?.refresh_cron_utc ? "daily_0200" : "off";
      byID("incrementalSelect").value = "off";
      const immutable = pkg?.lifecycle === "archived" || pkg?.lifecycle === "active";
      document.querySelectorAll("#panel-basic input,#panel-basic textarea,#panel-basic select,#panel-basic button,#panel-automation button,#panel-automation select,#panel-senders input,#panel-senders button").forEach((node) => { node.disabled = immutable; });
      byID("manualRefreshBtn").disabled = pkg?.lifecycle === "archived";
    }

    function renderGroups(groups) {
      byID("packageGroupSelect").innerHTML = `<option value="">未分组</option>${(groups || []).map((group) => `<option value="${group.id}"${state.pkg?.group_id === group.id ? " selected" : ""}>${escapeHTML(group.name)}</option>`).join("")}`;
    }

    function renderTemplates(templates) {
      byID("templateSelect").innerHTML = (templates || []).map((item) => `<option value="${escapeHTML(item.key)}"${item.available ? "" : " disabled"}>${escapeHTML(item.key)}${item.available ? "" : ` · ${escapeHTML(item.unavailable_reason)}`}</option>`).join("");
      byID("templateParameterForm").innerHTML = `<p class="ai-label">闭集 AST 以 JSON 形式保存；预览只返回数量、摘要与数据水位，不返回客户标识。</p>`;
      byID("templatePreviewBtn").textContent = "预览当前配置";
      byID("templateSaveBtn").textContent = "保存不可变配置版本";
    }

    function renderAgents() {
      const eligible = state.agents.filter((agent) => agent.automation_type === "fixed_script" && agent.status !== "archived");
      byID("automationCapabilitySelector").innerHTML = `<select class="ai-select" id="automationAgentSelect"><option value="">请选择已发布固定话术</option>${eligible.map((agent) => `<option value="${agent.id}"${state.binding?.agent_id === agent.id ? " selected" : ""}>${escapeHTML(agent.agent_name)} · ${escapeHTML(agent.status)}</option>`).join("")}</select><p class="ai-label">Agent 类型首版不可执行；绑定时冻结已发布版本与内容摘要。</p>`;
    }

    function renderSenders() {
      const items = state.senders?.members || [];
      byID("senderRows").innerHTML = items.length ? items.map((item) => `<tr><td>${item.sort_order}</td><td>Staff #${item.staff_id}</td><td>资格版本 ${item.eligibility_version}</td><td><span class="ai-pill">已冻结</span></td><td></td></tr>`).join("") : `<tr><td class="ai-empty" colspan="5">尚未配置发送人。只保存内部 Staff ID。</td></tr>`;
      if (!byID("senderReferenceInput")) {
        const holder = document.createElement("div");
        holder.className = "ai-field";
        holder.innerHTML = `<label class="ai-label" for="senderReferenceInput">企微成员引用（仅本次解析，每行一个）</label><textarea class="ai-textarea" id="senderReferenceInput" autocomplete="off" placeholder="输入 1–5 个成员引用；不会保存到 Segment 表或日志"></textarea>`;
        byID("senderRows").closest(".ai-table-wrap").before(holder);
      }
    }

    async function load() {
      setCapability("正在读取真实配置与持久执行状态…", "loading");
      try {
        state.dependencyIssues = [];
        const [pkgResult, groups, templates, configResult, bindingResult, senderResult, agentResult] = await Promise.all([
          request(`${API}/ai-audience/packages/${packageID}`),
          request(`${API}/ai-audience/package-groups`),
          request(`${API}/ai-audience/templates`),
          optional(`${API}/ai-audience/packages/${packageID}/configuration`, "基础配置"),
          optional(`${API}/ai-audience/packages/${packageID}/automation-binding`, "固定话术绑定"),
          optional(`${API}/ai-audience/packages/${packageID}/senders`, "发送人白名单"),
          request(`${API}/automation-agents?limit=100&offset=0`),
        ]);
        state.pkg = pkgResult.package;
        state.config = configResult?.configuration || null;
        state.binding = bindingResult?.binding || null;
        state.senders = senderResult?.sender_set || null;
        state.agents = agentResult.items || [];
        enable();
        renderGroups(groups.items);
        renderTemplates(templates.items);
        renderAgents();
        renderSenders();
        renderSummary();
        await loadMembers(true);
        try {
          const check = await request(`${API}/ai-audience/packages/${packageID}/precheck`, { method: "POST", body: {} });
          const value = check.precheck;
          setCapability(value.ready ? "执行预检通过：快照、固定话术、发送人和 Provider 均已就绪。" : `当前不可执行：${readinessMessage(value.reasons) || "条件未满足"}`, value.ready ? "ready" : "not-ready");
        } catch (error) {
          const detail = errorState(error);
          const dependencyMessage = state.dependencyIssues.length ? `部分配置读取失败：${state.dependencyIssues.join("；")}。` : "";
          setCapability(dependencyMessage || detail.message, dependencyMessage ? "not-ready" : detail.state);
        }
      } catch (error) {
        const detail = errorState(error);
        setCapability(detail.message, detail.state);
      }
    }

    async function savePackage() {
      if (!state.pkg || state.busy) return;
      state.busy = true;
      setStatus(byID("packageStatusLine"), "正在持久化业务状态、收据、审计与 Outbox…");
      try {
        const groupValue = byID("packageGroupSelect").value;
        const changed = await request(`${API}/ai-audience/packages/${packageID}`, { method: "PATCH", mutate: true, scope: "audience-package-update", body: { name: byID("packageNameInput").value.trim(), group_id: groupValue ? Number(groupValue) : null, expected_version: state.pkg.version } });
        const definition = JSON.parse(byID("packageDefinitionInput").value);
        await request(`${API}/ai-audience/packages/${packageID}/configuration`, { method: "PUT", mutate: true, scope: "audience-configuration", body: { expected_package_version: changed.package.version, refresh_cron_utc: byID("dailySelect").value === "daily_0200" ? "0 2 * * *" : "", definition } });
        setStatus(byID("packageStatusLine"), "配置已作为新不可变版本提交。", "success");
        state.preview = null;
        await load();
      } catch (error) {
        const detail = errorState(error);
        setStatus(byID("packageStatusLine"), detail.message, "error");
        if (error instanceof APIError && error.status === 409) await load();
      } finally { state.busy = false; }
    }

    async function previewAudience() {
      setStatus(byID("templateStatusLine"), "正在通过 canonical Customer Port 计算预览…");
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/preview`, { method: "POST", body: { reference_time: new Date().toISOString() } });
        const value = result.preview;
        state.audiencePreview = value;
        byID("templatePreviewBox").hidden = false;
        byID("templatePreviewBox").textContent = `${value.member_count} 人 · 成员摘要 ${value.member_digest} · 水位摘要 ${value.watermark_digest}`;
        const stale = value.watermarks?.some((item) => !item.fresh);
        setStatus(byID("templateStatusLine"), stale ? "预览成功，但存在 stale 数据水位；不可直接视为可执行。" : "预览成功；尚未物化快照。", stale ? "error" : "success");
      } catch (error) { const detail = errorState(error); setStatus(byID("templateStatusLine"), detail.message, "error"); }
    }

    async function refreshAudience() {
      if (!state.pkg || state.pkg.lifecycle === "archived") return;
      setCapability("刷新请求正在持久化并进入 River…", "loading");
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/refresh`, { method: "POST", mutate: true, scope: "audience-refresh", body: { reference_time: new Date().toISOString() } });
        const runID = result.refresh_run.id;
        for (let index = 0; index < 80; index++) {
          await new Promise((resolve) => window.setTimeout(resolve, 1500));
          const current = await request(`${API}/ai-audience/packages/${packageID}/refresh-runs/${runID}`);
          if (current.refresh_run.state === "published") { setCapability("新快照已原子发布；旧快照仍可审计回看。", "ready"); await load(); return; }
          if (current.refresh_run.state === "failed") { setCapability(`快照刷新失败：${current.refresh_run.error_code || "refresh_unavailable"}`, "unknown"); return; }
          setCapability(`持久刷新状态：${current.refresh_run.state}`, "loading");
        }
        setCapability("刷新仍在后台执行；当前页面未观察到完成，不能视为已发布。", "unknown");
      } catch (error) { const detail = errorState(error); setCapability(detail.message, detail.state); }
    }

    async function saveBinding() {
      const selected = Number(byID("automationAgentSelect")?.value || 0);
      const agent = state.agents.find((item) => item.id === selected);
      if (!agent || !state.pkg) return setStatus(byID("automationStatusLine"), "请选择已发布固定话术。", "error");
      try {
        const detailResult = await request(`${API}/automation-agents/${agent.id}`);
        const detail = detailResult.agent;
        if (!detail.published_version || !detail.published_digest) throw new APIError(422, "published_content_missing");
        await request(`${API}/ai-audience/packages/${packageID}/automation-binding`, { method: "PUT", mutate: true, scope: "audience-binding", body: { expected_version: state.pkg.version, agent_id: agent.id, published_version: detail.published_version, agent_digest: detail.published_digest } });
        setStatus(byID("automationStatusLine"), "绑定已冻结发布版本和摘要。", "success");
        await load();
      } catch (error) { const value = errorState(error); setStatus(byID("automationStatusLine"), value.message, "error"); }
    }

    async function saveSenders() {
      if (!state.pkg) return;
      const refs = (byID("senderReferenceInput")?.value || "").split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
      if (refs.length < 1 || refs.length > 5) return setStatus(byID("senderStatusLine"), "请输入 1–5 个发送人成员引用。", "error");
      try {
        await request(`${API}/ai-audience/packages/${packageID}/senders`, { method: "PUT", mutate: true, scope: "audience-senders", body: { expected_version: state.pkg.version, provider_member_references: refs } });
        byID("senderReferenceInput").value = "";
        setStatus(byID("senderStatusLine"), "成员引用已即时解析；Segment 只保存内部 Staff ID 和资格版本。", "success");
        await load();
      } catch (error) { const value = errorState(error); setStatus(byID("senderStatusLine"), value.message, "error"); }
    }

    async function loadMembers(silent = false) {
      if (!silent) setStatus(byID("memberTotal"), "读取中");
      try {
        const result = await optional(`${API}/ai-audience/packages/${packageID}/members?limit=100`);
        state.snapshot = result?.snapshot || null;
        const items = result?.items || [];
        byID("memberTotal").textContent = result ? `${result.snapshot.member_count} 人` : "尚无快照";
        byID("memberRows").innerHTML = items.length ? items.map((item) => `<tr><td>Customer #${item.customer_id}</td><td><span class="ai-pill${item.identity_disposition === "resolved" ? "" : " gray"}">${escapeHTML(item.identity_disposition)}</span></td><td>${formatTime(item.entered_at)}</td></tr>`).join("") : `<tr><td class="ai-empty" colspan="3">${result ? "当前快照为空" : "尚未发布人群快照"}</td></tr>`;
        renderSummary();
      } catch (error) { const detail = errorState(error); byID("memberRows").innerHTML = `<tr><td class="ai-empty" colspan="3">${escapeHTML(detail.message)}</td></tr>`; }
    }

    async function loadRuns() {
      setStatus(byID("sendRecordStatusLine"), "正在读取持久运行与收件人效果状态…");
      try {
        const result = await request(`${API}/automation-runs?limit=100`);
        state.runs = (result.items || []).filter((run) => run.package_id === packageID);
        byID("sendRecordTotal").textContent = `${state.runs.length} 次运行`;
        byID("sendRecordRows").innerHTML = state.runs.length ? state.runs.map((run) => `<tr><td>#${run.id}</td><td><span class="ai-pill${run.state === "outcome_unknown" ? " gray" : ""}">${runStateLabel(run.state)}</span>${run.ai_plan_state ? `<div class="ai-label">AI：${escapeHTML(run.ai_plan_state)}</div>` : ""}</td><td>${run.target_count} / ${run.skipped_count}</td><td>${formatTime(run.created_at)}</td><td>${run.outcome_unknown_count || 0}</td><td>${run.ai_plan_id ? `<a class="ai-btn soft" href="/admin/cloud-orchestrator/plans/${encodeURIComponent(run.ai_plan_id)}">进入 AI 审阅与收件人</a>` : `<button class="ai-btn soft" data-run-id="${run.id}">查看收件人</button>`}</td></tr>`).join("") : `<tr><td class="ai-empty" colspan="7">尚无真实运行记录</td></tr>`;
        byID("sendRecordRows").querySelectorAll("[data-run-id]").forEach((node) => node.addEventListener("click", () => loadRecipients(Number(node.dataset.runId))));
        setStatus(byID("sendRecordStatusLine"), "accepted / queued / Provider 接受 / 送达证明 / 未知结果分别展示。", "success");
      } catch (error) { const detail = errorState(error); setStatus(byID("sendRecordStatusLine"), detail.message, "error"); }
    }

    async function loadRecipients(runID) {
      try {
        const result = await request(`${API}/automation-runs/${runID}/recipients?limit=100`);
        byID("sendRecordDrawerSubtitle").textContent = `运行 #${runID} · ${result.items?.length || 0} 个收件人`;
        byID("sendRecordMeta").innerHTML = (result.items || []).map((item) => `<div class="ai-mini"><div class="label">Customer #${item.customer_id} · Staff #${item.sender_staff_id}</div><div class="value">${escapeHTML(runStateLabel(item.state))}</div>${item.effect_id ? `<div>Effect ${escapeHTML(item.effect_id)}</div>` : ""}${item.state === "outcome_unknown" && item.effect_id ? `<button class="ai-btn soft" data-reconcile-effect="${escapeHTML(item.effect_id)}">读取对账 fence</button>` : ""}</div>`).join("") || `<div class="ai-empty">暂无收件人</div>`;
        byID("sendRecordContentDetail").innerHTML = `<div class="ai-status-line">消息正文、渠道身份与 Provider 原始响应不会在运行详情中持久化或展示。</div>`;
        byID("sendRecordMeta").querySelectorAll("[data-reconcile-effect]").forEach((node) => node.addEventListener("click", () => showReconciliation(runID, node.dataset.reconcileEffect)));
        byID("sendRecordDrawerMask").style.display = "block";
        byID("sendRecordDrawer").style.display = "block";
        byID("sendRecordDrawer").setAttribute("aria-hidden", "false");
      } catch (error) { const detail = errorState(error); setStatus(byID("sendRecordStatusLine"), detail.message, "error"); }
    }

    async function showReconciliation(runID, effectID) {
      const holder = byID("sendRecordContentDetail");
      holder.innerHTML = `<div class="ai-status-line">正在读取精确 generation、fence 与已过期 lease…</div>`;
      try {
        const response = await request(`${API}/automation-runs/${runID}/effects/${encodeURIComponent(effectID)}/reconciliation-candidate`);
        const candidate = response.data;
        holder.innerHTML = `<div class="ai-field"><label class="ai-label">对账对象</label><div>Effect ${escapeHTML(effectID)} · generation ${candidate.generation} · fence ${candidate.fence}</div><div class="ai-label">lease ${escapeHTML(formatTime(candidate.lease_expires_at))}</div></div><div class="ai-field"><label class="ai-label" for="reconcileEvidenceDigest">证据摘要（64 位小写 SHA-256）</label><input class="ai-input" id="reconcileEvidenceDigest" maxlength="64" autocomplete="off"></div><div class="ai-field"><label class="ai-label" for="reconcileResolution">核验结论</label><select class="ai-select" id="reconcileResolution"><option value="delivery_proven">已证明送达</option><option value="provider_accepted">Provider 已接受</option><option value="final_failed">最终失败</option></select></div><button class="ai-btn primary" id="confirmReconciliationBtn">提交带 fence 的人工对账</button><div class="ai-status-line" id="reconciliationStatusLine">只记录摘要和结论，不保存 Provider 原始响应。</div>`;
        byID("confirmReconciliationBtn").addEventListener("click", async () => {
          const evidence = byID("reconcileEvidenceDigest").value.trim();
          if (!/^[0-9a-f]{64}$/.test(evidence)) return setStatus(byID("reconciliationStatusLine"), "证据摘要必须是 64 位小写 SHA-256。", "error");
          byID("confirmReconciliationBtn").disabled = true;
          try {
            await request(`${API}/automation-runs/${runID}/effects/${encodeURIComponent(effectID)}/reconcile`, { method: "POST", mutate: true, scope: "automation-effect-reconcile", body: { generation: candidate.generation, fence: candidate.fence, lease_expires_at: candidate.lease_expires_at, evidence_digest: evidence, resolution: byID("reconcileResolution").value } });
            setStatus(byID("reconciliationStatusLine"), "对账证据、Automation 投影与 EER receipt 已在同一事务提交。", "success");
            await loadRecipients(runID);
            await loadRuns();
          } catch (error) {
            const detail = errorState(error);
            setStatus(byID("reconciliationStatusLine"), detail.message, "error");
            byID("confirmReconciliationBtn").disabled = false;
          }
        });
      } catch (error) {
        const detail = errorState(error);
        holder.innerHTML = `<div class="ai-status-line" data-state="${escapeHTML(detail.state)}">${escapeHTML(detail.message)}</div>`;
      }
    }

    async function createBroadcastPreview() {
      state.preview = null;
      byID("broadcastConfirmBtn").disabled = true;
      setStatus(byID("broadcastPreviewState"), "正在冻结快照、内容与发送人版本…");
      try {
        state.preview = await request(`${API}/ai-audience/packages/${packageID}/broadcast-previews`, { method: "POST", body: {} });
        setStatus(byID("broadcastPreviewState"), `快照 #${state.preview.snapshot_id} · 目标 ${state.preview.target_count} · 跳过 ${state.preview.skipped_count} · 摘要 ${state.preview.preview_digest}`, "success");
        byID("broadcastConfirmBtn").disabled = false;
      } catch (error) { const detail = errorState(error); setStatus(byID("broadcastPreviewState"), detail.message, "error"); }
    }

    async function confirmBroadcast() {
      if (!state.preview) return;
      byID("broadcastConfirmBtn").disabled = true;
      try {
        const result = await request(`${API}/ai-audience/packages/${packageID}/runs`, { method: "POST", mutate: true, scope: "automation-run-confirm", body: { snapshot_id: state.preview.snapshot_id, agent_id: state.preview.agent_id, agent_published_version: state.preview.agent_published_version, preview_digest: state.preview.preview_digest, expected_package_version: state.preview.expected_package_version } });
        setStatus(byID("broadcastPreviewState"), `持久运行 #${result.run.id} 已创建，状态 ${runStateLabel(result.run.state)}；这不是送达证明。`, "success");
        state.preview = null;
        await loadRuns();
      } catch (error) { const detail = errorState(error); setStatus(byID("broadcastPreviewState"), detail.message, "error"); if (error instanceof APIError && error.status === 409) state.preview = null; }
    }

    async function loadPolicies() {
      setStatus(byID("policyStatusLine"), "正在读取策略版本…");
      try {
        const list = await request(`${API}/automations`);
        const details = await Promise.all((list.items || []).map((item) => optional(`${API}/automations/${item.id}`)));
        state.policies = details.map((item) => item?.data).filter((item) => item?.version?.package_id === packageID);
        byID("policyRows").innerHTML = state.policies.length ? state.policies.map(({ policy, version }) => `<tr><td>${escapeHTML(policy.name)}<br><span class="ai-label">${escapeHTML(policy.code)}</span></td><td>${escapeHTML(version.trigger_kind)}<br>${escapeHTML(version.action_kind)}</td><td>策略 v${policy.version} · 配置 v${version.version}</td><td>${lifecycleLabel(policy.lifecycle)}</td><td><div class="ai-btns"><button class="ai-btn" data-policy-action="${policy.lifecycle === "active" ? "pause" : "activate"}" data-policy-id="${policy.id}" data-policy-version="${policy.version}">${policy.lifecycle === "active" ? "暂停" : "激活"}</button><button class="ai-btn danger" data-policy-action="archive" data-policy-id="${policy.id}" data-policy-version="${policy.version}">归档</button></div></td></tr>`).join("") : `<tr><td class="ai-empty" colspan="5">尚无策略</td></tr>`;
        byID("policyRows").querySelectorAll("[data-policy-action]").forEach((node) => node.addEventListener("click", () => transitionPolicy(Number(node.dataset.policyId), Number(node.dataset.policyVersion), node.dataset.policyAction)));
        setStatus(byID("policyStatusLine"), state.policies.length ? "策略读取完成。打标触发器在正式生产者接入前保持 disabled。" : "可创建暂停策略；需预检后人工激活。", "success");
      } catch (error) { const detail = errorState(error); setStatus(byID("policyStatusLine"), detail.message, "error"); }
    }

    async function createPolicy() {
      const quiet = byID("policyQuietHoursInput").value.match(/^([0-2][0-9]:[0-5][0-9])-([0-2][0-9]:[0-5][0-9])$/);
      const action = byID("policyActionSelect").value;
      const agentID = Number(byID("automationAgentSelect")?.value || state.binding?.agent_id || 0);
      if (!quiet || (action === "outbound_message" && !agentID)) return setStatus(byID("policyStatusLine"), "请提供有效安静时段，并为发送动作选择固定话术。", "error");
      const body = { code: byID("policyCodeInput").value.trim(), name: byID("policyNameInput").value.trim(), package_id: packageID, trigger: byID("policyTriggerSelect").value, action, action_config: action === "outbound_message" ? { agent_id: agentID } : { record_type: "audience_member_entered" }, quiet_hours: { timezone: byID("policyTimezoneInput").value.trim(), start: quiet[1], end: quiet[2] }, single_run_limit: Number(byID("policyLimitInput").value), expected_version: 0 };
      try { await request(`${API}/automations`, { method: "POST", mutate: true, scope: "automation-policy-create", body }); setStatus(byID("policyStatusLine"), "暂停策略及不可变版本已创建。", "success"); await loadPolicies(); }
      catch (error) { const detail = errorState(error); setStatus(byID("policyStatusLine"), detail.message, "error"); }
    }

    async function transitionPolicy(id, version, action) {
      if (action === "archive" && !window.confirm("归档该策略？")) return;
      try { await request(`${API}/automations/${id}/${action}`, { method: "POST", mutate: true, scope: `automation-policy-${action}`, body: { expected_version: version } }); await loadPolicies(); }
      catch (error) { const detail = errorState(error); setStatus(byID("policyStatusLine"), detail.message, "error"); }
    }

    byID("savePackageBtn").addEventListener("click", savePackage);
    byID("templateSaveBtn").addEventListener("click", savePackage);
    byID("templatePreviewBtn").addEventListener("click", previewAudience);
    byID("manualRefreshBtn").addEventListener("click", refreshAudience);
    byID("refreshMembersBtn").addEventListener("click", () => loadMembers());
    byID("saveAutomationBtn").addEventListener("click", saveBinding);
    byID("unbindAutomationBtn").addEventListener("click", async () => {
      if (!state.binding || !state.pkg || !window.confirm("解除当前固定话术绑定？历史版本仍会保留用于审计。")) return;
      try {
        await request(`${API}/ai-audience/packages/${packageID}/automation-binding?expected_version=${state.pkg.version}`, { method: "DELETE", mutate: true, scope: "audience-binding-delete" });
        setStatus(byID("automationStatusLine"), "绑定已解除，历史冻结版本仍保留。", "success");
        await load();
      } catch (error) { const detail = errorState(error); setStatus(byID("automationStatusLine"), detail.message, "error"); }
    });
    byID("addSenderBtn").addEventListener("click", () => byID("senderReferenceInput")?.focus());
    byID("saveSendersBtn").addEventListener("click", saveSenders);
    byID("broadcastPreviewBtn").addEventListener("click", createBroadcastPreview);
    byID("broadcastConfirmBtn").addEventListener("click", confirmBroadcast);
    byID("createPolicyBtn").addEventListener("click", createPolicy);
    byID("saveCurrentDimensionBtn").addEventListener("click", () => ({ basic: savePackage, automation: saveBinding, senders: saveSenders, members: loadMembers, records: loadRuns, policies: loadPolicies })[currentPanel]?.());
    const closeDrawer = () => { byID("sendRecordDrawerMask").style.display = "none"; byID("sendRecordDrawer").style.display = "none"; byID("sendRecordDrawer").setAttribute("aria-hidden", "true"); };
    byID("closeSendRecordDrawerBtn").addEventListener("click", closeDrawer);
    byID("sendRecordDrawerMask").addEventListener("click", closeDrawer);
    showPanel("basic");
    await load();
  }

  document.addEventListener("DOMContentLoaded", () => { void bootList(); void bootDetail(); });
})();
