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
let operationMemberReads = 0;
let ownerProjection = { staff_id: 7, sender_userid: "real-owner", display_name: "真实昵称 · 完整姓名", name_source: "wecom_profile", profile_read_state: "ready" };
const detail = () => ({
  plan: { plan_id: 41, name: "浏览器计划", revision: 7, status: "draft", plan_type: "standard", owner: ownerProjection },
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
    operationMemberReads += 1;
    return new Response(JSON.stringify({ items: [] }), { status: 200 });
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
const ownerReadsBeforeProjection = operationMemberReads;
let projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7");
assert.equal(projectedOwner.owner_name, "真实昵称 · 完整姓名", "overview reads the server-owned responsible-member projection");
assert.equal(operationMemberReads, ownerReadsBeforeProjection, "detail must not perform a second operation-member directory read");
ownerProjection = { staff_id: 7, display_name: "保留的历史姓名", name_source: "wecom_profile", profile_read_state: "unavailable", profile_read_error_code: "provider_unavailable" };
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7", "directory outage must preserve the saved owner binding");
assert.equal(projectedOwner.owner_name, "负责人目录不可用", "directory outage remains distinct from an unconfigured owner");
ownerProjection = { staff_id: 7 };
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_name, "负责人目录未同步", "a missing directory row remains explicit");
ownerProjection = {};
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "");
assert.equal(projectedOwner.owner_name, "未配置负责人", "an unconfigured owner stays distinct from directory states");
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
  throw new Error(typeof message === "function" ? message() : message);
};
// The Host lease is deliberately narrower than a plan-ID cache. These
// controlled responses exercise same-kind reentry and A -> B -> A: late A
// success/error/finally must not replace the current A view or its subsequent
// write/readback.
const pending = () => {
  let resolve;
  let reject;
  const promise = new Promise((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, resolve, reject };
};
const raceResponse = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
const raceReads = [];
const raceWrites = [];
let raceEnableAttempts = 0;
const raceJourney = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/41",
  runScripts: "outside-only",
});
const raceWindow = raceJourney.window;
raceWindow.Headers = Headers;
raceWindow.Response = Response;
Object.defineProperty(raceWindow, "crypto", {
  configurable: true,
  value: crypto,
});
raceWindow.fetch = (input, init = {}) => {
  const url = new URL(String(input), raceWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (
    method === "GET" &&
    (/\/plans\/(41|42|88|90)$/.test(url.pathname) ||
      (url.pathname === "/api/admin/automation-conversion/group-ops/groups" &&
        url.searchParams.get("owner_userid") === null))
  ) {
    const request = pending();
    raceReads.push({ path: url.pathname + url.search, request });
    return request.promise;
  }
  if (
    url.pathname === "/api/admin/automation-conversion/group-ops/groups/sync" &&
    method === "POST"
  )
    return Promise.resolve(raceResponse({ total: 1 }));
  if (
    url.pathname ===
      "/api/admin/automation-conversion/group-ops/plans/41/enable" &&
    method === "POST"
  ) {
    raceWrites.push(JSON.parse(String(init.body || "{}")));
    raceEnableAttempts += 1;
    if (raceEnableAttempts === 1)
      return Promise.resolve(raceResponse({ code: "operations_conflict" }, 409));
    return Promise.resolve(raceResponse({ plan: { plan_id: 41, revision: 52 } }));
  }
  throw new Error(
    `unexpected race request ${method} ${url.pathname}${url.search}`,
  );
};
raceWindow.eval(bundle.outputFiles[0].text);
const raceHost = raceWindow.AdminApi;
const staleSuccessA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const staleErrorA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const interveningB = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/42",
);
const currentA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const currentAGroups = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41/groups",
);
await waitFor(
  () => raceReads.length === 8,
  "same-kind reentry did not create four independent plan/directory epochs",
);
const nextRaceRead = (expectedPath) => {
  const next = raceReads.shift();
  assert.equal(
    next.path,
    expectedPath,
    "race fixture must preserve request order",
  );
  return next.request;
};
const staleSuccessPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const staleSuccessDirectory = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
);
const staleErrorPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const staleErrorDirectory = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
);
const interveningBPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/42",
);
const interveningBDirectory = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
);
const currentPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const currentDirectory = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
);
currentPlan.resolve(
  raceResponse({
    plan: { plan_id: 41, name: "current", revision: 50 },
    group_assets: [{ asset_reference: "current-group" }],
  }),
);
currentDirectory.resolve(
  raceResponse({
    items: [
      {
        chat_reference: "current-group",
        display_name: "当前群",
        owner_staff_id: 7,
        member_count: 20,
        external_member_count: 12,
      },
    ],
    has_more: false,
  }),
);
const [currentPlanPayload, currentGroupPayload] = await Promise.all([
  currentA,
  currentAGroups,
]);
assert.equal(
  currentPlanPayload.revision,
  50,
  "the current A epoch must publish its own revision before old A completes",
);
assert.equal(
  currentPlanPayload.groups_summary,
  currentGroupPayload.summary,
  "the paired routes must retain one current summary view reference",
);
staleSuccessPlan.resolve(
  raceResponse({
    plan: { plan_id: 41, name: "stale", revision: 3 },
    group_assets: [{ asset_reference: "stale-group" }],
  }),
);
staleSuccessDirectory.resolve(
  raceResponse({
    items: [
      {
        chat_reference: "stale-group",
        display_name: "过期群",
        owner_staff_id: 7,
        member_count: 4,
        external_member_count: 1,
      },
    ],
    has_more: false,
  }),
);
staleErrorPlan.resolve(raceResponse({ code: "operations_conflict" }, 409));
staleErrorDirectory.resolve(raceResponse({ items: [], has_more: false }));
interveningBPlan.resolve(
  raceResponse({
    plan: { plan_id: 42, name: "B", revision: 4 },
    group_assets: [],
  }),
);
interveningBDirectory.resolve(raceResponse({ items: [], has_more: false }));
await staleSuccessA;
await assert.rejects(staleErrorA, /计划状态、版本或配置不满足要求/);
await interveningB;
assert.equal(
  currentGroupPayload.items[0].group_name,
  "当前群",
  "late A success cannot overwrite the published current group view",
);
assert.equal(
  currentPlanPayload.groups_summary,
  currentGroupPayload.summary,
  "late A success/error/finally cannot replace the current summary view reference",
);

