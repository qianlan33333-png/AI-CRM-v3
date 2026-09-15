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
let explicitRevisionWriteCalls = 0;
const foreignRequests = [];
const foreignPayload = { items: [{ staff_id: 5, sender_userid: "external-user", display_name: "External name" }] };
let nodes = [];
let savedOwner = [];
let operationMemberReads = 0;
let ownerProjection = { staff_id: 7, sender_userid: "real-owner", display_name: "真实昵称 · 完整姓名", name_source: "wecom_profile", profile_read_state: "ready" };
const planPage = (items, total = items.length, offset = 0, hasMore = false) => ({ items, total, limit: 50, offset, has_more: hasMore });
let listPayload = planPage([{ plan_id: 41, name: "列表计划", revision: 7, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 3 }]);
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
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && (!init.method || init.method === "GET")) {
    return new Response(JSON.stringify(listPayload), { status: 200, headers: { "content-type": "application/json" } });
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
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/enable" && init.method === "POST") {
    explicitRevisionWriteCalls += 1;
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
let projectedList = await host.requestJson("/api/admin/automation-conversion/group-ops/plans");
assert.equal(projectedList.items[0].bound_group_count, 3, "list binding count must come from the List DTO without a detail read");
listPayload = planPage([{ plan_id: 41, name: "旧服务列表计划", revision: 7, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0 }]);
projectedList = await host.requestJson("/api/admin/automation-conversion/group-ops/plans");
assert.equal(projectedList.items[0].bound_group_count, null, "an older List DTO must remain readable as an explicit unknown");
listPayload = planPage([
  { plan_id: 41, name: "已渲染计划", revision: 8, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 3 },
  { plan_id: 42, name: "错误列表计划", revision: 9, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: -1 },
]);
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划绑定群数数据无效/, "a later negative List DTO count must reject the complete page before it publishes an earlier revision");
listPayload = planPage([{ plan_id: 41, name: "错误列表计划", revision: 10, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 1.5 }]);
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划绑定群数数据无效/, "fractional List DTO counts must not become zero");
listPayload = planPage([{ plan_id: "41", name: "字符串计划 ID", revision: 11, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 0 }]);
assert.equal((await host.requestJson("/api/admin/automation-conversion/group-ops/plans")).items[0].id, 41, "the documented string plan ID remains a valid projection");
listPayload = { ...listPayload, total: "1" };
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划列表总数数据无效/, "a string total must not become a valid numeric page");
listPayload = planPage([{ plan_id: 41, name: "错误版本", revision: true, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 0 }]);
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划列表版本数据无效/, "a boolean List revision must not become a CAS value");
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/enable", { method: "POST", body: { expected_revision: true } }), /计划版本数据无效/, "a boolean expected revision must fail before a write");
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/enable", { method: "POST", body: { expected_revision: undefined } }), /计划版本数据无效/, "an explicitly undefined expected revision must fail before a write");
assert.equal(explicitRevisionWriteCalls, 0, "invalid explicit revisions must send zero writes");
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
  calls.push({ path: url.pathname + url.search, method, body });
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
    if (body.expected_revision !== state.revision) return response({ code: "revision_conflict" }, 409);
    state.group_assets.push({ asset_reference: body.asset_reference });
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
    state.directory[0].display_name = `同步群名${groupSyncAttempts}`;
    state.directory[0].member_count = 300 + groupSyncAttempts;
    state.directory[0].external_member_count = 230 + groupSyncAttempts;
    return response({ items: clone(state.directory), total: 1, limit: 100, offset: 0, has_more: false });
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
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="basic"]').click();
  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-owner"]'), "draft owner picker missing");
  fullWindow.document.querySelector('[data-operation-member-row][data-user-id="wecom-owner"] [data-operation-member-row-select]').click();
  fullWindow.document.querySelector('[data-operation-member-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[name="owner_userid"]')?.value === "7", "draft owner missing");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  const writesBeforeRefresh = calls.filter(item => item.method !== "GET" && !item.path.endsWith("/sync")).length;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("已刷新 1 个群聊"), "snapshot total notice missing");
  assert(fullWindow.document.querySelector('.group-ops__group-name').textContent.includes("同步群名1"), "bound group name must refresh without a page reload");
  assert(fullWindow.document.body.textContent.includes("231"), "external contact overview must read back the new snapshot");
  assert.equal(fullWindow.document.querySelector('[name="owner_userid"]').value, "7", "refresh must preserve unsaved owner");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "refresh must not save the draft owner");
  assert.equal(calls.filter(item => item.method !== "GET" && !item.path.endsWith("/sync")).length, writesBeforeRefresh, "refresh must not save or enable the plan");
  failGroupReadback = true;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("群聊已刷新，但页面读回失败"), "readback failure must not claim UI completion");
  assert(fullWindow.document.querySelector('.group-ops__group-name').textContent.includes("同步群名1"), "failed readback preserves displayed snapshot");
  assert.equal(fullWindow.document.body.textContent.includes("新增 0"), false);
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
let listPlan = { plan_id: 13, name: "授权测试群计划", revision: 8, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 2, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
let enableCalls = 0;
let releaseEnable;
let holdConflictReads = false;
const pendingConflictReads = [];
const listRequests = [];
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
  listRequests.push({ path: url.pathname + url.search, method });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return holdConflictReads ? delayedConflictRead({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] }) : response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return holdConflictReads ? delayedConflictRead(planPage([clone(listPlan)])) : response(planPage([clone(listPlan)]));
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
  assert.deepEqual(
    listRequests.filter((request) => request.method === "GET").map((request) => request.path).sort(),
    ["/api/admin/automation-conversion/group-ops/plans?limit=50&offset=0", "/api/admin/common/operation-members?scope=group_ops&page_size=100"].sort(),
    "an initial list must read only its page and the existing operation-member projection",
  );
  assert.equal(listWindow.document.body.textContent.includes("已绑定群（暂不可用）"), false, "a valid zero-or-positive List DTO count remains known");
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

