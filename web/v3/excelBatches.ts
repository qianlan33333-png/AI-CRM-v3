// Browser host only: it never resolves identity, enqueues work, or calls WeCom.
// It submits the versioned, CSRF-protected commands defined in the batch API.
type Obj = Record<string, any>;
const base = "/api/admin/operation-batches";
const states: Record<string, string> = {
  pending_review: "待审核",
  partially_approved: "待审核",
  approved: "已审核",
  dispatching: "任务创建中",
  needs_attention: "需要处理",
  completed: "已完成",
  completed_with_failures: "已完成，存在明确失败",
  rejected: "已拒绝",
};
const delivery: Record<string, string> = {
  pending_submission: "待提交",
  task_created_waiting_employee: "任务已创建，待员工执行",
  delivery_proven: "发送成功",
  final_failed: "明确失败",
  outcome_unknown: "结果待核实",
};

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  return node;
}
function key(prefix: string): string {
  return `${prefix}-${crypto.randomUUID()}`;
}
function csrf(): string {
  for (const part of document.cookie.split(";")) {
    const [name, ...value] = part.trim().split("=");
    if (name === "aicrm_csrf" || name === "aicrm_admin_csrf")
      return decodeURIComponent(value.join("="));
  }
  return "";
}
function errorText(status: number, body: Obj): string {
  if (status === 403) return "没有此操作权限";
  if (status === 409 && body.error === "version_conflict")
    return "内容已变化，请刷新后重新操作";
  if (status === 409 && body.error === "batch_submitted")
    return "该批次已提交，内容不能再修改";
  if (status === 409 && body.error === "duplicate_file")
    return "该文件已有批次；请切换到已有批次，或明确创建新的发送批次";
  if (status === 400 && body.error === "cover_required")
    return "请先上传本批次的统一封面";
  return (
    body.message ||
    body.error ||
    (status === 503 ? "批次服务暂时不可用" : "请求失败，请检查输入后重试")
  );
}
async function api(
  path: string,
  method = "GET",
  body?: Obj | Blob,
  idempotencyKey = key("excel-batch"),
): Promise<Obj> {
  const headers: Record<string, string> = { Accept: "application/json" };
  let encoded: BodyInit | undefined;
  if (method !== "GET") {
    headers["X-CSRF-Token"] = csrf();
    headers["Idempotency-Key"] = idempotencyKey;
  }
  if (body instanceof Blob) {
    encoded = body;
    headers["Content-Type"] =
      body.type ||
      (body instanceof File && body.name.toLowerCase().endsWith(".xlsx")
        ? "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
        : "application/octet-stream");
  } else if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    encoded = JSON.stringify(body);
  }
  const response = await fetch(path, {
    method,
    headers,
    body: encoded,
    credentials: "same-origin",
  });
  const result = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(errorText(response.status, result));
  return result;
}
async function readAllPages(
  path: string,
  field: "rows" | "items",
): Promise<Obj> {
  let cursor = "";
  let first: Obj | null = null;
  const records: Obj[] = [];
  const seen = new Set<string>();
  do {
    const query = new URLSearchParams({ limit: "50" });
    if (cursor) query.set("cursor", cursor);
    const page = await api(`${path}${path.includes("?") ? "&" : "?"}${query}`);
    if (!first) first = page;
    records.push(...(Array.isArray(page[field]) ? page[field] : []));
    const next = typeof page.next_cursor === "string" ? page.next_cursor : "";
    if (!next) return { ...first, [field]: records, next_cursor: "" };
    if (seen.has(next))
      throw new Error("分页游标重复，已停止读取以避免混合批次数据");
    seen.add(next);
    cursor = next;
  } while (true);
}
function action(
  text: string,
  run: () => Promise<void> | void,
): HTMLButtonElement {
  const node = el("button", text);
  node.type = "button";
  node.onclick = async () => {
    node.disabled = true;
    try {
      await run();
    } catch (error) {
      const status = node
        .closest(".operation-excel-workspace")
        ?.querySelector<HTMLElement>("[role=status]");
      if (status) status.textContent = (error as Error).message;
    } finally {
      node.disabled = false;
    }
  };
  return node;
}
function table(
  headings: string[],
  rows: Array<Array<string | HTMLElement>>,
): HTMLTableElement {
  const node = el("table"),
    head = el("tr");
  headings.forEach((value) => head.append(el("th", value)));
  node.append(head);
  rows.forEach((values) => {
    const row = el("tr");
    values.forEach((value) => {
      const cell = el("td");
      typeof value === "string"
        ? cell.append(document.createTextNode(value))
        : cell.append(value);
      row.append(cell);
    });
    node.append(row);
  });
  return node;
}
function style(): void {
  if (document.getElementById("operation-excel-batch-style")) return;
  const node = el("style");
  node.id = "operation-excel-batch-style";
  node.textContent = `.operation-excel-workspace{margin:8px 0;color:#1f2329}.operation-excel-workspace *{box-sizing:border-box}.xeb-card{background:#fff;border:1px solid #dee0e3;border-radius:10px;overflow:hidden}.xeb-head{padding:14px 16px;border-bottom:1px solid #eff0f1}.xeb-head h2,.xeb-head h3{margin:0;font-size:16px}.xeb-plan{display:block;text-align:left;border:0;border-bottom:1px solid #f2f3f5;background:#fff;padding:12px 14px;width:100%;cursor:pointer}.xeb-plan:hover{background:#f5f8ff}.operation-excel-workspace small{font-size:12px;color:#8f959e}.xeb-body{padding:16px}.xeb-meta,.xeb-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.xeb-pagination{justify-content:space-between}.xeb-detail{display:grid;grid-template-columns:190px minmax(0,1fr);gap:16px;margin-top:16px}.xeb-detail-nav{padding:8px}.xeb-detail-nav button{display:block;width:100%;text-align:left;margin:2px 0}.xeb-detail-nav button[data-selected=true]{background:#eff4ff;color:#245bdb;border-color:#c9d8ff}.operation-excel-workspace button,.operation-excel-workspace input,.operation-excel-workspace select{font:inherit}.operation-excel-workspace button{border:1px solid #dee0e3;border-radius:6px;background:#fff;color:#344054;padding:6px 10px;cursor:pointer;font-size:12px}.operation-excel-workspace button.xeb-primary{background:#3370ff;border-color:#3370ff;color:#fff}.operation-excel-workspace button:disabled{opacity:.5}.xeb-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin:14px 0}.xeb-stat{padding:10px;background:#fafbfc;border:1px solid #eff0f1;border-radius:8px}.xeb-stat b{display:block;font-size:18px}.xeb-scroll{overflow:auto;margin-top:14px}.operation-excel-workspace table{border-collapse:collapse;width:100%;font-size:12px}.operation-excel-workspace th,.operation-excel-workspace td{padding:10px;border-bottom:1px solid #eff0f1;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere}.operation-excel-workspace th{font-weight:500;color:#8f959e;background:#fafafb}.xeb-cover{width:80px;height:60px;object-fit:cover;border-radius:4px}.xeb-status{min-height:22px;color:#935420}.operation-excel-workspace dialog{width:min(680px,92vw);border:1px solid #dee0e3;border-radius:10px;padding:20px}.operation-excel-workspace dialog label{display:block;margin:10px 0 4px;font-size:12px}.operation-excel-workspace textarea{width:100%;min-height:96px}@media screen and (max-width:800px){.xeb-detail{grid-template-columns:1fr}.xeb-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.xeb-head,.xeb-body{padding:12px}.xeb-pagination{align-items:flex-start;flex-direction:column}}`;
  document.head.append(node);
  const pickerStyle = el("style");
  pickerStyle.textContent = `.xeb-cover-picker{display:grid;gap:12px}.xeb-cover-picker-list{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;max-height:420px;overflow:auto}.xeb-cover-picker-item{display:grid;gap:6px;padding:8px;border:1px solid #dee0e3;border-radius:8px;background:#fff;text-align:left}.xeb-cover-picker-item img{width:100%;height:96px;object-fit:cover;background:#f5f6f7;border-radius:5px}.xeb-cover-picker-item small{overflow-wrap:anywhere}@media screen and (max-width:800px){.xeb-cover-picker-list{grid-template-columns:repeat(2,minmax(0,1fr))}}`;
  document.head.append(pickerStyle);
}
function batchState(batch: Obj): string {
  return states[String(batch.state)] || String(batch.state || "未知");
}
function rowState(row: Obj): string {
  if (row.excluded) return "已排除";
  if (row.review_state !== "approved") return "未批准";
  return delivery[String(row.delivery_state || row.state)] || "待提交";
}
function rate(value: unknown): string {
  return typeof value === "number"
    ? `${(value * 100).toFixed(1)}%`
    : "暂不可统计";
}
function card(row: Obj): HTMLElement {
  const wrap = el("div"),
    value = row.card || {};
  if (value.cover_digest) {
    const image = el("img") as HTMLImageElement;
    image.className = "xeb-cover";
    image.src = `${base}/covers/${encodeURIComponent(String(value.cover_digest))}`;
    image.alt = "统一封面";
    wrap.append(image);
  } else wrap.append(el("small", "未上传统一封面"));
  wrap.append(
    el("div", String(value.title || "标题为空：执行时明确失败")),
    el("small", String(value.path || "")),
  );
  return wrap;
}

