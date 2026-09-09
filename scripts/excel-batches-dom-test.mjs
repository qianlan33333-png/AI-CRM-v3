import { build } from "esbuild";
import { JSDOM } from "jsdom";
import assert from "node:assert/strict";

const bundle = await build({
  entryPoints: ["web/v3/excelBatches.ts"],
  bundle: true,
  format: "iife",
  globalName: "ExcelTest",
  write: false,
});
const dom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const win = dom.window;
Object.defineProperty(win.crypto, "randomUUID", {
  value: (() => {
    let n = 0;
    return () => `00000000-0000-4000-8000-${String(++n).padStart(12, "0")}`;
  })(),
});
win.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
win.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};

let cover = "",
  approved = 0,
  excluded = false;
let releaseFirstRowPatch;
let pauseFirstRowPatch = true;
let releaseInitialDetail;
let pauseInitialDetail = true;
let importedBatch;
const calls = [];
const batch = {
  id: 918,
  batch_key: "xb_fixture",
  state: "pending_review",
  version: 3,
  cover_digest: "",
  current_content_version: 1,
  summary: {
    total_rows: 1,
    excluded_rows: 0,
    empty_title_rows: 0,
    expected_tasks: 1,
  },
};
const row = {
  id: 33,
  version: 4,
  unionid: "<script>unsafe</script>",
  sender_userid: "staff",
  text: "原话术",
  card: {
    appid: "configured-app",
    path: "pages/article?id=1",
    title: "案例标题",
    cover_digest: "",
  },
  segment: "A",
  excluded: false,
  delivery_state: "pending_submission",
  failure_reason: "",
  sent_at: null,
};
const extraRows = Array.from({ length: 50 }, (_, index) => ({
  ...row,
  id: 34 + index,
  unionid: index === 49 ? "第二页用户" : `用户-${index + 2}`,
  version: 1,
}));
extraRows[49] = {
  ...extraRows[49],
  review_state: "approved",
  state: "final_failed",
  reason: "旧投影失败原因",
};
const receiptRows = Array.from({ length: 51 }, (_, index) => ({
  unionid: index === 50 ? "第二页回执用户" : `回执用户-${index + 1}`,
  sender_userid: "staff",
  delivery_state: "delivery_proven",
  sent_at: "2026-09-09T00:00:00Z",
  failure_reason: "",
}));
const json = (body, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => body,
});
win.fetch = async (raw, init = {}) => {
  const url = String(raw);
  calls.push({ url, init });
  if (url === "/api/admin/operation-cycles/strategies?limit=100&offset=0")
    return json({
      items: [
        {
          strategy_key: "weekly.review",
          title: "每周复盘",
          status: "active",
          latest_batch: {
            id: 918,
            state: "pending_review",
            summary: { expected_tasks: 1 },
          },
        },
      ],
    });
  if (url === "/api/admin/operation-batches/legacy")
    return json({
      items: [
        { id: 917, name: "旧批次", state: "pending_review", linkable: true },
      ],
    });
  if (url === "/api/admin/operation-batches/strategies/weekly.review")
    return json({
      strategy: { strategy_key: "weekly.review", title: "每周复盘" },
      items: importedBatch ? [importedBatch, batch] : [batch],
    });
  if (
    url.startsWith(
      "/api/admin/operation-batches/strategies/weekly.review/imports",
    )
  ) {
    importedBatch = {
      ...batch,
      id: 919,
      batch_key: "xb_imported",
      version: 1,
    };
    return json({ batch: importedBatch });
  }
  if (url.startsWith("/api/admin/operation-batches/918?")) {
    if (pauseInitialDetail) {
      pauseInitialDetail = false;
      await new Promise((resolve) => {
        releaseInitialDetail = resolve;
      });
    }
    const cursor = new URL(url, "https://fixture.test").searchParams.get(
      "cursor",
    );
    return json(
      cursor === "detail-page-2"
        ? { batch, rows: [extraRows[49]], next_cursor: "" }
        : {
            batch,
            rows: [row, ...extraRows.slice(0, 49)],
            next_cursor: "detail-page-2",
          },
    );
  }
  if (url.startsWith("/api/admin/operation-batches/919?"))
    return json({ batch: importedBatch, rows: [row], next_cursor: "" });
  if (url.startsWith("/api/admin/operation-batches/918/cover?")) {
    assert.ok(init.body instanceof win.File);
    cover = batch.cover_digest = row.card.cover_digest = "sha256:cover";
    batch.version++;
    return json({ batch, cover_digest: cover });
  }
  if (url.endsWith("/preview-approval")) {
    assert.equal(JSON.parse(init.body).expected_version, batch.version);
    return json({ preview_digest: "sha256:preview", summary: batch.summary });
  }
  if (url.endsWith("/approve")) {
    const body = JSON.parse(init.body);
    assert.equal(body.preview_digest, "sha256:preview");
    approved++;
    batch.state = "dispatching";
    row.review_state = "approved";
    row.delivery_state = "task_created_waiting_employee";
    return json({ batch });
  }
  if (url.endsWith("/rows/33")) {
    if (pauseFirstRowPatch) {
      pauseFirstRowPatch = false;
      await new Promise((resolve) => {
        releaseFirstRowPatch = resolve;
      });
    }
    const body = JSON.parse(init.body);
    assert.equal(body.expected_version, row.version);
    excluded = row.excluded = body.excluded;
    row.version++;
    batch.version++;
    return json({ batch, row });
  }
  if (url.startsWith("/api/admin/operation-batches/918/receipts?")) {
    const cursor = new URL(url, "https://fixture.test").searchParams.get(
      "cursor",
    );
    return json(
      cursor === "receipts-page-2"
        ? { items: [receiptRows[50]], next_cursor: "" }
        : {
            items: receiptRows.slice(0, 50),
            next_cursor: "receipts-page-2",
          },
    );
  }
  if (url.endsWith("/report"))
    return json({
      updated_at: "2026-09-09T00:00:00Z",
      segment_source: "excel",
      has_segments: true,
      overall: {
        12: {
          sent: 1,
          matured: 1,
          observing: 0,
          opened: 1,
          unavailable: 0,
          open_rate: 1,
        },
      },
      windows: {
        12: {
          has_segments: true,
          overall: {
            sent: 1,
            matured: 1,
            observing: 0,
            opened: 1,
            unavailable: 0,
            open_rate: 1,
          },
          groups: {
            A: {
              sent: 1,
              matured: 1,
              observing: 0,
              opened: 1,
              unavailable: 0,
              open_rate: 1,
            },
          },
        },
      },
    });
  if (url.endsWith("/versions"))
    return json({
      items: [{ content_version: 1, created_at: "2026-09-09T00:00:00Z" }],
    });
  if (url.startsWith("/api/admin/operation-batches/918/versions/1?"))
    return json({ rows: [row], next_cursor: "" });
  throw new Error(`unexpected request ${url}`);
};
win.eval(bundle.outputFiles[0].text + ";window.ExcelTest=ExcelTest;");
await win.ExcelTest.mountOperationExcelWorkspace(
  win.document.getElementById("stage"),
);
assert.equal(
  win.document.querySelectorAll(".xeb-plan").length,
  0,
  "only the long-term plan is a first-level row",
);
assert.ok(win.document.body.textContent.includes("查看详情"));
assert.ok(
  win.document.body.textContent.includes("批次 #918 · 待审核 · 预计任务 1"),
);
const click = async (label) => {
  const node = [...win.document.querySelectorAll("button")].find(
    (item) => item.textContent === label,
  );
  assert.ok(node, label);
  node.click();
  await new Promise((resolve) => setTimeout(resolve, 20));
};
await click("查看详情");
assert.ok(
  [...win.document.querySelectorAll("button")].some(
    (item) => item.textContent === "新建发送批次",
  ),
  "new batch remains available while the initial detail is loading",
);
await click("新建发送批次");
const importInput = win.document.querySelector('dialog input[type="file"]');
Object.defineProperty(importInput, "files", {
  value: [new win.File(["fixture"], "batch.xlsx")],
});
await click("上传并开始审核");
releaseInitialDetail();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(
  win.document.body.textContent.includes("当前批次 #919"),
  "a stale initial detail response cannot overwrite the newly imported batch",
);
const history = win.document.querySelector('select[aria-label="历史批次"]');
history.value = "918";
history.dispatchEvent(new win.Event("change"));
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(
  win.document.querySelectorAll(".xeb-detail-nav button").length,
  2,
  "detail has exactly the two requested dimensions on its left side",
);
assert.ok(win.document.body.textContent.includes("内容准备与发送"));
assert.ok(
  win.document.body.textContent.includes("第二页用户"),
  "detail follows next_cursor through the second page",
);
assert.ok(
  win.document.body.textContent.includes("旧投影失败原因"),
  "state/reason projection remains readable during backend field migration",
);
assert.ok(
  win.document.body.textContent.includes("<script>unsafe</script>"),
  "untrusted text must stay text",
);
assert.equal(
  win.document.querySelectorAll("script").length,
  0,
  "batch data must not create script elements",
);
const approve = () =>
  [...win.document.querySelectorAll("button")].find(
    (item) => item.textContent === "审核通过并创建企微群发任务",
  );