// Offset pagination keeps an already rendered page authoritative for its own
// actions when another page fails. This mounts the real Host and Standard
// controller together so the assertions cover URL transport, DOM state and
// explicit row revisions as one contract.
const pagedPlan = {
  plan_id: 91,
  name: "第 1 页计划",
  revision: 50,
  status: "disabled",
  plan_type: "standard",
  queue_count: 3,
  bound_group_count: 2,
  owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" },
};
const firstPagePlans = Array.from({ length: 50 }, (_, index) => index === 0
  ? pagedPlan
  : { ...pagedPlan, plan_id: 91 + index, name: `第 1 页计划 ${index + 1}`, revision: 50 + index, queue_count: index % 3, bound_group_count: index % 2 });
const secondPagePlan = { ...pagedPlan, plan_id: 141, name: "第 2 页计划", revision: 6, queue_count: 1, bound_group_count: 1 };
let pageFiftyFails = true;
let pagedEnableWrites = 0;
let pagedArchiveWrites = 0;
let pagedWriteReply = "conflict";
let archiveTailRead = false;
let pagedDetailRevision = 50;
const pagedRequests = [];
const paginationJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const paginationWindow = paginationJourney.window;
paginationWindow.Headers = Headers;
paginationWindow.Response = Response;
Object.defineProperty(paginationWindow, "crypto", { configurable: true, value: crypto });
paginationWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
paginationWindow.confirm = () => true;
paginationWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), paginationWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  pagedRequests.push({ path: url.pathname + url.search, method, body: init.body ? JSON.parse(String(init.body)) : null });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    const offset = Number(url.searchParams.get("offset"));
    if (offset === 0) return response(planPage(clone(firstPagePlans), 51, 0, true));
    if (offset === 50 && pageFiftyFails) return response({ code: "service_unavailable" }, 503);
    if (offset === 50 && archiveTailRead) return response(planPage([], 50, 50, false));
    if (offset === 50) return response(planPage([clone(secondPagePlan)], 51, 50, false));
    throw new Error(`unexpected page offset ${offset}`);
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/91/enable" && method === "POST") {
    pagedEnableWrites += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 50, "a retained first-page row must submit its rendered revision, not a fresh detail revision");
    if (pagedWriteReply === "conflict") return response({ error: { code: "operations_conflict" } }, 409);
    if (pagedWriteReply === "empty") return response({});
    if (pagedWriteReply === "wrong-id") return response({ plan: { ...pagedPlan, plan_id: 999, revision: 51, status: "active" } });
    if (pagedWriteReply === "boolean-id") return response({ plan: { ...pagedPlan, plan_id: true, revision: 51, status: "active" } });
    return response({ plan: { ...pagedPlan, revision: 51, status: "disabled" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/91" && method === "DELETE") {
    pagedArchiveWrites += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 50, "archive must submit its rendered revision");
    return response({ plan: { ...pagedPlan, revision: 51, status: "disabled" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/141" && method === "DELETE") {
    pagedArchiveWrites += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 6, "tail-page archive must submit its rendered revision");
    archiveTailRead = true;
    return response({ plan: { ...secondPagePlan, revision: 7, status: "archived" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/91" && method === "GET") return response({ plan: { ...clone(pagedPlan), revision: pagedDetailRevision }, members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected paged request ${method} ${url.pathname}`);
};
try {
  paginationWindow.eval(pickerSource);
  paginationWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => paginationWindow.document.querySelector('[data-action="next-list-page"]'), "initial paged list did not render");
  assert.equal(paginationWindow.document.querySelectorAll("tbody tr").length, 50, "a first visible page must render exactly 50 rows from a 51-item fixture");
  assert(paginationWindow.document.body.textContent.includes("第 1–50 项，共 51 项"), "the first page range must describe 50 rendered rows");
  assert.equal(paginationWindow.document.querySelector('[data-action="previous-list-page"]').disabled, true, "the first page must disable previous");
  assert.equal(paginationWindow.document.querySelector('[data-action="next-list-page"]').disabled, false, "the first page must enable next when has_more");
  assert.deepEqual(
    pagedRequests.filter((request) => request.method === "GET").map((request) => request.path).sort(),
    ["/api/admin/automation-conversion/group-ops/plans?limit=50&offset=0", "/api/admin/common/operation-members?scope=group_ops&page_size=100"].sort(),
    "the initial visible page must issue only its page GET and the established member read",
  );
  paginationWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="retry-list-page"]'), "a failed second page did not render a retryable alert");
  assert.equal(paginationWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "a failed target page must lock pagination until its exact retry");
  assert.equal(paginationWindow.document.querySelector('[data-action="enable-plan"]').disabled, false, "a failed target page must not invalidate the retained row's explicit CAS action");
  assert(paginationWindow.document.querySelector('[role="alert"]').textContent.includes("读取当前页失败"), "the failed target page must remain visibly distinct from an empty list");
  pageFiftyFails = false;
  paginationWindow.document.querySelector('[data-action="retry-list-page"]').click();
  await waitFor(() => paginationWindow.document.body.textContent.includes("第 51–51 项，共 51 项"), "retry did not reread the exact second-page offset");
  assert.equal(paginationWindow.document.querySelector('[data-action="previous-list-page"]').disabled, false, "the last page must allow previous");
  assert.equal(paginationWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "the last page must disable next");
  assert.equal(paginationWindow.document.body.textContent.includes("本页通知排队"), true, "page-only metrics must be labeled as page-only");
  assert.equal(pagedRequests.filter((request) => request.path === "/api/admin/automation-conversion/group-ops/plans?limit=50&offset=50" && request.method === "GET").length, 2, "retry must resend only the original failed offset");
  paginationWindow.document.querySelector('[data-action="previous-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="enable-plan"]')?.dataset.planRevision === "50", "previous page did not restore its rendered action snapshot");
  pageFiftyFails = true;
  paginationWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="retry-list-page"]'), "second retained-page failure did not render");
  pagedDetailRevision = 51;
  await paginationWindow.AdminApi.requestJson("/api/admin/automation-conversion/group-ops/plans/91");
  assert.equal(paginationWindow.document.querySelector('[data-action="enable-plan"]')?.dataset.planRevision, "50", "a fresh Host detail cache must not replace the retained row snapshot");
  paginationWindow.document.querySelector('[data-action="enable-plan"]').click();
  await waitFor(() => pagedEnableWrites === 1 && paginationWindow.document.querySelector(".group-ops__notice--error"), "a retained row conflict did not surface after using its original revision");
  assert.equal(paginationWindow.document.body.textContent.includes("已启用"), false, "a 409 must not be rendered as an enable success");
  assert.equal(pagedEnableWrites, 1, "a 409 must not trigger an automatic second write");
  for (const reply of ["empty", "wrong-id", "boolean-id", "wrong-status"]) {
    pagedWriteReply = reply;
    paginationWindow.document.querySelector('[data-action="enable-plan"]').click();
    await waitFor(() => pagedEnableWrites === (reply === "empty" ? 2 : reply === "wrong-id" ? 3 : reply === "boolean-id" ? 4 : 5) && paginationWindow.document.body.textContent.includes("启用结果未确认"), `a 200 ${reply} enable reply was incorrectly accepted`);
  }
  paginationWindow.document.querySelector('[data-action="delete-plan"]').click();
  await waitFor(() => pagedArchiveWrites === 1 && paginationWindow.document.body.textContent.includes("归档结果未确认"), "a wrong-state archive reply was incorrectly accepted");
  pageFiftyFails = false;
  paginationWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="delete-plan"]')?.dataset.planId === "141", "the tail page did not render for archive fallback");
  const tailReadsBeforeArchive = pagedRequests.filter((request) => request.path === "/api/admin/automation-conversion/group-ops/plans?limit=50&offset=50" && request.method === "GET").length;
  paginationWindow.document.querySelector('[data-action="delete-plan"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="enable-plan"]')?.dataset.planId === "91" && paginationWindow.document.body.textContent.includes("已归档"), "an empty tail page after archive did not return once to the previous page");
  assert.equal(pagedRequests.filter((request) => request.path === "/api/admin/automation-conversion/group-ops/plans?limit=50&offset=50" && request.method === "GET").length, tailReadsBeforeArchive + 1, "tail-page archive must reread its own offset once before fallback");
  console.log("groupops-list-pagination-dom: PASS");
} finally {
  paginationJourney.window.close();
}

