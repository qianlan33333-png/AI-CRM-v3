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
const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion/packages/13",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.structuredClone = globalThis.structuredClone;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === "/api/admin/ai-audience/packages/13" && (!init.method || init.method === "GET")) return json({ package: { id: 13, version: packageVersion, lifecycle: "paused" } });
      if (url.pathname === "/api/admin/ai-audience/templates") return json({ items: templates });
      if (url.pathname === "/api/admin/ai-audience/packages/13/configuration" && (!init.method || init.method === "GET")) return json({ configuration: config });
      if (url.pathname === "/api/admin/ai-audience/packages/13/owner-references") return json({ owner_userids: ["bob"] });
      if (url.pathname === "/api/admin/ai-audience/packages/13/configuration" && init.method === "PUT") {
        const body = JSON.parse(init.body);
        writes.push(body);
        packageVersion += 1;
        config = { ...config, version: config.version + 1, definition: { ...body.definition, parameters: { ...body.definition.parameters, owner_staff_ids: ["9"] } } };
        delete config.definition.parameters.owner_userids;
        return json({ configuration: config });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/preview") return json({ preview: { member_count: 1, member_digest: "member", watermark_digest: "watermark" } });
      return json({ error: `unexpected ${url.pathname}` }, 500);
    };
  },
});

dom.window.eval(frozenController);
dom.window.eval(host);
await wait(180);
const document = dom.window.document;
const select = document.querySelector("#templateSelect");
if (select.options.length !== 6 || !document.querySelector("#templateParameterForm [data-field-name]")) throw new Error("six frozen template forms were not mounted");
for (const template of templates) {
  select.value = template.key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  if (!document.querySelector("#templateParameterForm [data-field-name]")) throw new Error(`form did not render for ${template.key}`);
}
const fieldInput = (name) => document.querySelector(`[data-field-name="${name}"] input, [data-field-name="${name}"] textarea, [data-field-name="${name}"] select`);
const saveTemplate = async (key, setValues) => {
  select.value = key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  setValues();
  document.querySelector("#templateSaveBtn").click();
  await wait(120);
};
await saveTemplate("wecom_contact_registration", () => {
  const statuses = fieldInput("contact_statuses");
  statuses.options[0].selected = true;
});
await saveTemplate("paid_order", () => {
  fieldInput("products").value = "course-v3";
  fieldInput("paid_at_from").value = "2026-09-05T08:00";
  fieldInput("paid_at_to").value = "2026-09-05T09:00";
});
await saveTemplate("channel_entry", () => {
  fieldInput("channels").value = "channel-v3";
  fieldInput("entered_days_min").value = "2";
  fieldInput("entered_days_max").value = "3";
});
await saveTemplate("radar_first_click_elapsed", () => {
  fieldInput("radars").value = "88";
  fieldInput("elapsed_min").value = "3";
  fieldInput("elapsed_max").value = "4";
});
await saveTemplate("member_usage_status", () => { fieldInput("membership_tiers").value = "pro"; });
const savedKeys = writes.map((item) => item.definition.template_key);
for (const key of ["wecom_contact_registration", "paid_order", "channel_entry", "radar_first_click_elapsed", "member_usage_status"]) {
  if (!savedKeys.includes(key)) throw new Error(`save did not use frozen form for ${key}: ${JSON.stringify(savedKeys)}`);
}
select.value = "questionnaire_choice_answers";
select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
await wait(20);
document.querySelector('[data-field-name="questionnaire"] input').value = "101";
const row = document.querySelector(".template-condition-row");
row.querySelector("[data-condition-question]").value = "202";
row.querySelector("[data-condition-options]").value = "303\n304";
const ownerScope = document.querySelector('[data-field-name="owner_scope"] select');
ownerScope.value = "specified";
ownerScope.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
document.querySelector('[data-field-name="owner_userids"] textarea').value = "bob";
document.querySelector("#templatePreviewBtn").click();
await wait(160);
const questionnaireWrite = writes.at(-1);
if (questionnaireWrite.definition.template_key !== "questionnaire_choice_answers" || questionnaireWrite.definition.parameters.owner_userids[0] !== "bob" || questionnaireWrite.definition.parameters.questionnaire_id !== "101" || questionnaireWrite.definition.parameters.conditions[0].option_ids.join(",") !== "303,304") throw new Error(`control values were not converted through the Host: ${JSON.stringify(writes)}`);
if (!document.querySelector("#templatePreviewBox").textContent.includes("1 人") || document.querySelector('[data-field-name="owner_userids"] textarea').value !== "bob") throw new Error("preview or Access-backed owner rehydration failed");
document.querySelector("#templateSaveBtn").click();
await wait(120);
if (writes.at(-1).definition.template_key !== "questionnaire_choice_answers" || !document.querySelector("#templateStatusLine").textContent.includes("已保存")) throw new Error("save did not use the same form conversion path");
dom.window.close();
console.log("admin-audience-template-host-browser: PASS");
