// V3 Host adapter for the byte-derived Group Ops presentation. It owns only
// authenticated transport and DTO projection; plan, node and directory facts
// remain in internal/groupops and the existing WeCom read adapter.
type Json = Record<string, any>;
const base = "/api/admin/automation-conversion/group-ops";
const revisions = new Map<number, number>();

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
  return String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ] || c,
  );
}
function errorMessage(error: unknown, fallback = "请求失败"): string {
  return error instanceof Error && error.message ? error.message : fallback;
}
async function nativeRequest(url: string, options: Json = {}): Promise<Json> {
  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");
  if (options.body !== undefined)
    headers.set("Content-Type", "application/json");
  if (options.method && options.method !== "GET") {
    headers.set("Idempotency-Key", key());
    const token = csrf();
    if (token) headers.set("X-CSRF-Token", token);
  }
  const response = await fetch(url, {
    method: options.method || "GET",
    headers,
    credentials: "same-origin",
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const raw = await response.text();
  let data: Json = {};
  try {
    data = raw ? JSON.parse(raw) : {};
  } catch {
    /* reported below */
  }
  if (!response.ok || data.ok === false)
    throw new Error(
      String(data.error || data.code || `HTTP ${response.status}`),
    );
  return data;
}
function plan(value: Json): Json {
  const id = Number(value.plan_id);
  revisions.set(id, Number(value.revision || 0));
  return {
    id,
    plan_name: value.name,
    plan_code: `v3-${id}`,
    plan_type: value.plan_type || "standard",
    status: value.status === "paused" ? "disabled" : value.status,
    revision: Number(value.revision || 0),
    owner_userid: "",
    owner_name: "",
    queue_count: Number(value.queue_count || 0),
    bound_group_count: null,
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
async function detail(id: number): Promise<Json> {
  return nativeRequest(`${base}/plans/${id}`);
}
async function revision(id: number): Promise<number> {
  if (!revisions.has(id)) plan(await detail(id).then((v) => v.plan || v));
  return revisions.get(id) || 0;
}
async function directory(): Promise<Json[]> {
  const data = await nativeRequest(`${base}/groups?limit=200&offset=0`);
  return data.items || [];
}
async function groupsForPlan(id: number): Promise<Json[]> {
  const [value, directoryItems] = await Promise.all([detail(id), directory()]);
  return (value.group_assets || []).map((asset: Json) => {
    const found =
      directoryItems.find(
        (item) => item.chat_reference === asset.asset_reference,
      ) || {};
    const external = found.external_member_count;
    const knownExternal = external !== null && external !== undefined && Number.isFinite(Number(external));
    const total = found.member_count;
    const knownTotal = total !== null && total !== undefined && Number.isFinite(Number(total));
    return {
      chat_id: asset.asset_reference,
      group_name: found.display_name || asset.asset_reference,
      owner_userid: found.owner_staff_id ? String(found.owner_staff_id) : "",
      // The provider directory has a total and (when member types are complete)
      // an external count. Internal count is derived only from both facts.
      internal_member_count_snapshot: knownTotal && knownExternal ? Number(total) - Number(external) : null,
      external_member_count_snapshot: knownExternal ? Number(external) : null,
    };
  });
}
async function summary(id: number): Promise<Json> {
  const rows = await groupsForPlan(id);
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
  if (url === `${base}/plans` && method === "GET") {
    const data = await nativeRequest(url);
    const items = await Promise.all(
      (data.items || []).map(async (item: Json) => ({
        ...plan(item),
        ...(await summary(Number(item.plan_id))),
      })),
    );
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
    if (body.plan_type || body.owner_userid) {
      const current = plan(value);
      let expected = await revision(current.id);
      if (body.plan_type) {
        value = await nativeRequest(`${base}/plans/${current.id}`, {
          method: "PUT",
          body: {
            expected_revision: expected,
            name: current.plan_name,
            plan_type: body.plan_type,
          },
        });
        expected = Number((value.plan || value).revision);
      }
      if (Number(body.owner_userid) > 0)
        value = await nativeRequest(`${base}/plans/${current.id}/members`, {
          method: "POST",
          body: {
            expected_revision: expected,
            staff_id: Number(body.owner_userid),
          },
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
  if (id && /\/groups$/.test(url) && method === "GET")
    return { items: await groupsForPlan(id), ...(await summary(id)) };
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
    const current = nodeID
      ? (await detail(id)).nodes.find(
          (item: Json) => Number(item.node_id) === nodeID,
        ) || {}
      : {};
    const payload = {
      expected_revision: await revision(id),
      position: Number(
        body.sort_order ||
          current.position ||
          (await detail(id)).nodes.length + 1,
      ),
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
  if (id && /\/webhook$/.test(url)) {
    const value = await detail(id);
    return { webhook_url: value.webhook_descriptor?.url || "" };
  }
  if (id && url === `${base}/plans/${id}` && method === "GET") {
    const value = await detail(id);
    const projected = plan(value.plan);
    const owner = (value.members || [])[0];
    if (owner?.staff_id) { projected.owner_userid = String(owner.staff_id); projected.owner_name = `员工 #${owner.staff_id}`; }
    return { ...projected, groups_summary: await summary(id) };
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
    let value = await nativeRequest(url, {
      method: "PUT",
      body: {
        expected_revision: await revision(id),
        name: body.plan_name,
        plan_type: body.plan_type,
      },
    });
    const wantedOwner = Number(body.owner_userid);
    if (wantedOwner > 0) {
      const current = await detail(id);
      const currentOwner = Number((current.members || [])[0]?.staff_id || 0);
      if (currentOwner !== wantedOwner) {
        let expected = Number((value.plan || value).revision);
        value = await nativeRequest(`${base}/plans/${id}/members`, {
          method: "POST",
          body: { expected_revision: expected, staff_id: wantedOwner },
        });
        expected = Number((value.plan || value).revision);
        // The standard UI has one responsible operator. Keep its projection
        // exact by removing every previous member under successive CAS values.
        for (const member of current.members || []) {
          const staffID = Number(member.staff_id);
          if (staffID > 0 && staffID !== wantedOwner) {
            value = await nativeRequest(`${base}/plans/${id}/members/${staffID}`, {
              method: "DELETE",
              body: { expected_revision: expected },
            });
            expected = Number((value.plan || value).revision);
          }
        }
      }
    }
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
    const data = await nativeRequest(
      url,
    );
    return {
      ...data,
      items: (data.items || []).map((item: Json) => ({
        chat_id: item.chat_reference,
        group_name: item.display_name,
        owner_userid: String(item.owner_staff_id || ""),
        internal_member_count_snapshot: item.member_count,
        external_member_count_snapshot: item.external_member_count,
      })),
    };
  }
  if (url === `${base}/groups/sync`)
    return nativeRequest(url, {
      method,
      body: {
        owner_staff_id: Number(body.owner_userid),
        limit: Number(body.limit || 100),
      },
    });
  if (url.startsWith("/api/admin/common/operation-members")) {
    const data = await nativeRequest(url);
    return {
      items: (data.items || []).map((item: Json) => ({
        user_id: item.staff_id,
        name: item.display_name || `员工 #${item.staff_id}`,
      })),
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
    String(data?.error || fallback),
};
// @ts-expect-error The standard donor script is intentionally JavaScript.
void import("./groupOpsStandard.js");
export {};
