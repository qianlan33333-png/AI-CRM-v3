// The sidebar Host is deliberately a narrow trusted boundary.  The dd8
// workbench overlay owns all standard rendering; this module owns only WeCom
// identity/JSSDK setup, the scoped context token, and the small DTO adapter.

type Json = Record<string, any>;
type RequestOptions = RequestInit & { timeoutMs?: number; retryCount?: number; retryDelayMs?: number };

type WX = {
  config(options: Json): void;
  ready(callback: () => void): void;
  error(callback: (result?: Json) => void): void;
  agentConfig(options: Json): void;
  invoke(method: string, payload: Json, callback: (result?: Json) => void): void;
};

declare global {
  interface Window {
    wx?: WX;
    __AICRMSidebarBridge?: SidebarBridge;
    ImageResourceLoader?: { loadInto(image: HTMLImageElement, url: string, options?: { signal?: AbortSignal; onState?: (state: string) => void }): Promise<void> };
  }
}

const TIMEOUT_MS = 5_000;
const REGULAR_APIS = ["getCurExternalContact", "sendChatMessage"];
const AGENT_APIS = ["getContext", "getCurExternalContact", "sendChatMessage"];

function failure(message: string, status?: number, payload?: Json): Error & { status?: number; payload?: Json } {
  const error = new Error(message) as Error & { status?: number; payload?: Json };
  error.status = status;
  error.payload = payload;
  return error;
}

function oneString(value: unknown, keys: string[]): string {
  if (!value || typeof value !== "object") return "";
  const record = value as Json;
  for (const key of keys) {
    const candidate = String(record[key] ?? "").trim();
    if (candidate) return candidate;
  }
  for (const nested of ["data", "context", "user", "currentUser", "current_user"]) {
    const candidate = oneString(record[nested], keys);
    if (candidate) return candidate;
  }
  return "";
}

function formatMoney(minor: unknown, currency = "CNY"): string {
  const value = Number(minor);
  if (!Number.isFinite(value)) return "";
  return `${currency === "CNY" ? "¥" : `${currency} `}${(value / 100).toFixed(2)}`;
}

function date(value: unknown): string {
  const parsed = new Date(String(value ?? ""));
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString().replace("T", " ").slice(0, 16);
}