// A successful write whose authority readback fails must not unlock any row
// until the retry performs only that GET. Two rows make a single-value lock
// regression observable.
let writeReadbackFails = false;
let writeReadbackPosts = 0;
let writeReadbackPlans = 0;
let writeFirst = { plan_id: 301, name: "写后回读 A", revision: 5, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 0, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
const writeSecond = { ...writeFirst, plan_id: 302, name: "写后回读 B", revision: 8 };
const writeReadbackJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const writeReadbackWindow = writeReadbackJourney.window;
writeReadbackWindow.Headers = Headers;
writeReadbackWindow.Response = Response;
Object.defineProperty(writeReadbackWindow, "crypto", { configurable: true, value: crypto });
writeReadbackWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
writeReadbackWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), writeReadbackWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    writeReadbackPlans += 1;
    if (writeReadbackFails) return response({ code: "service_unavailable" }, 503);
    return response(planPage([clone(writeFirst), clone(writeSecond)], 2, 0, false));
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/301/enable" && method === "POST") {
    writeReadbackPosts += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 5, "the write must use A's rendered revision");
    writeFirst = { ...writeFirst, revision: 6, status: "active" };
    return response({ plan: clone(writeFirst) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/302/enable" && method === "POST") {
    writeReadbackPosts += 1;
    return response({ plan: { ...writeSecond, revision: 9, status: "active" } });
  }
  throw new Error(`unexpected write-readback request ${method} ${url.pathname}`);
};
try {
  writeReadbackWindow.eval(pickerSource);
  writeReadbackWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => writeReadbackWindow.document.querySelectorAll('[data-action="enable-plan"]').length === 2, "write-readback fixture did not render two rows");
  writeReadbackFails = true;
  writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="301"]').click();
  await waitFor(() => writeReadbackWindow.document.body.textContent.includes("操作已执行，但当前页未更新") && writeReadbackWindow.document.querySelector('[data-action="retry-list-page"]'), "successful write with failed readback did not retain a GET-only retry");
  assert.equal(writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="301"]').disabled, true, "failed readback must lock the written row");
  assert.equal(writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="302"]').disabled, true, "failed readback must also retain any earlier write lock instead of replacing it");
  writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="302"]').click();
  assert.equal(writeReadbackPosts, 1, "a locked second row must not submit another write");
  writeReadbackFails = false;
  writeReadbackWindow.document.querySelector('[data-action="retry-list-page"]').click();
  await waitFor(() => writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="302"]')?.disabled === false, "a successful GET-only retry did not unlock the refreshed page");
  assert.equal(writeReadbackPosts, 1, "readback retry must not repeat the completed write");
  assert.equal(writeReadbackPlans, 3, "write readback must be initial GET, failed GET and one retry GET");
  console.log("groupops-list-write-readback-dom: PASS");
} finally {
  writeReadbackJourney.window.close();
}

