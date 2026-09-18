// Extends the canonical audience host; reuses its authenticated transport and styles.
(() => {
  "use strict";
  const base = "/api/admin/ai-audience/";
  const http = window.AudienceOperationsHTTP;
  if (!http) return;
  const el = (tag, text, cls) => {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  };
  const input = (value = "", multiline = false) => {
    const n = el(multiline ? "textarea" : "input");
    n.value = value;
    n.className = "ai-input";
    if (multiline) n.rows = 4;
    return n;
  };
  const field = (label, node) => {
    const wrapper = el("label", label, "ai-field");
    wrapper.append(node);
    return wrapper;
  };
  const read = async (path) => (await http.request(base + path)).data;
  async function allPackages() {
    const items = [];
    for (let offset = 0; ; offset += 100) {
      const page = await http.request(
        base + `packages?limit=100&offset=${offset}`,
      );
      items.push(...page.items);
      if (page.items.length < 100 || items.length >= page.total)
        return { items };
      if (offset >= 99900) throw new Error("package catalog too large");
    }
  }
  const write = async (path, body) =>
    (
      await http.request(base + path, {
        method: "POST",
        mutate: true,
        scope: "core-operations",
        body,
      })
    ).data;
  function action(label, fn, notice) {
    const button = el("button", label, "aud-btn");
    button.type = "button";
    button.onclick = async () => {
      button.disabled = true;
      try {
        await fn();
      } catch (e) {
        notice.textContent = http.errorState(e).message;
      } finally {
        button.disabled = false;
      }
    };
    return button;
  }
  const root = document.getElementById("coreOperationsRoot");
  if (root) {
    let generation = 0;
    async function load() {
      const gen = ++generation;
      root.replaceChildren(el("p", "正在读取配置…"));
      try {
        const [products, prompt, history, packages] = await Promise.all([
          read("core/products"),
          read("core/prompt"),
          read("core/prompt/history"),
          allPackages(),
        ]);
        if (gen !== generation) return;
        root.replaceChildren();
        const notice = el(
          "p",
          `已配置 ${products.length}/5。绑定现有人群包后，该包改由 AI 分配成员。`,
        );
        notice.setAttribute("role", "status");
        root.append(notice);
        for (let id = 1; id <= 5; id++) {
          const p = products.find((x) => x.id === id);
          const form = el("form", undefined, "aud-card");
          form.style.padding = "12px";
          form.style.marginBottom = "12px";
          const name = input(p?.name),
            description = input(p?.description, true),
            context = input(p?.ai_context, true),
            reference = input(p?.product_reference);
          const pkg = el("select");
          pkg.className = "ai-select";
          pkg.append(new Option("请选择现有人群包", ""));
          for (const item of packages.items || [])
            pkg.append(new Option(item.name, String(item.id)));
          pkg.value = String(p?.package_id || "");
          pkg.disabled = !!p;
          const enabled = input();
          enabled.type = "checkbox";
          enabled.checked = !!p?.enabled;
          form.append(
            el("h3", `核心产品 ${id}`),
            field("产品名称", name),
            field("绑定人群包", pkg),
            field("产品描述", description),
            field("AI 补充信息", context),
            field("关联销售商品引用（可选）", reference),
            field("允许新客户推荐进入", enabled),
          );
          form.append(
            action(
              "保存产品",
              async () => {
                if (
                  !name.value.trim() ||
                  !description.value.trim() ||
                  !pkg.value
                ) {
                  notice.textContent = "请填写名称、描述并选择人群包";
                  return;
                }
                await write("core/products", {
                  product: {
                    id,
                    package_id: Number(pkg.value),
                    name: name.value,
                    description: description.value,
                    ai_context: context.value,
                    product_reference: reference.value,
                    enabled: enabled.checked,
                    version: p?.version || 0,
                  },
                  expected_version: p?.version || 0,
                });
                await load();
              },
              notice,
            ),
          );
          form.onsubmit = (e) => e.preventDefault();
          root.append(form);
        }
        const editor = input(prompt.draft, true);
        editor.rows = 8;
        const historySelect = el("select");
        historySelect.className = "ai-select";
        historySelect.append(new Option("选择历史提示词恢复到编辑区", ""));
        for (const v of history)
          historySelect.append(new Option(`发布版本 ${v.id}`, String(v.id)));
        historySelect.onchange = () => {
          const v = history.find((x) => x.id === Number(historySelect.value));
          if (v) editor.value = v.body;
        };
        root.append(
          el(
            "h3",
            `分包提示词 · 当前发布版本 ${prompt.published_id || "未发布"}`,
          ),
          historySelect,
          field("判断提示词", editor),
        );
        let promptVersion = prompt.version;
        const savePrompt = async (publish) => {
          const saved = await write("core/prompt", {
            body: editor.value,
            expected_version: promptVersion,
            publish,
          });
          promptVersion = saved.version;
          notice.textContent = publish
            ? `已发布版本 ${saved.published_id}`
            : "草稿已保存";
        };
        root.append(
          action("保存草稿", () => savePrompt(false), notice),
          action(
            "发布提示词",
            async () => {
              await savePrompt(true);
              await load();
            },
            notice,
          ),
        );
        const customers = input();
        customers.placeholder = "客户 ID，用逗号分隔，最多 100 人";
        root.append(field("试运行 / 存量分包客户", customers));
        const result = el("div");
        async function run(preview) {
          const ids = customers.value
            .split(/[,，\s]+/)
            .filter(Boolean)
            .map(Number);
          if (
            !ids.length ||
            ids.some((id) => !Number.isSafeInteger(id) || id < 1)
          ) {
            notice.textContent = "请填写有效客户 ID";
            return;
          }
          if (preview) await savePrompt(false);
          const batch = await write("core/recommendations", {
            customer_ids: ids,
            preview,
          });
          result.replaceChildren();
          for (const item of batch.items) {
            const row = el("p");
            const refresh = action(
              "刷新结果",
              async () => {
                const latest = await read(`core/recommendations/${item.id}`);
                description.textContent = `客户 ${latest.customer_id} · ${latest.state} · ${latest.reason || "等待模型处理"}`;
              },
              notice,
            );
            const description = el(
              "span",
              `客户 ${item.customer_id} · ${item.state} `,
            );
            row.append(description, refresh);
            result.append(row);
          }
          notice.textContent = preview
            ? "试运行已提交，结果不会写入人群包"
            : "分包任务已提交，可刷新查看每个客户结果";
        }
        root.append(
          action("试运行（不入包）", () => run(true), notice),
          action("提交批量分包", () => run(false), notice),
          result,
        );
      } catch (e) {
        if (gen !== generation) return;
        root.replaceChildren(el("p", http.errorState(e).message));
        root.append(action("重新读取", load, root));
      }
    }
    void load();
  }
  document.addEventListener("click", async (event) => {
    const button = event.target.closest?.("[data-core-member]");
    if (!button) return;
    const customer = Number(button.dataset.coreMember),
      pkg = Number(button.dataset.corePackage);
    button.disabled = true;
    try {
      const detail = await read(
        `packages/${pkg}/members/${customer}/operations`,
      );
      const dialog = el("dialog");
      dialog.className = "aud-dialog core-operations-dialog";
      const notice = el(
        "p",
        `本包推送 ${detail.stats.push_count} 次 · 最近推送 ${detail.stats.last_push_at || "—"} · ${detail.stats.last_push_status || "—"} · 最近评估 ${detail.stats.last_evaluation_at || "—"} · 链接访问 ${detail.stats.visit_count ?? "未接入"}`,
      );
      dialog.append(el("h3", `客户 ${customer} 的运营明细`), notice);
      const rows = el("div");
      const add = (items) => {
        for (const p of items)
          rows.append(
            el(
              "p",
              `${p.occurred_at} · ${p.status} · ${p.materials.map((m) => m.kind + " #" + m.id).join("、")}`,
            ),
          );
      };
      add(detail.pushes);
      dialog.append(rows);
      let cursor = detail.next_cursor;
      dialog.append(
        action(
          "更多推送记录",
          async () => {
            if (!cursor) {
              notice.textContent = "已显示全部推送记录";
              return;
            }
            const next = await read(
              `packages/${pkg}/members/${customer}/operations?cursor=${encodeURIComponent(cursor)}`,
            );
            add(next.pushes);
            cursor = next.next_cursor;
          },
          notice,
        ),
      );
      const appendHistory = (items) => {
        for (const a of items)
          dialog.append(
            el(
              "p",
              `产品 ${a.core_product_id} · ${a.source} · ${a.reason} · 依据：${a.evidence || "—"} · 提示词版本 ${a.prompt_version || "—"} · ${a.entered_at} · ${a.ended_at ? a.end_reason + " · " + a.ended_at : "当前运营"}`,
            ),
          );
      };
      appendHistory(detail.assignments);
      let historyCursor = detail.assignment_next_cursor;
      if (historyCursor)
        dialog.append(
          action(
            "更多转包历史",
            async () => {
              if (!historyCursor) return;
              const page = await read(
                `packages/${pkg}/members/${customer}/history?cursor=${encodeURIComponent(historyCursor)}`,
              );
              appendHistory(page.items);
              historyCursor = page.next_cursor;
            },
            notice,
          ),
        );
      const current = detail.assignments.find((a) => !a.ended_at);
      if (current) {
        const target = input(String(current.core_product_id));
        target.type = "number";
        target.min = "1";
        target.max = "5";
        const reason = input();
        dialog.append(
          field("目标核心产品编号", target),
          field("调整原因", reason),
        );
        const change = async (purchase) => {
          await write("core/assignments", {
            customer_id: customer,
            core_product_id: purchase
              ? current.core_product_id
              : Number(target.value),
            expected_assignment_id: current.id,
            reason: reason.value,
            purchase,
          });
          notice.textContent = "已保存，刷新成员列表查看最新归属";
        };
        dialog.append(
          action("人工转包", () => change(false), notice),
          action("标记已购买当前产品", () => change(true), notice),
        );
      }
      dialog.append(action("关闭", () => dialog.close(), notice));
      dialog.addEventListener("close", () => dialog.remove());
      document.body.append(dialog);
      dialog.showModal();
    } catch (e) {
      const host = document.getElementById("memberTotal");
      if (host) host.textContent = http.errorState(e).message;
    } finally {
      button.disabled = false;
    }
  });
})();
