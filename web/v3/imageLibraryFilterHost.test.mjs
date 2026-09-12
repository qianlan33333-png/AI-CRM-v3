import assert from "node:assert/strict";
import { JSDOM, VirtualConsole } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json" },
});
const imageHost = await buildTestBrowserBundle(new URL("./imageLibraryFilterHost.ts", import.meta.url).pathname);
const materialHost = await buildTestBrowserBundle(new URL("./materialSaveAdapter.ts", import.meta.url).pathname);

function item(id, name, enabled = true) {
  return {
    id,
    name,
    file_name: `${name}.png`,
    mime_type: "image/png",
    file_size: 32,
    description: "素材说明",
    tags: ["回归"],
    category: "海报",
    enabled,
    created_at: "2026-09-12T00:00:00Z",
    original_url: `/api/admin/image-library/${id}/variants/original`,
    thumb_320_url: `/api/admin/image-library/${id}/variants/thumb_320`,
  };
}

const first = item(11, "默认启用素材");
const secondPage = item(31, "第二页素材");
const fresh = item(12, "新的搜索结果");
const stale = item(13, "旧的搜索结果");
const inactive = item(14, "已停用素材", false);
const calls = [];
let releaseStale;
let releaseDialogRead;
let defaultName = first.name;
let failSecondPage = false;
let failNextReadback = false;
let loseFirstDeleteResponse = false;
let imageDeleted = false;

const virtualConsole = new VirtualConsole();
virtualConsole.forwardTo(console);
const dom = new JSDOM(`<!doctype html><html><body data-page="images"><main id="stage" data-image-library-v3-root></main><script>${materialHost}</script><script>${imageHost}</script></body></html>`, {
  url: "https://test.invalid/admin/image-library",
  runScripts: "dangerously",
  pretendToBeVisual: true,
  virtualConsole,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.Request = Request;
    window.confirm = () => true;
    window.fetch = async (input, init = {}) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, window.location.origin);
      const method = String(init.method || (typeof input === "string" || input instanceof URL ? "GET" : input.method)).toUpperCase();
      calls.push({ path: url.pathname, query: url.searchParams.toString(), method, body: init.body, headers: Object.fromEntries(new Headers(init.headers).entries()) });
      if (url.pathname === "/api/admin/media-preparations")
        return json({ items: [], failures: [], done: true, next_cursor: "" });
      if (url.pathname === "/api/admin/image-library" && method === "GET") {
        const query = url.searchParams.get("q") || "";
        const offset = Number(url.searchParams.get("offset") || "0");
        const enabledOnly = url.searchParams.get("enabled_only");
        if (query === "旧") return new Promise((resolve) => { releaseStale = () => resolve(json({ items: [stale], total: 1, limit: 20, offset, has_more: false })); });
        if (query === "弹窗延迟") return new Promise((resolve) => { releaseDialogRead = () => resolve(json({ items: [fresh], total: 1, limit: 20, offset, has_more: false })); });
        if (query === "失败") return json({ code: "unavailable" }, 503);
        if (query === "空") return json({ items: [], total: 0, limit: 20, offset, has_more: false });
        if (query === "新") return json({ items: enabledOnly === "false" ? [fresh, inactive] : [fresh], total: enabledOnly === "false" ? 2 : 1, limit: 20, offset, has_more: false });
        if (failNextReadback) {
          failNextReadback = false;
          return json({ code: "readback_unavailable" }, 503);
        }
        if (offset === 20 && failSecondPage) return json({ code: "page_unavailable" }, 503);
        if (offset === 20) return json({ items: [secondPage], total: 41, limit: 20, offset, has_more: true });
        return json({ items: imageDeleted ? [] : enabledOnly === "false" ? [item(11, defaultName), inactive] : [item(11, defaultName)], total: imageDeleted ? 0 : 41, limit: 20, offset, has_more: !imageDeleted });
      }
      if (url.pathname === "/api/admin/image-library/11" && method === "PUT") {
        const body = JSON.parse(String(init.body));
        defaultName = body.name;
        return json({ ok: true, item: item(11, defaultName) });
      }
      if (url.pathname === "/api/admin/image-library/11" && method === "DELETE") {
        if (loseFirstDeleteResponse) {
          loseFirstDeleteResponse = false;
          throw new window.TypeError("delete response lost");
        }
        imageDeleted = true;
        return json({ ok: true });
      }
      return json({ code: "unexpected", path: url.pathname, method }, 500);
    };
  },
});

async function waitFor(predicate, label) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (predicate()) return;
    await sleep(10);
  }
  throw new Error(`image-library V3 Host regression: ${label}; stage=${dom.window.document.getElementById("stage")?.textContent?.trim()}; calls=${JSON.stringify(calls)}`);
}