assert.equal(
  approve().disabled,
  true,
  "frontend prevents approval before a batch cover exists",
);
const coverInput = win.document.querySelector(
  'input[aria-label="统一封面图片"]',
);
Object.defineProperty(coverInput, "files", {
  value: [new win.File(["fixture"], "cover.png", { type: "image/png" })],
});
await click("上传统一封面");
assert.equal(cover, "sha256:cover");
await click("排除");
assert.equal(
  approve().disabled,
  true,
  "approval is disabled while a row write still holds the current batch version",
);
releaseFirstRowPatch();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(excluded, true);
await click("恢复");
assert.equal(excluded, false);
await click("审核通过并创建企微群发任务");
assert.equal(
  approved,
  1,
  "one click previews then submits one approval command",
);
assert.ok(win.document.body.textContent.includes("任务已创建，待员工执行"));
await click("发送效果与复盘");
assert.equal(
  win.document.querySelector('.xeb-detail-nav button[data-tab="effects"]')
    .dataset.selected,
  "true",
  "left navigation selection follows the active dimension",
);
assert.ok(win.document.body.textContent.includes("逐人回执"));
assert.ok(
  win.document.body.textContent.includes("分层来源：Excel；已按分层统计。"),
  "report identifies its actual segment source",
);
assert.ok(
  win.document.body.textContent.includes("第二页回执用户"),
  "receipts follow next_cursor through the second page",
);
assert.ok(win.document.body.textContent.includes("100.0%"));
assert.equal(
  calls.some((call) => call.url.includes("/cloud-orchestrator/")),
  false,
  "Excel never routes through AI Assistant UI",
);
assert.ok(
  calls
    .filter((call) => call.url.endsWith("/approve"))
    .every((call) => call.init.headers["Idempotency-Key"]),
  "approval includes idempotency",
);
dom.window.close();
console.log(
  "Excel operation workspace list/detail, review, receipts, and report: PASS",
);
