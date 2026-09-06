import { JSDOM } from "jsdom";
import { build } from "esbuild";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../..");
const donor = fs.readFileSync(path.join(here, "static/admin_console/owner_migration_dd8d60d.html"), "utf8");
const output = await build({ entryPoints: [path.join(here, "static_src/admin_console/owner_handoff_host.ts")], bundle: true, format: "iife", platform: "browser", target: "es2022", write: false, logLevel: "silent" });
const host = output.outputFiles[0].text;
const wait = () => new Promise(resolve => setTimeout(resolve, 40));
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body, text: async () => body });

async function mountFixture(contextBody, contextStatus = 200) {
  const requests = [];
  const dom = new JSDOM('<!doctype html><html><body><main data-owner-handoff-host></main></body></html>', {
    url: "https://owner-host.fixture/admin/owner-migration", runScripts: "outside-only", pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = Headers;
      window.OperationMemberPicker = { open: async () => {} };
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        const method = init.method || "GET";
        requests.push({ path: url.pathname, method });
        if (url.pathname === "/static/admin_console/owner_migration_dd8d60d.html" && method === "GET") return response(donor);
        if (url.pathname === "/api/admin/customers/owner-handoffs/context" && method === "GET") return response(contextBody, contextStatus);
        return response({ error: "unexpected fixture route" }, 500);
      };
    },
  });
  dom.window.eval(host);
  await wait(); await wait();
  const stage = dom.window.document.querySelector("[data-owner-handoff-host]");
  const page = stage?.querySelector("[data-owner-migration-page]");
  const diagnostic = {
    init: stage?.dataset.ownerHandoffInit || "missing",
    http_status: stage?.dataset.ownerHandoffInitStatus || "",
    page: Boolean(page),
    has_curly_marker: Boolean(page?.innerHTML.includes("{{")),
    has_block_marker: Boolean(page?.innerHTML.includes("{%")),
    operator_ready: Boolean(page?.querySelector("#operator")?.value.startsWith("管理员 #")),
    welcome_ready: page?.querySelector("[data-transfer-welcome-msg]")?.value === "您好，后续将由新的服务同事继续为您服务。",
    wecom_checked: Boolean(page?.querySelector("[data-include-wecom-transfer]")?.checked),
    donor_gets: requests.filter(request => request.path === "/static/admin_console/owner_migration_dd8d60d.html" && request.method === "GET").length,
    context_gets: requests.filter(request => request.path === "/api/admin/customers/owner-handoffs/context" && request.method === "GET").length,
    user_message: stage?.textContent || "",
  };
  dom.window.close();
  return diagnostic;
}

const ready = await mountFixture({ staff: [], operator: "管理员 #42" });
if (ready.init !== "ready" || !ready.page || ready.has_curly_marker || ready.has_block_marker || !ready.operator_ready || !ready.welcome_ready || !ready.wecom_checked || ready.donor_gets !== 1 || ready.context_gets !== 1) throw new Error(`owner handoff Host ready fixture mismatch ${JSON.stringify(ready)}`);

const denied = await mountFixture({ error: "forbidden fixture" }, 403);
if (denied.init !== "context_error" || denied.page || denied.http_status !== "403" || denied.donor_gets !== 1 || denied.context_gets !== 1 || denied.user_message !== "负责人迁移页面不可用。") throw new Error(`owner handoff Host context failure fixture mismatch ${JSON.stringify(denied)}`);

console.log("owner_handoff_host: PASS");