// This order is distinct: old A finishes while the newer A is still pending.
// Its finally must leave the newer epoch claim installed so that both newer
// routes publish the same donor view once their shared reads arrive.
const oldPendingA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/90",
);
const newPendingA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/90",
);
const newPendingGroups = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/90/groups",
);
await waitFor(
  () => raceReads.length === 4,
  "pending-order fixture did not create distinct old and new A epochs",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/90").resolve(
  raceResponse({
    plan: { plan_id: 90, name: "old pending", revision: 1 },
    group_assets: [],
  }),
);
nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
).resolve(raceResponse({ items: [], has_more: false }));
await oldPendingA;
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/90").resolve(
  raceResponse({
    plan: { plan_id: 90, name: "new pending", revision: 2 },
    group_assets: [],
  }),
);
nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
).resolve(raceResponse({ items: [], has_more: false }));
const [newPendingPlan, newPendingGroupPayload] = await Promise.all([
  newPendingA,
  newPendingGroups,
]);
assert.equal(
  newPendingPlan.groups_summary,
  newPendingGroupPayload.summary,
  "old A finally must not delete the newer pending A epoch",
);

// A failed epoch cannot become a retained failed snapshot. The retry starts a
// fresh pair immediately and returns its own current DTOs.
const failed88 = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/88",
);
await waitFor(
  () => raceReads.length === 2,
  "failed detail did not start a new lease",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/88").resolve(
  raceResponse({ code: "service_unavailable" }, 503),
);
nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
).resolve(raceResponse({ code: "directory_unavailable" }, 503));
await assert.rejects(failed88, /HTTP 503/);
const retry88 = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/88",
);
const retry88Groups = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/88/groups",
);
await waitFor(
  () => raceReads.length === 2,
  "retry did not start a fresh paired lease",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/88").resolve(
  raceResponse({
    plan: { plan_id: 88, name: "retry", revision: 6 },
    group_assets: [],
  }),
);
nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
).resolve(raceResponse({ items: [], has_more: false }));
await Promise.all([retry88, retry88Groups]);

// Sync invalidates the initial epoch, reads fresh server data, and mutates the
// current donor view rather than an older A array. It does not silently discard
// the revision the operator saw before starting a write.
raceWindow.document.body.innerHTML =
  '<main id="group-ops-app" data-plan-id="41"></main>';
const sync = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/groups/sync",
  { method: "POST", body: { owner_userid: 7 } },
);
await waitFor(
  () => raceReads.length === 2,
  "sync readback did not force fresh plan and directory reads",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/41").resolve(
  raceResponse({
    plan: { plan_id: 41, name: "current", revision: 51 },
    group_assets: [{ asset_reference: "current-group" }],
  }),
);
nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
).resolve(
  raceResponse({
    items: [
      {
        chat_reference: "current-group",
        display_name: "同步后的当前群",
        owner_staff_id: 7,
        member_count: 22,
        external_member_count: 13,
      },
    ],
    has_more: false,
  }),
);
await sync;
assert.equal(
  currentGroupPayload.items[0].group_name,
  "同步后的当前群",
  "late A must not replace the group view that sync mutates",
);
const conflictingEnable = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41/enable",
  { method: "POST" },
);
assert.equal(raceReads.length, 0, "a write must retain the operator-visible revision instead of silently rereading it");
await assert.rejects(conflictingEnable, /计划状态、版本或配置不满足要求/);
assert.equal(raceWrites[0].expected_revision, 50, "the first write must preserve revision 50 and let the server reject the unseen revision 51");
const explicitReread = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
await waitFor(
  () => raceReads.length === 2,
  "explicit reread did not request a fresh plan and directory pair after the real conflict",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/41").resolve(
  raceResponse({
    plan: { plan_id: 41, name: "current", revision: 51 },
    group_assets: [{ asset_reference: "current-group" }],
  }),
);
nextRaceRead(
  "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0",
).resolve(raceResponse({ items: [], has_more: false }));
const refreshedPlan = await explicitReread;
assert.equal(refreshedPlan.revision, 51, "the explicit reread publishes the new server revision");
const retriedEnable = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41/enable",
  { method: "POST" },
);
assert.equal(raceReads.length, 0, "the post-reread write must use the newly read revision directly");
await retriedEnable;
assert.equal(raceWrites[1].expected_revision, 51, "only an explicit reread may advance the next write to revision 51");
console.log("groupops-hydration-epoch: PASS");
raceJourney.window.close();

