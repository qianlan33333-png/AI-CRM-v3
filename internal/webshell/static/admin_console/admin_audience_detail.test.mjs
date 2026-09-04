import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const adapter = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience.html"), "utf8")
  .replace(/^\{\{define "admin_audience"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const wait = () => new Promise((resolve) => setTimeout(resolve, 80));
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const requests = [];

const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.confirm = () => true;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = init.method || "GET";
      requests.push({ path: url.pathname, search: url.search, method });
      if (url.pathname === "/api/admin/ai-audience/package-groups") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/templates") return json({ items: [{ key: "active_contacts", available: true }] });
      if (url.pathname === "/api/admin/ai-audience/packages" && method === "GET") {
        return json({ items: [{ id: 13, code: "audience-073da67f778402ce", name: "近30天活跃客户", lifecycle: "paused", version: 2, member_count: 23460, published_at: "2026-09-04T08:30:00Z", readiness: "not_ready" }], total: 1, limit: 100, offset: 0 });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/precheck" && method === "POST") {
        return json({ precheck: { ready: false, reasons: ["automation_binding_missing", "sender_set_missing", "provider_disabled"] } });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/activate" && method === "POST") {
        return json({ package: { id: 13 } });
      }
      return json({ error: "unexpected_request" }, 500);
    };
  },
});

dom.window.eval(adapter);
await wait();

const document = dom.window.document;
const row = document.querySelector("#audRows tr");
const renderedCount = row?.querySelectorAll("td")[1]?.textContent.trim();
if (!row || Number(renderedCount) !== 23460) {
  throw new Error(`published snapshot count was not rendered: ${row?.textContent || "missing row"}`);
}
document.querySelector('[data-action="activate"][data-package-id="13"]')?.click();
await wait();

const notice = document.querySelector("#audNotice")?.textContent || "";
if (!notice.includes("未绑定已发布的固定话术") || !notice.includes("未配置发送人白名单") || !notice.includes("Provider")) {
  throw new Error(`activation blockers were not explained: ${notice}`);
}
if (requests.some((request) => request.path.endsWith("/activate"))) {
  throw new Error("activation mutation ran after a failed readiness precheck");
}

dom.window.close();
console.log("admin-audience-activation-readiness-browser: PASS");
