import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { build } from "esbuild";
import { JSDOM, VirtualConsole } from "jsdom";

const repository = new URL("..", import.meta.url);
const source = new URL("../web/v3/groupOpsHostAdapter.ts", import.meta.url);
const bundle = await build({
  entryPoints: [source.pathname],
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
let nodes = [];
const detail = () => ({
  plan: { plan_id: 41, name: "浏览器计划", revision: 7, status: "draft", plan_type: "standard" },
  nodes,
  members: [],
});
window.fetch = async (input, init = {}) => {
  const url = new URL(String(input), window.location.href);
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && (!init.method || init.method === "GET")) {
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  if (/\/plans\/41\/nodes(?:\/\d+)?$/.test(url.pathname) && (init.method === "POST" || init.method === "PUT")) {
    mutations.push(JSON.parse(String(init.body || "{}")));
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  throw new Error(`unexpected request ${init.method || "GET"} ${url.pathname}`);
};
window.eval(bundle.outputFiles[0].text);

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
console.log("groupops-host-adapter: PASS");
dom.window.close();

// This uses the frozen picker and standard DOM together. The Group Ops API
// deliberately returns its Access-facing staff projection; the Host must adapt
// the frozen picker's direct GET without exposing a WeCom sender identifier or
// confusing that sender with the local owner_staff_id written by plan commands.
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
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: clone(state.directory), total: state.directory.length, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected Group Ops request ${method} ${url.pathname}${url.search}`);
};

try {
  // The static picker is loaded before the Host in the rendered page. Its
  // direct fetch must nevertheless receive only the frozen picker DTO.
  fullWindow.eval(pickerSource);
  fullWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => fullWindow.document.querySelector('[data-action="pick-plan-owner"]'), "standard Group Ops detail did not render");
  const directMembers = await (await fullWindow.fetch("/api/admin/common/operation-members?scope=group_ops&page_size=100")).json();
  assert.deepEqual(directMembers.items, [
    { user_id: "7", display_name: "一号运营" },
    { user_id: "9", display_name: "九号运营" },
  ], "direct frozen picker fetch must project local staff ids and names only");
  assert.equal(JSON.stringify(directMembers).includes("sender_userid"), false, "Host must not expose WeCom sender ids to the picker");

  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelectorAll("[data-operation-member-row]").length === 2, "owner picker did not render both local staff");
  fullWindow.document.querySelector('[data-operation-member-row][data-user-id="9"] [data-operation-member-row-select]').click();
  fullWindow.document.querySelector("[data-operation-member-confirm]").click();
  await waitFor(() => fullWindow.document.querySelector('[name="owner_userid"]')?.value === "9", "owner picker did not retain the selected local staff id");
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
  assert.equal(fullWindow.document.body.textContent.includes("wecom-owner"), false, "sender identifier leaked into the standard DOM");
  if (fullJourneyErrors.length) throw new Error(`Group Ops standard DOM errors: ${JSON.stringify(fullJourneyErrors)}`);
  console.log("groupops-standard-dom: PASS");
} finally {
  fullJourney.window.close();
}
