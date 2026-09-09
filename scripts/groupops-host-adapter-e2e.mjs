import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { build } from "esbuild";
import { JSDOM, VirtualConsole } from "jsdom";
import { fileURLToPath } from "node:url";

const repository = new URL("..", import.meta.url);
const source = new URL("../web/v3/groupOpsHostAdapter.ts", import.meta.url);
const bundle = await build({
  entryPoints: [fileURLToPath(source)],
  bundle: true,
  write: false,
  format: "iife",
  platform: "browser",
  target: "es2020",
  logLevel: "silent",
});
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "https://groupops.test/admin/groupops.html",
  runScripts: "outside-only",
});
const { window } = dom;
window.Headers = Headers;
window.Response = Response;
Object.defineProperty(window, "crypto", { configurable: true, value: crypto });
window.document.cookie = "aicrm_admin_csrf=test-csrf";

const mutations = [];
const foreignRequests = [];
const foreignPayload = { items: [{ staff_id: 5, sender_userid: "external-user", display_name: "External name" }] };
let nodes = [];
let savedOwner = [];
let memberDirectory = [];
let memberDirectoryStatus = 200;
const detail = () => ({
  plan: { plan_id: 41, name: "浏览器计划", revision: 7, status: "draft", plan_type: "standard" },
  nodes,
  members: savedOwner,
});
window.fetch = async (input, init = {}) => {
  const url = new URL(String(input), window.location.href);
  if (url.origin !== window.location.origin) {
    foreignRequests.push({ input, init });
    return new Response(JSON.stringify(foreignPayload), { status: 200 });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && (!init.method || init.method === "GET")) {
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  if (url.pathname === "/api/admin/common/operation-members") {
    return new Response(JSON.stringify({ items: memberDirectory }), { status: memberDirectoryStatus });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups") {
    return new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 });
  }
  if (/\/plans\/41\/nodes(?:\/\d+)?$/.test(url.pathname) && (init.method === "POST" || init.method === "PUT")) {
    mutations.push(JSON.parse(String(init.body || "{}")));
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  throw new Error(`unexpected request ${init.method || "GET"} ${url.pathname}`);
};
window.eval(bundle.outputFiles[0].text);

const foreignOptions = { method: "POST", headers: { "X-Original": "preserved" }, body: "original body" };
await window.fetch("https://external.test/api/admin/common/operation-members/sync", foreignOptions);
assert.equal(foreignRequests[0].init, foreignOptions, "foreign requests must retain the original options unchanged");
assert.equal(new Headers(foreignRequests[0].init.headers).has("X-CSRF-Token"), false);
assert.equal(new Headers(foreignRequests[0].init.headers).has("Idempotency-Key"), false);
assert.equal(foreignRequests[0].init.body, "original body");
const foreignResult = await (await window.fetch("https://external.test/api/admin/common/operation-members?scope=group_ops")).json();
assert.deepEqual(foreignResult, foreignPayload, "foreign same-path GET payload must not be projected");

const host = window.AdminApi;
assert.equal(typeof host?.requestJson, "function", "Group Ops Host bridge must expose requestJson");
await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/nodes", {
  method: "POST",
  body: {
    sort_order: 10,
    day_index: 2,
    scheduled_time: "09:30",
    action_title: "默认排序动作",
    text_content: "真实内容",
    status: "active",
  },
});
assert.deepEqual(mutations[0], {
  expected_revision: 7,
  position: 1,
  kind: "message",
  day_index: 2,
  scheduled_time: "09:30",
  trigger_time_label: "09:30",
  action_title: "默认排序动作",
  status: "active",
  message_text: "真实内容",
  delay_minutes: 0,
  material_plan: { references: [] },
});

nodes = [{ node_id: 88, position: 2 }];
await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/nodes/88", {
  method: "PUT",
  body: {
    sort_order: 10,
    day_index: 3,
    scheduled_time: "10:00",
    action_title: "编辑保留位置",
    text_content: "更新内容",
    status: "active",
  },
});
assert.equal(mutations[1].position, 2, "out-of-range donor edit order must retain the persisted V3 position");
assert.equal(mutations[1].expected_revision, 7);
assert.equal(mutations[1].action_title, "编辑保留位置");
savedOwner = [{ staff_id: 7 }];
memberDirectory = [
  { staff_id: 9, sender_userid: "7", display_name: "不能按外部 ID 误匹配" },
  { staff_id: 7, sender_userid: "real-owner", display_name: "真实昵称 · 完整姓名", name_source: "wecom_profile" },
];
let projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7");
assert.equal(projectedOwner.owner_name, "真实昵称 · 完整姓名", "overview resolves the saved local staff key to its real display name");
memberDirectory = [];
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7", "missing profile must preserve the saved owner binding");
assert.equal(projectedOwner.owner_name, "姓名待同步");
memberDirectoryStatus = 503;
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7", "directory outage must preserve the saved owner binding");
assert.equal(projectedOwner.owner_name, "姓名待同步");
console.log("groupops-host-adapter: PASS");
dom.window.close();