function idempotency(scope: string): string {
  return `${scope}-${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

export class SidebarBridge {
  private token = "";
  private externalUserID = "";
  private profileVersion = 0;
  private profile: Json = {};
  private periodicVersions = new Map<string, number>();
  private startFlight: Promise<void> | null = null;
  private regularConfig: Promise<void> | null = null;

  contextToken(): string { return this.token; }

  async start(): Promise<void> {
    if (this.token) return;
    if (this.startFlight) return this.startFlight;
    this.startFlight = this.startTrustedContext();
    try { await this.startFlight; } finally { this.startFlight = null; }
  }

  private async startTrustedContext(): Promise<void> {
    const query = new URL(window.location.href).searchParams;
    const queryExternal = oneString(Object.fromEntries(query), ["external_userid", "externalUserid", "externalUserId", "user_id", "userId"]);
    const sdkExternal = await this.resolveWeComExternalUserID();
    this.externalUserID = sdkExternal || queryExternal;
    if (!this.externalUserID) throw failure("未识别到客户，请从企微客户侧边栏重新打开。");
    const bootstrap = await this.raw("/api/sidebar/v2/bootstrap", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ external_userid: this.externalUserID }),
    });
    if (bootstrap.state === "viewer_session_required") {
      const next = window.location.pathname + window.location.search;
      window.location.assign(`/api/sidebar/oauth/start?next=${encodeURIComponent(next)}`);
      throw failure("正在恢复员工登录态。");
    }
    if (bootstrap.state !== "ready" || !String(bootstrap.context_token || "").trim()) {
      throw failure(bootstrap.state === "customer_not_bound" ? "当前企微联系人尚未绑定本地客户。" : "侧边栏上下文未就绪。");
    }
    this.token = String(bootstrap.context_token);
    this.rememberWorkbench(bootstrap.workbench || {});
  }

  private async resolveWeComExternalUserID(): Promise<string> {
    const wx = window.wx;
    if (!wx) return "";
    const signature = await this.raw(`/api/sidebar/jssdk-config?url=${encodeURIComponent(window.location.href.split("#")[0])}`);
    // Retrying an agent-scoped setup must refresh its signed payload, but a
    // successful regular wx.config remains page-scoped. Reconfiguring it can
    // duplicate the dedicated SDK's preVerify handshake.
    if (!this.regularConfig) {
      const regular = this.configureRegular(wx, signature);
      this.regularConfig = regular;
      try {
        await regular;
      } catch (error) {
        if (this.regularConfig === regular) this.regularConfig = null;
        throw error;
      }
    } else {
      await this.regularConfig;
    }
    await new Promise<void>((resolve, reject) => {
      let done = false;
      const finish = (error?: Error) => {
        if (done) return;
        done = true;
        window.clearTimeout(timer);
        error ? reject(error) : resolve();
      };
      const timer = window.setTimeout(() => finish(failure("企微 agentConfig 超时。")), TIMEOUT_MS);
      wx.agentConfig({ corpid: signature.corp_id, agentid: String(signature.agent_id), timestamp: Number(signature.agent_config?.timestamp), nonceStr: String(signature.agent_config?.nonceStr || ""), signature: String(signature.agent_config?.signature || ""), jsApiList: AGENT_APIS,
        success: () => finish(), fail: (detail: Json) => finish(failure(String(detail?.err_msg || "企微 agentConfig 失败。"))),
      });
    });
    // Keep the V3 reliability sequence: the agent context is refreshed before
    // the contact lookup.  The value is not used as a customer identity.
    await this.invoke("getContext", {});
    const contact = await this.invoke("getCurExternalContact", {});
    return oneString(contact, ["external_userid", "externalUserid", "externalUserId", "userId", "user_id"]);
  }

  private configureRegular(wx: WX, signature: Json): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      let done = false;
      const finish = (error?: Error) => {
        if (done) return;
        done = true;
        window.clearTimeout(timer);
        error ? reject(error) : resolve();
      };
      const timer = window.setTimeout(() => finish(failure("企微 JSSDK 初始化超时。")), TIMEOUT_MS);
      wx.error((detail) => finish(failure(String(detail?.err_msg || "企微 JSSDK 初始化失败。"))));
      wx.config({ beta: true, debug: false, appId: signature.corp_id, timestamp: Number(signature.config?.timestamp), nonceStr: String(signature.config?.nonceStr || ""), signature: String(signature.config?.signature || ""), jsApiList: REGULAR_APIS });
      wx.ready(() => finish());
    });
  }

  async invoke(method: string, payload: Json): Promise<Json> {
    const wx = window.wx;
    if (!wx?.invoke) throw failure("企微发送能力未加载，请从企微客户侧边栏重新打开。");
    return new Promise<Json>((resolve, reject) => {
      let done = false;
      const timer = window.setTimeout(() => { if (!done) { done = true; reject(failure(`${method} 超时`)); } }, TIMEOUT_MS);
      wx.invoke(method, payload, (result) => {
        if (done) return;
        done = true; window.clearTimeout(timer);
        const response = result || {};
        const status = String(response.err_msg || response.errmsg || "");
        if (status && !status.includes(":ok")) { reject(failure(status)); return; }
        resolve(response);
      });
    });
  }

  async send(input: { resource_kind: "product" | "material"; resource_id: string; product_type?: string }): Promise<Json> {
    await this.start();
    const accepted = await this.scoped("/api/sidebar/v2/send-intents", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-send") },
      body: JSON.stringify(input),
    });
    const payload = accepted.payload || {};
    const grant = String(accepted.grant || "");
    const intentID = Number(accepted.intent_id || 0);
    if (!grant || !Number.isInteger(intentID) || intentID < 1 || !payload.msgtype) throw failure("发送意图未返回可执行回执。");
    try {
      const response = await this.invoke("sendChatMessage", payload);
      await this.scoped(`/api/sidebar/v2/send-intents/${intentID}/outcome`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ grant, outcome: "client_executed", evidence: "sidebar_jssdk_client_executed" }),
      });
      return response;
    } catch (error) {
      // A client-side API rejection/timeout cannot prove that WeCom did not
      // receive the payload. Record the established durable unknown outcome
      // under the original grant and never create a replacement send intent.
      try {
        await this.scoped(`/api/sidebar/v2/send-intents/${intentID}/outcome`, {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ grant, outcome: "outcome_unknown", evidence: "sidebar_jssdk_outcome_unknown" }),
        });
      } catch { /* retain the original client failure; the durable intent remains reconcilable */ }
      throw error;
    }
  }

  async request(input: string, options: RequestOptions = {}): Promise<Json> {
    await this.start();
    const url = new URL(input, window.location.origin);
    const path = url.pathname;
    if (path.includes("other-staff") || path.includes("chat")) throw failure("聊天能力不属于侧边栏。");
    if (path === "/api/sidebar/v2/workbench") return this.legacyWorkbench();
    if (path === "/api/sidebar/bind-mobile") return this.bindMobile(options);
    if (path === "/api/sidebar/v2/profile" && String(options.method || "GET").toUpperCase() === "PUT") return this.saveProfile(options);
    if (/^\/api\/sidebar\/v2\/periodic-orders\/\d+\/remark$/.test(path) && String(options.method || "GET").toUpperCase() === "PUT") return this.savePeriodicRemark(path, options);
    if (path === "/api/sidebar/v2/send-intents") return this.sendIntent(options);
    const raw = await this.scoped(url.pathname + url.search, options);
    return this.legacy(path, raw);
  }

  private async raw(path: string, options: RequestInit = {}): Promise<Json> {
    const response = await fetch(path, { cache: "no-store", ...options, headers: { Accept: "application/json", ...(options.headers || {}) } });
    const text = await response.text();
    let payload: Json = {};
    try { payload = text ? JSON.parse(text) : {}; } catch { throw failure("服务端返回了无效数据。", response.status); }
    if (!response.ok) throw failure(String(payload?.error?.code || payload?.error || "请求失败"), response.status, payload);
    return payload;
  }

  private async scoped(path: string, options: RequestOptions = {}): Promise<Json> {
    if (!this.token) throw failure("侧边栏上下文未就绪。");
    const { timeoutMs: _timeout, retryCount: _retry, retryDelayMs: _delay, ...init } = options;
    return this.raw(path, { ...init, headers: { "X-Sidebar-Context-Token": this.token, ...(init.headers || {}) } });
  }

  private rememberWorkbench(workbench: Json): void {
    const profile = workbench.profile || {};
    this.profile = profile;
    this.profileVersion = Number(profile.profile_version || 0);
  }

  private legacyWorkbench(): Json {
    const profile = this.profile;
    return {
      customer: { display_name: profile.name || "当前客户", mobile: profile.phone_masked || "", mobile_bound: profile.phone_assurance === "verified" },
      profile: { source: profile.profile_source || "", industry: profile.industry || "", industry_description: profile.industry_description || "", needs_blockers_followup: profile.needs_blockers_followup || "" },
      workflow: {}, diagnostics: { context_source_status: "ready" },
    };
  }

  private async saveProfile(options: RequestOptions): Promise<Json> {
    let donor: Json = {};
    try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("画像保存请求无效。"); }
    const body = {
      display_name: "", gender: 0, corp_name: "", expected_version: 0,
      expected_profile_version: this.profileVersion,
      source: String(donor.source ?? ""), industry: String(donor.industry ?? ""),
      industry_description: String(donor.industry_description ?? ""), needs_blockers_followup: String(donor.needs_blockers_followup ?? ""),
    };
    const updated = await this.scoped("/api/sidebar/v2/profile", { method: "PUT", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-profile") }, body: JSON.stringify(body) });
    const profile = updated.customer || {};
    this.profile = profile; this.profileVersion = Number(profile.profile_version || this.profileVersion);
    return { profile: { source: profile.profile_source || "", industry: profile.industry || "", industry_description: profile.industry_description || "", needs_blockers_followup: profile.needs_blockers_followup || "" } };
  }

  private async bindMobile(options: RequestOptions): Promise<Json> {
    let donor: Json = {}; try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("手机号保存请求无效。"); }
    const updated = await this.scoped("/api/sidebar/v2/phone-binding", {
      method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-phone") },
      body: JSON.stringify({ phone: String(donor.mobile || "") }),
    });
    return { binding: { mobile: updated.phone_masked || "" } };
  }

  private async savePeriodicRemark(path: string, options: RequestOptions): Promise<Json> {
    let donor: Json = {}; try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("备注保存请求无效。"); }
    const id = path.split("/")[5];
    const updated = await this.scoped(path, { method: "PUT", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-periodic-remark") }, body: JSON.stringify({ remark: String(donor.remark || ""), expected_version: this.periodicVersions.get(id) || 0 }) });
    const entry = updated || {}; this.periodicVersions.set(id, Number(entry.version || this.periodicVersions.get(id) || 0));
    return { periodic_order: { id, remark: entry.remark || "" } };
  }

  private async sendIntent(options: RequestOptions): Promise<Json> {
    let donor: Json = {}; try { donor = options.body ? JSON.parse(String(options.body)) : {}; } catch { throw failure("发送请求无效。"); }
    if (donor.type !== "image" || !String(donor.material_id || "").trim()) throw failure("不支持的发送资源。");
    const accepted = await this.scoped("/api/sidebar/v2/send-intents", { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": idempotency("sidebar-send") }, body: JSON.stringify({ resource_kind: "material", resource_id: String(donor.material_id) }) });
    const payload = accepted.payload || {}; const mediaID = String(payload?.image?.mediaid || "");
    if (!mediaID) throw failure("图片素材未取得 media_id。");
    return { media_id: mediaID, send_intent: accepted };
  }

  private legacy(path: string, payload: Json): Json {
    if (path === "/api/sidebar/v2/questionnaires") return { questionnaires: (payload.items || []).map((item: Json) => ({ title: item.title, submitted_at: date(item.submitted_at), answer_count: (item.answers || []).length, total_count: (item.answers || []).length, answers: (item.answers || []).map((answer: Json) => ({ question: answer.question, answer: (answer.answers || []).join("、") })) })) };
    if (path === "/api/sidebar/v2/timeline") return { items: (payload.items || []).filter((item: Json) => !String(item.event_type || "").includes("message")).map((item: Json) => ({ title: item.title, event_time: item.occurred_at, type: item.event_type })), total: payload.total || (payload.items || []).length, has_more: false };
    if (path === "/api/sidebar/v2/products") {
      const items = payload.items || [];
      const map = (item: Json) => ({ id: item.id, title: item.name, price_label: formatMoney(item.price_minor, item.currency), duration_days: item.service_period_duration_days, product_url: item.public_url || "" });
      return { products: items.filter((item: Json) => item.product_type === "standard").map(map), service_period_products: items.filter((item: Json) => item.product_type === "service_period").map(map) };
    }
    if (path === "/api/sidebar/v2/orders") return { orders: (payload.items || []).map((item: Json) => ({ id: item.merchant_order_no || item.id, title: item.items?.[0]?.product_name || "订单", amount_label: formatMoney(item.amount?.minor, item.amount?.currency), status_label: item.status, paid_at: date(item.created_at) })) };
    if (path === "/api/sidebar/v2/periodic-orders") return { periodic_orders: (payload.items || []).map((item: Json) => { this.periodicVersions.set(String(item.id), Number(item.version || 0)); return { id: item.id, title: item.title, status_label: item.status, remark: item.remark, duration_days: 0, remaining_days: 0 }; }) };
    if (path === "/api/sidebar/v2/materials") return { materials: (payload.items || []).map((item: Json) => ({ id: item.id, tags: item.tags || [], thumbnail_url: `/api/sidebar/v2/materials/${encodeURIComponent(String(item.id))}/variants/thumb_320` })), total: payload.total || 0, has_more: false, quick_keywords: [] };
    if (path === "/api/sidebar/v2/radar-links") return { items: (payload.items || []).map((item: Json) => ({ title: item.title || item.name, url: item.url, type_label: item.content_type || "追踪链接" })) };
    if (path === "/api/sidebar/v2/coupons") return {
      ...payload,
      items: (payload.items || []).map((item: Json) => ({
        ...item,
        discount_label: formatMoney(item.discount_minor, item.currency),
        products: item.targets || [],
        claim_ends_at: date(item.claim_ends_at),
      })),
    };
    return payload;
  }
}

function start(): void {
  const root = document.getElementById("sidebar-workbench-root");
  if (!root) return;
  const bridge = new SidebarBridge();
  window.__AICRMSidebarBridge = bridge;
  // The donor renderer requests image thumbnails through a small loader
  // contract. Keep image authorization in the trusted bridge: a DOM img URL
  // cannot attach the scoped context token, while this loader can fetch a
  // bounded enabled variant and hand the renderer a blob URL.
  window.ImageResourceLoader = {
    async loadInto(image, input, options = {}) {
      await bridge.start();
      options.onState?.("loading");
      const url = new URL(input, window.location.origin);
      const response = await fetch(url.pathname + url.search, { headers: { "X-Sidebar-Context-Token": bridge.contextToken() }, signal: options.signal, cache: "no-store" });
      if (!response.ok) throw failure("预览不可用。", response.status);
      const objectURL = URL.createObjectURL(await response.blob());
      const prior = image.dataset.sidebarBlobURL;
      if (prior) URL.revokeObjectURL(prior);
      image.dataset.sidebarBlobURL = objectURL;
      image.src = objectURL;
      image.dataset.materialPreview = "ready";
    },
  };
  const overlay = String(root.dataset.overlayUrl || "").trim();
  if (!overlay) { root.textContent = "侧边栏资源未就绪。"; return; }
  const script = document.createElement("script");
  script.src = overlay;
  script.async = false;
  script.onerror = () => { root.textContent = "侧边栏资源加载失败。"; };
  document.head.append(script);
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
  else start();
}
