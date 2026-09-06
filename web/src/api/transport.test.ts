import {
  apiRequestOptions,
  ApiError,
  request,
  unwrapGenerated,
} from "./transport";
import { sidebarApi } from "./sidebar";
import {
  profileReceiptSteps,
  SidebarBootstrapCoordinator,
} from "../sidebar/main";

function assert(ok: unknown, message: string): asserts ok {
  if (!ok) throw new Error(message);
}

export async function runTransportContractTests(): Promise<void> {
  const coordinator = new SidebarBootstrapCoordinator<string>();
  const signals: AbortSignal[] = [];
  let firstResolve: (value: string) => void = () => undefined;
  let secondResolve: (value: string) => void = () => undefined;
  const first = coordinator.run("external-1", (signal) => {
    signals.push(signal);
    return new Promise<string>((resolve) => {
      firstResolve = resolve;
    });
  });
  const duplicate = coordinator.run("external-1", () =>
    Promise.reject(new Error("duplicate request must not start")),
  );
  assert(
    first === duplicate && signals.length === 1,
    "same Sidebar customer bootstrap must be single-flight",
  );
  const second = coordinator.run("external-2", (signal) => {
    signals.push(signal);
    return new Promise<string>((resolve) => {
      secondResolve = resolve;
    });
  });
  assert(
    Number(signals.length) === 2 && signals[0].aborted,
    "Sidebar customer switch must abort the previous bootstrap",
  );
  firstResolve("stale");
  try {
    await first;
    throw new Error("stale Sidebar bootstrap was accepted");
  } catch (error) {
    assert(
      error instanceof Error && error.name === "AbortError",
      "stale Sidebar bootstrap response must be rejected",
    );
  }
  secondResolve("fresh");
  assert(
    (await second) === "fresh",
    "latest Sidebar bootstrap response must remain usable",
  );

  const options = apiRequestOptions(
    { method: "POST", headers: { "X-Request-ID": "test" } },
    "csrf_token=legacy; aicrm_csrf=token%201; session=x",
  );
  const headers = new Headers(options.headers);
  assert(
    options.credentials === "include",
    "same-origin cookie credentials must be included",
  );
  assert(
    !(options.headers instanceof Headers),
    "generated client options must use enumerable header records",
  );
  assert(
    headers.get("X-CSRF-Token") === "token 1",
    "OAuth session CSRF cookie must become X-CSRF-Token",
  );
  assert(
    headers.get("X-Request-ID") === "test",
    "caller headers must survive transport",
  );
  assert(
    unwrapGenerated({ status: 200, data: { cursor: "opaque" } }).cursor ===
      "opaque",
    "2xx generated response must unwrap",
  );
  try {
    unwrapGenerated({ status: 403, data: { code: "forbidden" } });
    throw new Error("403 was accepted");
  } catch (error) {
    assert(
      error instanceof ApiError && error.kind === "forbidden",
      "403 must be a structured forbidden error",
    );
  }

  const originalFetch = globalThis.fetch;
  const sidebarRequests: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    sidebarRequests.push({ input: String(input), init });
    const url = String(input);
    // 本地后端真实投影形状（internal/sidebar + internal/customer/port）。
    const data = url.includes("/questionnaires")
      ? {
          customer_id: 7,
          items: [
            {
              id: 11,
              title: "满意度回访",
              submitted_at: "2026-08-26T01:00:00Z",
              score: 8.5,
              answers: [{ question: "是否满意", answers: ["满意"] }],
            },
          ],
          source_status: "ready",
          as_of: "2026-08-26T02:00:00Z",
        }
      : {
          customer_id: 7,
          items: [
            {
              id: 7,
              event_type: "survey_submitted",
              title: "提交问卷",
              source_domain: "survey",
              occurred_at: "2026-08-26T00:00:00Z",
            },
          ],
          source_status: "ready",
          as_of: "2026-08-26T02:00:00Z",
        };
    return new Response(JSON.stringify(data), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  try {
    const timeline = await sidebarApi.timeline("sidebar-context", {
      cursor: "legacy-cursor-must-not-leak",
      limit: 20,
    });
    const questionnaires = await sidebarApi.questionnaires("sidebar-context", {
      limit: 100,
    });
    assert(
      timeline.items[0]?.event_type === "survey_submitted" &&
        timeline.items[0]?.id === 7 &&
        timeline.next_cursor === undefined &&
        timeline.safety.local_only,
      "Sidebar timeline adapter must map the local projection and stay single-page",
    );
    assert(
      questionnaires.items[0]?.submission_id === 11 &&
        questionnaires.items[0]?.title === "满意度回访" &&
        questionnaires.items[0]?.text_answers?.[0]?.question === "是否满意" &&
        questionnaires.items[0]?.text_answers?.[0]?.answers[0] === "满意" &&
        questionnaires.items[0]?.choice_answers.length === 0 &&
        questionnaires.scan_truncated === false &&
        questionnaires.safety.local_only,
      "Sidebar questionnaire adapter must map the local survey projection",
    );
    let chatFailure: unknown;
    try {
      await sidebarApi.chatActivity("sidebar-context", {
        chat_type: "private",
        limit: 10,
      });
    } catch (error) {
      chatFailure = error;
    }
    assert(
      chatFailure instanceof Error && chatFailure.message.includes("消息归档"),
      "Chat activity must fail honestly while message archive is disabled",
    );
    let otherFailure: unknown;
    try {
      await sidebarApi.otherStaffChats("sidebar-context");
    } catch (error) {
      otherFailure = error;
    }
    assert(
      otherFailure instanceof Error && otherFailure.message.includes("消息归档"),
      "Other-staff chats must fail honestly while message archive is disabled",
    );
    assert(
      sidebarRequests[0]?.input === "/api/sidebar/v2/timeline?limit=20",
      "Timeline must call the local backend without leaking cursors",
    );
    assert(
      sidebarRequests[1]?.input === "/api/sidebar/v2/questionnaires?limit=50",
      "Questionnaires limit must clamp to the backend bound of 50",
    );
    assert(
      sidebarRequests.length === 2,
      "Disabled chat archive must not issue requests",
    );
    for (const call of sidebarRequests) {
      assert(
        call.init?.method === undefined || call.init?.method === "GET",
        "Sidebar activity reads must use GET",
      );
      assert(
        new Headers(call.init?.headers).get("X-Sidebar-Context-Token") ===
          "sidebar-context",
        "Sidebar activity reads must carry scoped context token",
      );
      assert(
        call.init?.credentials === "include",
        "Sidebar activity reads must include same-origin credentials",
      );
    }
  } finally {
    globalThis.fetch = originalFetch;
  }

  let seen: RequestInit | undefined;
  globalThis.fetch = async (_input, init) => {
    seen = init;
    return new Response(JSON.stringify({ code: "csrf_invalid" }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    });
  };
  try {
    await request("/api/v1/example", {
      method: "PUT",
      headers: { "X-CSRF-Token": "explicit" },
    });
    throw new Error("401 was accepted");
  } catch (error) {
    assert(
      error instanceof ApiError && error.kind === "unauthenticated",
      "401 must be a structured unauthenticated error",
    );
    assert(
      seen?.credentials === "include",
      "direct fetch must include credentials",
    );
    assert(
      new Headers(seen?.headers).get("X-CSRF-Token") === "explicit",
      "explicit CSRF header must not be overwritten",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }

  const sidebarWriteRequests: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    sidebarWriteRequests.push({ input: String(input), init });
    const url = String(input);
    if (url.includes("jssdk-config")) {
      return new Response(
        JSON.stringify({
          corp_id: "corp-test",
          agent_id: "7",
          config: {
            timestamp: 1720000000,
            nonceStr: "nonce-config",
            signature: "b".repeat(40),
            jsApiList: [],
          },
          agent_config: {
            timestamp: 1720000000,
            nonceStr: "nonce-test",
            signature: "a".repeat(40),
            jsApiList: ["getContext", "getCurExternalContact", "sendChatMessage"],
          },
        }),
        { status: 200 },
      );
    }
    if (url.includes("/profile")) {
      return new Response(
        JSON.stringify({
          customer: {
            customer_id: 7,
            display_name: "测试客户",
            status: "active",
            gender: 0,
            corp_name: "测试公司",
            source: "新来源",
            version: 4,
            updated_at: "2026-08-26T02:00:00Z",
          },
        }),
        { status: 200 },
      );
    }
    if (url.includes("/phone-binding")) {
      return new Response(
        JSON.stringify({
          status: "attached",
          phone_masked: "138****8000",
          phone_assurance: "declared",
        }),
        { status: 200 },
      );
    }
    if (url.includes("/send-intents") && !url.includes("/outcome")) {
      return new Response(
        JSON.stringify({
          intent_id: 51,
          effect_id: "eff-1",
          state: "queued",
          grant: "grant-token",
          grant_expires_at: "2026-08-26T03:00:00Z",
          payload: { msgtype: "image", image: { mediaid: "media-31" } },
          replayed: false,
        }),
        { status: 202 },
      );
    }
    if (url.includes("/outcome")) {
      return new Response(
        JSON.stringify({ intent_id: 51, effect_id: "eff-1", state: "client_executed" }),
        { status: 200 },
      );
    }
    if (url.includes("/materials/31/variants/thumb_320")) {
      return new Response(new Blob(["image-bytes"], { type: "image/png" }), {
        status: 200,
        headers: { "Content-Type": "image/png", ETag: '"thumb"' },
      });
    }
    return new Response("", {
      status: 302,
      headers: { Location: "/sidebar/index.html?external_userid=ext-7" },
    });
  };
  try {
    const agentConfig = await sidebarApi.agentConfig(
      "https://app.test/sidebar/index.html",
    );
    assert(
      agentConfig.signature_type === "agent_config" &&
        agentConfig.corp_id === "corp-test" &&
        agentConfig.agent_id === 7 &&
        agentConfig.nonce === "nonce-test" &&
        agentConfig.timestamp === 1720000000 &&
        agentConfig.signature === "a".repeat(40) &&
        agentConfig.url === "https://app.test/sidebar/index.html",
      "JSSDK adapter must map the local agent_config signature",
    );
    const profile = await sidebarApi.profile(
      "sidebar-context",
      {
        display_name: "测试客户",
        gender: 0,
        corp_name: "测试公司",
        expected_version: 3,
      },
      "sidebar-profile-test-key",
    );
    assert(
      profile.profile.name === "测试客户" &&
        profile.profile.corp_name === "测试公司" &&
        profile.profile.version === 4 &&
        profile.safety.local_only &&
        !profile.safety.effect_queued,
      "Profile adapter must map the local customer projection",
    );
    const phone = await sidebarApi.bindPhone(
      "sidebar-context",
      { phone: "13800138000" },
      "sidebar-phone-test-key",
    );
    assert(
      phone.status === "bound" && phone.safety.local_only,
      "Phone adapter must map attached to bound with local safety",
    );
    const intent = await sidebarApi.createSendIntent(
      "sidebar-context",
      { resource_kind: "material", resource_id: "31" },
      "sidebar-send-test-key",
    );
    assert(
      intent.intent_id === 51 &&
        intent.grant === "grant-token" &&
        (intent.payload as { msgtype?: string })?.msgtype === "image",
      "Send intent must retain the server-wrapped payload and grant",
    );
    const outcome = await sidebarApi.completeSendIntent("sidebar-context", 51, {
      grant: "grant-token",
      outcome: "client_executed",
      evidence: "jssdk-callback-ok",
    });
    assert(
      outcome.state === "client_executed",
      "Send intent completion must retain the outcome receipt",
    );
    const thumbnail = await sidebarApi.thumbnailPreview("sidebar-context", 31);
    assert(
      thumbnail.type === "image/png" && thumbnail.size > 0,
      "Sidebar thumbnail preview must read real binary bytes",
    );
    const oauth = sidebarApi.oauthStartUrl({
      external_userid: "ext-7",
      next: "/sidebar/index.html",
    });
    assert(
      oauth === "/api/sidebar/oauth/start?next=%2Fsidebar%2Findex.html",
      "OAuth start must target the local backend route",
    );
    const callback = sidebarApi.oauthCallbackUrl({
      code: "oauth-code",
      state: "state_abcdefghijklmnopqrstuvwxyz0123456789_",
    });
    assert(
      callback.startsWith("/api/sidebar/oauth/callback?code=oauth-code&state="),
      "OAuth callback must target the local backend route",
    );
    const agentCall = sidebarWriteRequests.find((call) =>
      call.input.includes("jssdk-config"),
    );
    assert(
      agentCall?.init?.credentials === "include" &&
        agentCall.input.includes("url=https%3A%2F%2Fapp.test"),
      "JSSDK config must include the browser session and signed URL",
    );
    const profileCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/profile"),
    );
    const profileHeaders = new Headers(profileCall?.init?.headers);
    const profileBody = JSON.parse(String(profileCall?.init?.body));
    assert(
      profileHeaders.get("X-Sidebar-Context-Token") === "sidebar-context" &&
        profileHeaders.get("Idempotency-Key") === "sidebar-profile-test-key" &&
        profileCall?.init?.method === "PUT" &&
        profileCall?.init?.credentials === "include",
      "Profile writes must carry scoped token and idempotency key",
    );
    assert(
      profileBody.display_name === "测试客户" &&
        profileBody.expected_version === 3 &&
        profileBody.patch === undefined &&
        profileBody.expected_updated_at === undefined,
      "Profile writes must use the local display_name/expected_version contract",
    );
    const phoneCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/phone-binding"),
    );
    const phoneHeaders = new Headers(phoneCall?.init?.headers);
    const phoneBody = JSON.parse(String(phoneCall?.init?.body));
    assert(
      phoneCall?.init?.method === "POST" &&
        phoneHeaders.get("X-Sidebar-Context-Token") === "sidebar-context" &&
        phoneHeaders.get("Idempotency-Key") === "sidebar-phone-test-key" &&
        phoneBody.phone === "13800138000" &&
        phoneBody.mobile === undefined,
      "Phone binding must send the local 11-digit phone contract",
    );
    const intentCall = sidebarWriteRequests.find(
      (call) =>
        call.input.includes("/send-intents") && !call.input.includes("/outcome"),
    );
    const intentHeaders = new Headers(intentCall?.init?.headers);
    assert(
      intentCall?.init?.method === "POST" &&
        intentHeaders.get("Idempotency-Key") === "sidebar-send-test-key" &&
        JSON.parse(String(intentCall?.init?.body)).resource_kind === "material",
      "Send intent must be created with a stable scoped idempotency key",
    );
    const outcomeCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/send-intents/51/outcome"),
    );
    assert(
      outcomeCall?.init?.method === "POST" &&
        JSON.parse(String(outcomeCall?.init?.body)).outcome ===
          "client_executed",
      "Send intent completion must forward grant and outcome",
    );
    const thumbnailCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/materials/31/variants/thumb_320"),
    );
    assert(
      thumbnailCall?.init?.method === undefined &&
        new Headers(thumbnailCall?.init?.headers).get(
          "X-Sidebar-Context-Token",
        ) === "sidebar-context",
      "Thumbnail preview must use the sidebar variant route with scoped transport",
    );
    // 手机号归属冲突：后端 409 必须映射为 rejected，而非抛错。
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ error: { code: "conflict" } }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      });
    const rejected = await sidebarApi.bindPhone(
      "sidebar-context",
      { phone: "13800138000" },
      "sidebar-phone-conflict-key",
    );
    assert(
      rejected.status === "rejected",
      "Phone ownership conflict must map to the rejected receipt",
    );
    assert(
      profileReceiptSteps({
        effect_queued: true,
        provider_execution_eligible: true,
      })
        .map((step) => step.key)
        .join(",") === "accepted,queued,outcome_unknown",
      "Queued provider effects must remain outcome_unknown until a receipt exists",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
}