// This uses the frozen picker and standard DOM together. The Group Ops API
// returns the local staff key together with the trusted WeCom sender identity.
// The Host keeps the staff key for plan commands while adapting the frozen
// picker to display the sender identity as its second line.
const pickerSource = await readFile(new URL("../internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js", import.meta.url), "utf8");
const waitFor = async (condition, message) => {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(message);
};
const fullJourneyErrors = [];
const fullJourneyConsole = new VirtualConsole();
fullJourneyConsole.on("jsdomError", (error) => fullJourneyErrors.push(String(error?.message || error)));
const calls = [];
let memberRefreshAttempts = 0;
let ownerDirectoryFailures = 0;
const state = {
  revision: 4,
  plan: { plan_id: 41, name: "标准群运营计划", revision: 4, status: "draft", plan_type: "standard", updated_at: "2026-09-08T00:00:00Z" },
  members: [{ staff_id: 7 }],
  group_assets: [],
  nodes: [],
  directory: [{ chat_reference: "group-9", owner_staff_id: 9, display_name: "九号运营群", member_count: 12, external_member_count: 8 }],
};
const clone = (value) => JSON.parse(JSON.stringify(value));
const response = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const fullJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="41"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/41",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  virtualConsole: fullJourneyConsole,
});
const fullWindow = fullJourney.window;
fullWindow.Headers = Headers;
fullWindow.Response = Response;
Object.defineProperty(fullWindow, "crypto", { configurable: true, value: crypto });
fullWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
fullWindow.confirm = () => true;
fullWindow.AICRMSendContentComposer = {
  open(options) {
    options.onConfirm({ content_text: "真实话术", image_library_ids: [23], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [] });
  },
};
fullWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), fullWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : null;
  calls.push({ path: url.pathname + url.search, method, body });
  const detailPayload = () => ({ plan: clone(state.plan), members: clone(state.members), group_assets: clone(state.group_assets), nodes: clone(state.nodes) });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") {
    return response({
      scope: "group_ops",
      page_size: Number(url.searchParams.get("page_size") || 100),
      items: [
        { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" },
        { staff_id: 9, sender_userid: "wecom-replacement", display_name: "九号运营" },
      ],
    });
  }
  if (url.pathname === "/api/admin/common/operation-members/sync" && method === "POST") {
    memberRefreshAttempts += 1;
    assert.deepEqual(body, { scope: "group_ops", page_size: 100 }, "frozen picker refresh must use the scoped V3 command body");
    assert.equal(init.headers.get("X-CSRF-Token"), "test-csrf", "picker refresh must carry CSRF");
    assert(init.headers.get("Idempotency-Key"), "picker refresh must carry an idempotency key");
    if (memberRefreshAttempts === 1) return response({ error: { code: "provider_read_unavailable" } }, 503);
    return response({ items: [], page_size: 100 });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && method === "GET") return response(detailPayload());
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && method === "PUT") {
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.plan.name = body.name;
    state.plan.plan_type = body.plan_type;
    if (body.owner_staff_id) state.members = [{ staff_id: Number(body.owner_staff_id) }];
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/enable" && method === "POST") {
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.revision += 1;
    state.plan.status = "active";
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/groups" && method === "POST") {
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.group_assets.push({ asset_reference: body.asset_reference });
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/nodes" && method === "POST") {
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.nodes.push({ node_id: 101, ...body });
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
    if (url.searchParams.get("owner_userid") === "9" && ownerDirectoryFailures++ === 0) return response({ error: { code: "provider_read_unavailable" } }, 503);
    return response({ items: clone(state.directory), total: state.directory.length, limit: 200, offset: 0, has_more: false });
  }
  throw new Error(`unexpected Group Ops request ${method} ${url.pathname}${url.search}`);
};

