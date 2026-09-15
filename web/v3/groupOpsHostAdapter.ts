// V3 Host adapter for the byte-derived Group Ops presentation. It owns only
// authenticated transport and DTO projection; plan, node and directory facts
// remain in internal/groupops and the existing WeCom read adapter.
import { openGroupPicker, type GroupPickerRecord } from './shared/ui/groupPickerAdapter';

type Json = Record<string, any>;
const base = "/api/admin/automation-conversion/group-ops";
const revisions = new Map<number, number>();
const planGroupViews = new Map<number, Json[]>();
const planGroupDirectoryViews = new Map<number, Map<string, GroupPickerRecord>>();
const planSummaryViews = new Map<number, Json>();
type InitialDetailReadKind = "plan" | "groups";
type InitialDetailReadEpoch = {
  id: number;
  generation: number;
  planClaimed: boolean;
  groupsClaimed: boolean;
  pairable: boolean;
  claims: number;
  detail: Promise<Json>;
  groups: Promise<Json[]> | null;
};
const initialDetailReadEpochs = new Map<number, InitialDetailReadEpoch>();
const detailReadGenerations = new Map<number, number>();
type GroupSelectionStep = { planID: number; kind: "add" | "remove"; reference: string; idempotencyKey: string; body?: Json };
type GroupSelectionOperation = { signature: string; steps: Map<string, GroupSelectionStep> };
const groupSelectionOperations = new Map<number, GroupSelectionOperation>();
let refreshedGroupTotal: number | null = null;
let openingGroupPicker = false;
let activeGroupPickerPlan: number | undefined;
const operationMembersPath = "/api/admin/common/operation-members";
const nativeFetch = window.fetch.bind(window);

function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), window.location.origin);
  if (typeof input === "string") return new URL(input, window.location.origin);
  return new URL(input.url, window.location.origin);
}

function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  if (init?.method) return init.method.toUpperCase();
  if (typeof input !== "string" && !(input instanceof URL)) return input.method.toUpperCase();
  return "GET";
}

function pickerMembersPayload(source: Json): Json | null {
  if (!Array.isArray(source.items)) return null;
  const items = source.items.flatMap((value: Json) => {
    const staffID = Number(value.staff_id);
    if (!Number.isSafeInteger(staffID) || staffID < 1) return [];
    return [{
      // The picker shows `user_id` as its second line. Keep the real WeCom
      // identity there, while preserving the local staff ID as the value that
      // plan commands write through the Host.
      user_id: String(value.sender_userid || staffID),
      staff_id: String(staffID),
      display_name: memberDisplayName(value),
    }];
  });
  return { scope: "group_ops", page_size: source.page_size, items };
}

function memberDisplayName(value: Json): string {
  const name = String(value.display_name || "").trim();
  const userID = String(value.sender_userid || "").trim();
  // Imported placeholder names are not real WeCom profile names.
  if (!name || (value.name_source !== "wecom_profile" && (name === `企微客服 ${userID}` || name === userID))) return "姓名待同步";
  return name;
}

// The frozen picker reads this endpoint directly instead of AdminApi.requestJson.
// Keep its byte-derived implementation untouched and make the single Group Ops
// read compatible at the V3 Host boundary. The frozen refresh call predates
// the V3 scoped command body, so add its scope, CSRF and idempotency envelope
// here without changing the donor picker.
window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = requestURL(input);
  if (url.origin !== window.location.origin) return nativeFetch(input, init);
  const method = requestMethod(input, init);
  if (method === "POST" && url.pathname === `${operationMembersPath}/sync`) {
    const headers = new Headers(init?.headers || (typeof input === "string" || input instanceof URL ? undefined : input.headers));
    headers.set("Accept", "application/json");
    headers.set("Content-Type", "application/json");
    headers.set("Idempotency-Key", key());
    const token = csrf();
    if (token) headers.set("X-CSRF-Token", token);
    return nativeFetch(input, {
      ...init,
      method: "POST",
      headers,
      credentials: "same-origin",
      body: JSON.stringify({ scope: "group_ops", page_size: 100 }),
    });
  }
  const response = await nativeFetch(input, init);
  if (
    method !== "GET" ||
    url.pathname !== operationMembersPath ||
    url.searchParams.get("scope") !== "group_ops" ||
    !response.ok
  ) return response;
  const source = await response.clone().json().catch(() => null);
  const projected = source && typeof source === "object" ? pickerMembersPayload(source as Json) : null;
  if (!projected) return response;
  return new Response(JSON.stringify(projected), {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
};

function csrf(): string {
  return (
    document.cookie
      .split(";")
      .map((part) => part.trim())
      .map((part) => part.split("="))
      .find(([name]) => name === "aicrm_csrf" || name === "aicrm_admin_csrf")
      ?.slice(1)
      .join("=") || ""
  );
}
function key(): string {
  return `groupops-${Date.now()}-${crypto.randomUUID()}`;
}
function html(value: unknown): string {
  // The donor expects legacy new/updated counters; V3 returns a snapshot total.
  // Translate only the next notice belonging to a completed refresh/readback.
  if (refreshedGroupTotal !== null && value === "已刷新：新增 0 个，更新 0 个") {
    value = `已刷新 ${refreshedGroupTotal} 个群聊`;
    refreshedGroupTotal = null;
  }
  return String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ] || c,
  );
}
function errorMessage(error: unknown, fallback = "请求失败"): string {
  const message = error instanceof Error
    ? error.message
    : typeof error === "string"
      ? error
      : typeof (error as Json)?.message === "string"
        ? (error as Json).message
        : "";
  return message && message !== "[object Object]" ? message : fallback;
}
function responseMessage(data: Json, fallback: string): string {
  if (data?.code === "operations_conflict" || (data?.error as Json)?.code === "operations_conflict") return "计划状态、版本或配置不满足要求，请刷新后检查";
  const candidates = [data?.error_message, data?.message, data?.error, data?.code];
  for (const candidate of candidates) {
    if (typeof candidate !== "string") continue;
    const text = candidate.trim();
    if (text && text !== "[object Object]" && !/^[a-z][a-z0-9_]+$/i.test(text)) return text;
  }
  return fallback;
}
function isOperationsConflict(data: Json): boolean {
  return data?.code === "operations_conflict" || (data?.error as Json)?.code === "operations_conflict";
}
function announceDirectoryReadFailure(): void {
  // The frozen detail renderer still asks for its directory decoration while
  // loading a plan. Preserve authoritative bindings and make that independent
  // read failure visible after its own render completes.
  window.setTimeout(() => {
    const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
    if (notice) {
      notice.hidden = false;
      notice.classList.add("group-ops__notice--error");
      notice.textContent = "群目录读取失败，请重试；已绑定群仍可查看。";
    }
  }, 0);
}
function planIDFromAPIURL(value: string): number | null {
  const match = new URL(value, window.location.origin).pathname.match(/\/plans\/(\d+)(?:\/|$)/);
  if (!match) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}
