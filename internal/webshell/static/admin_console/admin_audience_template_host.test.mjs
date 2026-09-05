import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience_detail.html"), "utf8")
  .replace(/^\{\{define "admin_audience_detail"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const frozenController = fs.readFileSync(path.join(here, "template_parameter_form.js"), "utf8");
const detail = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const host = fs.readFileSync(path.join(here, "admin_audience_template_host.js"), "utf8");
const wait = (milliseconds = 100) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const ownerFields = [
  { name: "owner_scope", label: "负责人范围", type: "enum", required: true, enum: ["specified", "all"], default: "all" },
  { name: "owner_userids", label: "负责人 UserID", type: "string_list", default: [], visible_when: { owner_scope: "specified" } },
];
const templates = [
  { key: "wecom_contact_registration", label: "企微联系人与注册状态", template_version: 1, available: true, fields: [...ownerFields, { name: "contact_statuses", label: "联系人状态", type: "enum_list", required: true, enum: ["active", "deleted"], default: ["active"] }, { name: "registration_status", label: "注册状态", type: "enum", required: true, enum: ["any", "registered", "unregistered"], default: "any" }] },
  { key: "questionnaire_choice_answers", label: "问卷选择题答案", template_version: 1, available: true, fields: [{ name: "questionnaire", label: "问卷", type: "reference", required: true }, { name: "conditions", label: "题目条件", type: "condition_list", required: true, min_items: 1 }, ...ownerFields] },
  { key: "paid_order", label: "已支付订单", template_version: 1, available: true, fields: [{ name: "products", label: "商品", type: "reference_list", reference: "product", required: true }, { name: "paid_at_from", label: "支付时间起点", type: "datetime" }, { name: "paid_at_to", label: "支付时间终点（不含）", type: "datetime" }, ...ownerFields, { name: "require_active_wecom_contact", label: "要求有效企微联系人", type: "boolean", default: true }] },
  { key: "channel_entry", label: "渠道进入", template_version: 1, available: true, fields: [{ name: "channels", label: "渠道", type: "reference_list", reference: "channel", required: true }, { name: "entered_days_min", label: "最少天数", type: "integer", default: 0 }, { name: "entered_days_max", label: "最大天数", type: "integer" }, ...ownerFields, { name: "require_active_wecom_contact", label: "要求有效企微联系人", type: "boolean", default: true }] },
  { key: "radar_first_click_elapsed", label: "雷达首次点击距今", template_version: 1, available: true, fields: [{ name: "radars", label: "雷达", type: "reference_list", reference: "radar", required: true }, { name: "elapsed_min", label: "最小经过时间", type: "integer", default: 0 }, { name: "elapsed_max", label: "最大经过时间", type: "integer" }, { name: "elapsed_unit", label: "时间单位", type: "enum", enum: ["hour", "day"], default: "day" }, ...ownerFields] },
  { key: "member_usage_status", label: "会员与真实使用状态", template_version: 1, available: true, fields: [...ownerFields, { name: "service_period", label: "服务期", type: "enum", enum: ["any", "active", "expired"], default: "active" }, { name: "registration_status", label: "注册状态", type: "enum", enum: ["any", "registered", "unregistered"], default: "any" }, { name: "usage_status", label: "真实使用状态", type: "enum", enum: ["any", "used", "unused"], default: "any" }, { name: "membership_tiers", label: "会员层级", type: "string_list", default: [] }, { name: "membership_statuses", label: "会员状态", type: "string_list", default: [] }] },
];
let config = { id: 4, package_id: 13, version: 1, refresh_cron_utc: "", definition: { schema_version: 1, template_key: "wecom_contact_registration", parameters: { owner_scope: "all", owner_staff_ids: [], contact_statuses: ["active"], registration_status: "any" } } };
let packageVersion = 3;
const writes = [];
const previewWrites = [];
const packageWrites = [];
let templateReads = 0;
const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion/packages/13",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.structuredClone = globalThis.structuredClone;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === "/api/admin/ai-audience/packages/13" && (!init.method || init.method === "GET")) return json({ package: { id: 13, name: "原人群", code: "legacy-audience", version: packageVersion, lifecycle: "paused" } });
      if (url.pathname === "/api/admin/ai-audience/package-groups") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/templates") {
        templateReads += 1;
        // bootDetail starts first. Hold only that request so the Host mounts
        // before the legacy renderer finishes and the MutationObserver has to
        // restore the frozen form without relying on a timeout race.
        if (templateReads === 1) await new Promise((resolve) => window.setTimeout(resolve, 180));
        return json({ items: templates });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/configuration" && (!init.method || init.method === "GET")) return json({ configuration: config });
      if (url.pathname === "/api/admin/ai-audience/packages/13/owner-references") return json({ owner_userids: ["bob"] });
      if (url.pathname === "/api/admin/ai-audience/packages/13/automation-binding" || url.pathname === "/api/admin/ai-audience/packages/13/senders" || url.pathname === "/api/admin/ai-audience/packages/13/members") return json({ error: "not_found" }, 404);
      if (url.pathname === "/api/admin/automation-agents") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/packages/13/precheck") return json({ precheck: { ready: false, reasons: [] } });
      if (url.pathname === "/api/admin/ai-audience/packages/13" && init.method === "PATCH") {
        const body = JSON.parse(init.body);
        packageWrites.push(body);
        packageVersion += 1;
        return json({ package: { id: 13, name: body.name, code: "legacy-audience", version: packageVersion, lifecycle: "paused" } });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/configuration" && init.method === "PUT") {
        const body = JSON.parse(init.body);
        writes.push(body);
        const parameters = { ...body.definition.parameters };
        parameters.owner_staff_ids = parameters.owner_scope === "specified" ? ["9"] : [];
        delete parameters.owner_userids;
        config = { ...config, version: config.version + 1, refresh_cron_utc: body.refresh_cron_utc, definition: { ...body.definition, parameters } };
        return json({ configuration: config });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/preview") {
        const body = JSON.parse(init.body);
        previewWrites.push(body);
        return json({ preview: { member_count: 1, member_digest: "member", watermark_digest: "watermark" } });
      }
      return json({ error: `unexpected ${url.pathname}` }, 500);
    };
  },
});