try {
  // The static picker is loaded before the Host in the rendered page. Its
  // direct fetch must retain the trusted external display identity while the
  // selection still writes the local staff identifier.
  fullWindow.eval(pickerSource);
  fullWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => fullWindow.document.querySelector('[data-action="pick-plan-owner"]'), "standard Group Ops detail did not render");
  const directMembers = await (await fullWindow.fetch("/api/admin/common/operation-members?scope=group_ops&page_size=100")).json();
  assert.deepEqual(directMembers.items, [
    { user_id: "wecom-owner", staff_id: "7", display_name: "一号运营" },
    { user_id: "wecom-replacement", staff_id: "9", display_name: "九号运营" },
  ], "picker must show the trusted WeCom user ID while retaining the local staff key");

  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelectorAll("[data-operation-member-row]").length === 2, "owner picker did not render both local staff");
  assert.equal(fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-replacement"] .operation-member-picker__name')?.textContent, "九号运营");
  assert.equal(fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-replacement"] .operation-member-picker__user-id')?.textContent, "wecom-replacement");
  fullWindow.document.querySelector('[data-operation-member-refresh]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("企微客服刷新失败"), "member refresh error did not render a retryable message");
  assert.equal(fullWindow.document.body.textContent.includes("[object Object]"), false, "member refresh must not stringify an error object");
  fullWindow.document.querySelector('[data-operation-member-refresh]').click();
  await waitFor(() => memberRefreshAttempts === 2 && fullWindow.document.querySelectorAll("[data-operation-member-row]").length === 2, "member refresh retry did not recover the picker");
  fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-replacement"] [data-operation-member-row-select]').click();
  fullWindow.document.querySelector("[data-operation-member-confirm]").click();
  await waitFor(() => fullWindow.document.querySelector('[name="owner_userid"]')?.value === "9", "owner picker did not retain the selected local staff id");
  await waitFor(() => fullWindow.document.body.textContent.includes("群目录读取失败，请重试"), "owner directory failure must remain explicit rather than appear as an empty group list");
  fullWindow.document.querySelector('[name="status"]').value = "active";
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => state.plan.status === "active", "saving the selected owner did not enable the existing plan");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "Host must write the selected staff id as owner_staff_id");

  await waitFor(() => fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]'), "detail did not reload after owner save");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  fullWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-group-choice][value="group-9"]'), "eligible directory group did not render for selected owner");
  fullWindow.document.querySelector('[data-group-choice][value="group-9"]').checked = true;
  fullWindow.document.querySelector('[data-action="confirm-group-picker"]').click();
  await waitFor(() => state.group_assets.length === 1, "selected directory group was not bound through the Host command");

  await waitFor(() => fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]'), "detail did not reload after group bind");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  fullWindow.document.querySelector('[data-action="open-node-modal"]').click();
  await waitFor(() => fullWindow.document.querySelector('[name="node_action_title"]'), "node editor did not open");
  fullWindow.document.querySelector('[data-action="configure-node-content"]').click();
  fullWindow.document.querySelector('[name="node_day_index"]').value = "2";
  fullWindow.document.querySelector('[name="node_scheduled_time"]').value = "09:30";
  fullWindow.document.querySelector('[name="node_action_title"]').value = "节点结果";
  fullWindow.document.querySelector('[data-action="save-node"]').click();
  await waitFor(() => state.nodes.length === 1, "node with selected material was not saved through the Host command");
  assert.deepEqual(state.nodes[0].material_plan, { references: [{ kind: "image", id: 23 }] }, "material picker result must reach the V3 material-plan DTO");
  assert.equal(state.nodes[0].day_index, 2);
  assert.equal(state.nodes[0].scheduled_time, "09:30");
  assert.equal(state.nodes[0].action_title, "节点结果");
  assert(calls.some((item) => item.path.endsWith("/enable") && item.method === "POST"), "standard enable action did not call the V3 command");
  assert(fullWindow.document.body.textContent.includes("wecom-replacement"), "selected owner must show the trusted WeCom user ID");
  if (fullJourneyErrors.length) throw new Error(`Group Ops standard DOM errors: ${JSON.stringify(fullJourneyErrors)}`);
  console.log("groupops-standard-dom: PASS");
} finally {
  fullJourney.window.close();
}