class Workspace {
  private root: HTMLElement;
  private plans: Obj[] = [];
  private planTotal = 0;
  private planLimit = 20;
  private planOffset = 0;
  private planHasMore = false;
  private planNextOffset: number | null = null;
  private plansLoading = false;
  private plansError = "";
  private legacy: Obj[] = [];
  private legacyError = "";
  private batches: Obj[] = [];
  private strategyKey = "";
  private batchID = 0;
  private tab = "content";
  private generation = 0;
  private batchWritePending = false;
  constructor(parent: HTMLElement) {
    style();
    this.root = el("section");
    this.root.className = "operation-excel-workspace";
    parent.replaceChildren(this.root);
  }
  async mount(): Promise<void> {
    await this.loadPlans(0, true);
  }
  private right(): HTMLElement {
    return this.root.querySelector<HTMLElement>(".xeb-detail-main")!;
  }
  private tell(value: string): void {
    const node = this.root.querySelector<HTMLElement>("[role=status]");
    if (node) node.textContent = value;
  }
  private batchAction(
    node: HTMLButtonElement,
    approve = false,
    coverReady = false,
  ): HTMLButtonElement {
    node.dataset.batchMutation = "true";
    if (approve) {
      node.dataset.batchApprove = "true";
      node.dataset.coverReady = String(coverReady);
    }
    node.disabled = this.batchWritePending || (approve && !coverReady);
    return node;
  }
  private syncBatchMutationControls(): void {
    this.root
      .querySelectorAll<HTMLButtonElement>("button[data-batch-mutation]")
      .forEach((node) => {
        const needsCover = node.dataset.batchApprove === "true";
        node.disabled =
          this.batchWritePending ||
          (needsCover && node.dataset.coverReady !== "true");
      });
  }
  private async withBatchWrite<T>(run: () => Promise<T>): Promise<T> {
    if (this.batchWritePending)
      throw new Error("本批次正在保存，请等待当前操作完成");
    this.batchWritePending = true;
    this.syncBatchMutationControls();
    try {
      return await run();
    } finally {
      this.batchWritePending = false;
      this.syncBatchMutationControls();
    }
  }
  private async loadPlans(offset = this.planOffset, openHash = false): Promise<void> {
    if (this.plansLoading) return;
    this.plansLoading = true;
    this.plansError = "";
    this.renderShell();
    const [summaryResult, legacyResult] = await Promise.all([
      api(`${base}/strategy-summaries?limit=20&offset=${Math.max(0, offset)}`)
        .then((value) => ({ value, error: "" }))
        .catch((error) => ({ value: {} as Obj, error: (error as Error).message })),
      api(`${base}/legacy`)
        .then((value) => ({ value, error: "" }))
        .catch((error) => ({
          value: { items: [] },
          error: (error as Error).message,
        })),
    ]);
    this.plansLoading = false;
    this.legacy = Array.isArray(legacyResult.value.items)
      ? legacyResult.value.items
      : [];
    this.legacyError = legacyResult.error;
    if (summaryResult.error) {
      this.plans = [];
      this.plansError = `长期计划读取失败：${summaryResult.error}`;
      this.renderShell();
      return;
    }
    const result = summaryResult.value;
    const items = Array.isArray(result.items) ? result.items : [];
    const total = Number(result.total);
    const limit = Number(result.limit);
    const returnedOffset = Number(result.offset);
    const next = result.next_offset === null ? null : Number(result.next_offset);
    if (!Number.isSafeInteger(total) || total < 0 || !Number.isSafeInteger(limit) || limit !== 20 || !Number.isSafeInteger(returnedOffset) || returnedOffset < 0 || returnedOffset !== Math.max(0, offset) || items.length > limit || (next !== null && (!Number.isSafeInteger(next) || next <= returnedOffset))) {
      this.plans = [];
      this.plansError = "长期计划分页响应无效，请刷新后重试";
      this.renderShell();
      return;
    }
    if (!items.length && returnedOffset > 0 && total > 0) {
      await this.loadPlans(Math.floor((total - 1) / limit) * limit, openHash);
      return;
    }
    this.plans = items;
    this.planTotal = total;
    this.planLimit = limit;
    this.planOffset = returnedOffset;
    this.planHasMore = result.has_more === true;
    this.planNextOffset = this.planHasMore ? next : null;
    if (this.planHasMore !== (returnedOffset + items.length < total) || (this.planHasMore && this.planNextOffset !== returnedOffset + items.length)) {
      this.plans = [];
      this.plansError = "长期计划分页游标不一致，请刷新后重试";
      this.renderShell();
      return;
    }
    this.renderShell();
    const strategy = openHash
      ? new URLSearchParams(location.hash.slice(1)).get("strategy")
      : "";
    if (strategy)
      await this.openStrategy(strategy, false);
  }
  private renderShell(): void {
    this.root.replaceChildren();
    const left = el("section");
    left.className = "xeb-card";
    const head = el("div", "长期计划");
    head.className = "xeb-head";
    left.append(head);
    if (this.plansLoading) {
      left.append(el("p", "正在读取长期计划…"));
      this.root.append(left);
      return;
    }
    if (this.plansError) {
      const error = el("p", this.plansError);
      error.setAttribute("role", "alert");
      left.append(error, action("重新读取", () => this.loadPlans(this.planOffset)));
      this.root.append(left);
      return;
    }
    if (!this.plans.length) left.append(el("p", "暂无可访问的长期计划。"));
    const rows = this.plans.map((plan) => {
      const latest = plan.latest_batch || {};
      const progress = latest.id
        ? `批次 #${latest.id} · ${batchState(latest)} · 预计任务 ${latest.summary?.expected_tasks ?? 0}`
        : plan.latest_batch_status === "unavailable"
          ? "批次摘要暂不可用"
          : "暂无批次";
      return [
        String(plan.title || plan.strategy_key),
        progress,
        action("查看详情", () => this.openStrategy(String(plan.strategy_key))),
      ];
    });
    left.append(table(["任务名称", "当前进度", "操作"], rows));
    const pager = el("div");
    pager.className = "xeb-actions xeb-pagination";
    const start = this.planTotal ? this.planOffset + 1 : 0;
    const end = Math.min(this.planOffset + this.plans.length, this.planTotal);
    const previous = action("上一页", () => this.loadPlans(Math.max(0, this.planOffset - this.planLimit)));
    previous.disabled = this.planOffset <= 0;
    const next = action("下一页", () => this.loadPlans(this.planNextOffset ?? this.planOffset));
    next.disabled = !this.planHasMore || this.planNextOffset === null;
    pager.append(el("small", `第 ${start}–${end} 项，共 ${this.planTotal} 项`), previous, next);
    left.append(pager);
    if (this.legacy.length) {
      const legacy = el("div");
      legacy.className = "xeb-head";
      legacy.append(
        el("h3", "未关联的旧 Excel 审核记录"),
        el("small", "仅供发现；没有自动关联或迁移入口。"),
      );
      this.legacy.forEach((item) =>
        legacy.append(
          el(
            "small",
            `${item.name || `计划 #${item.id}`} · ${batchState(item)}`,
          ),
        ),
      );
      left.append(legacy);
    }
    if (this.legacyError)
      left.append(el("p", `旧 Excel 审核记录暂不可读取：${this.legacyError}`));
    this.root.append(left);
  }
  private renderDetailShell(): void {
    this.root.replaceChildren();
    const header = el("section");
    header.className = "xeb-card";
    const head = el("div");
    head.className = "xeb-head xeb-meta";
    head.append(
      action("返回计划列表", () => {
        this.generation++;
        this.strategyKey = "";
        this.batchID = 0;
        location.hash = "";
        this.renderShell();
      }),
      el("h2", "运营闭环批次详情"),
    );
    header.append(head);
    const layout = el("div");
    layout.className = "xeb-detail";
    const nav = el("aside");
    nav.className = "xeb-card xeb-detail-nav";
    [
      ["content", "内容准备与发送"],
      ["effects", "发送效果与复盘"],
    ].forEach(([id, label]) => {
      const node = action(label, async () => {
        this.tab = id;
        this.updateTabNav();
        if (this.batchID) await this.loadSelected();
      });
      node.dataset.tab = id;
      node.dataset.selected = String(this.tab === id);
      nav.append(node);
    });
    const main = el("section");
    main.className = "xeb-card xeb-detail-main";
    main.append(el("p", "正在读取批次详情…"));
    layout.append(nav, main);
    this.root.append(header, layout);
  }
  private updateTabNav(): void {
    this.root
      .querySelectorAll<HTMLElement>(".xeb-detail-nav button[data-tab]")
      .forEach((node) => {
        node.dataset.selected = String(node.dataset.tab === this.tab);
      });
  }
  private async openStrategy(
    next: string,
    updateLocation = true,
    preferredBatchID = 0,
  ): Promise<void> {
    const currentGeneration = ++this.generation;
    this.strategyKey = next;
    this.batchID = preferredBatchID;
    if (updateLocation)
      location.hash = new URLSearchParams({ strategy: next }).toString();
    this.renderDetailShell();
    try {
      const result = await api(
        `${base}/strategies/${encodeURIComponent(next)}`,
      );
      if (currentGeneration !== this.generation || next !== this.strategyKey)
        return;
      this.batches = Array.isArray(result.items) ? result.items : [];
      this.batchID = Number(
        this.batches.find((batch) => Number(batch.id) === preferredBatchID)
          ?.id ||
          this.batches[0]?.id ||
          0,
      );
      const listedStrategy =
        this.plans.find(
          (value) => String(value.strategy_key) === this.strategyKey,
        ) || {};
      await this.renderDetail(
        { ...listedStrategy, ...(result.strategy || {}) },
        this.batches,
      );
    } catch (error) {
      if (currentGeneration === this.generation)
        this.right().replaceChildren(el("p", (error as Error).message));
    }
  }
  private async loadSelected(): Promise<void> {
    const id = this.batchID,
      requestGeneration = ++this.generation;
    const result = await readAllPages(`${base}/${id}`, "rows");
    if (requestGeneration !== this.generation || id !== this.batchID) return;
    const current = result.batch || result.plan;
    this.batches = this.batches.map((item) =>
      Number(item.id) === id ? { ...item, ...current } : item,
    );
    const strategy =
      this.plans.find(
        (value) => String(value.strategy_key) === this.strategyKey,
      ) || {};
    await this.renderDetail(strategy, this.batches, result);
  }
  private async renderDetail(
    strategy: Obj,
    batches: Obj[],
    detail?: Obj,
  ): Promise<void> {
    const selectedID = this.batchID;
    const renderGeneration = this.generation;
    const right = this.right();
    right.replaceChildren();
    const head = el("div");
    head.className = "xeb-head";
    const actions = el("div");
    actions.className = "xeb-actions";
    actions.append(action("新建发送批次", () => this.importDialog()));
    if (batches.length) {
      const select = el("select") as HTMLSelectElement;
      select.setAttribute("aria-label", "历史批次");
      batches.forEach((batch) => {
        const option = el(
          "option",
          `批次 #${batch.id} · ${batchState(batch)}`,
        ) as HTMLOptionElement;
        option.value = String(batch.id);
        option.selected = Number(batch.id) === this.batchID;
        select.append(option);
      });
      select.onchange = async () => {
        this.batchID = Number(select.value);
        await this.loadSelected();
      };
      actions.append(el("label", "历史批次："), select);
    }
    head.append(el("h2", String(strategy.title || this.strategyKey)), actions);
    right.append(head);
    if (!selectedID) {
      right.append(
        el(
          "div",
          "此长期计划还没有 Excel 批次。文件在本地选择期间不会创建任何计划或批次。",
        ),
      );
      return;
    }
    const payload =
      detail || (await readAllPages(`${base}/${selectedID}`, "rows"));
    if (
      renderGeneration !== this.generation ||
      selectedID !== this.batchID
    )
      return;
    const batch =
      payload.batch ||
      payload.plan ||
      batches.find((value) => Number(value.id) === selectedID);
    if (!batch) throw new Error("批次读取响应缺少当前批次");
    const body = el("div");
    body.className = "xeb-body";
    body.append(this.batchSummary(batch));
    const status = el("p");
    status.className = "xeb-status";
    status.setAttribute("role", "status");
    body.append(status);
    if (this.tab === "content")
      this.content(
        body,
        batch,
        Array.isArray(payload.rows) ? payload.rows : [],
      );
    else await this.effects(body, batch);
    if (
      renderGeneration !== this.generation ||
      selectedID !== this.batchID
    )
      return;
    right.append(body);
  }
  private batchSummary(batch: Obj): HTMLElement {
    const summary = batch.summary || {};
    const wrap = el("div");
    const meta = el("div");
    meta.className = "xeb-meta";
    meta.append(
      el("small", `当前批次 #${batch.id}`),
      el("small", `状态：${batchState(batch)}`),
      el("small", `内容版本：${batch.current_content_version || 1}`),
    );
    const grid = el("div");
    grid.className = "xeb-grid";
    [
      ["总行数", summary.total_rows],
      ["已排除", summary.excluded_rows],
      ["空标题", summary.empty_title_rows],
      ["预计任务", summary.expected_tasks],
    ].forEach(([label, value]) => {
      const stat = el("div");
      stat.className = "xeb-stat";
      stat.append(el("small", String(label)), el("b", String(value ?? 0)));
      grid.append(stat);
    });
    const coverImageID = Number(batch.cover_image_id || 0);
    const cover = el(
      "p",
      coverImageID > 0
        ? `冻结封面：素材 #${coverImageID}；内容摘要已冻结，凭据刷新不会触发重新审核。`
        : batch.cover_digest
          ? "冻结封面：已上传内容；内容摘要已冻结。"
          : "统一封面：未设置。",
    );
    cover.className = "xeb-status";
    wrap.append(meta, grid, cover);
    return wrap;
  }
  private editable(batch: Obj): boolean {
    return ["pending_review", "partially_approved"].includes(
      String(batch.state),
    );
  }
  private content(parent: HTMLElement, batch: Obj, rows: Obj[]): void {
    const id = Number(batch.id),
      editable = this.editable(batch),
      actions = el("div");
    actions.className = "xeb-actions";
    if (editable) {
      actions.append(
        this.batchAction(action("替换 Excel", () => this.importDialog(batch))),
      );
      const cover = el("input") as HTMLInputElement;
      cover.type = "file";
      cover.accept = "image/png,image/jpeg";
      cover.setAttribute("aria-label", "统一封面图片");
      const approve = this.batchAction(
        action("审核通过并创建企微群发任务", async () => {
          await this.withBatchWrite(async () => {
            const preview = await api(
              `${base}/${id}/preview-approval`,
              "POST",
              { expected_version: batch.version },
              previewKey,
            );
            const digest = String(preview.preview_digest || "");
            if (!digest) throw new Error("审核预览无效，请刷新后重试");
            await api(
              `${base}/${id}/approve`,
              "POST",
              { expected_version: batch.version, preview_digest: digest },
              approveKey,
            );
            await this.loadSelected();
            this.tell(
              "企微任务意图已创建，员工仍需在企微端执行；这不等于发送成功。",
            );
          });
        }),
        true,
        Boolean(batch.cover_digest || Number(batch.cover_image_id || 0) > 0),
      );
      const previewKey = key(`excel-preview-${id}-${batch.version}`);
      const approveKey = key(`excel-approve-${id}-${batch.version}`);
      const coverKey = key(`excel-cover-${id}-${batch.version}`);
      const selectCover = this.batchAction(
        action("选择已有启用图片", () => this.coverPickerDialog(batch)),
      );
      actions.append(
        cover,
        this.batchAction(
          action("上传统一封面", async () => {
            await this.withBatchWrite(async () => {
              const file = cover.files?.[0];
              if (!file) throw new Error("请选择 PNG 或 JPEG 封面");
              await api(
                `${base}/${id}/cover?expected_version=${batch.version}`,
                "POST",
                file,
                coverKey,
              );
              await this.loadSelected();
              this.tell("统一封面已更新；请重新核对预览。");
            });
          }),
        ),
        selectCover,
        approve,
      );
    }
    actions.append(action("查看旧版本", () => this.versionDialog(id)));
    parent.append(actions);
    if (!(batch.cover_digest || Number(batch.cover_image_id || 0) > 0) && editable)
      parent.append(el("p", "没有统一封面，不能审核通过并创建企微群发任务。"));
    const scroll = el("div");
    scroll.className = "xeb-scroll";
    scroll.append(
      table(
        ["UnionID / 员工", "话术", "小程序卡片", "分层", "状态", "审核操作"],
        rows.map((row) => {
          const controls = el("div");
          if (editable) {
            controls.append(
              this.batchAction(action("修改", () => this.rowDialog(batch, row))),
              this.batchAction(
                action(row.excluded ? "恢复" : "排除", async () => {
                  await this.withBatchWrite(async () => {
                    await api(
                      `${base}/${id}/rows/${row.id}`,
                      "PATCH",
                      {
                        expected_version: row.version,
                        text: row.text,
                        path: row.card?.path || "",
                        title: row.card?.title || "",
                        segment: row.segment || "",
                        excluded: !row.excluded,
                      },
                      key(`excel-row-${id}-${row.id}`),
                    );
                    await this.loadSelected();
                  });
                }),
              ),
            );
          }
          return [
            `${row.unionid}\n${row.sender_userid}`,
            String(row.text || ""),
            card(row),
            String(row.segment || "未分层"),
            `${rowState(row)}${row.failure_reason || row.reason ? `\n${row.failure_reason || row.reason}` : ""}${row.sent_at ? `\n${row.sent_at}` : ""}`,
            controls,
          ];
        }),
      ),
    );
    parent.append(scroll);
  }
  private coverPickerDialog(batch: Obj): void {
    if (!this.editable(batch) || Number(batch.id) !== this.batchID) return;
    const boundBatch = Number(batch.id);
    const boundGeneration = this.generation;
    const dialog = el("dialog") as HTMLDialogElement;
    dialog.className = "xeb-cover-picker";
    dialog.setAttribute("aria-label", "选择已有启用图片");
    const heading = el("h3", "选择已有启用图片");
    const hint = el("p", "仅显示当前启用的图片。选择后会冻结图片编号和内容摘要，凭据刷新不会改变已提交批次。");
    const query = el("input") as HTMLInputElement;
    query.type = "search";
    query.setAttribute("aria-label", "搜索启用图片");
    query.placeholder = "搜索图片名称";
    const search = action("查询", () => { void load(0); });
    const searchRow = el("div");
    searchRow.className = "xeb-actions";
    searchRow.append(query, search);
    const status = el("p");
    status.className = "xeb-status";
    status.setAttribute("role", "status");
    const list = el("div");
    list.className = "xeb-cover-picker-list";
    const pager = el("div");
    pager.className = "xeb-actions";
    const range = el("small");
    const previous = action("上一页", () => { void load(Math.max(0, offset - pageSize)); });
    const next = action("下一页", () => { void load(nextOffset); });
    pager.append(range, previous, next);
    const close = action("取消", () => {
      closed = true;
      generation += 1;
      dialog.close();
      dialog.remove();
    });
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      close.click();
    });
    dialog.append(heading, hint, searchRow, status, list, pager, close);
    this.root.append(dialog);

    const pageSize = 12;
    let offset = 0;
    let nextOffset = pageSize;
    let hasMore = false;
    let generation = 0;
    let closed = false;
    let loading = false;
    const selectionKeys = new Map<number, string>();
    const imageID = (value: Obj): number => Number(value.id ?? value.resource_id ?? value.material_id ?? 0);
    const imageName = (value: Obj, id: number): string => String(value.name || value.file_name || value.filename || `图片素材 #${id}`);
    const imageThumb = (value: Obj, id: number): string => String(value.thumb_160_url || value.thumb_320_url || value.thumb_url || value.variant_url || (id > 0 ? `/api/admin/image-library/${id}/variants/thumb_160` : ""));
    const setLoading = (busy: boolean): void => {
      loading = busy;
      search.disabled = busy;
      previous.disabled = busy || offset <= 0;
      next.disabled = busy || !hasMore;
      list.querySelectorAll<HTMLButtonElement>("button[data-cover-image-id]").forEach((button) => { button.disabled = busy; });
    };
    const choose = async (item: Obj, button: HTMLButtonElement): Promise<void> => {
      if (closed || loading || boundBatch !== this.batchID || boundGeneration !== this.generation) {
        status.textContent = "批次已切换，未保存封面选择。";
        status.setAttribute("role", "alert");
        return;
      }
      const id = imageID(item);
      if (!Number.isSafeInteger(id) || id < 1 || item.enabled === false) {
        status.textContent = "该图片已停用或编号无效，请重新选择启用图片。";
        status.setAttribute("role", "alert");
        return;
      }
      const selectionKey = selectionKeys.get(id) || key(`excel-cover-select-${boundBatch}-${batch.version}-${id}`);
      selectionKeys.set(id, selectionKey);
      setLoading(true);
      button.textContent = "保存中…";
      try {
        await api(
          `${base}/${boundBatch}/cover?expected_version=${batch.version}`,
          "POST",
          { cover_image_id: id },
          selectionKey,
        );
        selectionKeys.delete(id);
        closed = true;
        dialog.close();
        dialog.remove();
        await this.loadSelected();
        this.tell("已选择启用图片作为统一封面；请重新核对预览。");
      } catch (error) {
        button.disabled = false;
        button.textContent = "选择";
        setLoading(false);
        status.textContent = `${(error as Error).message}；可重试，仍使用同一操作 key。`;
        status.setAttribute("role", "alert");
      }
    };
    const draw = (items: Obj[]): void => {
      list.replaceChildren();
      items.filter((item) => item.enabled !== false).forEach((item) => {
        const id = imageID(item);
        if (!Number.isSafeInteger(id) || id < 1) return;
        const card = el("div");
        card.className = "xeb-cover-picker-item";
        const thumb = imageThumb(item, id);
        if (thumb) {
          const image = el("img") as HTMLImageElement;
          image.src = thumb;
          image.alt = imageName(item, id);
          card.append(image);
        }
        card.append(el("small", `${imageName(item, id)} · 素材 #${id}`));
        const chooseButton = action("选择", () => { void choose(item, chooseButton); });
        chooseButton.dataset.coverImageId = String(id);
        card.append(chooseButton);
        list.append(card);
      });
      if (!list.children.length) list.append(el("small", "当前页没有可选择的启用图片。"));
    };
    async function load(nextPageOffset: number): Promise<void> {
      if (loading || closed) return;
      const requestGeneration = ++generation;
      offset = Math.max(0, nextPageOffset);
      nextOffset = offset + pageSize;
      hasMore = false;
      setLoading(true);
      status.textContent = "正在读取启用图片…";
      status.setAttribute("role", "status");
      try {
        const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset), enabled_only: "true" });
        if (query.value.trim()) params.set("q", query.value.trim());
        const result = await api(`/api/admin/image-library?${params}`);
        if (closed || requestGeneration !== generation) return;
        const items = Array.isArray(result.items) ? result.items : Array.isArray(result.images) ? result.images : [];
        const providedNext = Number(result.next_offset);
        nextOffset = Number.isSafeInteger(providedNext) && providedNext > offset ? providedNext : offset + pageSize;
        hasMore = result.has_more === true || (result.has_more === undefined && items.length === pageSize);
        draw(items.map((value) => value as Obj));
        const total = Number(result.total);
        range.textContent = Number.isFinite(total) && total > 0
          ? `第 ${offset + 1}–${Math.min(offset + items.length, total)} 项，共 ${total} 项`
          : `第 ${offset + 1}–${offset + items.length} 项`;
        status.textContent = "请选择一张启用图片；取消不会修改批次。";
      } catch (error) {
        if (closed || requestGeneration !== generation) return;
        list.replaceChildren();
        range.textContent = "";
        status.textContent = (error as Error).message;
        status.setAttribute("role", "alert");
      } finally {
        if (!closed && requestGeneration === generation) setLoading(false);
      }
    }
    dialog.showModal();
    void load(0);
  }
  private async effects(parent: HTMLElement, batch: Obj): Promise<void> {
    const id = Number(batch.id);
    let receipts: Obj = {};
    let receiptsError = "";
    let report: Obj | null = null;
    let reportError = "";
    try {
      receipts = await readAllPages(`${base}/${id}/receipts`, "items");
    } catch (error) {
      receiptsError = (error as Error).message;
    }
    try {
      report = await api(`${base}/${id}/report`);
    } catch (error) {
      reportError = (error as Error).message;
    }
    parent.append(
      el(
        "p",
        `报告按每人实际成功发送时间计算；结果未知先进入对账，不会换 key 重发。${report?.updated_at ? ` 最近采集：${report.updated_at}` : ""}`,
      ),
    );
    if (report) {
      const source =
        report.segment_source === "excel"
          ? "Excel"
          : report.segment_source === "legacy_snapshot"
            ? "旧审核快照"
            : "暂不可识别";
      parent.append(
        el(
          "p",
          report.has_segments
            ? `分层来源：${source}；已按分层统计。`
            : `分层来源：${source}；分层数据暂不可用，只显示总体。`,
        ),
      );
      const hours = el("select") as HTMLSelectElement;
      hours.setAttribute("aria-label", "观察窗口");
      [12, 24, 48].forEach((value) => {
        const option = el("option", `${value} 小时累计`) as HTMLOptionElement;
        option.value = String(value);
        hours.append(option);
      });
      const grid = el("div");
      const draw = () => {
        const window = report.windows?.[hours.value] || {};
        const groups: Array<[string, Obj]> = [
          ["总体", window.overall || report.overall?.[hours.value] || {}],
          ...(Object.entries(window.groups || {}) as Array<[string, Obj]>),
        ];
        grid.replaceChildren(
          table(
            [
              "分组",
              "成功发送",
              "已满窗口",
              "观察中",
              "打开人数",
              "数据缺失",
              "打开率",
            ],
            groups.map(([label, stats]) => {
              return [
                label,
                String(stats.sent ?? 0),
                String(stats.matured ?? 0),
                String(stats.observing ?? 0),
                String(stats.opened ?? 0),
                String(stats.unavailable ?? 0),
                rate(stats.open_rate),
              ];
            }),
          ),
        );
      };
      hours.onchange = draw;
      draw();
      parent.append(hours, grid);
      const download = el("a", "下载逐人报告");
      download.href = `${base}/${id}/report.csv`;
      download.download = `excel-batch-${id}-report.csv`;
      parent.append(download);
    } else parent.append(el("p", `效果报告暂不可统计：${reportError}`));
    const items = Array.isArray(receipts.items)
      ? receipts.items
      : Array.isArray(receipts.rows)
        ? receipts.rows
        : [];
    const scroll = el("div");
    scroll.className = "xeb-scroll";
    scroll.append(
      el("h3", "逐人回执"),
      table(
        ["接收人", "发送员工", "状态", "实际发送时间", "原因"],
        items.map((item: Obj) => [
          String(item.unionid || item.recipient || ""),
          String(item.sender_userid || ""),
          delivery[String(item.delivery_state || item.state)] ||
            String(item.delivery_state || item.state || "结果待核实"),
          String(item.sent_at || "—"),
          String(item.failure_reason || item.reason || "—"),
        ]),
      ),
    );
    parent.append(scroll);
    if (receiptsError)
      parent.append(el("p", `逐人回执暂不可读取：${receiptsError}`));
  }
  private rowDialog(batch: Obj, row: Obj): void {
    const boundBatch = Number(batch.id),
      boundGeneration = this.generation,
      dialog = el("dialog") as HTMLDialogElement,
      text = el("textarea") as HTMLTextAreaElement,
      path = el("input") as HTMLInputElement,
      title = el("input") as HTMLInputElement,
      segment = el("select") as HTMLSelectElement;
    text.value = String(row.text || "");
    path.value = String(row.card?.path || "");
    title.value = String(row.card?.title || "");
    ["", "A", "B", "C", "D"].forEach((value) => {
      const option = el("option", value || "未分层") as HTMLOptionElement;
      option.value = value;
      option.selected = value === String(row.segment || "");
      segment.append(option);
    });
    dialog.append(el("h3", "修改发送内容"));
    [
      ["话术", text],
      ["小程序 path", path],
      ["标题（可空，执行时明确失败）", title],
      ["分层", segment],
    ].forEach(([label, control]) =>
      dialog.append(el("label", String(label)), control as HTMLElement),
    );
    dialog.append(
      action("保存并重新审核", async () => {
        if (boundBatch !== this.batchID || boundGeneration !== this.generation)
          throw new Error("批次已切换，未将编辑写入其他批次");
        await this.withBatchWrite(async () => {
          await api(
            `${base}/${boundBatch}/rows/${row.id}`,
            "PATCH",
            {
              expected_version: row.version,
              text: text.value,
              path: path.value.trim(),
              title: title.value.trim(),
              segment: segment.value,
              excluded: Boolean(row.excluded),
            },
            key(`excel-row-${boundBatch}-${row.id}`),
          );
          dialog.close();
          dialog.remove();
          await this.loadSelected();
        });
      }),
      action("取消", () => {
        dialog.close();
        dialog.remove();
      }),
    );
    this.root.append(dialog);
    dialog.showModal();
  }
  private importDialog(replacing?: Obj): void {
    const dialog = el("dialog") as HTMLDialogElement,
      file = el("input") as HTMLInputElement,
      fresh = el("input") as HTMLInputElement;
    file.type = "file";
    file.accept = ".xlsx";
    fresh.type = "checkbox";
    const strategyKey = this.strategyKey;
    const replacedID = Number(replacing?.id || 0);
    let uploadKey = key(
      replacing ? `excel-replace-${replacedID}` : `excel-import-${strategyKey}`,
    );
    file.onchange = () => {
      uploadKey = key(
        replacing
          ? `excel-replace-${replacedID}`
          : `excel-import-${strategyKey}`,
      );
    };
    fresh.onchange = () => {
      uploadKey = key(`excel-import-${strategyKey}`);
    };
    dialog.append(
      el("h3", replacing ? "替换 Excel 批次" : "新建发送批次"),
      el(
        "p",
        replacing
          ? "替换保留批次编号与已上传封面，旧内容版本只读保留，并重新审核。"
          : "文件在本地选择期间不会创建任何计划或批次。",
      ),
      file,
    );
    if (!replacing)
      dialog.append(el("label", "同一文件也明确创建新的发送批次"), fresh);
    dialog.append(
      action(replacing ? "替换并重新审核" : "上传并开始审核", async () => {
        const source = file.files?.[0];
        if (!source) throw new Error("请选择 Excel 文件");
        const target = replacing
          ? `${base}/${replacing.id}/import?expected_version=${replacing.version}`
          : `${base}/strategies/${encodeURIComponent(strategyKey)}/imports${fresh.checked ? "?new=1" : ""}`;
        const result = await api(
          target,
          replacing ? "PUT" : "POST",
          source,
          uploadKey,
        );
        const selectedID = Number(
          (result.batch || result.plan || {}).id || replacedID,
        );
        dialog.close();
        dialog.remove();
        await this.openStrategy(strategyKey, false, selectedID);
      }),
      action("取消", () => {
        dialog.close();
        dialog.remove();
      }),
    );
    this.root.append(dialog);
    dialog.showModal();
  }
  private async versionDialog(id: number): Promise<void> {
    const dialog = el("dialog") as HTMLDialogElement;
    dialog.append(el("h3", "历史上传内容版本"));
    const result = await api(`${base}/${id}/versions`);
    const items = Array.isArray(result.items) ? result.items : [];
    dialog.append(
      table(
        ["版本", "封面素材", "创建时间", "操作"],
        items.map((item: Obj) => [
          String(item.content_version || item.version),
          Number(item.cover_image_id || 0) > 0
            ? `素材 #${item.cover_image_id}`
            : item.cover_digest
              ? "已上传内容"
              : "—",
          String(item.created_at || ""),
          action("只读查看", async () => {
            const detail = await readAllPages(
              `${base}/${id}/versions/${item.content_version || item.version}`,
              "rows",
            );
            const view = el("pre", JSON.stringify(detail.rows || [], null, 2));
            view.style.whiteSpace = "pre-wrap";
            dialog.append(view);
          }),
        ]),
      ),
    );
    dialog.append(
      action("关闭", () => {
        dialog.close();
        dialog.remove();
      }),
    );
    this.root.append(dialog);
    dialog.showModal();
  }
}

export async function mountOperationExcelWorkspace(
  parent: HTMLElement,
): Promise<void> {
  const workspace = new Workspace(parent);
  await workspace.mount();
}
