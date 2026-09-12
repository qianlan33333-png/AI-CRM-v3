import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const script = fs.readFileSync(path.join(here, "admin_customers.js"), "utf8");
const html = `<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-sync-url="/api/admin/customer-sync-runs" data-tag-preview-url="/api/v1/customer-tag-commands/preview" data-tag-command-url="/api/v1/customer-tag-commands" data-tags-url="/api/admin/wecom/tags">
  <form id="customer-list-filters"><input name="keyword"><input name="phone"><select name="status"><option value=""></option></select></form>
  <button id="customer-list-clear"></button><button id="customer-list-refresh"></button>
  <span id="customer-list-summary"></span><div id="customer-list-state"></div>
  <div id="customer-list-table-wrap"><table><tbody id="customer-list-body"></tbody></table></div>
  <button id="customer-prev-page"></button><button id="customer-next-page"></button>
  <form id="customer-tag-batch"><select name="add_tag_ids" multiple disabled></select><select name="remove_tag_ids" multiple disabled></select><button type="submit">confirm</button><button id="customer-tag-batch-refresh" type="button" hidden>refresh</button></form><span id="customer-tag-batch-result"></span>
</div>`;
const dom = new JSDOM(html, { url: "https://test.invalid/admin/customers", runScripts: "outside-only" });
dom.window.Headers = Headers;
dom.window.document.cookie = "aicrm_admin_csrf=test-csrf; path=/";
dom.window.confirm = () => true;
dom.window.AdminDateTime = {};
dom.window.AdminFmt = { localTime: (value) => value === "2026-09-05T00:00:00Z" ? "2026-09-05 08:00:00" : "时间暂不可用", whenAdminDateTimeReady: (ready) => ready(dom.window.AdminDateTime) };
const tagCalls = [];
let tagPreviewUnavailable = false;
dom.window.fetch = async (input, options = {}) => {
  const url = new URL(String(input), dom.window.location.origin);
  if (url.pathname === "/api/admin/wecom/tags") return { ok: true, status: 200, json: async () => ({ items: [{ id: 9, group_name: "分组", tag_name: "标签九" }, { id: 10, group_name: "分组", tag_name: "标签十" }] }) };
  if (url.pathname === "/api/v1/customer-tag-commands/preview") { tagCalls.push({ path: url.pathname, options }); if (tagPreviewUnavailable) return { ok: false, status: 503, json: async () => ({ error: "provider_unavailable（上游错误）" }) }; return { ok: true, status: 200, json: async () => ({ state: "preview", lines: [{ customer_id: 42, state: "eligible" }] }) }; }
  if (url.pathname === "/api/v1/customer-tag-commands") { tagCalls.push({ path: url.pathname, options }); return { ok: true, status: 202, json: async () => ({ id: 7, state: "queued", lines: [{ customer_id: 42, state: "queued", effect_ref: "eer_7" }] }) }; }
  if (url.pathname === "/api/v1/customers/42/tag-commands") return { ok: true, status: 200, json: async () => ({ items: [{ id: 7, state: "executed", lines: [{ customer_id: 42, state: "executed" }] }] }) };
  if (url.pathname === "/api/admin/customers/42/tags") return { ok: true, status: 200, json: async () => ({ items: [{ name: "标签九", group_name: "分组", status: "active" }] }) };
  if (url.pathname !== "/api/admin/customers") throw new Error("unexpected request: " + url.pathname);
  return { ok: true, status: 200, json: async () => ({ items: [{ customer_id: 42, display_name: "测试客户", oneid: "cus_42", phone_masked: "138****0000", last_synced_at: "2026-09-05T00:00:00Z" }], total: 1, total_is_estimate: false }) };
};
dom.window.eval(script);
await new Promise((resolve) => setTimeout(resolve, 20));

const links = [...dom.window.document.querySelectorAll("#customer-list-body a")];
if (!dom.window.document.querySelector("#customer-list-body")?.textContent.includes("2026-09-05 08:00:00")) throw new Error("customer timestamp did not use exact Shanghai seconds");
if (!links.some((link) => link.textContent === "查看档案" && link.getAttribute("href") === "/admin/customers/42")) throw new Error("existing customer profile entry was not preserved");
if (!links.some((link) => link.textContent === "会话存档" && link.getAttribute("href") === "/admin/message-archive/customers/42")) throw new Error("selected canonical customer did not receive a message archive entry");
const checkbox = dom.window.document.querySelector('input[type="checkbox"]');
checkbox.checked = true;
checkbox.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
const tagForm = dom.window.document.querySelector("#customer-tag-batch");
for (const option of tagForm.querySelector('[name="add_tag_ids"]').options) option.selected = ["9", "10"].includes(option.value);
tagForm.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
await new Promise((resolve) => setTimeout(resolve, 20));
if (tagCalls.length !== 2 || tagCalls[0].path !== "/api/v1/customer-tag-commands/preview" || tagCalls[1].path !== "/api/v1/customer-tag-commands" || tagCalls[1].options.headers.get("X-CSRF-Token") !== "test-csrf") throw new Error("tag preview/confirm did not use the controlled Host contract");
const commandBody = JSON.parse(tagCalls[1].options.body);
if (commandBody.customer_ids[0] !== 42 || commandBody.add_tag_ids.join(",") !== "9,10" || commandBody.remove_tag_ids.length !== 0) throw new Error("tag command body was not canonical");
if (!dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("客户 #42：已执行") || dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("executed")) throw new Error("tag history refresh did not present the persisted per-customer result in Chinese");
const refresh = dom.window.document.querySelector("#customer-tag-batch-refresh");
if (refresh.hidden) throw new Error("accepted tag command did not expose an explicit result refresh action");
refresh.click();
await new Promise((resolve) => setTimeout(resolve, 20));
if (!dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("观察标签：分组 / 标签九（已生效）") || dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("active")) throw new Error("explicit tag result refresh did not retain the observed-provider readback in Chinese");
tagPreviewUnavailable = true;
tagForm.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
await new Promise((resolve) => setTimeout(resolve, 20));
const tagFailure = dom.window.document.querySelector("#customer-tag-batch-result").textContent || "";
if (!tagFailure.includes("标签服务暂不可用") || tagFailure.includes("provider_unavailable") || tagFailure.includes("上游错误")) throw new Error(`tag failure leaked technical detail: ${tagFailure}`);
dom.window.close();
console.log("admin-customers-browser: PASS");
