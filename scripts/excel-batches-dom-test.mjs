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
const dom = new JSDOM("<main><div data-cloud-plan-root></div></main>", {
  url: "https://fixture.test/admin/cloud-orchestrator/plans/1",
  runScripts: "outside-only",
});
const win = dom.window;
let approved = 0,
  excluded = false;
const calls = [];
const card = {
  appid: "app",
  path: "pages/article/article?lesson_id=1",
  title: "案例标题",
  cover_digest: "sha256:" + "a".repeat(64),
};
let data = {
  plan: {
    id: 1,
    name: "导入批次",
    version: 1,
    source_kind: "excel_batch",
    state: "pending_review",
  },
  rows: [
    {
      id: 1,
      unionid: "<script>unsafe</script>",
      sender_userid: "staff",
      text: "原话术",
      path: card.path,
      version: 1,
      state: "not_accepted",
      review_state: "pending_review",
      content: [
        { kind: "text", text: "原话术" },
        { kind: "mini_program", excel_card: card },
      ],
    },
  ],
};
win.fetch = async (url, init = {}) => {
  calls.push({ url, init });
  let body = {};
  if (url === "/api/admin/operation-batches") body = { items: [data.plan] };
  else if (url === "/api/admin/ai-assistant/plans/1")
    body = { plan: data.plan };
  else if (url === "/api/admin/operation-batches/1")
    body = structuredClone(data);
  else if (url.endsWith("/report")) body = { pending: true };
  else if (url.endsWith("/review")) {
    const input = JSON.parse(init.body);
    assert.equal(input.expected_version, data.rows[0].version);
    excluded = input.decision === "rejected";
    data.rows[0].review_state = input.decision;
    data.rows[0].version++;
    data.plan.version++;
  } else if (url.endsWith("/approve")) {
    assert.equal(JSON.parse(init.body).expected_version, data.plan.version);
    approved++;
    data.plan.state = "dispatching";
    data.rows[0].state = "queued";
  } else throw new Error("unexpected URL " + url);
  return { ok: true, status: 200, json: async () => body };
};
win.eval(bundle.outputFiles[0].text + ";window.ExcelTest=ExcelTest;");
assert.equal(await win.ExcelTest.mountExcelDetailIfNeeded(), true);
assert.equal(win.document.querySelectorAll("script").length, 0);
assert.ok(win.document.body.textContent.includes("<script>unsafe</script>"));
const click = async (text) => {
  const b = [...win.document.querySelectorAll("button")].find(
    (x) => x.textContent === text,
  );
  assert.ok(b, text);
  b.click();
  await new Promise((r) => setTimeout(r, 30));
};
await click("排除");
assert.equal(excluded, true);
assert.ok(win.document.body.textContent.includes("预计创建 0 个企微任务"));
await click("恢复");
assert.equal(excluded, false);
await click("批准并创建群发任务");
assert.equal(approved, 1);
assert.ok(win.document.body.textContent.includes("待提交：1"));
assert.equal(
  [...win.document.querySelectorAll("button")].some(
    (x) => x.textContent === "批准并创建群发任务",
  ),
  false,
);
assert.equal(calls.filter((x) => x.url.endsWith("/approve")).length, 1);
data.plan.state = "pending_review";
win.document.body.replaceChildren();
const operations = win.document.createElement("main");
win.document.body.append(operations);
await win.ExcelTest.mountExcelBatchPanel(operations);
await click("查看执行与效果");
assert.ok(win.document.body.textContent.includes("效果观察"));
assert.ok(win.document.body.textContent.includes("进入 AI 助手审核"));
assert.equal(
  [...win.document.querySelectorAll("button")].some(
    (b) => b.textContent === "批准并创建群发任务",
  ),
  false,
);
dom.window.close();
console.log("Excel DOM review/exclude/restore/single approval: PASS");