dom.window.eval(detail);
dom.window.eval(frozenController);
dom.window.eval(host);
await wait(350);
const document = dom.window.document;
const select = document.querySelector("#templateSelect");
if (templateReads < 3 || select.options.length !== 6 || !document.querySelector("#templateParameterForm [data-field-name]")) throw new Error("six frozen template forms were not restored after the delayed detail renderer");
for (const template of templates) {
  select.value = template.key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  if (!document.querySelector("#templateParameterForm [data-field-name]")) throw new Error(`form did not render for ${template.key}`);
}
const fieldInput = (name) => document.querySelector(`[data-field-name="${name}"] input, [data-field-name="${name}"] textarea, [data-field-name="${name}"] select`);
const setSpecifiedOwner = () => {
  const ownerScope = fieldInput("owner_scope");
  ownerScope.value = "specified";
  ownerScope.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  fieldInput("owner_userids").value = "bob";
};
const reOpenTemplate = async (key) => {
  const alternate = templates.find((item) => item.key !== key);
  select.value = alternate.key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  select.value = key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
};
const saveTemplate = async (key, setValues, verifyWrite, verifyReopened) => {
  select.value = key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  setValues();
  document.querySelector("#templatePreviewBtn").click();
  await wait(180);
  verifyWrite(previewWrites.at(-1).definition);
  if (!document.querySelector("#templatePreviewBox").textContent.includes("1 人")) throw new Error(`preview did not use ${key}`);
  document.querySelector("#templateSaveBtn").click();
  await wait(180);
  verifyWrite(writes.at(-1).definition);
  await reOpenTemplate(key);
  verifyReopened();
};
await saveTemplate("wecom_contact_registration", () => {
  const statuses = fieldInput("contact_statuses");
  statuses.options[0].selected = true;
  document.querySelector("#packageNameInput").value = "已更新的人群";
  document.querySelector("#dailySelect").value = "daily_0200";
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "wecom_contact_registration" || parameters.owner_scope !== "all" || parameters.owner_userids.length !== 0 || parameters.contact_statuses.join(",") !== "active" || parameters.registration_status !== "any") throw new Error(`WeCom parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("owner_scope").value !== "all" || !fieldInput("contact_statuses").options[0].selected) throw new Error("WeCom all-scope values did not reopen");
});
if (packageWrites[0]?.name !== "已更新的人群" || writes[0]?.refresh_mode !== "daily_0200" || writes[0]?.refresh_cron_utc !== "") throw new Error(`template save bypassed basic configuration or refresh mode: ${JSON.stringify({ packageWrites, writes })}`);
await saveTemplate("paid_order", () => {
  fieldInput("products").value = "course-v3";
  fieldInput("paid_at_from").value = "2026-09-05T08:00";
  fieldInput("paid_at_to").value = "2026-09-05T09:00";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "paid_order" || parameters.product_codes.join(",") !== "course-v3" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[.]\d{3}Z$/.test(parameters.paid_at_from) || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[.]\d{3}Z$/.test(parameters.paid_at_to) || parameters.owner_userids.join(",") !== "bob") throw new Error(`paid parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("products").value !== "course-v3" || fieldInput("owner_userids").value !== "bob") throw new Error("paid references or owner did not reopen");
});
await saveTemplate("channel_entry", () => {
  fieldInput("channels").value = "渠道标题";
  fieldInput("entered_days_min").value = "2";
  fieldInput("entered_days_max").value = "3";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "channel_entry" || parameters.channel_codes.join(",") !== "渠道标题" || parameters.entered_days_min !== 2 || parameters.entered_days_max !== 3 || parameters.owner_userids.join(",") !== "bob") throw new Error(`channel parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("channels").value !== "渠道标题" || fieldInput("entered_days_min").value !== "2" || fieldInput("owner_userids").value !== "bob") throw new Error("channel references or owner did not reopen");
});
await saveTemplate("radar_first_click_elapsed", () => {
  fieldInput("radars").value = "雷达标题";
  fieldInput("elapsed_min").value = "3";
  fieldInput("elapsed_max").value = "4";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "radar_first_click_elapsed" || parameters.radar_ids.join(",") !== "雷达标题" || parameters.elapsed_min !== 3 || parameters.elapsed_max !== 4 || parameters.owner_userids.join(",") !== "bob") throw new Error(`radar parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("radars").value !== "雷达标题" || fieldInput("elapsed_max").value !== "4" || fieldInput("owner_userids").value !== "bob") throw new Error("radar references or owner did not reopen");
});
await saveTemplate("member_usage_status", () => {
  fieldInput("service_period").value = "expired";
  fieldInput("registration_status").value = "registered";
  fieldInput("usage_status").value = "used";
  fieldInput("membership_tiers").value = "pro";
  fieldInput("membership_statuses").value = "expired";
  document.querySelector("#incrementalSelect").value = "incremental_3m";
  document.querySelector("#dailySelect").value = "daily_0200";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "member_usage_status" || parameters.service_period !== "expired" || parameters.registration_status !== "registered" || parameters.usage_status !== "used" || parameters.membership_tiers.join(",") !== "pro" || parameters.membership_statuses.join(",") !== "expired" || parameters.owner_userids.join(",") !== "bob") throw new Error(`member parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("membership_tiers").value !== "pro" || fieldInput("membership_statuses").value !== "expired" || fieldInput("owner_userids").value !== "bob") throw new Error("member facts or owner did not reopen");
});
if (writes.find((item) => item.definition.template_key === "member_usage_status")?.refresh_mode !== "every_3m_plus_daily_0200") throw new Error(`combined refresh mode was downgraded: ${JSON.stringify(writes)}`);
const savedKeys = writes.map((item) => item.definition.template_key);
for (const key of ["wecom_contact_registration", "paid_order", "channel_entry", "radar_first_click_elapsed", "member_usage_status"]) {
  if (!savedKeys.includes(key)) throw new Error(`save did not use frozen form for ${key}: ${JSON.stringify(savedKeys)}`);
}
await saveTemplate("questionnaire_choice_answers", () => {
  fieldInput("questionnaire").value = "客户调研";
  const conditionField = document.querySelector('[data-field-name="conditions"]');
  const first = conditionField.querySelector(".template-condition-row");
  first.querySelector("[data-condition-question]").value = "获客方式";
  first.querySelector("[data-condition-options]").value = "内容\n投放";
  conditionField.querySelector(".template-condition-list > button").click();
  const second = conditionField.querySelectorAll(".template-condition-row")[1];
  second.querySelector("[data-condition-question]").value = "成交方式";
  second.querySelector("[data-condition-options]").value = "咨询";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "questionnaire_choice_answers" || parameters.owner_userids.join(",") !== "bob" || parameters.questionnaire_id !== "客户调研" || parameters.conditions.length !== 2 || parameters.conditions[0].question_id !== "获客方式" || parameters.conditions[0].option_ids.join(",") !== "内容,投放" || parameters.conditions[1].question_id !== "成交方式" || parameters.conditions[1].option_ids.join(",") !== "咨询") throw new Error(`questionnaire parameters=${JSON.stringify(definition)}`);
}, () => {
  const rows = document.querySelectorAll('[data-field-name="conditions"] .template-condition-row');
  if (fieldInput("questionnaire").value !== "客户调研" || rows.length !== 2 || rows[0].querySelector("[data-condition-options]").value !== "内容\n投放" || fieldInput("owner_userids").value !== "bob") throw new Error("questionnaire conditions or Access-backed owner did not reopen");
});
if (writes.length !== 6 || previewWrites.length !== 6 || packageWrites.length !== 6) throw new Error(`six-form save/preview contract incomplete: ${JSON.stringify({ saves: writes.length, previews: previewWrites.length, packages: packageWrites.length })}`);
dom.window.close();
console.log("admin-audience-template-host-browser: PASS");
