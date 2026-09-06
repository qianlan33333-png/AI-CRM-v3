/**
 * V3-owned compatibility Host for the byte-frozen customer list and detail
 * documents. The frozen generated client still names v2 `/api/v1` resources;
 * this seam translates only those reads into current Customer-owned safe
 * projections before the frozen admin entry executes. It neither creates
 * customers nor changes Customer mutations, OneID resolution, or ownership.
 */
type JSONRecord = Record<string, unknown>;

const originalFetch = window.fetch.bind(window);

function record(value: unknown): JSONRecord {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as JSONRecord)
    : {};
}

function items(value: unknown): unknown[] {
  return Array.isArray(record(value).items) ? (record(value).items as unknown[]) : [];
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), window.location.origin);
  if (typeof input === "string") return new URL(input, window.location.origin);
  return new URL(input.url, window.location.origin);
}

function currentCustomerID(pathname: string, suffix: string): number | undefined {
  const match = new RegExp(`^/api/v1/customers/([1-9][0-9]*)/${suffix}$`).exec(pathname);
  if (!match) return undefined;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}

async function customerDirectory(url: URL, init?: RequestInit): Promise<Response> {
  if (url.searchParams.has("owner_staff_id") || url.searchParams.has("tag_id")) {
    // The live directory has no equivalent predicate. Return a clear rejected
    // HTTP request rather than silently filtering against a stale browser cache.
    return json({ code: "unsupported_customer_directory_filter" }, 400);
  }
  const query = new URLSearchParams();
  query.set("limit", url.searchParams.get("limit") || "50");
  for (const key of ["cursor", "keyword"] as const) {
    const value = url.searchParams.get(key);
    if (value) query.set(key, value);
  }
  const mobile = url.searchParams.get("mobile");
  if (mobile) query.set("phone", mobile);
  const response = await originalFetch(`/api/admin/customers?${query.toString()}`, init);
  if (!response.ok) return response;
  const page = record(await response.json());
  return json({
    ...page,
    items: items(page).map((value) => {
      const customer = record(value);
      const id = Number(customer.customer_id);
      return {
        id,
        name: text(customer.display_name) || `客户 ${id}`,
        owner_staff_id: null,
        stage_id: null,
        is_deleted: false,
        extra: {},
        created_at: text(customer.last_synced_at),
        updated_at: text(customer.updated_at),
      };
    }),
  });
}

async function safeAuxiliaryRead(path: string, init?: RequestInit): Promise<unknown> {
  try {
    const response = await originalFetch(path, init);
    return response.ok ? await response.json() : undefined;
  } catch {
    // The main Customer360 projection remains authoritative. Auxiliary sections
    // have no substitute source and intentionally degrade to an empty display.
    return undefined;
  }
}

async function customerContext(id: number, init?: RequestInit): Promise<Response> {
  const response = await originalFetch(`/api/admin/customers/${id}/360`, init);
  if (!response.ok) return response;
  const source = record(await response.json());
  const [tagsSource, chatSource] = await Promise.all([
    safeAuxiliaryRead(`/api/admin/customers/${id}/tags`, init),
    safeAuxiliaryRead(`/api/admin/customers/${id}/chat-activity?limit=20`, init),
  ]);
  const profile = record(record(source.profile).data);
  const recentTouchpoints = record(source.recent_touchpoints);
  const timeline = recentTouchpoints.status === "ready" && Array.isArray(recentTouchpoints.data)
    ? recentTouchpoints.data
    : [];
  const chat = items(chatSource).map((value) => {
    const item = record(value);
    return {
      chat_type: item.chat_type,
      message_type: item.message_type,
      sent_at: item.occurred_at,
    };
  });
  return json({
    customer: {
      id,
      name: text(profile.display_name) || `客户 ${id}`,
      owner_staff_id: null,
      stage_id: null,
      channel_id: null,
      added_at: profile.last_synced_at ?? null,
      last_interact_at: profile.updated_at ?? null,
    },
    tags: items(tagsSource),
    timeline,
    chat: { items: chat, total: chat.length, local_archive_available: false },
    hxc: { available: false },
    non_atomic_snapshot: true,
    real_external_call_executed: false,
  });
}

async function customerSurvey(id: number, init?: RequestInit): Promise<Response> {
  const response = await originalFetch(`/api/v1/customers/${id}/survey-answers`, init);
  if (!response.ok) return response;
  const source = record(await response.json());
  const sourceItems = items(source);
  return json({
    customer_id: id,
    identity_values_included: false,
    free_text_included: false,
    real_external_call_executed: false,
    non_atomic_snapshot: true,
    scan_truncated: false,
    result_truncated: Number(source.total) > sourceItems.length,
    items: sourceItems.map((value) => {
      const submission = record(value);
      const choices = (Array.isArray(submission.answers) ? submission.answers : [])
        .map(record)
        .filter((answer) => answer.question_type === "single_choice" || answer.question_type === "multi_choice")
        .map((answer) => ({
          question_id: answer.question_id,
          question_type: answer.question_type,
          sort_order: Number.isSafeInteger(Number(answer.sort_order)) ? Number(answer.sort_order) : 0,
          option_ids: (Array.isArray(answer.selected_options) ? answer.selected_options : [])
            .map(record)
            .map((option) => option.option_id)
            .filter((optionID) => Number.isSafeInteger(Number(optionID)) && Number(optionID) > 0),
        }));
      return {
        submission_id: submission.id,
        questionnaire_id: submission.questionnaire_id,
        submitted_at: submission.submitted_at,
        score: submission.total_score,
        choice_answers: choices,
      };
    }),
  });
}

window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = requestURL(input);
  if (url.pathname === "/api/v1/customers") return customerDirectory(url, init);
  if (url.pathname === "/api/v1/stages") return json({ items: [] });
  const contextID = currentCustomerID(url.pathname, "context");
  if (contextID !== undefined) return customerContext(contextID, init);
  const surveyID = currentCustomerID(url.pathname, "survey-answers");
  if (surveyID !== undefined) return customerSurvey(surveyID, init);
  return originalFetch(input, init);
};