// Abort reduces work, but an old fetch can still fulfill or reject. A write
// readback starts a newer generation while a user navigation is outstanding;
// neither the late success nor its late rejection may release the newer busy
// state or publish stale rows.
const racePlan = { plan_id: 211, name: "竞态旧页", revision: 12, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 0, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
const raceReadbackPlan = { ...racePlan, name: "权威回读页", revision: 13, status: "active" };
let releaseRaceWrite;
let releaseRaceReadback;
let releaseLatePage;
let releaseLateMember;
let raceNextSignal;
let racePlanZeroReads = 0;
let raceMemberReads = 0;
const paginationRaceJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const paginationRaceWindow = paginationRaceJourney.window;
paginationRaceWindow.Headers = Headers;
paginationRaceWindow.Response = Response;
Object.defineProperty(paginationRaceWindow, "crypto", { configurable: true, value: crypto });
paginationRaceWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
paginationRaceWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), paginationRaceWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") {
    raceMemberReads += 1;
    if (raceMemberReads === 2) return new Promise((resolve) => { releaseLateMember = resolve; });
    return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    const offset = Number(url.searchParams.get("offset"));
    if (offset === 50) {
      raceNextSignal = init.signal;
      return new Promise((resolve) => { releaseLatePage = resolve; });
    }
    racePlanZeroReads += 1;
    if (racePlanZeroReads === 1) return response(planPage([clone(racePlan)], 51, 0, true));
    return new Promise((resolve) => { releaseRaceReadback = resolve; });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/211/enable" && method === "POST") {
    assert.equal(JSON.parse(String(init.body)).expected_revision, 12, "write/readback race must retain the rendered CAS revision");
    return new Promise((resolve) => { releaseRaceWrite = resolve; });
  }
  throw new Error(`unexpected race request ${method} ${url.pathname}`);
};
try {
  paginationRaceWindow.eval(pickerSource);
  paginationRaceWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => paginationRaceWindow.document.querySelector('[data-action="enable-plan"]'), "race fixture did not render initial action");
  paginationRaceWindow.document.querySelector('[data-action="enable-plan"]').click();
  await waitFor(() => typeof releaseRaceWrite === "function", "race fixture did not start the write");
  paginationRaceWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => typeof releaseLatePage === "function" && typeof releaseLateMember === "function", "race fixture did not start the older navigation read");
  releaseRaceWrite(response({ plan: clone(raceReadbackPlan) }));
  await waitFor(() => typeof releaseRaceReadback === "function", "write did not begin its newer authoritative readback");
  assert.equal(raceNextSignal?.aborted, true, "a write readback must abort its superseded navigation request");
  releaseLatePage(response(planPage([{ ...racePlan, plan_id: 261, name: "过期第 2 页" }], 51, 50, false)));
  releaseLateMember(response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] }));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(paginationRaceWindow.document.body.textContent.includes("过期第 2 页"), false, "a late old success must not publish over the newer readback");
  assert.equal(paginationRaceWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "an old finally must not release the newer readback busy state");
  releaseRaceReadback(response(planPage([clone(raceReadbackPlan)], 51, 0, true)));
  await waitFor(() => paginationRaceWindow.document.querySelector('[data-action="disable-plan"]')?.dataset.planRevision === "13", "the authoritative readback did not publish its newer row");
  assert.equal(paginationRaceWindow.document.querySelector('[data-action="next-list-page"]').disabled, false, "the completed newer readback must unlock pagination after an old success finally");
  console.log("groupops-list-pagination-race-dom: PASS");
} finally {
  paginationRaceJourney.window.close();
}

