import assert from "node:assert/strict";
import fs from "node:fs";
import { JSDOM } from "jsdom";

const html = fs.readFileSync("internal/webshell/templates/sidebar.html", "utf8")
  .replaceAll("{{.DebugEnabled}}", "false")
  .replaceAll("{{.WorkbenchURL}}", "/api/sidebar/v2/workbench")
  .replaceAll("{{.BindMobileURL}}", "/sidebar/bind-mobile")
  .replaceAll("{{.JSSDKConfigURL}}", "/api/sidebar/jssdk-config")
  .replaceAll("{{.ContextTokenURL}}", "/api/sidebar/context-token");
const source = fs.readFileSync("internal/webshell/static/sidebar_workbench/sidebar_workbench.js", "utf8");

function response(payload, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async text() { return JSON.stringify(payload); },
  };
}

async function waitFor(check, description) {
  const deadline = Date.now() + 1500;
  while (Date.now() < deadline) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${description}`);
}

async function run(externalContactResult) {
  const requests = [];
  let readyCallback = null;
  const dom = new JSDOM(html, {
    runScripts: "outside-only",
    url: "https://id-dev.youcangogogo.com/sidebar/bind-mobile",
  });
  const { window } = dom;
  window.wx = {
    ready(callback) { readyCallback = callback; },
    error() {},
    config() { window.setTimeout(() => readyCallback?.(), 0); },
    agentConfig(options) { window.setTimeout(() => options.success?.({ err_msg: "agentConfig:ok" }), 0); },
    invoke(method, _payload, callback) {
      assert.equal(method, "getCurExternalContact");
      window.setTimeout(() => callback(externalContactResult), 0);
    },
  };
  window.fetch = async (input, init = {}) => {
    const url = new URL(String(input), window.location.origin);
    requests.push({ path: url.pathname, init });
    if (url.pathname === "/api/sidebar/jssdk-config") {
      return response({
        corp_id: "ww-test",
        agent_id: "1000001",
        config: { timestamp: 1, nonceStr: "config", signature: "config-signature" },
        agent_config: { timestamp: 1, nonceStr: "agent", signature: "agent-signature" },
      });
    }
    if (url.pathname === "/api/sidebar/context-token") return response({ context_token: "context-token" });
    if (url.pathname === "/api/sidebar/v2/workbench") return response({});
    if (url.pathname === "/api/sidebar/v2/profile") {
      return response({ customer: { customer_id: 42, display_name: "测试客户", corp_name: "", status: "active", version: 1 } });
    }
    if (url.pathname === "/api/sidebar/v2/timeline") return response({ items: [] });
    return response({ error: "unexpected_request" }, 500);
  };
  window.eval(source);
  return { dom, requests };
}

{
  const harness = await run({ err_msg: "getCurExternalContact:ok", userId: "external-from-wecom" });
  await waitFor(
    () => harness.dom.window.document.getElementById("sidebar-workbench-root").dataset.sidebarShellState === "ready",
    "userId sidebar bootstrap",
  );
  const contextRequest = harness.requests.find((item) => item.path === "/api/sidebar/context-token");
  assert.ok(contextRequest, "userId must reach the context-token endpoint");
  assert.deepEqual(JSON.parse(contextRequest.init.body), { external_userid: "external-from-wecom" });
  harness.dom.window.close();
}

{
  const harness = await run({ err_msg: "getCurExternalContact:fail_no permission" });
  await waitFor(
    () => harness.dom.window.document.getElementById("sidebar-workbench-root").dataset.sidebarShellState === "error",
    "provider error feedback",
  );
  assert.match(harness.dom.window.document.body.textContent, /企微未授权当前外部联系人能力/);
  assert.equal(harness.requests.some((item) => item.path === "/api/sidebar/context-token"), false);
  harness.dom.window.close();
}

console.log("sidebar host adapter userId compatibility: ok");
