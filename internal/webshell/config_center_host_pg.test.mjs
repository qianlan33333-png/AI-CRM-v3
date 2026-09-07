import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const baseURL = process.env.AICRM_RUNTIME_RELEASE_TEST_URL;
if (!/^https?:\/\//.test(baseURL || "")) throw new Error("Config Center PostgreSQL HTTP test server is required");
const here = path.dirname(fileURLToPath(import.meta.url));
const host = fs.readFileSync(path.join(here, "static/admin_console/config_center_host.js"), "utf8");
const wait = (milliseconds = 25) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const waitFor = async (predicate, message) => {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (predicate()) return;
    await wait();
  }
  throw new Error(message);
};
let submitted;
let centerSubmitted;
const cookie = `aicrm_admin_session=config-center-browser; aicrm_admin_csrf=${"c".repeat(43)}`;
const dom = new JSDOM('<!doctype html><html><body data-runtime-config-page="runtimeConfigCategory"><main data-runtime-release-host></main></body></html>', {
  url: `${baseURL}/admin/configDetail.html?cat=wecom_base`, runScripts: "outside-only", pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.document.cookie = "aicrm_admin_session=config-center-browser";
    window.document.cookie = `aicrm_admin_csrf=${"c".repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const headers = new Headers(init.headers);
      headers.set("Cookie", cookie);
      const response = await globalThis.fetch(url, { ...init, headers });
      if (url.pathname === "/api/admin/config/runtime-releases" && String(init.method || "GET").toUpperCase() === "POST") {
        submitted = JSON.parse(String(init.body || "{}"));
      }
      return response;
    };
  },
});
const centerDom = new JSDOM('<!doctype html><html><body data-runtime-config-page="runtimeConfigCenter"><main data-runtime-release-host></main></body></html>', {
  url: `${baseURL}/admin/config`, runScripts: "outside-only", pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.document.cookie = "aicrm_admin_session=config-center-browser";
    window.document.cookie = `aicrm_admin_csrf=${"c".repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const headers = new Headers(init.headers); headers.set("Cookie", cookie);
      const response = await globalThis.fetch(url, { ...init, headers });
      if (url.pathname === "/api/admin/config/runtime-releases" && String(init.method || "GET").toUpperCase() === "POST") {
        centerSubmitted = JSON.parse(String(init.body || "{}"));
      }
      return response;
    };
  },
});
try {
  centerDom.window.eval(host);
  await waitFor(() => centerDom.window.document.querySelectorAll("[data-category-row]").length === 12, "Config Center did not render the complete legacy category list");
  const headers = [...centerDom.window.document.querySelectorAll(".cc-category-table thead th")].map((cell) => cell.textContent.trim());
  assert.deepEqual(headers, ["类目", "是否生效", "生效开关", "配置"], "Config Center must retain the donor table columns");
  assert.ok(centerDom.window.document.querySelector('[data-category-row="wecom_base"] [data-category-enabled]') === null, "Config Center must not invent a data-category-enabled command");
  const wecomSwitch = centerDom.window.document.querySelector('[data-category-row="wecom_base"] .cc-switch input');
  assert.ok(wecomSwitch, "WeCom retains its owned primary switch");
  assert.equal(centerDom.window.document.querySelector('[data-category-row="sidebar_identity"] .cc-switch'), null, "a category without one primary enable field must show no aggregate switch");
  wecomSwitch.checked = true;
  wecomSwitch.dispatchEvent(new centerDom.window.Event("change", { bubbles: true }));
  await waitFor(() => Boolean(centerSubmitted), "Config Center switch did not create a draft through the actual HTTP endpoint");
  const centerValues = new Map((centerSubmitted.settings || []).map((item) => [item.key, item.value]));
  assert.equal(centerValues.get("wecom.enabled"), true, "Config Center switch must stage only its owned enabled field");
  assert.equal(centerValues.get("wecom.agent_id"), "agent-preserved", "Config Center switch must retain the full effective snapshot");
} finally {
  centerDom.window.close();
}

try {
  dom.window.eval(host);
  await waitFor(() => dom.window.document.querySelector('[data-runtime-setting="wecom.agent_id"]')?.value === "agent-preserved", "native AgentID string was not rendered");
  const form = dom.window.document.querySelector("form");
  assert.ok(form, "Config Center detail form was not rendered");
  form.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => Boolean(submitted), "Config Center did not create a draft through the actual HTTP endpoint");
  const values = new Map((submitted.settings || []).map((item) => [item.key, item.value]));
  assert.equal(values.get("wecom.agent_id"), "agent-preserved", "saving an untouched category must retain AgentID");
  assert.equal(values.get("automation.operations.provider_mode"), "limited", "saving an untouched category must retain the native automation mode string");
  assert.equal(values.has("WECOM_API_BASE"), false, "deployment-maintained donor fields must remain informational and cannot be submitted");
  console.log("config_center_host_pg: PASS");
} finally {
  dom.window.close();
}