// The rejection side is independent from the late-success case above: even
// after the newer readback has rendered, a superseded request can still fail.
const lateRejectPlan = { plan_id: 281, name: "旧请求", revision: 12, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 0, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
const lateRejectReadbackPlan = { ...lateRejectPlan, name: "新回读", revision: 13, status: "active" };
let releaseLateRejectWrite;
let releaseLateRejectReadback;
let rejectLatePage;
let lateRejectPlanZeroReads = 0;
const lateRejectJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const lateRejectWindow = lateRejectJourney.window;
lateRejectWindow.Headers = Headers;
lateRejectWindow.Response = Response;
Object.defineProperty(lateRejectWindow, "crypto", { configurable: true, value: crypto });
lateRejectWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
lateRejectWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), lateRejectWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    const offset = Number(url.searchParams.get("offset"));
    if (offset === 50) return new Promise((resolve, reject) => { rejectLatePage = reject; });
    lateRejectPlanZeroReads += 1;
    if (lateRejectPlanZeroReads === 1) return response(planPage([clone(lateRejectPlan)], 51, 0, true));
    return new Promise((resolve) => { releaseLateRejectReadback = resolve; });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/281/enable" && method === "POST")
    return new Promise((resolve) => { releaseLateRejectWrite = resolve; });
  throw new Error(`unexpected late-reject request ${method} ${url.pathname}`);
};
try {
  lateRejectWindow.eval(pickerSource);
  lateRejectWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => lateRejectWindow.document.querySelector('[data-action="enable-plan"]'), "late-reject fixture did not render initial action");
  lateRejectWindow.document.querySelector('[data-action="enable-plan"]').click();
  await waitFor(() => typeof releaseLateRejectWrite === "function", "late-reject fixture did not start its write");
  lateRejectWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => typeof rejectLatePage === "function", "late-reject fixture did not start the superseded navigation read");
  releaseLateRejectWrite(response({ plan: clone(lateRejectReadbackPlan) }));
  await waitFor(() => typeof releaseLateRejectReadback === "function", "late-reject write did not start newer readback");
  releaseLateRejectReadback(response(planPage([clone(lateRejectReadbackPlan)], 51, 0, true)));
  await waitFor(() => lateRejectWindow.document.querySelector('[data-action="disable-plan"]')?.dataset.planRevision === "13", "newer readback did not render before late rejection");
  rejectLatePage(new Error("late page rejection"));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(lateRejectWindow.document.body.textContent.includes("late page rejection"), false, "a late old rejection must not replace a completed newer page");
  assert.equal(lateRejectWindow.document.querySelector('[data-action="next-list-page"]').disabled, false, "a late old finally must not re-lock the completed newer page");
  console.log("groupops-list-pagination-late-reject-dom: PASS");
} finally {
  lateRejectJourney.window.close();
}