function controls() {
  const input = dom.window.document.querySelector('input[data-image-library-query="true"]');
  const includeInactive = dom.window.document.querySelector('input[data-image-library-include-inactive="true"]');
  const reset = dom.window.document.querySelector('button[data-image-library-reset="true"]');
  assert.ok(input instanceof dom.window.HTMLInputElement, "source-owned image query input was not mounted");
  assert.ok(includeInactive instanceof dom.window.HTMLInputElement, "source-owned inactive checkbox was not mounted");
  assert.ok(reset instanceof dom.window.HTMLButtonElement, "source-owned reset was not mounted");
  return { input, includeInactive, reset };
}

await waitFor(() => Boolean(dom.window.document.querySelector('input[data-image-library-query="true"]')), "Host did not mount");
await waitFor(() => Boolean(dom.window.document.querySelector('[data-material-refresh="true"]')), "MaterialSaveHost refresh/credential panel was not preserved");
assert.ok(dom.window.document.querySelector('button[data-material-refresh-all]'), "MaterialSaveHost refresh action is not usable from the V3 image workspace");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query === "limit=20&offset=0&enabled_only=true"),
  `default image-library read did not explicitly request a bounded enabled-only page: ${JSON.stringify(calls)}`,
);
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "default enabled image was not rendered");
assert.ok(dom.window.document.body.textContent.includes("已启用"), "enabled image did not render a Chinese status");
assert.ok(dom.window.document.body.textContent.includes("2026-09-12 08:00:00"), "image time did not render in Asia/Shanghai YYYY-MM-DD HH:mm:ss form");
assert.ok(!dom.window.document.body.textContent.includes("2026-09-12T00:00:00Z"), "raw ISO time leaked into the image workspace");

let current = controls();
current.input.value = "旧";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => typeof releaseStale === "function", "debounced first search did not start");
const focusedQuery = current.input;
focusedQuery.focus();
current.input.value = "新";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("新的搜索结果"), "newer search result did not render");
assert.equal(dom.window.document.querySelector('[data-image-library-query="true"]'), focusedQuery, "debounced search rebuilt the focused query input");
assert.equal(dom.window.document.activeElement, focusedQuery, "debounced search lost query focus");
assert.equal(focusedQuery.value, "新", "debounced search lost the current query text");
releaseStale();
await sleep(30);
assert.ok(!dom.window.document.body.textContent.includes("旧的搜索结果"), "stale search response overwrote the newer result");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=true")),
  "search did not send q with enabled_only=true",
);

current = controls();
current.input.value = "弹窗延迟";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => typeof releaseDialogRead === "function", "delayed dialog read did not start");
const upload = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "上传图片");
assert.ok(upload, "upload action missing from source-owned Host");
upload.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgUpFile")), "upload dialog did not open during pending read");
const pendingFile = dom.window.document.querySelector("#fImgUpFile");
const pendingName = dom.window.document.querySelector("#fImgUpName");
assert.ok(pendingFile instanceof dom.window.HTMLInputElement && pendingName instanceof dom.window.HTMLInputElement, "upload controls missing");
const selectedFile = new dom.window.File(["image"], "keep-selected.png", { type: "image/png" });
Object.defineProperty(pendingFile, "files", { configurable: true, value: { 0: selectedFile, length: 1 } });
pendingName.value = "仍在编辑的素材名称";
releaseDialogRead();
await waitFor(() => dom.window.document.body.textContent.includes("新的搜索结果"), "pending dialog read did not finish");
assert.equal(dom.window.document.querySelector("#fImgUpFile"), pendingFile, "read completion rebuilt the upload file input");
assert.equal(pendingFile.files?.[0], selectedFile, "read completion discarded the selected upload file");
assert.equal(pendingName.value, "仍在编辑的素材名称", "read completion discarded in-progress upload fields");
dom.window.document.querySelector('button[aria-label="关闭弹窗"]')?.click();
current = controls();
const callsBeforeReturningQuery = calls.length;
current.input.value = "新";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => calls.slice(callsBeforeReturningQuery).some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=true")), "query did not return to the active filter after dialog preservation check");

current = controls();
current.includeInactive.checked = true;
current.includeInactive.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("已停用素材"), "include-inactive read did not render a disabled image");
assert.ok(dom.window.document.body.textContent.includes("已停用"), "disabled image did not render a Chinese status");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=false")),
  "include-inactive read did not send enabled_only=false",
);

current = controls();
current.reset.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "reset did not restore the default result");
current = controls();
assert.equal(current.input.value, "", "reset did not clear the query");
assert.equal(current.includeInactive.checked, false, "reset did not restore enabled-only filtering");

const next = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "下一页");
assert.ok(next, "image pagination next control missing");
next.click();
await waitFor(() => dom.window.document.body.textContent.includes("第二页素材"), "next page did not load images after the first 20");
assert.ok(calls.some((call) => call.path === "/api/admin/image-library" && call.query === "limit=20&offset=20&enabled_only=true"), "next page did not use a reachable offset");

const previous = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "上一页");
assert.ok(previous, "image pagination previous control missing");
previous.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "previous page did not return to the first results");

