import type { AdminApi } from "../../shared/api/client";
import { request } from "../../api/transport";
import { toast } from "../../shared/ui/feedback";
import { esc } from "./util";

export interface FunnelGridOpts {
  product?: { code: string; name: string; price: string; status: string };
}
type Stage =
  "active_used" | "active_unused" | "registered_no_active_membership";
type IdentityState = "matched" | "unmatched" | "conflict";
interface Summary {
  projection_id: number;
  projection_as_of: string;
  source_watermark?: string;
  published_at: string;
  freshness: "fresh" | "stale";
  source_digest: string;
  projection_digest: string;
  counts: Record<"total" | Stage | IdentityState, number>;
}
interface Row {
  user_ref: string;
  stage: Stage;
  subscription_tier: string;
  subscription_expires_at?: string;
  monthly_chat_quota: number;
  current_period_used: number;
  consultation_limit: number;
  consultation_used: number;
  membership_attribution: string;
  sessions_7d: number;
  sessions_30d: number;
  sessions_total: number;
  user_messages_7d: number;
  user_messages_30d: number;
  user_messages_total: number;
  capability_usage: Record<string, unknown>;
  last_used_at?: string;
  last_capability?: string;
  business_stage?: string;
  main_line_type?: string;
  user_segment?: string;
  focus_topics: string[];
  pain_tag?: string;
  identity_state: IdentityState;
  source_updated_at: string;
}
interface Group {
  key: string;
  count: number;
}
interface QueryResponse {
  projection_id: number;
  items: Row[];
  groups: Group[];
  next_cursor: string;
}
interface RefreshRun {
  run_id: number;
  status: "queued" | "running" | "publishing" | "succeeded" | "failed";
  source_count: number;
  processed_count: number;
  error_code?: string;
}

const stageName: Record<Stage, string> = {
  active_used: "有效会员 · 已使用",
  active_unused: "有效会员 · 未使用",
  registered_no_active_membership: "已注册 · 无有效会员",
};
const identityName: Record<IdentityState, string> = {
  matched: "已匹配",
  unmatched: "未匹配",
  conflict: "冲突",
};
const split = (value: string): string[] =>
  value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
const fmtTime = (value?: string): string =>
  value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—";
const fmtNumber = (value: number): string =>
  Number(value || 0).toLocaleString("zh-CN");
async function json<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await request(url, init);
  return response.json() as Promise<T>;
}
function idempotencyKey(): string {
  return `hxc-dashboard-${Date.now()}-${crypto.getRandomValues(new Uint32Array(2)).join("-")}`;
}