const fullJourneyErrors = [];
const fullJourneyConsole = new VirtualConsole();
fullJourneyConsole.on("jsdomError", (error) => fullJourneyErrors.push(String(error?.message || error)));
const calls = [];
let memberRefreshAttempts = 0;
let ownerDirectoryFailures = 0;
let groupSyncAttempts = 0;
let failGroupReadback = false;
let failPlanReadback = false;
let failNextPlanReadbackAfterWrite = false;
let wrongPlanIDOnce = false;
let returnWrongPlanIDAfterWrite = false;
let delayNextPlanRead = false;
let releaseDelayedPlanRead = null;
let saveFailure = "";
let dropCommittedGroupResponse = "";
let rejectGroupOnce = "";
const groupSelectionCommands = [];
const state = {
  revision: 4,
  plan: { plan_id: 41, name: "标准群运营计划", revision: 4, status: "paused", plan_type: "standard", updated_at: "2026-09-08T00:00:00Z" },
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
  calls.push({ path: url.pathname + url.search, method, body, idempotencyKey: init.headers?.get?.("Idempotency-Key") || "" });
  const ownerFor = (staffID) => ({
    staff_id: staffID,
    sender_userid: staffID === 9 ? "wecom-replacement" : "wecom-owner",
    display_name: staffID === 9 ? "九号运营" : "一号运营",
    name_source: "wecom_profile",
    profile_read_state: "ready",
  });
  const detailPayload = () => ({ plan: { ...clone(state.plan), owner: ownerFor(Number(state.members[0]?.staff_id || 0)) }, members: clone(state.members), group_assets: clone(state.group_assets), nodes: clone(state.nodes) });
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
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && method === "GET") {
    if (failPlanReadback) throw new Error("详情读取中断");
    if (wrongPlanIDOnce) {
      wrongPlanIDOnce = false;
      return response({ ...detailPayload(), plan: { ...clone(state.plan), plan_id: 99 } });
    }
    if (delayNextPlanRead) {
      delayNextPlanRead = false;
      const captured = response(detailPayload());
      return new Promise((resolve) => { releaseDelayedPlanRead = () => resolve(captured); });
    }
    return response(detailPayload());
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && method === "PUT") {
    if (saveFailure === "network") throw new Error("网络连接中断");
    if (saveFailure) return response({ code: saveFailure === "409" ? "revision_conflict" : "service_unavailable" }, Number(saveFailure));
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.plan.name = body.name;
    state.plan.plan_type = body.plan_type;
    if (body.owner_staff_id) state.members = [{ staff_id: Number(body.owner_staff_id) }];
    state.revision += 1;
    state.plan.revision = state.revision;
    if (failNextPlanReadbackAfterWrite) {
      failNextPlanReadbackAfterWrite = false;
      failPlanReadback = true;
    }
    if (returnWrongPlanIDAfterWrite) {
      returnWrongPlanIDAfterWrite = false;
      wrongPlanIDOnce = true;
    }
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
    groupSelectionCommands.push({ reference: body.asset_reference, body: clone(body), idempotencyKey: init.headers?.get?.("Idempotency-Key") || "" });
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    if (!state.group_assets.some((item) => item.asset_reference === body.asset_reference)) state.group_assets.push({ asset_reference: body.asset_reference });
    state.revision += 1;
    state.plan.revision = state.revision;
    if (dropCommittedGroupResponse === body.asset_reference) {
      dropCommittedGroupResponse = "";
      // Another actor changes the plan after the accepted write. The Host must
      // prove the dropped response by Owner readback instead of changing this
      // request body or minting a second idempotency key.
      state.group_assets.push({ asset_reference: "other-concurrent" });
      state.revision += 1;
      state.plan.revision = state.revision;
      throw new Error("网络连接中断");
    }
    if (rejectGroupOnce === body.asset_reference) {
      rejectGroupOnce = "";
      state.group_assets = state.group_assets.filter((item) => item.asset_reference !== body.asset_reference);
      state.revision -= 1;
      state.plan.revision = state.revision;
      return response({ code: "service_unavailable" }, 503);
    }
    return response({ plan: clone(state.plan) });
  }
  if (/\/api\/admin\/automation-conversion\/group-ops\/plans\/41\/groups\/.+$/.test(url.pathname) && method === "DELETE") {
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    const reference = decodeURIComponent(url.pathname.split("/").pop() || "");
    state.group_assets = state.group_assets.filter((item) => item.asset_reference !== reference);
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/groups/group-9" && method === "DELETE") {
    state.group_assets = state.group_assets.filter((item) => item.asset_reference !== "group-9");
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/nodes" && method === "POST") {
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.nodes.push({ node_id: 101, ...body });
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups/sync" && method === "POST") {
    groupSyncAttempts++;
    assert.equal(body.owner_staff_id, 7, "refresh uses unsaved selected local member, not the saved owner");
    const refreshTarget = state.directory.find((item) => item.chat_reference === "group-10") || state.directory[0];
    refreshTarget.display_name = `同步群名${groupSyncAttempts}`;
    refreshTarget.member_count = 300 + groupSyncAttempts;
    refreshTarget.external_member_count = 230 + groupSyncAttempts;
    return response({ items: clone(state.directory), total: state.directory.length, limit: 100, offset: 0, has_more: false });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
    if (failGroupReadback) return response({ code: "directory_unavailable" }, 503);
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
  assert.equal(fullWindow.document.querySelector('[data-operation-member-title]')?.textContent, "选择负责人", "Group Ops must declare the owner-selection context");
  assert.match(fullWindow.document.querySelector('[data-operation-member-description]')?.textContent || "", /选择一位负责人/, "single owner selection must state its own business purpose");
  assert.equal(fullWindow.document.querySelector('[data-operation-member-description]')?.textContent.includes("最多"), false, "single owner selection must not claim the channel member limit");
  assert.equal(fullWindow.document.querySelector('[data-operation-member-picker] input[type="checkbox"]'), null, "Group Ops owner selection must use the single-select control");
  assert.equal(fullWindow.document.querySelector('[data-operation-member-confirm]')?.textContent, "确认负责人", "single owner selection must keep the standard primary action explicit");
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
  const draftName = fullWindow.document.querySelector('[name="plan_name"]');
  const writesBeforeBlankName = calls.filter((call) => call.method === "PUT").length;
  draftName.value = "   ";
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("请输入计划名称后再保存"), "blank plan name must be rejected locally");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeBlankName, "blank plan name must not issue a PUT");
  assert.equal(fullWindow.document.querySelector('[name="plan_name"]')?.value, "", "blank name must remain blank after local rejection");
  assert.equal(fullWindow.document.querySelector('[name="owner_userid"]')?.value, "9", "blank name rejection must preserve the other drafted fields");
  const doubleClickName = fullWindow.document.querySelector('[name="plan_name"]');
  doubleClickName.value = "一次提交";
  const writesBeforeDoubleClick = calls.filter((call) => call.method === "PUT").length;
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  fullWindow.document.querySelector('[data-action="save-active-detail-panel"]').click();
  await waitFor(() => state.plan.name === "一次提交", "single save did not persist before the shared-lock assertion");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeDoubleClick + 1, "two save controls must share one in-flight PUT");
  assert.equal(fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, false, "both save controls unlock only after authoritative readback");
  fullWindow.document.querySelector('[name="plan_name"]').value = "失败重试保留草稿";
  for (const failure of ["409", "500", "network"]) {
    saveFailure = failure;
    const writesBefore = calls.filter((call) => call.method === "PUT").length;
    const action = failure === "500" ? "save-active-detail-panel" : "save-plan";
    fullWindow.document.querySelector(`[data-action="${action}"]`).click();
    await waitFor(() => calls.filter((call) => call.method === "PUT").length > writesBefore && fullWindow.document.querySelector('.group-ops__notice--error'), "real save rejection must be visible");
    const retainedName = fullWindow.document.querySelector('[name="plan_name"]');
    assert.equal(retainedName.value, "失败重试保留草稿", "failure must retain the draft after the standard page rerenders");
    assert.equal(fullWindow.document.querySelector('.group-ops__notice--error')?.getAttribute('role'), 'alert', "save failure must remain an accessible alert");
    assert.equal(fullWindow.document.querySelector('[name="owner_userid"]').value, "9");
    assert.equal(state.plan.name, "一次提交", "failed save must not pretend the server changed");
    assert.equal(fullWindow.document.body.textContent.includes("已保存"), false);
  }
  saveFailure = "";
  assert.equal(fullWindow.document.querySelector('[name="status"]').value, "disabled", "paused server state must remain stopped in the frozen form");
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => state.plan.name === "失败重试保留草稿" && fullWindow.document.querySelector('[name="plan_name"]') !== draftName, "successful paused save must persist then reload the original form");
  assert.equal(state.plan.status, "paused", "saving paused configuration must not activate the plan");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "paused save must persist the chosen owner");
  assert.equal(calls.some((call) => call.method === "POST" && /\/(enable|disable)$/.test(call.path)), false, "paused save must not trigger a lifecycle command");
  assert.equal(fullWindow.document.querySelector('.group-ops__notice--error'), null);
  fullWindow.document.querySelector('[name="plan_name"]').value = "已写待回读";
  const writesBeforeReadbackFailure = calls.filter((call) => call.method === "PUT").length;
  failNextPlanReadbackAfterWrite = true;
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => calls.filter((call) => call.method === "PUT").length === writesBeforeReadbackFailure + 1 && fullWindow.document.querySelector('[data-action="reload-plan-detail"]'), "a successful PUT followed by failed detail readback must lock for an explicit retry");
  assert.match(fullWindow.document.body.textContent, /已保存，但读取最新配置失败/, "a successful PUT followed by failed detail readback must be explicit");
  assert.equal(fullWindow.document.querySelector('.group-ops__notice--error')?.getAttribute('role'), 'alert', "readback failure must remain an accessible alert");
  assert.equal(fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, true, "readback-pending state must keep basic save locked");
  assert.equal(fullWindow.document.querySelector('[data-action="save-active-detail-panel"]')?.disabled, true, "readback-pending state must keep shared save locked");
  fullWindow.document.querySelector('[data-action="save-active-detail-panel"]').click();
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeReadbackFailure + 1, "readback-pending state must not issue a replacement PUT");
  failPlanReadback = false;
  fullWindow.document.querySelector('[data-action="reload-plan-detail"]').click();
  await waitFor(() => state.plan.name === "已写待回读" && !fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, "retrying detail readback must restore the authoritative editable plan");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeReadbackFailure + 1, "readback retry must not submit a second PUT");
  fullWindow.document.querySelector('[name="plan_name"]').value = "错误详情 ID 只读恢复";
  const writesBeforeWrongID = calls.filter((call) => call.method === "PUT").length;
  returnWrongPlanIDAfterWrite = true;
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => calls.filter((call) => call.method === "PUT").length === writesBeforeWrongID + 1 && fullWindow.document.querySelector('[data-action="reload-plan-detail"]'), "a mismatched 200 detail ID must enter readback-only recovery");
  assert.match(fullWindow.document.body.textContent, /与当前页面不一致/, "a mismatched 200 detail ID must be visible instead of silently leaving the save locked");
  fullWindow.document.querySelector('[data-action="reload-plan-detail"]').click();
  await waitFor(() => state.plan.name === "错误详情 ID 只读恢复" && !fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, "mismatched detail recovery must read the authoritative plan without a second write");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeWrongID + 1, "mismatched detail recovery must stay read-only");
  fullWindow.document.querySelector('[name="status"]').value = "active";
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => state.plan.status === "active", "saving the selected owner did not enable the existing plan");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "Host must write the selected staff id as owner_staff_id");
  assert.equal(state.plan.name, "错误详情 ID 只读恢复", "successful retry must submit the retained authoritative draft");
  assert.equal(fullWindow.document.querySelector('.group-ops__notice--error'), null, "successful retry clears the prior failure");

  await waitFor(() => fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]'), "detail did not reload after owner save");
  // Asset commands are draft-only at the Owner boundary. The selector must
  // make that state explicit, and this scoped bind journey proceeds from a
  // genuine draft plan rather than weakening the service rule.
  state.plan.status = "draft";
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  const groupOpen = fullWindow.document.querySelector('[data-action="open-group-picker"]');
  groupOpen.focus();
  groupOpen.click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key]'), "V3 scoped group picker did not render the authorised directory page");
  assert.equal(fullWindow.document.querySelector('[data-group-picker-search]'), null, 'the frozen per-keystroke group picker never opens beneath the V3 session');
  const pickerSearch = fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-picker-search-input]');
  const readsBeforePickerSearch = calls.length;
  pickerSearch.value = "九号";
  pickerSearch.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  pickerSearch.dispatchEvent(new fullWindow.KeyboardEvent("keydown", { bubbles: true, key: "Enter" }));
  await waitFor(() => calls.slice(readsBeforePickerSearch).some((call) => call.method === "GET" && call.path.includes("/group-ops/groups?") && new URL(call.path, fullWindow.location.href).searchParams.get("q") === "九号"), "V3 group picker did not send the server query");
  const ownerScopedPickerRead = calls.slice(readsBeforePickerSearch).find((call) => call.method === "GET" && call.path.includes("/group-ops/groups?") && new URL(call.path, fullWindow.location.href).searchParams.get("q") === "九号");
  assert.equal(new URL(ownerScopedPickerRead.path, fullWindow.location.href).searchParams.get("owner_userid"), "9", "picker search must retain the current Owner scope");
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]')?.disabled === false, "Owner-scoped search did not settle before confirmation");
  const groupRow = fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key]');
  assert.equal(groupRow.textContent.includes("group-9"), true, "picker displays the opaque GroupOps chat reference");
  groupRow.click();
  assert.equal(fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key]')?.getAttribute("aria-pressed"), "true", "Owner-scoped query row remains selectable");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]').click();
  await waitFor(() => state.group_assets.length === 1, "selected directory group was not bound through the existing GroupOps Owner command");
  assert.equal(state.group_assets[0].asset_reference, "group-9");
  assert.equal(fullWindow.document.querySelector('[data-v3-selection-session="group"]'), null, "successful commit closes the temporary selection session");

  state.directory.push(
    { chat_reference: "group-10", owner_staff_id: 9, display_name: "十号运营群", member_count: 13, external_member_count: 9 },
    { chat_reference: "group-11", owner_staff_id: 9, display_name: "十一号运营群", member_count: 14, external_member_count: 10 },
  );
  dropCommittedGroupResponse = "group-10";
  rejectGroupOnce = "group-11";
  fullWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key$="group-11"]'), "multi-step group picker did not load scoped results");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key$="group-10"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key$="group-11"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-remove$="group-9"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]')?.textContent.includes("已实际保存：添加 group-10"), "partial group save did not preserve the actual accepted step");
  const group10Commands = groupSelectionCommands.filter((command) => command.reference === "group-10");
  assert.equal(group10Commands.length, 1, "dropped accepted response must not replay the confirmed step");
  assert(state.group_assets.some((item) => item.asset_reference === "group-10") && state.group_assets.some((item) => item.asset_reference === "other-concurrent"), "Owner readback must retain accepted and concurrent bindings");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]') === null && state.group_assets.some((item) => item.asset_reference === "group-11") && !state.group_assets.some((item) => item.asset_reference === "group-9"), "explicit retry did not finish only the remaining group differences");
  const group11Commands = groupSelectionCommands.filter((command) => command.reference === "group-11");
  assert.equal(group11Commands.length, 2, "only the unconfirmed step is retried");
  assert.equal(group11Commands[0].idempotencyKey, group11Commands[1].idempotencyKey, "retry preserves the step idempotency key");
  assert.deepEqual(group11Commands[0].body, group11Commands[1].body, "retry preserves the frozen CAS command body");
  assert(state.group_assets.some((item) => item.asset_reference === "other-concurrent"), "selection retry never removes another actor's concurrent binding");
  const cachedGroups = await fullWindow.AdminApi.requestJson("/api/admin/automation-conversion/group-ops/plans/41/groups");
  const cachedGroup10 = cachedGroups.items.find((item) => item.chat_id === "group-10");
  assert.deepEqual(
    { name: cachedGroup10.group_name, owner: cachedGroup10.owner_userid, internal: cachedGroup10.internal_member_count_snapshot, external: cachedGroup10.external_member_count_snapshot },
    { name: "十号运营群", owner: "9", internal: 4, external: 9 },
    "confirmed groups retain raw directory name, owner and member snapshots",
  );
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-action="open-group-picker"]'), "group panel did not return after a tab switch");
  fullWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]')?.textContent.includes("十号运营群"), "reopened group picker lost the confirmed directory name");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-cancel]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]') === null, "cancel must close without inventing a rollback");
  await waitFor(() => state.group_assets.some((item) => item.asset_reference === "group-10"), "cancel readback must preserve the actual saved binding");
  await waitFor(() => fullWindow.document.querySelector('[data-action="remove-group"][data-chat-id="group-11"]'), "successful selection did not redraw a native removable group row");
  fullWindow.document.querySelector('[data-action="remove-group"][data-chat-id="group-11"]').click();
  await waitFor(() => !state.group_assets.some((item) => item.asset_reference === "group-11"), "native row action did not remove the freshly rendered binding");
  assert(state.group_assets.some((item) => item.asset_reference === "other-concurrent"), "fresh native remove action preserves concurrent binding");

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
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="basic"]').click();
  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-owner"]'), "draft owner picker missing");
  fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-owner"] [data-operation-member-row-select]').click();
  fullWindow.document.querySelector('[data-operation-member-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[name="owner_userid"]')?.value === "7", "draft owner missing");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  const writesBeforeRefresh = calls.filter(item => item.method !== "GET" && !item.path.endsWith("/sync")).length;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("已刷新 3 个群聊"), "snapshot total notice missing");
  assert(fullWindow.document.querySelector('.group-ops__group-name').textContent.includes("同步群名1"), "bound group name must refresh without a page reload");
  assert(fullWindow.document.body.textContent.includes("other-concurrent"), "readback must retain another actor's concurrent binding");
  assert.equal(fullWindow.document.body.textContent.includes("231"), false, "an unknown concurrent binding must not fabricate an aggregate external count");
  assert.equal(fullWindow.document.querySelector('[name="owner_userid"]').value, "7", "refresh must preserve unsaved owner");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "refresh must not save the draft owner");
  assert.equal(calls.filter(item => item.method !== "GET" && !item.path.endsWith("/sync")).length, writesBeforeRefresh, "refresh must not save or enable the plan");
  failGroupReadback = true;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("群目录读取失败，请重试；已绑定群仍可查看"), "directory failure must preserve the bound projection and state its reason");
  assert.match(fullWindow.document.querySelector('.group-ops__group-name').textContent, /同步群名[12]/, "directory failure preserves a readable bound snapshot");
  assert.equal(fullWindow.document.body.textContent.includes("暂无绑定群"), false);
  failGroupReadback = false;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.querySelector('.group-ops__group-name')?.textContent.includes("同步群名3"), "retry must update the bound projection");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="basic"]').click();
  fullWindow.document.querySelector('[name="plan_name"]').value = "新保存不会被旧读覆盖";
  const deferredSave = fullWindow.document.querySelector('[data-action="save-plan"]');
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  delayNextPlanRead = true;
  fullWindow.document.querySelector('[data-action="remove-group"]').click();
  await waitFor(() => releaseDelayedPlanRead && fullWindow.document.body.textContent.includes("加载中"), "old detail load did not pause for the stale-response assertion");
  const staleInput = fullWindow.document.createElement('input');
  staleInput.name = 'plan_name'; staleInput.value = '新保存不会被旧读覆盖';
  fullWindow.document.getElementById('group-ops-app').append(staleInput);
  deferredSave.click();
  await waitFor(() => state.plan.name === '新保存不会被旧读覆盖', "new save/readback did not complete before stale detail resumed");
  releaseDelayedPlanRead();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(state.plan.name, '新保存不会被旧读覆盖', "a delayed old detail response must not overwrite the newer authoritative save/readback");
  assert.equal(fullWindow.document.body.textContent.includes('新保存不会被旧读覆盖'), true, "render after a stale response must retain the newer plan state");
  if (fullJourneyErrors.length) throw new Error(`Group Ops standard DOM errors: ${JSON.stringify(fullJourneyErrors)}`);
  console.log("groupops-standard-dom: PASS");
} finally {
  fullJourney.window.close();
}

