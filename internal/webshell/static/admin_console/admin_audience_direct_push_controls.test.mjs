import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience_detail.html"), "utf8")
  .replace(/^\{\{define "admin_audience_detail"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const adapter = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const wait = (milliseconds = 120) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const writes = [];

const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion/packages/12",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.AdminFmt = { localTime: (value) => value || "" };
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = init.method || "GET";
      if (url.pathname === "/api/admin/ai-audience/packages/12" && method === "GET") return json({ package: { id: 12, name: "运行中人群包", lifecycle: "active", membership_mode: "recommendation", version: 3, member_count: 1 } });
      if (url.pathname === "/api/admin/ai-audience/package-groups") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/templates") return json({ items: [] });
      if (url.pathname === "/api/admin/automation-agents") return json({ items: [] });
      if (url.pathname.endsWith("/configuration") || url.pathname.endsWith("/automation-binding") || url.pathname.endsWith("/senders")) return json({ error: "not_found" }, 404);
      if (url.pathname.endsWith("/members")) return json({ snapshot: { member_count: 1 }, items: [] });
      if (url.pathname.endsWith("/precheck")) return json({ precheck: { ready: false, reasons: [] } });
      if (url.pathname.endsWith("/direct-push") && method === "GET") return json({ data: { enabled: false, max_per_customer_24h: 1, version: 0, client_id: "aicrm-audience-direct-push", webhook_path: "" } });
      if (url.pathname.endsWith("/direct-push") && method === "PUT") {
        const body = JSON.parse(init.body);
        writes.push(body);
        return json({ data: { enabled: body.enabled, max_per_customer_24h: body.max_per_customer_24h, version: 1, client_id: "aicrm-audience-direct-push", webhook_path: "/api/automation/audience/webhooks/awh_test" } });
      }
      return json({ error: `unexpected ${method} ${url.pathname}` }, 500);
    };
  },
});

dom.window.eval(adapter);
await wait(300);
const document = dom.window.document;
for (const id of ["directPushEnabled", "directPushLimit", "directPushPath", "saveDirectPushBtn"]) {
  if (document.querySelector(`#${id}`)?.disabled) throw new Error(`${id} stayed disabled for an active package`);
}
if (!document.querySelector("#packageDefinitionInput")?.disabled) throw new Error("active audience definition unexpectedly became editable");
document.querySelector("#directPushEnabled").checked = true;
document.querySelector("#directPushLimit").value = "2";
document.querySelector("#saveDirectPushBtn").click();
await wait(160);
if (writes.length !== 1 || writes[0].enabled !== true || writes[0].max_per_customer_24h !== 2 || writes[0].expected_version !== 0) throw new Error(`direct push save did not preserve the independent command: ${JSON.stringify(writes)}`);
if (document.querySelector("#directPushPath").value !== "/api/automation/audience/webhooks/awh_test") throw new Error("saved webhook path was not rendered");

dom.window.close();
console.log("admin-audience-direct-push-controls: PASS");
