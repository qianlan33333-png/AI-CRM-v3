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
  const requiredList = (value, label, convert) => {
    const values = Array.isArray(value) ? value : [];
    if (!values.length) throw new Error(`${label}不能为空。`);
    return values.map((item) => convert(item, label));
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
      parameters.product_codes = requiredList(parameters.products, "商品", canonicalCode);
      delete parameters.products;
      parameters.paid_at_from = parameters.paid_at_from || "";
      parameters.paid_at_to = parameters.paid_at_to || "";
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
    const state = { package: null, configuration: null, templates: [] };
    const setStatus = (message, kind = "") => { status.textContent = message; status.dataset.state = kind; };
    const templateFor = () => state.templates.find((item) => item.key === select.value);

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
	  const unresolvedReferences = (template?.fields || []).filter((field) => field.reference).map((field) => field.label);
	  if (unresolvedReferences.length) setStatus(`${unresolvedReferences.join("、")}当前仅接受 V3 稳定 ID/code；精确标题解析尚未由对应 Owner 提供。`, "not-ready");
    }
    async function load() {
      const [pkg, templates, configuration] = await Promise.all([
        request(`${api}/packages/${id}`), request(`${api}/templates`), request(`${api}/packages/${id}/configuration`),
      ]);
      state.package = pkg.package;
      state.templates = templates.items || [];
      state.configuration = configuration.configuration;
      select.replaceChildren();
      state.templates.forEach((template) => {
        const option = document.createElement("option");
        option.value = template.key;
        option.textContent = `${template.label} · v${template.template_version}`;
        option.disabled = !template.available;
        select.appendChild(option);
      });
      const current = state.configuration?.definition?.template_key;
      select.value = state.templates.some((template) => template.key === current) ? current : state.templates[0]?.key || "";
      await render();
    }
    async function persist(andPreview) {
      const template = templateFor();
      if (!template) throw new Error("请选择模板。");
      const definition = canonicalDefinition(template.key, form.getValue());
      setStatus(andPreview ? "正在按当前表单保存并预览…" : "正在保存表单配置…");
      const saved = await request(`${api}/packages/${id}/configuration`, { method: "PUT", mutate: true, body: { expected_package_version: state.package.version, refresh_cron_utc: state.configuration?.refresh_cron_utc || "", definition } });
      state.configuration = saved.configuration;
      state.package = (await request(`${api}/packages/${id}`)).package;
      await render();
      if (!andPreview) { setStatus("配置已保存为新的不可变版本。", "success"); return; }
      const result = await request(`${api}/packages/${id}/preview`, { method: "POST", body: { reference_time: new Date().toISOString() } });
      const preview = result.preview;
      previewBox.hidden = false;
      previewBox.textContent = `${preview.member_count} 人 · 成员摘要 ${preview.member_digest} · 水位摘要 ${preview.watermark_digest}`;
      setStatus("当前表单已按相同规则保存并完成预览。", "success");
    }
    document.addEventListener("click", (event) => {
      const target = event.target;
      if (target !== previewButton && target !== saveButton && target !== byID("savePackageBtn")) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      persist(target === previewButton).catch((error) => setStatus(error.message || "表单操作失败。", "error"));
    }, true);
    select.addEventListener("change", () => { previewBox.hidden = true; render().catch((error) => setStatus(error.message, "error")); });
    try {
      await load();
      // The existing detail adapter can finish after this Host during a slow
      // API response. Reapply the frozen form once without taking over it.
      window.setTimeout(() => { if (!root.querySelector("[data-field-name]")) render().catch(() => {}); }, 120);
    } catch (error) { setStatus(error.message || "模板表单无法加载。", "error"); }
  }

  document.addEventListener("DOMContentLoaded", () => { window.setTimeout(() => { void start(); }, 0); });
})();