// Webhook presentation is rendered by the same standard Host: it must expose
// the configured, callable URL and give a truthful copy receipt. A missing
// descriptor takes the explicit unavailable branch in the production code.
const copiedWebhook = [];
const webhookCalls = [];
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
  webhookCalls.push({ path: url.pathname + url.search, method });
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
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/plans/52").length, 1, "Webhook detail hydration must issue one plan read");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0").length, 1, "Webhook detail hydration must issue one unfiltered group-directory read");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/groups?owner_userid=7").length, 1, "Webhook owner picker directory remains an independent read");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/plans/52/webhook-descriptor").length, 1, "Webhook descriptor remains an independent read");
  assert.equal(webhookWindow.document.querySelector('[name="webhook_reference"]'), null, "users must not enter technical webhook references");
  webhookWindow.document.querySelector('[data-action="save-webhook"]').click();
  await waitFor(() => webhookWindow.document.querySelector('[data-action="copy-webhook"]'), "saved webhook did not reread and render its copy action");
  const expectedWebhookURL = `https://groupops.test${webhookDescriptor.path}`;
  assert.equal(webhookWindow.document.querySelector(".group-ops__url")?.textContent, expectedWebhookURL, "Webhook presentation must show the configured callable URL");
  assert(webhookWindow.document.body.textContent.includes("无需预设节点") && webhookWindow.document.body.textContent.includes("已绑定群的子集") && webhookWindow.document.body.textContent.includes("签名验证（HMAC-SHA256）") && webhookWindow.document.querySelector(".group-ops__webhook-guide")?.textContent.includes("复制地址不包含凭据，也不能绕过签名验证") && webhookWindow.document.body.textContent.includes("X-Signature / X-Timestamp / X-Nonce / X-Client-ID"), "Webhook presentation must explain dynamic content, descriptor headers and signing without exposing a secret");
  webhookWindow.document.querySelector('[data-action="copy-webhook"]').click();
  await waitFor(() => copiedWebhook[0] === expectedWebhookURL, "Webhook copy did not reach the clipboard");
  await waitFor(() => webhookWindow.document.body.textContent.includes("Webhook 地址已复制"), "Webhook copy did not render a success receipt");
  console.log("groupops-webhook-dom: PASS");
} finally {
  webhookJourney.window.close();
}