async function nativeRequest(url: string, options: Json = {}, onOperationsConflict?: (planID: number) => void): Promise<Json> {
  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");
  if (options.body !== undefined)
    headers.set("Content-Type", "application/json");
  if (options.method && options.method !== "GET") {
    headers.set("Idempotency-Key", typeof options.idempotencyKey === "string" ? options.idempotencyKey : key());
    const token = csrf();
    if (token) headers.set("X-CSRF-Token", token);
  }
  const response = await fetch(url, {
    method: options.method || "GET",
    headers,
    credentials: "same-origin",
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal && typeof options.signal === "object" ? options.signal as AbortSignal : undefined,
  });
  const raw = await response.text();
  let data: Json = {};
  try {
    data = raw ? JSON.parse(raw) : {};
  } catch {
    /* reported below */
  }
  if (!response.ok || data.ok === false) {
    // A lifecycle/configuration conflict invalidates the cached optimistic
    // revision. The UI re-reads before an operator can choose another write.
    if (response.status === 409 && isOperationsConflict(data)) {
      const planID = planIDFromAPIURL(url);
      if (planID !== null) {
        if (onOperationsConflict) onOperationsConflict(planID);
        else revisions.delete(planID);
      }
    }
    const error = new Error(responseMessage(data, `HTTP ${response.status}`)) as Error & { status?: number };
    error.status = response.status;
    throw error;
  }
  return data;
}
function planOwner(value: Json): Json {
  const owner = value.owner && typeof value.owner === "object" ? value.owner : {};
  const staffID = Number(owner.staff_id);
  if (!Number.isSafeInteger(staffID) || staffID < 1)
    return { owner_userid: "", owner_name: "未配置负责人", owner_state: "unconfigured" };
  const name = String(owner.display_name || "").trim();
  if (owner.profile_read_state === "ready" && owner.name_source === "wecom_profile" && name)
    return { owner_userid: String(staffID), owner_name: name, owner_state: "ready" };
  if (owner.profile_read_state === "unavailable")
    return { owner_userid: String(staffID), owner_name: "负责人目录不可用", owner_state: "directory_unavailable" };
  return { owner_userid: String(staffID), owner_name: "负责人目录未同步", owner_state: "directory_pending" };
}
function boundGroupCount(value: Json): number | null {
  // Servers that predate this list projection remain readable. The caller
  // renders the missing fact as unknown; it must never turn into a false zero
  // or trigger the former per-plan detail and directory waterfall.
  if (!Object.prototype.hasOwnProperty.call(value, "bound_group_count")) return null;
  const count = value.bound_group_count;
  if (typeof count !== "number" || !Number.isSafeInteger(count) || count < 0)
    throw new Error("计划绑定群数数据无效");
  return count;
}
function plan(value: Json, publishRevision = true): Json {
  const id = Number(value.plan_id);
  const count = boundGroupCount(value);
  if (publishRevision) revisions.set(id, Number(value.revision || 0));
  return {
    id,
    plan_name: value.name,
    plan_code: `v3-${id}`,
    plan_type: value.plan_type || "standard",
    status: value.status === "paused" ? "disabled" : value.status,
    revision: Number(value.revision || 0),
    ...planOwner(value),
    queue_count: Number(value.queue_count || 0),
    bound_group_count: count,
    today_estimated_reach: null,
    updated_at: value.updated_at,
  };
}
function node(value: Json): Json {
  return {
    id: Number(value.node_id),
    day_index: Number(value.day_index || 1),
    scheduled_time: value.scheduled_time || "20:00",
    trigger_time_label:
      value.trigger_time_label || value.scheduled_time || "20:00",
    action_title: value.action_title || "",
    text_content: value.message_text || "",
    content_package_json: packageFor(value.material_plan),
    attachments: [],
    sort_order: Number(value.position || 1),
    status: value.status || "active",
  };
}
function packageFor(value: Json | undefined): Json {
  const result: Json = {
    content_text: "",
    image_library_ids: [],
    miniprogram_library_ids: [],
    attachment_library_ids: [],
    group_invite_library_ids: [],
  };
  for (const ref of value?.references || []) {
    const target = (
      {
        image: "image_library_ids",
        miniprogram: "miniprogram_library_ids",
        attachment: "attachment_library_ids",
        group_invite: "group_invite_library_ids",
      } as Json
    )[ref.kind];
    if (target) result[target].push(Number(ref.id));
  }
  return result;
}
function materialPlan(input: Json): Json {
  const pkg = input.content_package_json || {};
  const refs: Json[] = [];
  const kinds: Array<[string, string]> = [
    ["image_library_ids", "image"],
    ["miniprogram_library_ids", "miniprogram"],
    ["attachment_library_ids", "attachment"],
    ["group_invite_library_ids", "group_invite"],
  ];
  for (const [field, kind] of kinds)
    for (const id of pkg[field] || [])
      if (Number(id) > 0) refs.push({ kind, id: Number(id) });
  return { references: refs };
}
async function detail(id: number, onOperationsConflict?: (planID: number) => void): Promise<Json> {
  return nativeRequest(`${base}/plans/${id}`, {}, onOperationsConflict);
}
async function revision(id: number): Promise<number> {
  if (!revisions.has(id)) plan(await detail(id).then((v) => v.plan || v));
  return revisions.get(id) || 0;
}
function groupView(asset: Json, directoryItem?: GroupPickerRecord): Json {
  const external = directoryItem?.external_member_count;
  const total = directoryItem?.member_count;
  const knownExternal = external !== null && external !== undefined && Number.isFinite(Number(external));
  const knownTotal = total !== null && total !== undefined && Number.isFinite(Number(total));
  return {
    chat_id: String(asset.asset_reference || asset.chat_reference || ""),
    group_name: String(directoryItem?.display_name || asset.display_name || "群名称待同步"),
    owner_userid: directoryItem?.owner_staff_id ? String(directoryItem.owner_staff_id) : "",
    internal_member_count_snapshot: knownTotal && knownExternal ? Number(total) - Number(external) : null,
    external_member_count_snapshot: knownExternal ? Number(external) : null,
  };
}
function newInitialDetailReadEpoch(id: number): InitialDetailReadEpoch {
  const generation = (detailReadGenerations.get(id) || 0) + 1;
  detailReadGenerations.set(id, generation);
  let epoch: InitialDetailReadEpoch;
  const clearCurrentRevisionOnConflict = () => {
    if (initialDetailReadEpochs.get(id) === epoch && detailReadGenerations.get(id) === generation)
      revisions.delete(id);
  };
  epoch = {
    id,
    generation,
    planClaimed: false,
    groupsClaimed: false,
    pairable: true,
    claims: 0,
    detail: detail(id, clearCurrentRevisionOnConflict),
    groups: null,
  };
  // The paired initial plan/group projections share the same Owner detail
  // request. A persisted binding does not trigger a full directory crawl.
  void epoch.detail.catch(() => undefined);
  queueMicrotask(() => { epoch.pairable = false; });
  return epoch;
}
function claimInitialDetailRead(id: number, kind: InitialDetailReadKind): { epoch: InitialDetailReadEpoch; release: () => void } {
  let epoch = initialDetailReadEpochs.get(id);
  const alreadyClaimed = epoch && (kind === "plan" ? epoch.planClaimed : epoch.groupsClaimed);
  if (!epoch || !epoch.pairable || alreadyClaimed) {
    epoch = newInitialDetailReadEpoch(id);
    initialDetailReadEpochs.set(id, epoch);
  }
  if (kind === "plan") epoch.planClaimed = true;
  else epoch.groupsClaimed = true;
  epoch.claims += 1;
  let released = false;
  return {
    epoch,
    release: () => {
      if (released) return;
      released = true;
      epoch.claims -= 1;
      if (epoch.claims === 0 && initialDetailReadEpochs.get(id) === epoch)
        initialDetailReadEpochs.delete(id);
    },
  };
}
function currentInitialDetailRead(epoch: InitialDetailReadEpoch): boolean {
  return initialDetailReadEpochs.get(epoch.id) === epoch && detailReadGenerations.get(epoch.id) === epoch.generation;
}
function invalidateInitialDetailRead(id: number): void {
  detailReadGenerations.set(id, (detailReadGenerations.get(id) || 0) + 1);
  initialDetailReadEpochs.delete(id);
}
function groupsForInitialDetailRead(epoch: InitialDetailReadEpoch): Promise<Json[]> {
  if (!epoch.groups) epoch.groups = groupsForPlan(epoch.id, epoch);
  return epoch.groups;
}
async function groupsForPlan(id: number, source?: Pick<InitialDetailReadEpoch, "detail">): Promise<Json[]> {
  const value = await (source?.detail || detail(id));
  const known = planGroupDirectoryViews.get(id) || new Map<string, GroupPickerRecord>();
  // A plan binding is the Owner fact. A scoped picker page can enrich matching
  // rows; otherwise the binding remains explicitly pending.
  return (value.group_assets || []).map((asset: Json) => groupView(asset, known.get(String(asset.asset_reference || ""))));
}
async function summary(id: number): Promise<Json> {
  return summarizeGroups(await groupsForPlan(id));
}
function publishGroupViews(id: number, items: Json[], publish: boolean): Json {
  const values = summarizeGroups(items);
  if (!publish) return values;
  planGroupViews.set(id, items);
  const view = planSummaryViews.get(id) || {};
  Object.assign(view, values);
  planSummaryViews.set(id, view);
  return view;
}
function summarizeGroups(rows: Json[]): Json {
  const known =
    rows.length > 0 &&
    rows.every((item) =>
      item.external_member_count_snapshot !== null &&
      item.external_member_count_snapshot !== undefined &&
      Number.isFinite(Number(item.external_member_count_snapshot)),
    );
  return {
    bound_group_count: rows.length,
    internal_member_count: known
      ? rows.reduce((sum, item) => sum + Number(item.internal_member_count_snapshot), 0)
      : null,
    external_member_count: known
      ? rows.reduce((sum, item) => sum + Number(item.external_member_count_snapshot), 0)
      : null,
    estimated_reach: null,
  };
}
async function requestJson(url: string, options: Json = {}): Promise<Json> {
  const method = String(options.method || "GET").toUpperCase();
  const body = options.body || {};
  const match = url.match(/\/plans\/(\d+)/);
  const id = match ? Number(match[1]) : 0;
  if (id && method !== "GET") invalidateInitialDetailRead(id);
  if (url === `${base}/plans` && method === "GET") {
    const data = await nativeRequest(url);
    if (!Array.isArray(data.items)) throw new Error("计划列表数据无效");
    // Parse the complete page before publishing any row revision. A malformed
    // later row must not advance CAS for an earlier row that remains visible
    // after the list read fails.
    data.items.forEach((item: Json) => boundGroupCount(item));
    const items = data.items.map((item: Json) => plan(item));
    return {
      ...data,
      items,
      queue_count: items.reduce(
        (sum, item) => sum + Number(item.queue_count || 0),
        0,
      ),
    };
  }
  if (url === `${base}/plans` && method === "POST") {
    const created = await nativeRequest(url, {
      method,
      body: { name: String(body.plan_name || "").trim() || "新建群运营计划" },
    });
    let value = created.plan || created;
    const current = plan(value);
    const owner = Number(body.owner_userid);
    // Creation has no member yet. The optional owner command below is one
    // atomic plan mutation, rather than a separate member add operation.
    if (body.plan_type || owner > 0) {
      const payload: Json = {
        expected_revision: await revision(current.id),
        name: current.plan_name,
        plan_type: body.plan_type,
      };
      if (owner > 0) payload.owner_staff_id = owner;
      value = await nativeRequest(`${base}/plans/${current.id}`, {
        method: "PUT",
        body: payload,
      });
    }
    return { item: plan((value.plan || value).plan || value.plan || value) };
  }
  if (id && /\/enable$/.test(url))
    return nativeRequest(`${base}/plans/${id}/enable`, {
      method: "POST",
      body: { expected_revision: await revision(id) },
    });
  if (id && /\/disable$/.test(url))
    return nativeRequest(`${base}/plans/${id}/disable`, {
      method: "POST",
      body: { expected_revision: await revision(id) },
    });
  if (id && /\/groups$/.test(url) && method === "GET") {
    const claimed = claimInitialDetailRead(id, "groups");
    try {
      const items = await groupsForInitialDetailRead(claimed.epoch);
      return { items, summary: publishGroupViews(id, items, currentInitialDetailRead(claimed.epoch)) };
    } finally {
      claimed.release();
    }
  }
  if (id && /\/groups$/.test(url) && method === "POST")
    return nativeRequest(`${base}/plans/${id}/groups`, {
      method,
      body: {
        expected_revision: await revision(id),
        asset_reference: body.chat_id,
      },
    });
  if (id && /\/groups\//.test(url) && method === "DELETE")
    return nativeRequest(
      `${base}/plans/${id}/groups/${encodeURIComponent(url.split("/").pop() || "")}`,
      { method, body: { expected_revision: await revision(id) } },
    );
  if (id && /\/nodes$/.test(url) && method === "GET") {
    const value = await detail(id);
    return { items: (value.nodes || []).map(node) };
  }
  if (id && /\/nodes\/\d+$/.test(url) && method === "DELETE")
    return nativeRequest(`${base}/plans/${id}/nodes/${encodeURIComponent(url.split("/").pop() || "")}`, { method, body: { expected_revision: await revision(id) } });
  if (id && /\/nodes(?:\/\d+)?$/.test(url) && method !== "GET") {
    const nodeID = Number(url.split("/").pop());
    const currentDetail = await detail(id);
    const nodes = currentDetail.nodes || [];
    const current = nodeID
      ? nodes.find((item: Json) => Number(item.node_id) === nodeID) || {}
      : {};
    // The frozen form keeps its historic default sort value (10) for a new
    // action. V3 stores contiguous positions and rejects a position after the
    // current tail, so interpret an out-of-range legacy sort value as append.
    // Existing actions retain their real position unless the submitted value
    // identifies a valid insertion point in this current plan.
    const requestedPosition = Number(body.sort_order);
    const maximumPosition = nodes.length + (nodeID ? 0 : 1);
    const position =
      Number.isInteger(requestedPosition) &&
      requestedPosition >= 1 &&
      requestedPosition <= maximumPosition
        ? requestedPosition
        : Number(current.position || nodes.length + 1);
    const payload = {
      expected_revision: await revision(id),
      position,
      kind: "message",
      day_index: Number(body.day_index || 1),
      scheduled_time: body.scheduled_time || "20:00",
      trigger_time_label: body.scheduled_time || "20:00",
      action_title: String(body.action_title || "").trim(),
      status: body.status || "active",
      message_text: body.text_content || "",
      delay_minutes: 0,
      material_plan: materialPlan(body),
    };
    if (!payload.action_title) throw new Error("动作标题不能为空");
    return nativeRequest(
      `${base}/plans/${id}/nodes${nodeID ? `/${nodeID}` : ""}`,
      { method: nodeID ? "PUT" : method, body: payload },
    );
  }
  if (id && /\/webhook$/.test(url) && method === "GET") {
    const descriptor = await nativeRequest(`${base}/plans/${id}/webhook-descriptor`);
    const path = String(descriptor.path || "").trim();
    const configured = Boolean(descriptor.configured && descriptor.reference && path);
    return {
      configured,
      reference: String(descriptor.reference || ""),
      webhook_url: configured ? new URL(path, window.location.origin).href : "",
      signature_algorithm: descriptor.signature_algorithm || "",
      signature_header: descriptor.signature_header || "",
      timestamp_header: descriptor.timestamp_header || "",
      nonce_header: descriptor.nonce_header || "",
      client_id_header: descriptor.client_id_header || "",
    };
  }
  if (id && /\/webhook-descriptor$/.test(url) && method === "PUT") {
    const value = await nativeRequest(`${base}/plans/${id}/webhook-descriptor`, {
      method,
      body: { expected_revision: await revision(id), reference: String(body.reference || "").trim() },
    });
    const updated = value.plan || value;
    if (Number.isSafeInteger(Number(updated.revision))) revisions.set(id, Number(updated.revision));
    return value;
  }
  if (id && url === `${base}/plans/${id}` && method === "GET") {
    const claimed = claimInitialDetailRead(id, "plan");
    try {
      const [value, items] = await Promise.all([claimed.epoch.detail, groupsForInitialDetailRead(claimed.epoch)]);
      const rawPlan = value.plan || {};
      // A short-lived compatibility fallback preserves the local staff key when
      // a browser reads a server that predates the owner projection. It never
      // makes a second directory request or invents a profile name.
      const legacyOwner = (value.members || [])[0];
      const publish = currentInitialDetailRead(claimed.epoch);
      const projected = plan(rawPlan.owner || !legacyOwner?.staff_id
        ? rawPlan
        : { ...rawPlan, owner: { staff_id: legacyOwner.staff_id } }, publish);
      return { ...projected, groups_summary: publishGroupViews(id, items, publish) };
    } finally {
      claimed.release();
    }
  }
  if (id && url === `${base}/plans/${id}` && method === "DELETE")
    return nativeRequest(url, {
      method,
      body: { expected_revision: await revision(id) },
    });
  if (
    id &&
    url === `${base}/plans/${id}` &&
    (method === "PUT" || method === "PATCH")
  ) {
    const current = await detail(id);
    const wantedOwner = Number(body.owner_userid);
    const currentOwner = Number((current.members || [])[0]?.staff_id || 0);
    const payload: Json = {
      expected_revision: await revision(id),
      name: body.plan_name,
      plan_type: body.plan_type,
    };
    // The donor submits its visible value on every save. Preserve legacy
    // multi-member plans unless the responsible employee actually changed.
    if (wantedOwner > 0 && wantedOwner !== currentOwner)
      payload.owner_staff_id = wantedOwner;
    let value = await nativeRequest(url, { method: "PUT", body: payload });
    const wanted = body.status;
    const mapped = (value.plan || value).status;
    if (wanted === "active" && mapped !== "active")
      value = await nativeRequest(`${base}/plans/${id}/enable`, {
        method: "POST",
        body: { expected_revision: (value.plan || value).revision },
      });
    if (wanted === "disabled" && mapped === "active")
      value = await nativeRequest(`${base}/plans/${id}/disable`, {
        method: "POST",
        body: { expected_revision: (value.plan || value).revision },
      });
    return value;
  }
  if (url.startsWith(`${base}/groups`) && method === "GET") {
    let data: Json;
    try {
      data = await nativeRequest(url);
    } catch {
      refreshedGroupTotal = null;
      announceDirectoryReadFailure();
      // Keep the page and its Owner binding projection usable. The V3 picker
      // performs its own scoped read and reports a retryable load failure.
      return { items: [], total: 0, limit: 50, offset: 0, has_more: false };
    }
    if (!Array.isArray(data.items)) {
      announceDirectoryReadFailure();
      return { items: [], total: 0, limit: 50, offset: 0, has_more: false };
    }
    return {
      ...data,
      items: (data.items || []).map((item: Json) => {
        const total = item.member_count;
        const external = item.external_member_count;
        const knownTotal = total !== null && total !== undefined && Number.isFinite(Number(total));
        const knownExternal = external !== null && external !== undefined && Number.isFinite(Number(external));
        return {
          chat_id: item.chat_reference,
          group_name: item.display_name || item.chat_reference,
          owner_userid: String(item.owner_staff_id || ""),
          internal_member_count_snapshot: knownTotal && knownExternal ? Number(total) - Number(external) : null,
          external_member_count_snapshot: knownExternal ? Number(external) : null,
        };
      }),
    };
  }
  if (url === `${base}/groups/sync`) {
    refreshedGroupTotal = null;
    const planID = Number(document.getElementById("group-ops-app")?.dataset.planId);
    if (planID > 0) invalidateInitialDetailRead(planID);
    const result = await nativeRequest(url, {
      method,
      body: {
        owner_staff_id: Number(body.owner_userid),
        limit: Number(body.limit || 100),
      },
    });
    if (planID > 0) {
      try {
        // Refresh returns the current owner-scoped directory page. Merge only
        // matching decoration into the Host cache; plan bindings remain Owner
        // facts and an unsaved form still does not trigger a plan write.
        const refreshed = new Map<string, GroupPickerRecord>((result.items || []).flatMap((item: Json): [string, GroupPickerRecord][] => {
          const record = groupRecord({ asset_reference: item.chat_reference }, item);
          return record.chat_reference ? [[record.chat_reference, record]] : [];
        }));
        const directoryViews = planGroupDirectoryViews.get(planID) || new Map<string, GroupPickerRecord>();
        for (const [reference, directoryRecord] of refreshed) directoryViews.set(reference, directoryRecord);
        planGroupDirectoryViews.set(planID, directoryViews);
        // Read the Owner binding again and decorate only references returned
        // by this scoped refresh. Do not reload the donor form or create a
        // broader directory read, so an unsaved owner/name draft remains local.
        const persisted = await detail(planID);
        const rows = (persisted.group_assets || []).map((asset: Json) => {
          const reference = String(asset.asset_reference || "");
          return groupView(asset, refreshed.get(reference) || directoryViews.get(reference));
        });
        const view = planGroupViews.get(planID);
        if (view) view.splice(0, view.length, ...rows);
        const counts = planSummaryViews.get(planID);
        const summaryValue = summarizeGroups(rows);
        if (counts) Object.assign(counts, summaryValue);
        window.dispatchEvent(new CustomEvent("aicrm:groupops-directory-decoration", { detail: { planId: planID, rows, summary: summaryValue } }));
      } catch {
        throw new Error("群聊已刷新，但页面读回失败，请重新打开页面查看");
      }
    }
    if (Number.isSafeInteger(result.total) && result.total >= 0) refreshedGroupTotal = result.total;
    return result;
  }
  if (url.startsWith(operationMembersPath)) {
    const data = await nativeRequest(url);
    return {
      ...data,
      items: (data.items || []).flatMap((item: Json) => {
        const staffID = String(item.staff_id || "").trim();
        const userID = String(item.sender_userid || item.user_id || staffID).trim();
        if (!userID || !staffID) return [];
        return [{
          user_id: userID,
          staff_id: staffID,
          display_name: String(item.display_name || item.name || `员工 #${userID}`),
        }];
      }),
    };
  }
  return nativeRequest(url, options);
}

(window as any).AdminApi = {
  ...(window as any).AdminApi,
  requestJson,
  escapeHtml: html,
  errorMessage,
  responseErrorMessage: (_response: unknown, data: Json, fallback: string) =>
    responseMessage(data, fallback),
};
// Keep the frozen save flow intact, but observe the promise its DOM listener
// returns. Only this root's two save controls are bridged; no global event
// prototype or unrelated business action is changed.
function installSaveFailureFeedback(): void {
  const app = document.getElementById("group-ops-app");
  if (!app) return;
  const query = app.querySelectorAll.bind(app);
  const decorated = new WeakSet<Element>();
  const clear = () => app.querySelector('[data-groupops-save-error]')?.remove();
  const report = (error: unknown) => {
    let alert = app.querySelector<HTMLElement>('[data-groupops-save-error]');
    if (!alert) {
      alert = document.createElement("div");
      alert.dataset.groupopsSaveError = "1";
      alert.setAttribute("role", "alert");
      alert.className = "group-ops__notice";
      alert.style.color = "#b42318";
      alert.style.backgroundColor = "#fff1f0";
      alert.style.borderColor = "#fda29b";
      app.prepend(alert);
    }
    alert.textContent = `保存失败：${errorMessage(error, "请重试")}；当前填写内容已保留。`;
  };
  app.querySelectorAll = ((selector: string) => {
    const nodes = query(selector);
    if (selector === "[data-action]") for (const element of nodes) {
      if (!element.matches('[data-action="save-plan"],[data-action="save-active-detail-panel"]') || decorated.has(element)) continue;
      decorated.add(element);
      const add = element.addEventListener.bind(element);
      element.addEventListener = ((type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | AddEventListenerOptions) => {
        if (!listener) return;
        if (type !== "click") return add(type, listener, options);
        add(type, (event: Event) => {
          clear();
          try {
            const result = typeof listener === "function" ? listener.call(element, event) : listener.handleEvent(event);
            void Promise.resolve(result).catch(report);
          } catch (error) { report(error); }
        }, options);
      }) as typeof element.addEventListener;
    }
    return nodes;
  }) as typeof app.querySelectorAll;
}
function groupRecord(asset: Json, directoryItem?: Json): GroupPickerRecord {
  const reference = String(asset.asset_reference || asset.chat_reference || "").trim();
  const displayName = String(directoryItem?.display_name || asset.display_name || "群名称待同步").trim() || "群名称待同步";
  return {
    chat_reference: reference,
    display_name: displayName,
    owner_staff_id: Number.isSafeInteger(Number(directoryItem?.owner_staff_id)) ? Number(directoryItem?.owner_staff_id) : undefined,
    member_count: Number.isFinite(Number(directoryItem?.member_count)) ? Number(directoryItem?.member_count) : undefined,
    external_member_count: directoryItem?.external_member_count === null ? null : Number.isFinite(Number(directoryItem?.external_member_count)) ? Number(directoryItem?.external_member_count) : undefined,
    unavailable_reason: directoryItem ? undefined : "群目录状态待确认，仍保留已绑定记录。",
  };
}

async function selectedGroupRecords(planID: number): Promise<{ records: GroupPickerRecord[]; ownerStaffID: number | undefined; status: string }> {
  const value = await detail(planID);
  const current = value.plan || value;
  const ownerStaffID = Number(current?.owner?.staff_id);
  const owner = Number.isSafeInteger(ownerStaffID) && ownerStaffID > 0 ? ownerStaffID : undefined;
  return {
    ownerStaffID: owner,
    status: String(current?.status || ""),
    records: (value.group_assets || []).flatMap((asset: Json) => {
      // The bound plan asset is authoritative even when the local directory
      // is unavailable. A previous scoped picker page may enrich it, but opening
      // never discards or blocks a persisted binding behind a full crawl.
      const reference = String(asset.asset_reference || "");
      const row = groupRecord(asset, planGroupDirectoryViews.get(planID)?.get(reference));
      return row.chat_reference ? [row] : [];
    }),
  };
}

function updateGroupRevision(planID: number, value: Json): void {
  const candidate = value.plan && typeof value.plan === "object" ? value.plan : value;
  const next = Number(candidate.revision);
  if (Number.isSafeInteger(next) && next >= 0) revisions.set(planID, next);
}

function selectionSignature(added: GroupPickerRecord[], removed: GroupPickerRecord[]): string {
  return [
    ...added.map((record) => `add:${record.chat_reference}`),
    ...removed.map((record) => `remove:${record.chat_reference}`),
  ].sort().join("|");
}

function selectionOperation(planID: number, added: GroupPickerRecord[], removed: GroupPickerRecord[]): GroupSelectionOperation {
  const signature = selectionSignature(added, removed);
  const previous = groupSelectionOperations.get(planID);
  if (previous?.signature === signature) return previous;
  const operation: GroupSelectionOperation = { signature, steps: new Map() };
  const prefix = `groupops-group-selection-${planID}-${crypto.randomUUID()}`;
  for (const [kind, records] of [["add", added], ["remove", removed]] as const) {
    for (const record of records) {
      const reference = String(record.chat_reference || "").trim();
      if (!reference) continue;
      const stepKey = `${kind}:${reference}`;
      operation.steps.set(stepKey, { planID, kind, reference, idempotencyKey: `${prefix}-${operation.steps.size + 1}` });
    }
  }
  groupSelectionOperations.set(planID, operation);
  return operation;
}

type PersistedGroupSelection = { value: Json; references: Set<string>; revision: number };

async function readPersistedGroupSelection(planID: number): Promise<PersistedGroupSelection> {
  const value = await detail(planID);
  updateGroupRevision(planID, value);
  const current = value.plan && typeof value.plan === "object" ? value.plan : value;
  const revisionValue = Number(current.revision);
  if (!Number.isSafeInteger(revisionValue) || revisionValue < 1) throw new Error("群聊绑定读回缺少有效版本，请刷新计划后重试。");
  return {
    value,
    references: new Set((value.group_assets || []).map((asset: Json) => String(asset.asset_reference || "")).filter(Boolean)),
    revision: revisionValue,
  };
}

function stepReached(selection: PersistedGroupSelection, step: GroupSelectionStep): boolean {
  return step.kind === "add" ? selection.references.has(step.reference) : !selection.references.has(step.reference);
}

function partialSaveError(cause: unknown, confirmed: GroupSelectionStep[]): Error {
  const confirmedText = confirmed.length
    ? `已实际保存：${confirmed.map((step) => `${step.kind === "add" ? "添加" : "移除"} ${step.reference}`).join("、")}；`
    : "尚未确认新的保存步骤；";
  return new Error(`${confirmedText}${errorMessage(cause, "保存结果未确认")}。已保留本次选择，请使用原确认操作重试未完成差异。`);
}

function updateRenderedGroupBindings(planID: number, persisted: PersistedGroupSelection, selected: GroupPickerRecord[]): void {
  // This cache intentionally retains source directory records. The renderer
  // projection (`groupView`) has different field names, so caching it here
  // would make a confirmed group fall back to “群名称待同步” on reopen.
  const directoryViews = planGroupDirectoryViews.get(planID) || new Map<string, GroupPickerRecord>();
  for (const record of selected) directoryViews.set(record.chat_reference, { ...record });
  planGroupDirectoryViews.set(planID, directoryViews);
  const rows: Json[] = (persisted.value.group_assets || []).map((asset: Json): Json => groupView(asset, directoryViews.get(String(asset.asset_reference || ""))));
  planGroupViews.set(planID, rows);
  const summaryValue = summarizeGroups(rows);
  const summaryView = planSummaryViews.get(planID) || {};
  Object.assign(summaryView, summaryValue);
  planSummaryViews.set(planID, summaryView);

  // Let the existing Group Ops renderer own the DOM refresh and event binding.
  // It calls this Host's cached request adapter, so the immediate readback stays
  // local and no full directory is introduced merely to repaint a binding.
  window.dispatchEvent(new CustomEvent("aicrm:groupops-detail-refresh", { detail: { planId: planID } }));
}

async function refreshGroupBindingsAfterCancel(planID: number): Promise<void> {
  try {
    const persisted = await readPersistedGroupSelection(planID);
    updateRenderedGroupBindings(planID, persisted, []);
    window.setTimeout(() => {
      const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
      if (notice) {
        notice.hidden = false;
        notice.textContent = "已关闭群聊选择；已保存的群聊不会因取消而撤销。";
      }
    }, 0);
  } catch (error) {
    const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
    if (notice) {
      notice.hidden = false;
      notice.textContent = `已关闭群聊选择；无法读回实际绑定，请重新打开页面查看：${errorMessage(error, "读取失败")}`;
    }
  }
}

function commandForStep(step: GroupSelectionStep, revision: number): { url: string; method: string; body: Json } {
  if (!step.body) step.body = step.kind === "add"
    ? { expected_revision: revision, asset_reference: step.reference }
    : { expected_revision: revision };
  return {
    url: step.kind === "add"
      ? `${base}/plans/${currentSelectionPlanID(step)}/groups`
      : `${base}/plans/${currentSelectionPlanID(step)}/groups/${encodeURIComponent(step.reference)}`,
    method: step.kind === "add" ? "POST" : "DELETE",
    body: step.body,
  };
}

// The plan ID is not part of a receipt key, but command URLs are. Attach it
// once when an operation is created so a later explicit retry sends the exact
// same full command body and endpoint.
function currentSelectionPlanID(step: GroupSelectionStep): number {
  const planID = Number(step.planID);
  if (!Number.isSafeInteger(planID) || planID < 1) throw new Error("群聊保存步骤缺少计划标识。");
  return planID;
}

function explicitCASConflict(error: unknown): boolean {
  return (error as { status?: unknown })?.status === 409;
}

async function saveGroupSelection(planID: number, selected: GroupPickerRecord[], added: GroupPickerRecord[], removed: GroupPickerRecord[]): Promise<void> {
  const operation = selectionOperation(planID, added, removed);
  let persisted = await readPersistedGroupSelection(planID);
  const confirmed: GroupSelectionStep[] = [];
  for (const step of operation.steps.values()) {
    if (stepReached(persisted, step)) continue;
    try {
      const command = commandForStep(step, persisted.revision);
      const value = await nativeRequest(command.url, {
        method: command.method,
        idempotencyKey: step.idempotencyKey,
        body: command.body,
      });
      updateGroupRevision(planID, value);
      persisted = await readPersistedGroupSelection(planID);
      if (!stepReached(persisted, step)) throw new Error("群聊保存后读回未达到原选择，请刷新后检查。");
      confirmed.push(step);
    } catch (cause) {
      // A lost response is outcome-unknown. Read the Owner fact first; only a
      // matching receipt replay with the frozen command may prove completion.
      try {
        persisted = await readPersistedGroupSelection(planID);
        if (stepReached(persisted, step)) {
          confirmed.push(step);
          continue;
        }
      } catch {
        // Keep the original cause; a failed readback is not permission to guess.
      }
      if (explicitCASConflict(cause)) {
        // HTTP 409 proves this exact command did not mutate. Drop only this
        // operation after preserving the draft so the next explicit confirm
        // can make a new intent from the current revision and new keys.
        groupSelectionOperations.delete(planID);
        throw new Error(`${partialSaveError(cause, confirmed).message} 计划版本已变化；该步骤未提交，请再次确认后创建新的保存意图。`);
      }
      throw partialSaveError(cause, confirmed);
    }
  }
  updateRenderedGroupBindings(planID, persisted, selected);
  groupSelectionOperations.delete(planID);
}

function installGroupPickerBridge(): void {
  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest<HTMLButtonElement>("#group-ops-app button[data-action='open-group-picker']") : null;
    if (!target) return;
    const app = document.getElementById("group-ops-app");
    const planID = Number(app?.dataset.planId);
    if (!Number.isSafeInteger(planID) || planID < 1) return;
    if (openingGroupPicker || activeGroupPickerPlan === planID || document.querySelector('[data-v3-selection-session="group"]')) return;
    // Capture before the frozen donor's click listener. The standard picker
    // remains untouched; this V3 overlay has no legacy raw chat_id channel.
    event.preventDefault();
    event.stopImmediatePropagation();
    openingGroupPicker = true;
    target.disabled = true;
    void (async () => {
      try {
        const initial = await selectedGroupRecords(planID);
        activeGroupPickerPlan = planID;
        openGroupPicker({
          source: `groupops-plan-${planID}`,
          scope: "group_ops.plan_group_assets",
          selectedRecords: initial.records,
          readonlyReason: initial.status === "draft" ? undefined : initial.status === "archived"
            ? "计划已归档，不能修改群聊。"
            : "当前计划状态不允许修改群聊；仅草稿计划可编辑。",
          loadPage: async ({ query, cursor, signal }) => {
            const offset = Number(cursor || "0");
            if (!Number.isSafeInteger(offset) || offset < 0) throw new Error("群目录分页标记无效，请重新打开选择器。");
            const params = new URLSearchParams({ limit: "50", offset: String(offset) });
            // The Owner port scopes this local projection before pagination.
            // Client-side disabled text remains a defensive presentation of
            // any historic/cached record, not a substitute for server scope.
            if (initial.ownerStaffID) params.set("owner_userid", String(initial.ownerStaffID));
            if (query.trim()) params.set("q", query.trim());
            const page = await nativeRequest(`${base}/groups?${params.toString()}`, { signal });
            if (signal.aborted) throw new DOMException("群目录读取已替换", "AbortError");
            const items = Array.isArray(page.items) ? page.items : [];
            const records = items.flatMap((entry: Json) => {
              const record = groupRecord({ chat_reference: entry.chat_reference }, entry);
              if (!record.chat_reference) return [];
              // Cache the raw directory record before adding picker-only
              // disabled text, so the plan renderer keeps actual owner/counts.
              const directoryViews = planGroupDirectoryViews.get(planID) || new Map<string, GroupPickerRecord>();
              directoryViews.set(record.chat_reference, { ...record });
              planGroupDirectoryViews.set(planID, directoryViews);
              if (!initial.ownerStaffID) record.unavailable_reason = "计划尚未配置负责人，不能选择新群。";
              else if (record.owner_staff_id !== initial.ownerStaffID) record.unavailable_reason = "当前负责人不可管理此群。";
              return [record];
            });
            return {
              items: records,
              nextCursor: page.has_more === true && items.length ? String(offset + items.length) : undefined,
            };
          },
          accessLossMessage: (error) => { const status = (error as { status?: unknown }).status; return status === 401 || status === 403 ? '群目录权限已失效；已绑定群仍可查看，请取消后重新登录。' : undefined; },
          onCommit: async ({ selected, added, removed }) => {
            await saveGroupSelection(planID, selected, added, removed);
            activeGroupPickerPlan = undefined;
          },
          onCancel: ({ saveAttempted }) => {
            activeGroupPickerPlan = undefined;
            // A pure draft cancel never contacted the Owner, so do not imply a
            // rollback or a persisted binding. After any save attempt, however,
            // only Owner readback can state what remains real.
            if (saveAttempted) void refreshGroupBindingsAfterCancel(planID);
          },
        });
      } catch (error) {
        activeGroupPickerPlan = undefined;
        const notice = document.querySelector<HTMLElement>("#group-ops-app .group-ops__notice");
        if (notice) { notice.hidden = false; notice.textContent = `群聊选择器无法打开：${errorMessage(error, "请重试")}`; }
      } finally {
        openingGroupPicker = false;
        target.disabled = false;
      }
    })();
  }, true);
}
installGroupPickerBridge();

installSaveFailureFeedback();
// @ts-expect-error The standard donor script is intentionally JavaScript.
void import("./groupOpsStandard.js");
export {};
