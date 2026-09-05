// v3 Host for the byte-frozen dd8 TemplateParameterForm controller. It owns
// only API adaptation and closed-AST conversion; the donor control renderer is
// neither forked nor modified here.
(() => {
  "use strict";

  const api = "/api/admin/ai-audience";
  const byID = (id) => document.getElementById(id);
  const packageID = () => {
    const match = window.location.pathname.match(/\/packages\/(\d+)$/);
    return match ? Number(match[1]) : 0;
  };
  const csrf = () => document.cookie.split(";").map((part) => part.trim()).map((part) => part.split("=")).find(([name]) => name === "aicrm_admin_csrf" || name === "aicrm_csrf")?.[1] || "";
  const key = (scope) => `${scope}-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`}`;

  async function request(path, options = {}) {
    const headers = new Headers({ Accept: "application/json" });
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (options.mutate) {
      headers.set("X-CSRF-Token", csrf());
      headers.set("Idempotency-Key", key("audience-template-host"));
    }
    const response = await fetch(path, { method: options.method || "GET", credentials: "same-origin", cache: "no-store", headers, body: options.body === undefined ? undefined : JSON.stringify(options.body) });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || `HTTP ${response.status}`);
    return payload;
  }

  const canonicalPositiveID = (value, label) => {
    const text = String(value || "");
    if (!/^[1-9]\d*$/.test(text)) throw new Error(`${label}只接受当前 V3 的稳定正整数 ID；标题解析尚未由对应 Owner 提供。`);
    return text;
  };
  const canonicalCode = (value, label) => {
    const text = String(value || "");
    if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$/.test(text)) throw new Error(`${label}只接受当前 V3 的稳定 code；精确标题解析尚未由对应 Owner 提供。`);
    return text;
  };
  const canonicalProductReference = (value, label) => {
    const text = String(value || "");
    if (text.trim() !== text || !text || text.length > 80) throw new Error(`${label}必须是当前 V3 的稳定 code 或精确商品标题。`);
    return text;
  };
  const requiredList = (value, label, convert) => {
    const values = Array.isArray(value) ? value : [];
    if (!values.length) throw new Error(`${label}不能为空。`);
    return values.map((item) => convert(item, label));
  };
  const canonicalTimestamp = (value, label) => {
    if (value === null || value === undefined || value === "") return "";
    const parsed = new Date(String(value));
    if (Number.isNaN(parsed.getTime())) throw new Error(`${label}必须是有效日期时间。`);
    return parsed.toISOString();
  };

  function canonicalDefinition(templateKey, value) {
    const parameters = { ...value };
    if (parameters.owner_scope === "all") parameters.owner_userids = [];
    if (templateKey === "questionnaire_choice_answers") {
      parameters.questionnaire_id = canonicalPositiveID(parameters.questionnaire, "问卷");
      delete parameters.questionnaire;
      const conditions = Array.isArray(parameters.conditions) ? parameters.conditions : [];
      if (!conditions.length) throw new Error("至少需要一个题目条件。");
      parameters.conditions = conditions.map((item) => ({
        question_id: canonicalPositiveID(item.question, "题目"),
        option_ids: requiredList(item.options, "选项", canonicalPositiveID),
      }));
    } else if (templateKey === "paid_order") {
      parameters.product_codes = requiredList(parameters.products, "商品", canonicalProductReference);
      delete parameters.products;
      parameters.paid_at_from = canonicalTimestamp(parameters.paid_at_from, "支付时间起点");
      parameters.paid_at_to = canonicalTimestamp(parameters.paid_at_to, "支付时间终点");
    } else if (templateKey === "channel_entry") {
      parameters.channel_codes = requiredList(parameters.channels, "渠道", canonicalCode);
      delete parameters.channels;
    } else if (templateKey === "radar_first_click_elapsed") {
      parameters.radar_ids = requiredList(parameters.radars, "雷达", canonicalPositiveID);
      delete parameters.radars;
    }
    delete parameters.owner_staff_ids;
    return { schema_version: 1, template_key: templateKey, parameters };
  }

  function editableParameters(templateKey, parameters) {
    const value = { ...(parameters || {}) };
	if (value.owner_scope === undefined) value.owner_scope = "all";
	if (value.owner_userids === undefined) value.owner_userids = [];
    if (templateKey === "questionnaire_choice_answers") {
      value.questionnaire = value.questionnaire_id || "";
      value.conditions = (value.conditions || []).map((item) => ({ question: item.question_id || "", options: item.option_ids || [] }));
    } else if (templateKey === "paid_order") {
      value.products = value.product_codes || [];
    } else if (templateKey === "channel_entry") {
      value.channels = value.channel_codes || [];
    } else if (templateKey === "radar_first_click_elapsed") {
      value.radars = value.radar_ids || [];
    }
    return value;
  }

  async function start() {
    const id = packageID();
    const root = byID("templateParameterForm");
    if (!id || !root || !window.TemplateParameterForm) return;
    const select = byID("templateSelect");
    const previewButton = byID("templatePreviewBtn");
    const saveButton = byID("templateSaveBtn");
    const status = byID("templateStatusLine");
    const previewBox = byID("templatePreviewBox");
    const form = window.TemplateParameterForm.create(root);
    const legacyDefinition = byID("packageDefinitionInput");
    if (legacyDefinition?.closest(".ai-field")) legacyDefinition.closest(".ai-field").hidden = true;
    const state = { package: null, configuration: null, templates: [], selectedTemplate: "", ready: false, restoring: false };
    const setStatus = (message, kind = "") => { status.textContent = message; status.dataset.state = kind; };
    const templateFor = () => state.templates.find((item) => item.key === state.selectedTemplate);

    function renderTemplateOptions() {
      const stored = state.configuration?.definition?.template_key;
      const fallback = state.templates.some((template) => template.key === state.selectedTemplate)
        ? state.selectedTemplate
        : (state.templates.some((template) => template.key === stored) ? stored : state.templates[0]?.key || "");
      state.selectedTemplate = fallback;
      select.replaceChildren();
      state.templates.forEach((template) => {
        const option = document.createElement("option");
        option.value = template.key;
        option.textContent = `${template.label} · v${template.template_version}`;
        option.disabled = !template.available;
        select.appendChild(option);
      });
      select.value = fallback;
    }
    function renderRefreshMode() {
      const incremental = byID("incrementalSelect");
      const daily = byID("dailySelect");
      if (!incremental || !daily) return;
      switch (state.configuration?.refresh_mode) {
        case "every_3m": incremental.value = "incremental_3m"; daily.value = "off"; break;
        case "daily_0200": incremental.value = "off"; daily.value = "daily_0200"; break;
        case "every_3m_plus_daily_0200": incremental.value = "incremental_3m"; daily.value = "daily_0200"; break;
        case "manual": incremental.value = "off"; daily.value = "off"; break;
        // Historical arbitrary UTC cron remains visible through the existing
        // detail adapter; this Host never rewrites its time semantics.
      }
    }

    async function rehydrateOwnerUserIDs(parameters) {
      if (parameters?.owner_scope !== "specified") return parameters;
      const staff = Array.isArray(parameters.owner_staff_ids) ? parameters.owner_staff_ids : [];
      const query = new URLSearchParams();
      staff.forEach((idValue) => query.append("staff_id", idValue));
      const result = await request(`${api}/packages/${id}/owner-references?${query}`);
      return { ...parameters, owner_userids: result.owner_userids || [] };
    }
    async function render() {
      const template = templateFor();
      const stored = state.configuration?.definition;
      const source = stored?.template_key === template?.key ? await rehydrateOwnerUserIDs(stored.parameters) : {};
      const readOnly = ["active", "archived"].includes(state.package?.lifecycle);
      form.setSchema(template?.fields || [], editableParameters(template?.key, source), { readOnly });
      previewButton.disabled = readOnly || !template;
      saveButton.disabled = readOnly || !template;
      byID("templateVersionBadge").textContent = template ? `${template.label} · v${template.template_version}` : "请选择模板";
      byID("templateHistoryNote").hidden = Boolean(template);
    }
    async function load() {
      const [pkg, templates, configuration] = await Promise.all([
        request(`${api}/packages/${id}`), request(`${api}/templates`), request(`${api}/packages/${id}/configuration`),
      ]);
      state.package = pkg.package;
      state.templates = templates.items || [];
      state.configuration = configuration.configuration;
      state.ready = true;
      renderRefreshMode();
      renderTemplateOptions();
      await render();
    }
    function currentDefinition() {
      const template = templateFor();
      if (!template) throw new Error("请选择模板。");
      return canonicalDefinition(template.key, form.getValue());
    }
    function prepareDetailSave() {
      if (!legacyDefinition) throw new Error("基础配置控件不可用。");
      legacyDefinition.value = JSON.stringify(currentDefinition());
    }
    function selectedRefreshMode() {
      const incremental = byID("incrementalSelect")?.value;
      const daily = byID("dailySelect")?.value;
      if (incremental === "incremental_3m" && daily === "daily_0200") return "every_3m_plus_daily_0200";
      if (incremental === "incremental_3m") return "every_3m";
      if (daily === "daily_0200") return "daily_0200";
      return "manual";
    }
    async function save() {
      const definition = currentDefinition();
      const groupValue = byID("packageGroupSelect")?.value || "";
      const changed = await request(`${api}/packages/${id}`, { method: "PATCH", mutate: true, body: { name: byID("packageNameInput")?.value.trim() || state.package.name, group_id: groupValue ? Number(groupValue) : null, expected_version: state.package.version } });
      const saved = await request(`${api}/packages/${id}/configuration`, { method: "PUT", mutate: true, body: { expected_package_version: changed.package.version, refresh_cron_utc: "", refresh_mode: selectedRefreshMode(), definition } });
      state.package = changed.package;
      state.configuration = saved.configuration;
      await load();
      setStatus("基础配置和模板条件已保存为新的不可变版本。", "success");
    }
    async function preview() {
      const definition = currentDefinition();
      setStatus("正在按当前表单预览，未保存配置…");
      const result = await request(`${api}/packages/${id}/preview`, { method: "POST", body: { definition, reference_time: new Date().toISOString() } });
      const preview = result.preview;
      previewBox.hidden = false;
      previewBox.textContent = `${preview.member_count} 人 · 成员摘要 ${preview.member_digest} · 水位摘要 ${preview.watermark_digest}`;
      setStatus("当前表单预览完成，尚未保存配置。", "success");
    }
    document.addEventListener("click", (event) => {
      const target = event.target;
      if (target === previewButton) {
        event.preventDefault();
        event.stopImmediatePropagation();
        preview().catch((error) => setStatus(error.message || "表单操作失败。", "error"));
        return;
      }
      if (target !== saveButton && target !== byID("savePackageBtn") && target !== byID("saveCurrentDimensionBtn")) return;
	  if (target === byID("saveCurrentDimensionBtn") && !byID("panel-basic")?.classList.contains("active")) return;
      try {
        prepareDetailSave();
        event.preventDefault();
        event.stopImmediatePropagation();
        setStatus("正在保存基础配置和模板条件…");
        save().catch((error) => setStatus(error.message || "表单操作失败。", "error"));
      } catch (error) {
        event.preventDefault();
        event.stopImmediatePropagation();
        setStatus(error.message || "表单操作失败。", "error");
      }
    }, true);
    select.addEventListener("change", () => {
      state.selectedTemplate = select.value;
      previewBox.hidden = true;
      render().catch((error) => setStatus(error.message, "error"));
    });
    const observer = new MutationObserver(() => {
      if (!state.ready || state.restoring || root.querySelector("[data-field-name]")) return;
      state.restoring = true;
      queueMicrotask(() => {
        load().catch((error) => setStatus(error.message || "模板表单无法重新加载。", "error")).finally(() => { state.restoring = false; });
      });
    });
    observer.observe(root, { childList: true });
    try {
      await load();
    } catch (error) { setStatus(error.message || "模板表单无法加载。", "error"); }
  }

  document.addEventListener("DOMContentLoaded", () => { void start(); });
})();
