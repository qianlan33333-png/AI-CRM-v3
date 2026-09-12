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
let failCoverUploadOnce = false;
let failImportOnce = false;
let releaseFirstRowPatch;
let pauseFirstRowPatch = true;
let releaseInitialDetail;
let pauseInitialDetail = true;
let importedBatch;
let selectedCoverID = 0;
let coverSelectionPosts = 0;
const coverLibrary = Array.from({ length: 13 }, (_, index) => ({
  id: 42 + index,
  name: index === 12 ? "第二页封面" : `启用图片-${index + 1}`,
  enabled: true,
  thumb_160_url: `/api/admin/image-library/${42 + index}/variants/thumb_160`,
}));
const calls = [];
const batch = {
  id: 918,
  batch_key: "xb_fixture",
  state: "pending_review",
  version: 3,
  cover_digest: "",
  cover_image_id: 0,
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
  if (url === "/api/admin/operation-batches/strategy-summaries?limit=20&offset=0")
    return json({
      items: [
        {
          strategy_key: "weekly.review",
          title: "每周复盘",
          status: "active",
          latest_batch_status: "ready",
          latest_batch: {
            id: 918,
            state: "pending_review",
            summary: { expected_tasks: 1 },
          },
        },
      ],
      total: 1,
      limit: 20,
      offset: 0,
      has_more: false,
      next_offset: null,
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
    if (failImportOnce) {
      failImportOnce = false;
      return json({}, 503);
    }
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
    const contentType = new Headers(init.headers).get("Content-Type") || "";
    if (contentType.startsWith("application/json")) {
      const body = JSON.parse(init.body);
      assert.equal(body.cover_image_id, 42);
      coverSelectionPosts += 1;
      selectedCoverID = body.cover_image_id;
      cover = batch.cover_digest = row.card.cover_digest = "sha256:existing-cover";
      batch.cover_image_id = selectedCoverID;
    } else {
      if (failCoverUploadOnce) {
        failCoverUploadOnce = false;
        return json({}, 503);
      }
      assert.ok(init.body instanceof win.File);
      cover = batch.cover_digest = row.card.cover_digest = "sha256:cover";
      batch.cover_image_id = 43;
    }
    batch.version++;
    return json({ batch, cover_digest: cover, cover_image_id: batch.cover_image_id });
  }
  if (url.startsWith("/api/admin/image-library?") && url.includes("enabled_only=true")) {
    const query = new URL(url, "https://fixture.test").searchParams;
    assert.equal(query.get("limit"), "12");
    const offset = Number(query.get("offset"));
    const items = coverLibrary.slice(offset, offset + 12);
    return json({
      items,
      total: coverLibrary.length,
      limit: 12,
      offset,
      has_more: offset + items.length < coverLibrary.length,
      next_offset: offset + items.length < coverLibrary.length ? offset + items.length : null,
    });
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
      items: [{ content_version: 1, cover_image_id: 42, cover_digest: "sha256:existing-cover", created_at: "2026-09-09T00:00:00Z" }],
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
assert.ok(
  importInput.closest("label.admin-field"),
  "new batch file input did not use the standard field wrapper",
);
await click("上传并开始审核");
const importDialog = importInput.closest("dialog");
assert.ok(importDialog?.open, "missing Excel file closed the new-batch dialog");
assert.ok(
  importDialog.querySelector("[data-excel-feedback]")?.textContent.includes("请选择 Excel 文件"),
  "missing Excel file did not remain visible inside its dialog",
);
assert.equal(
  [...importDialog.querySelectorAll("button")].find((item) => item.textContent === "上传并开始审核")?.getAttribute("aria-busy"),
  null,
  "missing Excel file left the dialog action busy",
);
Object.defineProperty(importInput, "files", {
  value: [new win.File(["fixture"], "batch.xlsx")],
});
failImportOnce = true;
await click("上传并开始审核");
assert.ok(importDialog.open, "failed Excel import closed the new-batch dialog");
assert.ok(
  importDialog.querySelector("[data-excel-feedback]")?.textContent.includes("批次服务暂时不可用"),
  "failed Excel import did not remain visible inside its dialog",
);
assert.equal(importInput.files?.[0]?.name, "batch.xlsx", "failed Excel import discarded the selected file");
assert.equal(
  [...importDialog.querySelectorAll("button")].find((item) => item.textContent === "上传并开始审核")?.getAttribute("aria-busy"),
  null,
  "failed Excel import left the dialog action busy",
);
await click("上传并开始审核");
releaseInitialDetail();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(
  win.document.body.textContent.includes("当前批次 #919"),
  "a stale initial detail response cannot overwrite the newly imported batch",
);
const history = win.document.querySelector('select[aria-label="历史批次"]');
assert.ok(
  history.closest("label.admin-field"),
  "historical batch selector did not use the standard field wrapper",
);
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
assert.ok(
  approve().classList.contains("admin-button--primary"),
  "the approval action did not retain its primary action hierarchy",
);
assert.ok(
  [...win.document.querySelectorAll("button")].every((button) =>
    button.classList.contains("admin-button"),
  ),
  "Excel actions did not use the shared standard button component",
);
await click("选择已有启用图片");
const picker = () => win.document.querySelector('dialog[aria-label="选择已有启用图片"]');
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(picker(), "enabled image picker did not open");
assert.ok(picker().textContent.includes("启用图片-1"), "picker did not render the first image page");
assert.equal(batch.cover_image_id, 0, "opening the picker changed the frozen cover before selection");
const nextCoverPage = [...picker().querySelectorAll("button")].find((item) => item.textContent === "下一页");
assert.ok(nextCoverPage && !nextCoverPage.disabled, "picker did not expose its second page");
nextCoverPage.click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(picker().textContent.includes("第二页封面"), "picker did not follow image-library offset pagination");
const cancelPicker = [...picker().querySelectorAll("button")].find((item) => item.textContent === "取消");
assert.ok(cancelPicker, "picker did not expose cancellation");
cancelPicker.click();
await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(picker(), null, "cancelling the image picker did not close it");
assert.equal(batch.cover_image_id, 0, "cancelling the image picker changed the batch cover");
await click("选择已有启用图片");
await new Promise((resolve) => setTimeout(resolve, 20));
const firstCoverChoice = picker().querySelector('button[data-cover-image-id="42"]');
assert.ok(firstCoverChoice, "picker did not expose the selected stable image id");
firstCoverChoice.click();
await new Promise((resolve) => setTimeout(resolve, 30));
assert.equal(coverSelectionPosts, 1, "selecting an existing image did not issue one JSON cover command");
assert.equal(selectedCoverID, 42, "existing cover command used the wrong stable material id");
assert.ok(
  calls.some((call) => call.url.startsWith("/api/admin/operation-batches/918/cover?") && String(call.init.headers["Content-Type"] || "").startsWith("application/json") && JSON.parse(call.init.body).cover_image_id === 42),
  "existing cover selection did not use the documented JSON body",
);
assert.ok(win.document.body.textContent.includes("冻结封面：素材 #42"), "selected cover was not shown as frozen material evidence");
await click("查看旧版本");
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(win.document.body.textContent.includes("素材 #42"), "history did not retain the frozen cover material id");
const closeHistory = [...win.document.querySelectorAll("dialog button")].find((item) => item.textContent === "关闭");
assert.ok(closeHistory, "history dialog did not expose close");
closeHistory.click();
await new Promise((resolve) => setTimeout(resolve, 10));
const coverInput = win.document.querySelector(
  'input[aria-label="统一封面图片"]',
);
assert.ok(
  coverInput.closest("label.admin-field"),
  "cover upload input did not use the standard field wrapper",
);
Object.defineProperty(coverInput, "files", {
  value: [new win.File(["fixture"], "cover.png", { type: "image/png" })],
});
failCoverUploadOnce = true;
await click("上传统一封面");
const feedback = win.document.querySelector("[data-excel-feedback]");
assert.ok(
  feedback.classList.contains("admin-alert--error") &&
    feedback.textContent.includes("批次服务暂时不可用"),
  "failed upload did not retain an error in the persistent feedback container",
);
assert.equal(
  win.document.querySelectorAll("[data-excel-feedback] .v3-action-busy").length,
  0,
  "feedback retained the action spinner after the failed upload completed",
);
assert.equal(
  [...win.document.querySelectorAll("button")].find(
    (item) => item.textContent === "上传统一封面",
  )?.getAttribute("aria-busy"),
  null,
  "failed upload left the action button busy",
);
assert.equal(
  approve().disabled,
  false,
  "a failed replacement upload changed the existing frozen-cover approval guard",
);
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
assert.equal(
  [...win.document.querySelectorAll("button")].some((item) => item.textContent === "选择已有启用图片"),
  false,
  "submitted batch still exposed existing-cover selection",
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
