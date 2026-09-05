import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const script = fs.readFileSync(path.join(here, "admin_customers.js"), "utf8");
const html = `<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-sync-url="/api/admin/customer-sync-runs">
  <form id="customer-list-filters"><input name="keyword"><input name="phone"><select name="status"><option value=""></option></select></form>
  <button id="customer-list-clear"></button><button id="customer-list-refresh"></button>
  <span id="customer-list-summary"></span><div id="customer-list-state"></div>
  <div id="customer-list-table-wrap"><table><tbody id="customer-list-body"></tbody></table></div>
  <button id="customer-prev-page"></button><button id="customer-next-page"></button>
</div>`;
const dom = new JSDOM(html, { url: "https://test.invalid/admin/customers", runScripts: "outside-only" });
dom.window.Headers = Headers;
dom.window.fetch = async (input) => {
  const url = new URL(String(input), dom.window.location.origin);
  if (url.pathname !== "/api/admin/customers") throw new Error("unexpected request: " + url.pathname);
  return {
    ok: true,
    status: 200,
    json: async () => ({ items: [{ customer_id: 42, display_name: "测试客户", oneid: "cus_42", phone_masked: "138****0000", last_synced_at: "2026-09-05T00:00:00Z" }], total: 1, total_is_estimate: false }),
  };
};
dom.window.eval(script);
await new Promise((resolve) => setTimeout(resolve, 20));

const links = [...dom.window.document.querySelectorAll("#customer-list-body a")];
if (!links.some((link) => link.textContent === "查看档案" && link.getAttribute("href") === "/admin/customers/42")) {
  throw new Error("existing customer profile entry was not preserved");
}
if (!links.some((link) => link.textContent === "会话存档" && link.getAttribute("href") === "/admin/message-archive/customers/42")) {
  throw new Error("selected canonical customer did not receive a message archive entry");
}
dom.window.close();
console.log("admin-customers-browser: PASS");