// An initial read failure is unknown data, not a legitimate zero-plan page.
const initialFailureJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const initialFailureWindow = initialFailureJourney.window;
initialFailureWindow.Headers = Headers;
initialFailureWindow.Response = Response;
Object.defineProperty(initialFailureWindow, "crypto", { configurable: true, value: crypto });
initialFailureWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
let initialFailureStatus = 503;
initialFailureWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), initialFailureWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response({ code: initialFailureStatus === 403 ? "forbidden" : "service_unavailable" }, initialFailureStatus);
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
  throw new Error(`unexpected initial-failure request ${method} ${url.pathname}`);
};
try {
  initialFailureWindow.eval(pickerSource);
  initialFailureWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => initialFailureWindow.document.querySelector('[role="alert"]'), "initial list failure did not render a visible alert");
  assert.equal(initialFailureWindow.document.querySelector(".group-ops__metric-value")?.textContent, "—", "an initial failure must not display a fabricated zero total");
  assert(initialFailureWindow.document.body.textContent.includes("尚未取得列表数据"), "an initial failure must remain distinct from an authoritative empty page");
  assert.equal(initialFailureWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "pagination must remain disabled until the failed first page is retried");
  initialFailureWindow.document.querySelector('[data-action="show-create-plan"]').click();
  assert.equal(initialFailureWindow.document.querySelector(".group-ops__metric-value")?.textContent, "—", "opening creation after an unknown page must not fabricate a zero total");
  initialFailureWindow.document.querySelector('[data-action="cancel-create-plan"]').click();
  assert.equal(initialFailureWindow.document.querySelector(".group-ops__metric-value")?.textContent, "—", "cancelling creation after an unknown page must not fabricate a zero total");
  initialFailureStatus = 403;
  initialFailureWindow.document.querySelector('[data-action="retry-list-page"]').click();
  await waitFor(() => initialFailureWindow.document.body.textContent.includes("当前账号无权读取运营计划"), "403 list read did not clear the unknown page into a visible access error");
  assert.equal(initialFailureWindow.document.querySelector('[data-action="show-create-plan"]').disabled, true, "a forbidden list read must disable list writes");
  console.log("groupops-list-initial-failure-dom: PASS");
} finally {
  initialFailureJourney.window.close();
}