// Webhook presentation is rendered by the same standard Host: it must expose
// the configured, callable URL and give a truthful copy receipt. A missing
// descriptor takes the explicit unavailable branch in the production code.
const copiedWebhook = [];
let webhookPlan = { plan_id: 52, name: "Webhook 计划", revision: 2, status: "draft", plan_type: "webhook" };
let webhookDescriptor = { configured: false, reference: "", path: "", signature_algorithm: "HMAC-SHA256", signature_header: "X-Signature", timestamp_header: "X-Timestamp", nonce_header: "X-Nonce", client_id_header: "X-Client-ID" };
const webhookJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="52"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/52",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const webhookWindow = webhookJourney.window;
webhookWindow.Headers = Headers;
webhookWindow.Response = Response;
Object.defineProperty(webhookWindow, "crypto", { configurable: true, value: crypto });
Object.defineProperty(webhookWindow.navigator, "clipboard", { configurable: true, value: { writeText: async (value) => copiedWebhook.push(value) } });
webhookWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
webhookWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), webhookWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52" && method === "GET") return response({ plan: clone(webhookPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52/webhook-descriptor" && method === "GET") return response(clone(webhookDescriptor));
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52/webhook-descriptor" && method === "PUT") {
    const body = JSON.parse(String(init.body));
    assert.equal(body.expected_revision, 2, "Webhook save must use the current plan revision");
    assert.match(body.reference, /^groupops-[0-9a-f-]{36}$/, "Webhook must generate its own opaque reference");
    webhookPlan = { ...webhookPlan, revision: 3 };
    webhookDescriptor = { ...webhookDescriptor, configured: true, reference: body.reference, path: `/api/automation/group-ops/webhooks/${body.reference}` };
    return response({ plan: clone(webhookPlan) });
  }
  throw new Error(`unexpected webhook request ${method} ${url.pathname}`);
};
try {
  webhookWindow.eval(pickerSource);
  webhookWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => webhookWindow.document.querySelector('[data-action="save-webhook"]'), "unconfigured webhook did not render its configuration action");
  assert.equal(webhookWindow.document.querySelector('[name="webhook_reference"]'), null, "users must not enter technical webhook references");
  webhookWindow.document.querySelector('[data-action="save-webhook"]').click();
  await waitFor(() => webhookWindow.document.querySelector('[data-action="copy-webhook"]'), "saved webhook did not reread and render its copy action");
  const expectedWebhookURL = `https://groupops.test${webhookDescriptor.path}`;
  assert.equal(webhookWindow.document.querySelector(".group-ops__url")?.textContent, expectedWebhookURL, "Webhook presentation must show the configured callable URL");
  assert(webhookWindow.document.body.textContent.includes("地址已配置；调用仍需签名配置和启用计划") && webhookWindow.document.body.textContent.includes("签名验证（HMAC-SHA256）") && webhookWindow.document.querySelector(".group-ops__webhook-guide")?.textContent.includes("复制地址不包含凭据，也不能绕过签名验证") && webhookWindow.document.body.textContent.includes("X-Signature / X-Timestamp / X-Nonce / X-Client-ID"), "Webhook presentation must explain the descriptor headers and signing requirement without claiming readiness or exposing a secret");
  webhookWindow.document.querySelector('[data-action="copy-webhook"]').click();
  await waitFor(() => copiedWebhook[0] === expectedWebhookURL, "Webhook copy did not reach the clipboard");
  await waitFor(() => webhookWindow.document.body.textContent.includes("Webhook 地址已复制"), "Webhook copy did not render a success receipt");
  console.log("groupops-webhook-dom: PASS");
} finally {
  webhookJourney.window.close();
}

// List lifecycle controls must give a visible in-flight state, submit exactly
// once, and only show enabled after the V3 command response has been read.
let listPlan = { plan_id: 13, name: "授权测试群计划", revision: 8, status: "disabled", plan_type: "standard" };
let enableCalls = 0;
let releaseEnable;
const listJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const listWindow = listJourney.window;
listWindow.Headers = Headers;
listWindow.Response = Response;
Object.defineProperty(listWindow, "crypto", { configurable: true, value: crypto });
listWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
listWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), listWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response({ items: [clone(listPlan)], total: 1 });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/13" && method === "GET") return response({ plan: clone(listPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/13/enable" && method === "POST") {
    enableCalls += 1;
    const body = JSON.parse(String(init.body));
    assert.equal(body.expected_revision, 8, "enable must use the read revision");
    if (enableCalls === 1) return response({ error: { code: "operations_conflict" } }, 409);
    return new Promise((resolve) => {
      releaseEnable = () => {
        listPlan = { ...listPlan, status: "active", revision: 9 };
        resolve(response({ plan: clone(listPlan) }));
      };
    });
  }
  throw new Error(`unexpected list request ${method} ${url.pathname}`);
};
try {
  listWindow.eval(pickerSource);
  listWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => listWindow.document.querySelector('[data-action="enable-plan"]'), "disabled plan did not render its enable control");
  const enable = () => listWindow.document.querySelector('[data-action="enable-plan"]');
  enable().click();
  await waitFor(() => listWindow.document.body.textContent.includes("计划状态、版本或配置不满足要求，请刷新后检查") && !enable()?.disabled, "failed enable must keep a retryable control and visible error");
  enable().click();
  enable().click();
  await waitFor(() => enableCalls === 2 && enable()?.disabled && enable()?.textContent === "启用中", "enable must lock repeat clicks and show progress");
  releaseEnable();
  await waitFor(() => listWindow.document.querySelector('[data-action="disable-plan"]') && listWindow.document.body.textContent.includes("已启用"), "enable must read back active status and show a receipt");
  assert.equal(enableCalls, 2, "enable retry may submit once after failure but must ignore the concurrent repeat click");
  console.log("groupops-enable-dom: PASS");
} finally {
  listJourney.window.close();
}