current = controls();
current.input.value = "空";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => Boolean(dom.window.document.querySelector("[data-image-library-empty]")), "empty image result did not render its explicit empty state");
current = controls();
current.input.value = "";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "successful empty-filter reset did not restore the latest list");

current = controls();
current.input.value = "失败";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => Boolean(dom.window.document.querySelector('[data-image-library-filter-feedback][role="alert"]')), "failed read did not show an in-context error");
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "failed read discarded the last successful image list");
assert.equal(current.input.value, "失败", "failed read discarded the query being retried");
assert.ok(dom.window.document.body.textContent.includes("仍显示上一次成功结果"), "failed read did not identify the preserved result as stale");

current.input.value = "";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "successful retry did not restore the latest list");

failSecondPage = true;
const failingNext = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "下一页");
assert.ok(failingNext, "next page action missing before failed-page retry");
failingNext.click();
await waitFor(() => Boolean(dom.window.document.querySelector('[data-image-library-filter-feedback][role="alert"]')), "failed second page did not show an in-context error");
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "failed second page discarded the first successful page");
assert.ok(dom.window.document.querySelector('button[data-image-library-retry="true"]'), "failed second page did not offer an explicit retry");
assert.ok(!calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("offset=40")), "failed page advanced the pagination offset before retry");
failSecondPage = false;
dom.window.document.querySelector('button[data-image-library-retry="true"]')?.click();
await waitFor(() => dom.window.document.body.textContent.includes("第二页素材"), "retry did not request the failed second page");
assert.ok(!calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("offset=40")), "retry skipped from page two to page three");
const retryPrevious = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "上一页");
assert.ok(retryPrevious, "previous page action missing after retry");
retryPrevious.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "previous page did not restore the successful first page after retry");

const edit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(edit, "edit action missing from source-owned card");
edit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "edit dialog did not open");
const name = dom.window.document.querySelector("#fImgName");
assert.ok(name instanceof dom.window.HTMLInputElement, "edit name field missing");
name.value = "已更新素材";
const save = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "保存");
assert.ok(save, "edit save action missing");
failNextReadback = true;
save.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")) && dom.window.document.body.textContent.includes("素材已保存，但列表回读失败"), "write/readback failure did not preserve the edit dialog");
assert.equal(dom.window.document.querySelector("#fImgName"), name, "write/readback failure rebuilt the edit input");
assert.equal(name.value, "已更新素材", "write/readback failure discarded the edited name");
assert.equal([...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "重新读取列表")?.disabled, false, "write/readback failure did not offer a read-only retry");
assert.ok(calls.some((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT"), "edit did not reuse the typed Media update route");
const writeAt = calls.findIndex((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT");
assert.ok(calls.slice(writeAt + 1).some((call) => call.path === "/api/admin/image-library" && call.method === "GET"), "successful edit did not perform its required list readback");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT").length, 1, "readback failure retried the write instead of preserving its idempotent result");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => !dom.window.document.querySelector("#fImgName") && dom.window.document.body.textContent.includes("已更新素材"), "readback retry did not confirm the saved edit");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT").length, 1, "readback retry repeated the saved mutation");

const deleteEdit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(deleteEdit, "edit action missing before delete recovery check");
deleteEdit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "delete recovery edit dialog did not open");
loseFirstDeleteResponse = true;
const remove = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "删除");
assert.ok(remove, "delete action missing from source-owned dialog");
remove.click();
await waitFor(() => dom.window.document.body.textContent.includes("删除结果暂不可确认"), "lost delete response did not require a readback check");
assert.equal(remove.disabled, true, "lost delete response left the destructive button enabled");
const deleteCalls = () => calls.filter((call) => call.path === "/api/admin/image-library/11" && call.method === "DELETE");
assert.equal(deleteCalls().length, 1, "lost delete response triggered a duplicate request");
const deleteKey = deleteCalls()[0].headers["idempotency-key"];
assert.ok(typeof deleteKey === "string" && deleteKey.startsWith("image-delete-"), "delete did not send a controlled idempotency key");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => [...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "按原操作重试删除"), "delete verification did not offer a same-intent retry");
assert.equal(deleteCalls().length, 1, "delete verification sent another destructive request");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => !dom.window.document.querySelector("#fImgName") && Boolean(dom.window.document.querySelector("[data-image-library-empty]")), "same-key delete retry did not read back the removed image");
assert.equal(deleteCalls().length, 2, "same-intent retry did not issue exactly one follow-up delete");
assert.equal(deleteCalls()[1].headers["idempotency-key"], deleteKey, "delete retry changed the original idempotency key");

for (const call of calls.filter((call) => call.path === "/api/admin/image-library" && call.method === "GET")) {
  assert.ok(call.query.includes("limit=20") && call.query.includes("offset=") && call.query.includes("enabled_only="), `image read escaped bounded pagination/filter contract: ${JSON.stringify(call)}`);
}

dom.window.close();
console.log("image-library V3 Host DOM: PASS");