// Archived plans are terminal in both projected list and detail views. The
// browser must not render an enable/delete path or a writable detail control.
const archivedPlan = {
  plan_id: 77,
  name: "已归档群运营计划",
  revision: 12,
  status: "archived",
  plan_type: "standard",
  queue_count: 0,
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
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response(planPage([clone(archivedPlan)]));
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/77" && method === "GET") return response({ plan: clone(archivedPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected archived-list request ${method} ${url.pathname}`);
};
try {
  archivedListWindow.eval(pickerSource);
  archivedListWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => archivedListWindow.document.body.textContent.includes("已归档"), "archived list status did not render");
  assert(archivedListWindow.document.body.textContent.includes("已绑定群（暂不可用）"), "an older list response must make the missing binding metric visibly unknown");
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

// The groups page must distinguish an unavailable directory from a confirmed
// empty result. A failed follow-up read keeps the last rows visible, while an
// initial failure has no rows to retain.
async function assertGroupsReadFailureJourney({ hasPreviousRows }) {
  let groupReads = 0;
  const journey = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="groups"></main></body></html>`, {
    url: "https://groupops.test/admin/automation-conversion/group-ops/groups/ui",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  const page = journey.window;
  page.Headers = Headers;
  page.Response = Response;
  Object.defineProperty(page, "crypto", { configurable: true, value: crypto });
  page.fetch = async (input, init = {}) => {
    const url = new URL(String(input), page.location.href);
    const method = String(init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
      groupReads += 1;
      if (!hasPreviousRows || groupReads > 1)
        return response({ code: "directory_unavailable" }, 503);
      return response({
        items: [{ chat_reference: "known-group", display_name: "已读取群", owner_staff_id: 7 }],
        total: 1,
        limit: 200,
        offset: 0,
        has_more: false,
      });
    }
    if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response({ items: [] });
    if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
    throw new Error(`unexpected group read request ${method} ${url.pathname}${url.search}`);
  };
  try {
    page.eval(pickerSource);
    page.eval(bundle.outputFiles[0].text);
    if (!hasPreviousRows) {
      await waitFor(
        () => page.document.body.textContent.includes("群聊列表暂不可读取"),
        "initial groups read failure did not identify an unavailable directory",
      );
      assert.equal(
        page.document.body.textContent.includes("暂无数据"),
        false,
        "an initial group read failure must not look like a confirmed empty directory",
      );
      return;
    }
    await waitFor(() => page.document.body.textContent.includes("已读取群"), "initial group rows did not render");
    const bindStatus = page.document.querySelector('select[name="bind_status"]');
    bindStatus.value = "bound";
    bindStatus.dispatchEvent(new page.Event("change", { bubbles: true }));
    await waitFor(
      () => page.document.body.textContent.includes("当前显示上次读取结果"),
      "failed follow-up groups read did not identify retained rows",
    );
    assert(
      page.document.body.textContent.includes("已读取群"),
      "failed follow-up read must retain the previous rows",
    );
  } finally {
    journey.window.close();
  }
}

await assertGroupsReadFailureJourney({ hasPreviousRows: false });
await assertGroupsReadFailureJourney({ hasPreviousRows: true });
console.log("groupops-groups-read-failure-dom: PASS");