// List lifecycle controls must give a visible in-flight state, submit exactly
// once, and only show enabled after the V3 command response has been read.
let listPlan = { plan_id: 13, name: "授权测试群计划", revision: 8, status: "disabled", plan_type: "standard", owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
let enableCalls = 0;
let releaseEnable;
let holdConflictReads = false;
const pendingConflictReads = [];
const delayedConflictRead = (body) => new Promise((resolve) => pendingConflictReads.push(() => resolve(response(body))));
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
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return holdConflictReads ? delayedConflictRead({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] }) : response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return holdConflictReads ? delayedConflictRead({ items: [clone(listPlan)], total: 1 }) : response({ items: [clone(listPlan)], total: 1 });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/13" && method === "GET") return holdConflictReads ? delayedConflictRead({ plan: clone(listPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] }) : response({ plan: clone(listPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/13/enable" && method === "POST") {
    enableCalls += 1;
    const body = JSON.parse(String(init.body));
    assert.equal(body.expected_revision, enableCalls === 1 ? 8 : 9, "enable must use the latest read revision");
    if (enableCalls === 1) {
      // Model a concurrent server mutation. The failure must cause reads only;
      // a second POST happens only after the next explicit click.
      listPlan = { ...listPlan, revision: 9 };
      holdConflictReads = true;
      return response({ error: { code: "operations_conflict" } }, 409);
    }
    return new Promise((resolve) => {
      releaseEnable = () => {
        listPlan = { ...listPlan, status: "active", revision: 10 };
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
  await waitFor(() => pendingConflictReads.length === 3 && enableCalls === 1 && enable()?.disabled, "conflict refresh did not keep lifecycle control locked");
  enable().click();
  assert.equal(enableCalls, 1, "an explicit click during conflict readback must not submit another POST");
  holdConflictReads = false;
  pendingConflictReads.splice(0).forEach((release) => release());
  await waitFor(() => listWindow.document.body.textContent.includes("计划状态、版本或配置不满足要求，请刷新后检查") && !enable()?.disabled, "failed enable must keep a retryable control and visible error");
  assert.equal(listPlan.revision, 9, "conflict fixture must expose a newer server revision");
  assert.equal(listWindow.document.querySelector(".group-ops__notice")?.classList.contains("group-ops__notice--error"), true, "failed enable notice must not use the green success style");
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

// Archived plans are terminal in both projected list and detail views. The
// browser must not render an enable/delete path or a writable detail control.
const archivedPlan = {
  plan_id: 77,
  name: "已归档群运营计划",
  revision: 12,
  status: "archived",
  plan_type: "standard",
  owner: {
    staff_id: 7,
    sender_userid: "wecom-owner",
    display_name: "一号运营",
    name_source: "wecom_profile",
    profile_read_state: "ready",
  },
};
const archivedListJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const archivedListWindow = archivedListJourney.window;
archivedListWindow.Headers = Headers;
archivedListWindow.Response = Response;
Object.defineProperty(archivedListWindow, "crypto", { configurable: true, value: crypto });
archivedListWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), archivedListWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response({ items: [clone(archivedPlan)], total: 1 });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/77" && method === "GET") return response({ plan: clone(archivedPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected archived-list request ${method} ${url.pathname}`);
};
try {
  archivedListWindow.eval(pickerSource);
  archivedListWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => archivedListWindow.document.body.textContent.includes("已归档"), "archived list status did not render");
  assert(archivedListWindow.document.body.textContent.includes("一号运营"), "list must render the same trusted owner projection as detail");
  assert.equal(archivedListWindow.document.querySelector('[data-action="enable-plan"]'), null, "archived list must not render an enable action");
  assert.equal(archivedListWindow.document.querySelector('[data-action="delete-plan"]'), null, "archived list must not offer a repeat archive action");
  console.log("groupops-archived-list-dom: PASS");
} finally {
  archivedListJourney.window.close();
}

const archivedWrites = [];
const archivedDetailJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="77"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/77",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const archivedDetailWindow = archivedDetailJourney.window;
archivedDetailWindow.Headers = Headers;
archivedDetailWindow.Response = Response;
Object.defineProperty(archivedDetailWindow, "crypto", { configurable: true, value: crypto });
archivedDetailWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), archivedDetailWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (method !== "GET") archivedWrites.push({ path: url.pathname, method });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/77" && method === "GET") return response({ plan: clone(archivedPlan), members: [{ staff_id: 7 }], group_assets: [{ asset_reference: "archived-group" }], nodes: [{ node_id: 11, position: 1, kind: "message", day_index: 1, scheduled_time: "20:00", trigger_time_label: "20:00", action_title: "已归档动作", node_status: "active", message_text: "只读内容", delay_minutes: 0, material_plan: { references: [] } }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [{ chat_reference: "archived-group", owner_staff_id: 7, display_name: "已归档群", member_count: 2, external_member_count: 1 }], total: 1, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected archived-detail request ${method} ${url.pathname}`);
};
try {
  archivedDetailWindow.eval(pickerSource);
  archivedDetailWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => archivedDetailWindow.document.querySelector('[name="plan_name"]'), "archived detail did not render");
  assert.equal(archivedDetailWindow.document.querySelector('[name="plan_name"]')?.disabled, true, "archived plan name must be read-only");
  assert.equal(archivedDetailWindow.document.querySelector('[name="status"]')?.value, "archived", "archived detail must keep the terminal status selected");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="save-plan"]'), null, "archived detail must not render a base save action");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="save-active-detail-panel"]')?.disabled, true, "archived detail must disable save-current-dimension");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="pick-plan-owner"]'), null, "archived detail must not offer owner changes");
  archivedDetailWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="open-group-picker"]'), null, "archived detail must not offer group binding");
  archivedDetailWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="open-node-modal"]'), null, "archived detail must not offer node creation");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="edit-node"]'), null, "archived detail must not offer node edits");
  assert.deepEqual(archivedWrites, [], "archived UI navigation must not submit a write");
  console.log("groupops-archived-detail-dom: PASS");
} finally {
  archivedDetailJourney.window.close();
}
