(() => {
  "use strict";
  const root = document.querySelector("[data-runtime-release-host]");
  const page = document.body?.dataset?.runtimeConfigPage;
  if (!root || (page !== "runtimeConfigCenter" && page !== "runtimeConfigCategory")) return;
  // dd8d60d's receipt-bound Config Center stylesheet defines the cc-* layout.
  // This V3 host preserves its table/detail skeleton while owning data and
  // commands through the closed runtime-release API.
  root.classList.add("cc-page");

  const catalogAPI = "/api/admin/config/runtime-catalog";
  const releaseAPI = "/api/admin/config/runtime-releases";
  const text = (value, fallback = "-") => value === null || value === undefined || value === "" ? fallback : String(value);
  const element = (tag, className, value) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (value !== undefined) node.textContent = String(value);
    return node;
  };
  const button = (label, kind = "ghost") => {
    const node = element("button", `admin-button admin-button--${kind} cc-btn${kind === "primary" ? " cc-btn-primary" : ""}`, label);
    node.type = "button";
    return node;
  };
  const csrf = () => {
    for (const item of String(document.cookie || "").split(";")) {
      const [name, ...value] = item.trim().split("=");
      if (name === "aicrm_admin_csrf" || name === "aicrm_csrf") {
        try { return decodeURIComponent(value.join("=")); } catch (_) { return ""; }
      }
    }
    return "";
  };
  const requestID = () => globalThis.crypto?.randomUUID?.() || `config-center-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const request = async (url, init = {}) => {
    const response = await fetch(url, { credentials: "same-origin", cache: "no-store", ...init, headers: { Accept: "application/json", ...(init.headers || {}) } });
    let payload = null;
    try { payload = await response.json(); } catch (_) {}
    if (!response.ok) {
      throw new Error(payload?.error === "runtime_release_conflict" ? "发布版本已变化，请重新读取后再保存。" : `配置操作失败（${response.status}）`);
    }
    return payload;
  };
  const writeHeaders = () => ({ Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": csrf(), "Idempotency-Key": requestID() });
  const clear = () => root.replaceChildren();
  const addStatus = (parent) => {
    const node = element("section", "cc-alert admin-muted");
    node.dataset.configCenterStatus = "";
    parent.append(node);
    return node;
  };
  const status = (message, kind = "") => {
    const node = root.querySelector("[data-config-center-status]");
    if (!node) return;
    node.textContent = message;
    node.className = kind ? `cc-alert is-${kind} admin-alert admin-alert--${kind}` : "cc-alert admin-muted";
  };
  // json.RawMessage is serialized by Go as its native JSON primitive. Parsing a
  // string a second time turns values such as "limited" or an AppID into
  // undefined, and a later save would overwrite the real effective value.
  const decode = (raw) => raw;
  const categoryURL = (key) => `/admin/configDetail.html?cat=${encodeURIComponent(key)}`;
  const releaseURL = (id) => `/admin/config/releases/${encodeURIComponent(String(id))}`;
  const categoryKey = () => new URL(location.href).searchParams.get("cat") || "";
  const catalog = () => request(catalogAPI);
  const releases = () => request(releaseAPI);
  const rolesFor = (field) => Array.isArray(field?.required_roles) ? field.required_roles : [];
  const roleLabel = (role) => ({
    api: "管理接口服务",
    worker: "后台任务服务",
    "effects-worker": "受控执行服务",
  }[role] || "相应服务");
  const categoryState = (category, model) => {
    if (category.disabled) return { label: "不支持", detail: category.disabled };
    if (category.managed_url) return { label: "请在对应管理页维护", detail: "此分类由专门的管理页面维护。" };
    const effective = model.effective || {};
    if (effective.source !== "published" || !Number.isInteger(effective.revision) || effective.revision < 1) {
      return { label: "受保护环境默认", detail: "尚未发布版本；不能显示为已生效。" };
    }
    const required = new Set();
    for (const field of category.fields || []) for (const role of rolesFor(field)) required.add(role);
    if (!required.size) return { label: "没有可发布字段", detail: "当前没有可以在此发布的运行时字段。" };
    const seen = new Set((model.applications || []).filter((item) => item?.revision === effective.revision && item?.source === "published" && item?.snapshot_checksum === effective.checksum).map((item) => item.role));
    const missing = [...required].filter((role) => !seen.has(role));
    if (missing.length) return { label: "已发布，等待服务读取", detail: `版本 #${effective.revision} 尚未由 ${missing.map(roleLabel).join("、")} 启动读取。` };
    return { label: "已发布并已读取", detail: `版本 #${effective.revision} 已由所需服务读取。` };
  };
  const card = (heading, copy) => {
    const section = element("section", "admin-card cc-card");
    const header = element("div", "cc-card-h");
    header.append(element("h2", "", heading));
    section.append(header, element("p", "admin-muted", copy));
    return section;
  };

  const showCenter = async () => {
    clear();
    const intro = card("配置中心", "沿用旧版分类和操作顺序：先保存草稿、校验、再发布。密钥只显示受保护引用；发布不会直接改环境、重启服务或发送业务请求。");
    const history = button("查看发布记录", "ghost");
    history.addEventListener("click", () => location.assign("/admin/config/releases"));
    intro.append(history, addStatus(intro)); root.append(intro);
    const table = element("table", "admin-table cc-table cc-category-table");
    table.innerHTML = "<thead><tr><th>分类</th><th>说明</th><th>字段</th><th>发布/应用状态</th><th>操作</th></tr></thead>";
    const rows = document.createElement("tbody"); table.append(rows);
    const wrap = element("div", "admin-table-wrap cc-table-wrap"); wrap.append(table); root.append(wrap);
    try {
      const model = await catalog();
      for (const category of model.categories || []) {
        const state = categoryState(category, model);
        const row = document.createElement("tr");
        row.append(element("td", "", category.label), element("td", "", category.group), element("td", "", String((category.fields || []).length)));
        const stateCell = document.createElement("td");
        stateCell.append(element("strong", "", state.label), element("div", "admin-muted", state.detail)); row.append(stateCell);
        const action = document.createElement("td");
        if (category.managed_url) {
          const link = document.createElement("a"); link.className = "admin-button admin-button--ghost"; link.href = category.managed_url; link.textContent = "打开管理页面"; action.append(link);
        } else if (category.disabled) {
          action.append(element("span", "admin-muted", "不可配置"));
        } else {
          const edit = button("查看与配置", "ghost"); edit.addEventListener("click", () => location.assign(categoryURL(category.key))); action.append(edit);
        }
        row.append(action); rows.append(row);
      }
    } catch (error) { status(error instanceof Error ? error.message : "配置中心不可用", "error"); }
  };

  const inputFor = (field, value) => {
    if (field.input === "secret-reference") {
      const row = element("div", "admin-form-field");
      row.append(element("strong", "", field.label), element("code", "", field.secret_reference), element("p", "admin-muted", field.configured === true ? "已配置：受保护引用只在受控部署应用时解析；页面不读取或保存密钥。" : "缺失：该受保护引用在当前启动快照中未配置。页面不读取或保存密钥。"));
      return row;
    }
    if (field.input === "protected") {
      const row = element("div", "admin-form-field"); row.append(element("strong", "", field.label), element("p", "admin-muted", field.unsupported)); return row;
    }
    if (field.input === "deployment") {
      const row = element("div", "admin-form-field"); row.append(element("strong", "", field.label), element("p", "admin-muted", field.unsupported)); return row;
    }
    if (field.input === "unsupported") {
      const row = element("div", "admin-form-field"); row.append(element("strong", "", field.label), element("p", "admin-muted", field.unsupported)); return row;
    }
    const row = element("label", "admin-form-field"); row.append(element("span", "", field.label));
    let input;
    if (field.input === "boolean") {
      input = document.createElement("input"); input.type = "checkbox"; input.checked = value === true;
    } else if (field.input === "number") {
      input = document.createElement("input"); input.type = "number"; input.value = Number.isFinite(value) ? String(value) : ""; input.required = true;
    } else if (String(field.input || "").startsWith("select:")) {
      input = document.createElement("select");
      for (const optionValue of field.input.slice("select:".length).split(",")) {
        const option = document.createElement("option"); option.value = optionValue; option.textContent = optionValue; option.selected = optionValue === value; input.append(option);
      }
    } else {
      input = document.createElement("input"); input.type = "text"; input.value = typeof value === "string" ? value : ""; input.maxLength = 256;
    }
    if (field.input === "scope-bound" && typeof value === "string" && value !== "") {
      input.readOnly = true;
      input.setAttribute("aria-readonly", "true");
    }
    input.dataset.runtimeSetting = field.key; row.append(input);
    if (field.input === "scope-bound") row.append(element("small", "admin-muted", field.unsupported));
    row.append(element("small", "admin-muted", field.application === "immediate" ? "发布后由相应服务读取；仍保留发布记录。" : "发布后需重启相应服务读取此版本。"));
    return row;
  };

  const showCategory = async () => {
    clear();
    const intro = card("配置详情", "正在读取当前配置。");
    const back = button("返回配置中心", "ghost"); back.addEventListener("click", () => location.assign("/admin/config")); intro.append(back, addStatus(intro)); root.append(intro);
    try {
      const [model, releaseModel] = await Promise.all([catalog(), releases()]);
      const category = (model.categories || []).find((item) => item?.key === categoryKey());
      if (!category) throw new Error("配置分类不存在");
      intro.querySelector("h2").textContent = category.label;
      intro.querySelector("p").textContent = category.disabled || category.managed_url ? (category.disabled || "此分类由对应管理页面维护。") : categoryState(category, model).detail;
      if (category.managed_url) {
        const link = document.createElement("a"); link.href = category.managed_url; link.className = "admin-button admin-button--primary"; link.textContent = "打开管理页面"; intro.append(link); return;
      }
      if (category.disabled) return;
      const effective = new Map((model.effective?.settings || []).map((item) => [item.key, decode(item.value)]));
      const form = element("form", "admin-form-grid admin-form-grid--stacked cc-form");
      for (const field of category.fields || []) form.append(inputFor(field, effective.get(field.key)));
      form.append(element("p", "admin-muted", "保存只创建草稿；请在发布详情完成校验和发布。未列入此页面的字段、密钥引用和不支持字段都会被拒绝。"));
      const actions = element("div", "admin-form-actions"); const save = button("保存草稿", "primary"); save.type = "submit"; actions.append(save); form.append(actions); root.append(form);
      form.addEventListener("submit", async (event) => {
        event.preventDefault(); if (!form.reportValidity()) return;
        const values = new Map((model.effective?.settings || []).map((item) => [item.key, item.value]));
        for (const input of form.querySelectorAll("[data-runtime-setting]")) {
          const field = (category.fields || []).find((item) => item.key === input.dataset.runtimeSetting);
          if (!field) continue;
          let value = input.value;
          if (field.input === "boolean") value = input.checked;
          if (field.input === "number") value = Number(input.value);
          values.set(field.key, value);
        }
        save.disabled = true;
        try {
          const draft = await request(releaseAPI, { method: "POST", headers: writeHeaders(), body: JSON.stringify({ expected_base_revision: releaseModel.runtime_releases?.active_revision || 0, settings: [...values.entries()].map(([key, value]) => ({ key, value })), admin_action_token: releaseModel.admin_action_token }) });
          location.assign(releaseURL(draft.runtime_release.id));
        } catch (error) { status(error instanceof Error ? error.message : "保存草稿失败", "error"); save.disabled = false; }
      });
    } catch (error) { status(error instanceof Error ? error.message : "配置详情不可用", "error"); }
  };
  if (page === "runtimeConfigCategory") void showCategory(); else void showCenter();
})();
