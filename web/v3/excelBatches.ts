// Excel batches use the existing AI review records and approval permissions.
// The component only contributes import/preparation and observation reports.
type Obj = Record<string, any>;
const base = "/api/admin/operation-batches";
const labels: Record<string, string> = {
  not_accepted: "未批准",
  queued: "待提交",
  accepted: "待提交",
  attempted: "提交中",
  provider_accepted: "已创建任务，待员工执行",
  delivery_proven: "发送成功",
  final_failed: "明确失败",
  outcome_unknown: "结果待核实",
  retryable_failed: "等待重试",
  reconciled: "待核实发送结果",
};
function failure(value: string): string {
  const messages: Record<string, string> = {
    title_missing: "标题为空，发送失败；请在审核时填写标题",
    cover_missing: "未上传统一封面，禁止发送",
    unionid_not_unique: "无法唯一匹配接收用户",
    unionid_unverified: "接收用户身份尚未核实",
    wecom_identity_unavailable: "未找到对应企微客户",
    target_unavailable: "接收用户信息不可用",
    payload_unavailable: "发送内容或封面不可用",
    outcome_unknown: "发送结果暂时无法确认",
    provider_rejected: "企微拒绝发送请求",
  };
  if (messages[value]) return messages[value];
  if (value.startsWith("wecom_errcode_"))
    return `企微拒绝请求（错误码 ${value.slice(14)}）`;
  if (value.startsWith("wecom_status_"))
    return `企微发送失败（状态 ${value.slice(13)}）`;
  return value;
}
function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  return node;
}
function csrf(): string {
  for (const c of document.cookie.split(";")) {
    const [k, ...v] = c.trim().split("=");
    if (k === "aicrm_csrf" || k === "aicrm_admin_csrf")
      return decodeURIComponent(v.join("="));
  }
  return "";
}
async function call(
  path: string,
  method = "GET",
  body?: any,
  key: string = crypto.randomUUID(),
): Promise<Obj> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method !== "GET") {
    headers["X-CSRF-Token"] = csrf();
    headers["Idempotency-Key"] = key;
  }
  if (body !== undefined && !(body instanceof File)) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(body);
  }
  const response = await fetch(path, {
    method,
    headers,
    body,
    credentials: "same-origin",
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(
      response.status === 403
        ? "没有此操作权限"
        : response.status === 409
          ? "内容已变化，请刷新后重新审核"
          : response.status === 503
            ? "批次服务未启用或暂时不可用"
            : payload.message || "请检查五列表头、文本格式和是否有重复用户",
    );
  }
  return payload;
}
function button(
  text: string,
  action: () => Promise<void> | void,
): HTMLButtonElement {
  const b = el("button", text);
  b.type = "button";
  b.onclick = async () => {
    b.disabled = true;
    try {
      await action();
    } catch (error) {
      b.closest(".excel-batches")
        ?.querySelector("[role=status]")
        ?.replaceChildren(
          document.createTextNode(String((error as Error).message)),
        );
    } finally {
      b.disabled = false;
    }
  };
  return b;
}
function table(
  headers: string[],
  rows: (string | HTMLElement)[][],
): HTMLTableElement {
  const t = el("table"),
    head = el("tr");
  for (const h of headers) head.append(el("th", h));
  t.append(head);
  for (const row of rows) {
    const tr = el("tr");
    for (const v of row) {
      const td = el("td");
      typeof v === "string" ? (td.textContent = v) : td.append(v);
      tr.append(td);
    }
    t.append(tr);
  }
  return t;
}
function style() {
  if (document.getElementById("excel-batch-style")) return;
  const s = el("style");
  s.id = "excel-batch-style";
  s.textContent = `.excel-batches{margin:20px 0;padding:20px;border:1px solid #ddd;border-radius:12px;background:#fff;color:#20252c}.excel-batches h2{font-size:20px}.excel-batches table{border-collapse:collapse;width:100%;margin:16px 0}.excel-batches td,.excel-batches th{padding:10px;border-bottom:1px solid #e5e7eb;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere}.excel-batches button,.excel-batches input,.excel-batches select{margin:4px;padding:8px}.excel-batches textarea{width:100%;min-height:120px}.excel-batches img{width:96px;height:72px;object-fit:cover}.excel-batches [role=status]{color:#935420;min-height:24px}.excel-batches .excel-scroll{overflow:auto}.excel-batches .excel-actions{display:flex;gap:8px;flex-wrap:wrap}.excel-batches dialog{width:min(680px,90vw);border:1px solid #ddd;border-radius:12px;padding:24px}.excel-batches small{display:block;color:#68707b}`;
  document.head.append(s);
}
function shell(parent: HTMLElement): HTMLElement {
  style();
  const box = el("section");
  box.className = "excel-batches";
  const status = el("p");
  status.setAttribute("role", "status");
  box.append(status);
  parent.prepend(box);
  return box;
}
function tell(box: HTMLElement, text: string) {
  box.querySelector("[role=status]")!.textContent = text;
}
function link(plan: Obj): HTMLAnchorElement {
  const a = el("a", plan.name);
  a.href = `/admin/cloud-orchestrator/plans/${plan.id}`;
  return a;
}
function formatRate(value: any): string {
  return typeof value === "number"
    ? `${(value * 100).toFixed(1)}%`
    : "暂不可统计";
}
async function report(box: HTMLElement, id: number) {
  const area = el("section"),
    title = el("h3", "效果观察");
  area.append(title);
  box.append(area);
  const payload = await call(`${base}/${id}/report`);
  if (payload.pending) {
    area.append(
      el("p", "等待后台采集。报告每 5 分钟更新，按每人实际发送时间计算。"),
    );
    return;
  }
  area.append(
    el(
      "small",
      `最近采集：${payload.updated_at}。打开指打开对应内容，不代表从本次卡片进入。`,
    ),
  );
  const choose = el("select");
  for (const h of [12, 24, 48]) {
    const o = el("option", `${h} 小时累计`);
    o.value = String(h);
    choose.append(o);
  }
  area.append(choose);
  const grid = el("div");
  area.append(grid);
  const render = () => {
    const values = payload.windows?.[choose.value] || {};
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
        Object.entries(values).map(([g, v]) => {
          const n = v as Obj;
          return [
            g === "unknown" ? "未知" : g,
            String(n.sent),
            String(n.matured),
            String(n.observing),
            String(n.opened),
            String(n.unavailable),
            formatRate(n.open_rate),
          ];
        }),
      ),
    );
  };
  choose.onchange = render;
  render();
  area.append(
    button("下载逐人报告", () => {
      const blob = new Blob([JSON.stringify(payload, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = el("a");
      a.href = url;
      a.download = `群发批次-${id}-效果.json`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }),
  );
}
async function edit(
  box: HTMLElement,
  id: number,
  row: Obj,
  reload: () => Promise<void>,
) {
  const dialog = el("dialog"),
    text = el("textarea"),
    path = el("input"),
    title = el("input");
  title.value =
    row.content.find((v: Obj) => v.excel_card)?.excel_card.title || "";
  title.setAttribute("aria-label", "标题");
  text.value = row.text;
  path.value = row.path;
  path.style.width = "100%";
  dialog.append(
    el("h3", "修改发送内容"),
    el("label", "话术"),
    text,
    el("label", "标题（为空将发送失败）"),
    title,
    el("label", "小程序 path"),
    path,
  );
  const note = el("p");
  note.setAttribute("role", "status");
  dialog.append(note);
  dialog.append(
    button("保存并重新审核", async () => {
      const card = {
        ...row.content.find((v: Obj) => v.excel_card).excel_card,
        path: path.value.trim(),
        title: title.value.trim(),
      };
      await call(
        `/api/admin/ai-assistant/plans/${id}/recipients/${row.id}/content`,
        "PATCH",
        {
          expected_version: row.version,
          blocks: [
            { kind: "text", text: text.value },
            { kind: "mini_program", excel_card: card },
          ],
        },
      );
      dialog.close();
      dialog.remove();
      await reload();
    }),
    button("取消", () => {
      dialog.close();
      dialog.remove();
    }),
  );
  box.append(dialog);
  dialog.showModal();
}
async function detail(parent: HTMLElement, id: number, readOnly = false) {
  const box = shell(parent);
  let page = 0;
  const reload = async () => {
    const payload = await call(`${base}/${id}`);
    box.replaceChildren();
    const status = el("p");
    status.setAttribute("role", "status");
    box.append(el("h2", payload.plan.name), status);
    const rows = payload.rows as Obj[],
      reviewable =
        !readOnly &&
        ["pending_review", "partially_approved"].includes(payload.plan.state);
    const pending = rows.filter(
      (r) => r.review_state !== "rejected" && r.review_state !== "ineligible",
    );
    const cardOf = (r: Obj): Obj =>
      r.content.find((v: Obj) => v.excel_card)?.excel_card || {};
    const readyCount = pending.filter((r) =>
      String(cardOf(r).title || "").trim(),
    ).length;
    const missingCover =
      pending.length === 0 ||
      pending.some((r) => !cardOf(r).cover_digest) ||
      new Set(pending.map((r) => cardOf(r).cover_digest)).size > 1;
    box.append(
      el(
        "p",
        `共 ${rows.length} 人；预计创建 ${readyCount} 个企微任务。缺少标题 ${pending.length - readyCount} 行将发送失败。网页批准一次，员工随后在企微端执行。`,
      ),
    );
    const counts: Record<string, number> = {};
    for (const r of rows) {
      const s =
        r.review_state === "rejected" ? "已排除" : labels[r.state] || r.state;
      counts[s] = (counts[s] || 0) + 1;
    }
    box.append(
      el(
        "p",
        Object.entries(counts)
          .map(([s, n]) => `${s}：${n}`)
          .join("　"),
      ),
    );
    const actions = el("div");
    if (readOnly) {
      const reviewLink = link(payload.plan);
      reviewLink.textContent = "进入 AI 助手审核";
      actions.append(reviewLink);
    }
    actions.className = "excel-actions";
    if (reviewable) {
      const coverInput = el("input");
      coverInput.type = "file";
      coverInput.accept = "image/png,image/jpeg";
      coverInput.setAttribute("aria-label", "统一封面图片");
      const coverKey = crypto.randomUUID();
      actions.append(
        coverInput,
        button("上传统一封面", async () => {
          if (!coverInput.files?.[0]) {
            tell(box, "请选择 PNG 或 JPEG 封面，最大 2 MB");
            return;
          }
          await call(
            `${base}/${id}/cover?expected_version=${payload.plan.version}`,
            "POST",
            coverInput.files[0],
            coverKey,
          );
          await reload();
          tell(box, "统一封面已保存；请重新审核后批准发送。");
        }),
      );
      const approve = button("批准并创建群发任务", async () => {
        await call(
          `${base}/${id}/approve`,
          "POST",
          { expected_version: payload.plan.version },
          `excel-approve-${id}-${payload.plan.version}`,
        );
        await reload();
        tell(box, "已提交创建任务，请员工在企微端执行。");
      });
      approve.disabled = missingCover;
      actions.append(approve);
      if (missingCover)
        box.append(
          el("p", "未上传统一封面或没有可批准的行，禁止发送。请先上传封面。"),
        );
    }
    actions.append(button("刷新", reload));
    const back = el("a", "返回运营闭环");
    back.href = "/admin/operation-cycles";
    actions.append(back);
    box.append(actions);
    const scroll = el("div");
    scroll.className = "excel-scroll";
    box.append(scroll);
    scroll.append(
      table(
        ["用户 / 发送员工", "话术", "小程序卡片", "状态", "审核操作"],
        rows.slice(page * 50, page * 50 + 50).map((r) => {
          const card = r.content.find((v: Obj) => v.excel_card)?.excel_card;
          const cardView = el("div");
          if (card) {
            const image = el("img");
            if (card.cover_digest)
              image.src = `${base}/covers/${encodeURIComponent(card.cover_digest)}`;
            image.alt = "卡片封面";
            if (card.cover_digest) cardView.append(image);
            else cardView.append(el("small", "未上传统一封面"));
            cardView.append(
              el("p", card.title || "标题为空：发送失败"),
              el("small", card.path),
            );
          }
          const controls = el("div");
          if (reviewable) {
            controls.append(
              button("修改", () => edit(box, id, r, reload)),
              button(
                r.review_state === "rejected" ? "恢复" : "排除",
                async () => {
                  await call(
                    `/api/admin/ai-assistant/plans/${id}/recipients/${r.id}/review`,
                    "POST",
                    {
                      expected_version: r.version,
                      decision:
                        r.review_state === "rejected" ? "approved" : "rejected",
                      reason: "Excel 批次人工审核",
                    },
                  );
                  await reload();
                },
              ),
            );
          }
          return [
            `${r.unionid}\n${r.sender_userid}`,
            r.text,
            cardView,
            `${r.review_state === "rejected" ? "已排除" : labels[r.state] || r.state}${r.reason ? "\n" + failure(r.reason) : ""}${r.sent_at ? "\n" + r.sent_at : ""}`,
            controls,
          ];
        }),
      ),
    );
    box.append(
      button("上一页", async () => {
        page = Math.max(0, page - 1);
        await reload();
      }),
      el("span", `${page + 1} / ${Math.max(1, Math.ceil(rows.length / 50))}`),
      button("下一页", async () => {
        page = Math.min(Math.ceil(rows.length / 50) - 1, page + 1);
        await reload();
      }),
    );
    try {
      await report(box, id);
    } catch {
      box.append(el("p", "效果报告暂时不可用；审核和发送明细已保留。"));
    }
  };
  await reload();
}
export async function mountExcelBatchPanel(parent: HTMLElement): Promise<void> {
  const box = shell(parent);
  box.append(
    el("h2", "Excel 群发批次"),
    el(
      "p",
      "上传五列文本，依次为：unionid、话术、小程序 path、发送人 userid、标题。标题为空的行发送失败。上传后进入 AI 助手审核，并上传批次统一封面；没有封面禁止发送。",
    ),
  );
  const file = el("input");
  file.type = "file";
  file.accept = ".xlsx";
  const fresh = el("input");
  fresh.type = "checkbox";
  const label = el("label", "明确创建新的发送批次（相同文件也重新发送）");
  label.prepend(fresh);
  const list = el("div");
  const details = el("div");
  box.append(file, label);
  const refresh = async () => {
    const data = await call(base);
    list.replaceChildren(
      table(
        ["批次", "人数", "状态", "执行与效果"],
        (data.items || []).map((p: Obj) => [
          link(p),
          String(p.target_count),
          p.state === "pending_review" ? "待审核" : p.state,
          button("查看执行与效果", async () => {
            details.replaceChildren();
            await detail(details, Number(p.id), true);
          }),
        ]),
      ),
    );
  };
  let importKey = crypto.randomUUID();
  file.onchange = () => {
    importKey = crypto.randomUUID();
  };
  fresh.onchange = () => {
    importKey = crypto.randomUUID();
  };
  box.append(
    button("上传并进入审核", async () => {
      if (!file.files?.[0]) {
        tell(box, "请先选择 Excel 文件");
        return;
      }
      const result = await call(
        `${base}/imports${fresh.checked ? "?new=1" : ""}`,
        "POST",
        file.files[0],
        importKey,
      );
      tell(
        box,
        result.replayed ? "该文件已有批次，正在打开原审核记录" : "上传成功",
      );
      location.href = `/admin/cloud-orchestrator/plans/${result.plan.id}`;
    }),
    list,
    details,
  );
  try {
    await refresh();
  } catch (error) {
    tell(box, (error as Error).message);
  }
}
export async function mountExcelDetailIfNeeded(): Promise<boolean> {
  const match = location.pathname.match(
    /^\/admin\/cloud-orchestrator\/plans\/(\d+)$/,
  );
  if (!match) return false;
  const payload = await call(`/api/admin/ai-assistant/plans/${match[1]}`);
  if (payload.plan?.source_kind !== "excel_batch") return false;
  const parent = document.querySelector<HTMLElement>("[data-cloud-plan-root]");
  if (!parent) return false;
  parent.replaceChildren();
  await detail(parent, Number(match[1]));
  return true;
}