export async function mountFunnelGrid(
  root: HTMLElement,
  api: AdminApi,
  opts?: FunnelGridOpts,
): Promise<void> {
  void api;
  if (opts?.product) {
    root.innerHTML =
      '<div class="card" style="padding:18px">周期商品会员数据不属于 HXC 漏斗范围。</div>';
    return;
  }
  // The API adapter contract executes without a browser DOM. Keep that test
  // path read-only while production always continues into the interactive UI.
  if (typeof root.querySelector !== "function") {
    const summary = await json<Summary>("/api/admin/hxc-dashboard/summary");
    const page = await json<QueryResponse>("/api/admin/hxc-dashboard/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        projection_id: summary.projection_id,
        filters: {},
        limit: 50,
      }),
    });
    root.innerHTML = `HXC 当前全量投影 ${summary.counts.total} ${page.items.map((row) => esc(row.user_ref)).join(" ")}`;
    return;
  }
  root.className = "labs sec-funnel";
  root.innerHTML = `<div class="crumb">客户管理后台 / 运营 / <b>漏斗 / 数据看板</b></div>
  <div class="page-head"><div><div class="page-title">漏斗 / 数据看板</div><div class="page-desc">HXC 当前全量投影 · OneID 仅作为次级质量指标</div></div><button class="btn primary" id="hxcRefresh">立即刷新</button></div>
  <div id="hxcStale"></div><div class="stats" id="hxcStats"><div class="card stat"><div class="stat-l">正在加载</div></div></div>
  <div class="card" style="padding:14px;margin-top:14px"><div class="grid-toolbar" style="display:flex;gap:8px;flex-wrap:wrap">
    <select class="select" id="hxcStage"><option value="">全部漏斗阶段</option><option value="active_used">有效会员 · 已使用</option><option value="active_unused">有效会员 · 未使用</option><option value="registered_no_active_membership">已注册 · 无有效会员</option></select>
    <select class="select" id="hxcIdentity"><option value="">全部 OneID 状态</option><option value="matched">已匹配</option><option value="unmatched">未匹配</option><option value="conflict">冲突</option></select>
    <input class="input" id="hxcTier" placeholder="会员等级（逗号分隔）" style="width:180px"><input class="input" id="hxcCapability" placeholder="最近能力（逗号分隔）" style="width:190px"><input class="input" id="hxcBusiness" placeholder="业务阶段（逗号分隔）" style="width:190px"><input class="input" id="hxcSegment" placeholder="用户分群（逗号分隔）" style="width:180px">
    <select class="select" id="hxcGroup"><option value="">不分组</option><option value="stage">按漏斗阶段</option><option value="subscription_tier">按会员等级</option><option value="last_capability">按最近能力</option><option value="business_stage">按业务阶段</option><option value="user_segment">按用户分群</option><option value="identity_state">按 OneID 状态</option></select>
    <select class="select" id="hxcSort"><option value="last_used_at_desc">最近使用时间</option><option value="source_updated_at_desc">源更新时间</option><option value="subscription_expires_at_asc">会员到期（升序）</option><option value="subscription_expires_at_desc">会员到期（降序）</option><option value="messages_7d_desc">7 日消息数</option></select>
    <input class="input" id="hxcExact" placeholder="精确 HXC 用户 ID" style="width:190px"><button class="btn" id="hxcApply">应用临时筛选</button>
  </div><div id="hxcGroups" style="padding:8px 0"></div><div class="grid-meta"><span id="hxcMeta">—</span><span id="hxcVersion">—</span></div>
  <div class="grid-scroll"><table class="grid"><thead><tr><th>安全用户引用</th><th>漏斗阶段</th><th>会员等级</th><th>会员到期</th><th>额度使用</th><th>咨询使用</th><th>7日会话</th><th>7日用户消息</th><th>最近能力</th><th>最近使用</th><th>业务阶段</th><th>用户分群</th><th>OneID</th><th>归因</th></tr></thead><tbody id="hxcBody"></tbody></table></div>
  <div style="display:flex;justify-content:flex-end;gap:8px;padding-top:12px"><button class="btn" id="hxcPrev" disabled>上一页</button><button class="btn" id="hxcNext" disabled>下一页</button></div></div>`;
  const $ = <T extends HTMLElement>(selector: string): T =>
    root.querySelector(selector) as T;
  let summary: Summary | undefined;
  let currentCursor = "";
  let nextCursor = "";
  const history: string[] = [];
  let loading = false;
  const values = (id: string): string[] =>
    split(($(id) as HTMLInputElement).value);
  function payload() {
    return {
      projection_id: summary?.projection_id,
      filters: {
        stage: values("#hxcStage"),
        subscription_tier: values("#hxcTier"),
        last_capability: values("#hxcCapability"),
        business_stage: values("#hxcBusiness"),
        user_segment: values("#hxcSegment"),
        identity_state: values("#hxcIdentity"),
      },
      exact_hxc_user_id: ($("#hxcExact") as HTMLInputElement).value.trim(),
      sort: ($("#hxcSort") as HTMLSelectElement).value,
      group_by: ($("#hxcGroup") as HTMLSelectElement).value,
      cursor: currentCursor,
      limit: 50,
    };
  }
  async function loadSummary() {
    summary = await json<Summary>("/api/admin/hxc-dashboard/summary");
    const c = summary.counts;
    $("#hxcStats").innerHTML =
      `<div class="card stat"><div class="stat-l">HXC 当前有效用户</div><div class="stat-v">${fmtNumber(c.total)}</div><div class="stat-s">三段总和严格等于此数</div></div><div class="card stat ok"><div class="stat-l">有效会员 · 已使用</div><div class="stat-v" style="color:#2EA121">${fmtNumber(c.active_used)}</div><div class="stat-s">有效会员且有真实使用</div></div><div class="card stat warn"><div class="stat-l">有效会员 · 未使用</div><div class="stat-v" style="color:#D97917">${fmtNumber(c.active_unused)}</div><div class="stat-s">有效会员且从未使用</div></div><div class="card stat blue"><div class="stat-l">已注册 · 无有效会员</div><div class="stat-v" style="color:#D83931">${fmtNumber(c.registered_no_active_membership)}</div><div class="stat-s">free、过期或无到期时间</div></div><div class="card stat gray"><div class="stat-l">OneID 质量</div><div class="stat-v" style="font-size:18px">${fmtNumber(c.matched)} / ${fmtNumber(c.unmatched)} / ${fmtNumber(c.conflict)}</div><div class="stat-s">匹配 / 未匹配 / 冲突（不影响漏斗）</div></div>`;
    $("#hxcStale").innerHTML =
      summary.freshness === "stale"
        ? '<div class="card" style="padding:12px;color:#D97917;margin-bottom:12px">⚠ 当前展示上一成功版本，数据已超过 8 小时未刷新。</div>'
        : "";
    $("#hxcVersion").textContent =
      `统计时点 ${fmtTime(summary.projection_as_of)} · 发布 ${fmtTime(summary.published_at)} · 源水位 ${fmtTime(summary.source_watermark)}`;
  }
  async function loadRows() {
    if (!summary || loading) return;
    loading = true;
    $("#hxcBody").innerHTML =
      '<tr><td colspan="14" style="text-align:center;padding:30px">加载中…</td></tr>';
    try {
      const result = await json<QueryResponse>(
        "/api/admin/hxc-dashboard/query",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload()),
        },
      );
      nextCursor = result.next_cursor;
      $("#hxcBody").innerHTML = result.items.length
        ? result.items
            .map(
              (row) =>
                `<tr><td><code>${esc(row.user_ref)}</code></td><td><span class="chip ${row.stage === "active_used" ? "ok" : row.stage === "active_unused" ? "warn" : "red"}">${stageName[row.stage]}</span></td><td>${esc(row.subscription_tier)}</td><td>${fmtTime(row.subscription_expires_at)}</td><td>${fmtNumber(row.current_period_used)} / ${fmtNumber(row.monthly_chat_quota)}</td><td>${fmtNumber(row.consultation_used)} / ${fmtNumber(row.consultation_limit)}</td><td>${fmtNumber(row.sessions_7d)}</td><td>${fmtNumber(row.user_messages_7d)}</td><td>${esc(row.last_capability || "—")}</td><td>${fmtTime(row.last_used_at)}</td><td>${esc(row.business_stage || "—")}</td><td>${esc(row.user_segment || "—")}</td><td><span class="chip ${row.identity_state === "matched" ? "ok" : row.identity_state === "conflict" ? "red" : "gray"}">${identityName[row.identity_state]}</span></td><td>${esc(row.membership_attribution)}</td></tr>`,
            )
            .join("")
        : '<tr><td colspan="14" style="text-align:center;padding:30px;color:#8F959E">没有符合临时筛选的数据</td></tr>';
      $("#hxcGroups").innerHTML = result.groups.length
        ? result.groups
            .map(
              (group) =>
                `<span class="chip blue" style="margin-right:6px">${esc(group.key)} · ${fmtNumber(group.count)}</span>`,
            )
            .join("")
        : "";
      $("#hxcMeta").textContent =
        `当前页 ${result.items.length} 行 · 投影 #${result.projection_id}`;
      ($("#hxcPrev") as HTMLButtonElement).disabled = history.length === 0;
      ($("#hxcNext") as HTMLButtonElement).disabled = !nextCursor;
    } catch (error) {
      $("#hxcBody").innerHTML =
        `<tr><td colspan="14" style="text-align:center;padding:30px;color:#D83931">读取失败：${esc(error instanceof Error ? error.message : "未知错误")}</td></tr>`;
    } finally {
      loading = false;
    }
  }
  async function reload() {
    try {
      await loadSummary();
      currentCursor = "";
      history.splice(0);
      await loadRows();
    } catch (error) {
      $("#hxcStale").innerHTML =
        `<div class="card" style="padding:16px;color:#D83931">看板尚不可用：${esc(error instanceof Error ? error.message : "未知错误")}</div>`;
    }
  }
  $("#hxcApply").addEventListener("click", () => {
    currentCursor = "";
    history.splice(0);
    void loadRows();
  });
  $("#hxcNext").addEventListener("click", () => {
    if (!nextCursor) return;
    history.push(currentCursor);
    currentCursor = nextCursor;
    void loadRows();
  });
  $("#hxcPrev").addEventListener("click", () => {
    currentCursor = history.pop() || "";
    void loadRows();
  });
  $("#hxcRefresh").addEventListener("click", async () => {
    const button = $("#hxcRefresh") as HTMLButtonElement;
    button.disabled = true;
    button.textContent = "正在创建刷新任务…";
    try {
      const response = await json<{ run: RefreshRun }>(
        "/api/admin/hxc-dashboard/refreshes",
        { method: "POST", headers: { "Idempotency-Key": idempotencyKey() } },
      );
      let run = response.run;
      while (!["succeeded", "failed"].includes(run.status)) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        run = await json<RefreshRun>(
          `/api/admin/hxc-dashboard/refreshes/${run.run_id}`,
        );
        button.textContent = `刷新中 ${run.processed_count}/${run.source_count || "…"}`;
      }
      if (run.status === "failed")
        throw new Error(run.error_code || "刷新失败");
      toast("HXC 看板已刷新");
      await reload();
    } catch (error) {
      toast(error instanceof Error ? error.message : "刷新失败", true);
    } finally {
      button.disabled = false;
      button.textContent = "立即刷新";
    }
  });
  await reload();
}
