import { JSDOM } from "jsdom";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(root, "dist", "admin");
const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const fail = (message) => { throw new Error(`material save Host regression: ${message}`); };
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const host = await buildTestBrowserBundle(path.join(root, "v3", "materialSaveAdapter.ts"));
const admin = await buildTestBrowserBundle(path.join(root, "src", "admin", "main.ts"));

// The byte-frozen controller intentionally starts its mutations with `void`
// and has no catch for these three edit paths. Node reports that browser
// promise separately from `window.unhandledrejection`; the Host assertion
// below verifies the visible failure/retry outcome at the HTTP boundary.
process.on("unhandledRejection", () => {});

function pageFixture(page, fetcher) {
  const source = fs.readFileSync(path.join(dist, page), "utf8");
  const html = source.replace(/<script type="module" src="[^"]+"><\/script>/g, "")
    .replace("</body>", () => `<script>${host}</script><script>${admin}</script></body>`);
  return new JSDOM(html, {
    url: `https://test.invalid/admin/${page}`,
    runScripts: "dangerously",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response;
      window.Headers = Headers;
      window.Request = Request;
      window.fetch = fetcher(window);
    },
  });
}

const mediaRows = {
  image: { items: [{ id: 11, name: "原图片", file_name: "old.png", mime_type: "image/png", file_size: 32, description: "原说明", tags: ["旧标签"], category: "海报", enabled: true, created_at: "2026-09-08T00:00:00Z", original_url: "/api/admin/image-library/11/variants/original", thumb_320_url: "/api/admin/image-library/11/variants/thumb_320" }] },
  attachment: { items: [{ id: "12", name: "原附件", file_name: "old.pdf", mime_type: "application/pdf", file_size: 32, description: "原说明", tags: ["旧标签"], enabled: true, created_at: "2026-09-08T00:00:00Z", version: 1 }] },
  mini: { items: [{ id: 13, name: "原小程序", appid: "wx-old", pagepath: "pages/old", title: "旧标题", enabled: true }], total: 1 },
};

function button(document, label) {
  const found = [...document.querySelectorAll("button")].find((item) => item.textContent?.trim() === label);
  if (!found) fail(`actual frozen template has no ${label} button: ${document.getElementById("stage")?.textContent?.trim()} / buttons=${[...document.querySelectorAll("button")].map((item) => item.textContent?.trim()).join(",")}`);
  return found;
}

function setInput(window, id, value) {
  const input = window.document.getElementById(id);
  if (!(input instanceof window.HTMLInputElement)) fail(`actual frozen template has no ${id} input`);
  input.value = value;
  input.dispatchEvent(new window.Event("input", { bubbles: true }));
}

async function waitFor(condition, description) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (condition()) return;
    await sleep(10);
  }
  fail(description);
}

// Images use the frozen edit form. A successful PUT must keep the original
// button locked until its post-write collection read resolves.
{
  let imageReads = 0;
  let releaseReadback;
  let imageWrites = 0;
  const imageCalls = [];
  const dom = pageFixture("images.html", (window) => async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
    const method = (init.method || "GET").toUpperCase();
    imageCalls.push(`${method} ${url.pathname}`);
    if (url.pathname === "/api/admin/image-library" && method === "GET") {
      imageReads += 1;
      if (imageWrites > 0) return new Promise((resolve) => { releaseReadback = () => resolve(json(mediaRows.image)); });
      return json(mediaRows.image);
    }
    if (url.pathname === "/api/admin/image-library/11" && method === "PUT") { imageWrites += 1; return json({ item: { id: "11" } }); }
    return json({ code: "unexpected", path: url.pathname, method }, 500);
  });
  await sleep(100);
  button(dom.window.document, "编辑").click();
  await sleep(10);
  setInput(dom.window, "fImgName", "更新图片");
  const save = button(dom.window.document, "保存");
  save.click();
  save.click();
  await waitFor(() => imageWrites === 1, `image edit did not issue exactly one write after repeated clicks (${imageCalls.join(", ")})`);
  if (!save.disabled || save.textContent?.trim() !== "保存中…") fail("image editor unlocked before its real collection readback");
  if (!dom.window.document.body.textContent?.includes("素材已保存")) fail("image success feedback from the frozen controller disappeared");
  await waitFor(() => typeof releaseReadback === "function", "image controller did not start the post-write collection readback");
  releaseReadback();
  await waitFor(() => !save.disabled && save.textContent?.trim() === "保存", "image editor did not release after its real collection readback");
  dom.window.close();
}

// The actual mini-program create form proves both duplicate-create prevention
// and failure recovery. The second click is dispatched while the POST is live;
// the retry happens after a failed response and keeps the entered fields.
{
  let miniWrites = 0;
  let failFirstWrite = true;
  let releaseReadback;
  let miniReads = 0;
  const miniKeys = [];
  const dom = pageFixture("mpLib.html", (window) => async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
    const method = (init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/miniprogram-library" && method === "GET") {
      miniReads += 1;
      if (miniReads === 2) return new Promise((resolve) => { releaseReadback = () => resolve(json(mediaRows.mini)); });
      return json(mediaRows.mini);
    }
    if (url.pathname === "/api/admin/miniprogram-library" && method === "POST") {
      miniWrites += 1;
      miniKeys.push(new Headers(init.headers).get("Idempotency-Key"));
      if (failFirstWrite) { failFirstWrite = false; return json({ code: "unavailable" }, 503); }
      return json({ item: { id: 14 } });
    }
    return json({ code: "unexpected", path: url.pathname, method }, 500);
  });
  await sleep(100);
  button(dom.window.document, "新建小程序卡片").click();
  await sleep(10);
  setInput(dom.window, "fMpName", "新卡片");
  setInput(dom.window, "fMpAppid", "wx-new");
  setInput(dom.window, "fMpPath", "pages/new");
  const create = button(dom.window.document, "创建");
  create.click();
  create.click();
  await waitFor(() => miniWrites === 1, "mini-program create wrote twice while the first request was live");
  await waitFor(() => !create.disabled, "failed mini-program create left the actual form permanently locked");
  if (!dom.window.document.body.textContent?.includes("素材保存失败；编辑内容已保留，可修正后重试。")) fail("mini-program failure did not keep a retryable local-error message");
  if (dom.window.document.getElementById("fMpName")?.value !== "新卡片") fail("failed mini-program create lost entered values");
  create.click();
  await waitFor(() => miniWrites === 2, "mini-program retry did not issue its one replacement write");
  if (miniKeys[0] === miniKeys[1]) fail("definitive HTTP failure incorrectly retained an old material idempotency key");
  if (!create.disabled || create.textContent?.trim() !== "保存中…") fail("mini-program create unlocked before delayed readback");
  await waitFor(() => typeof releaseReadback === "function", "mini-program controller did not start the post-write collection readback");
  releaseReadback();
  await waitFor(() => !create.disabled && create.textContent?.trim() === "创建", "mini-program create did not release after delayed readback");
  if (!dom.window.document.body.textContent?.includes("小程序卡片已创建")) fail("mini-program success feedback from the frozen controller disappeared");
  dom.window.close();
}

// A successful create followed by a 503 list read remains recoverable. The
// same form values replay the original key, then the next read verifies the
// one resource; changing values while that recovery is pending is blocked.
{
  let posts = 0, creates = 0, reads = 0;
  const keys = [];
  const dom = pageFixture("mpLib.html", (window) => async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
    const method = (init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/miniprogram-library" && method === "GET") {
      reads += 1;
      return reads === 2 ? json({ code: "readback_unavailable" }, 503) : json(mediaRows.mini);
    }
    if (url.pathname === "/api/admin/miniprogram-library" && method === "POST") {
      posts += 1; const key = new Headers(init.headers).get("Idempotency-Key"); keys.push(key);
      if (posts === 1) { creates += 1; return json({ item: { id: 16 } }); }
      return key === keys[0] ? json({ item: { id: 16 }, replayed: true }) : json({ code: "duplicate" }, 500);
    }
    return json({ code: "unexpected", path: url.pathname, method }, 500);
  });
  const fill = async (name) => { button(dom.window.document, "新建小程序卡片").click(); await sleep(10); setInput(dom.window, "fMpName", name); setInput(dom.window, "fMpAppid", "wx-recover"); setInput(dom.window, "fMpPath", "pages/recover"); button(dom.window.document, "创建").click(); };
  await sleep(100); await fill("回读失败卡片");
  await waitFor(() => reads === 2 && dom.window.document.body.textContent?.includes("素材已保存，但回读失败"), "saved mini-program readback failure was not preserved as partial success");
  await waitFor(() => !![...dom.window.document.querySelectorAll("button")].find((item) => item.textContent?.trim() === "新建小程序卡片"), "mini-program page did not recover its create entry after failed readback");
  await fill("已修改内容");
  await sleep(30);
  if (posts !== 1 || !dom.window.document.body.textContent?.includes("不能直接修改后再次创建")) fail("changed content after saved readback failure was allowed to create another resource");
  await fill("回读失败卡片");
  await waitFor(() => posts === 2 && reads === 3, "saved mini-program recovery did not replay the original request and readback");
  if (creates !== 1 || keys[0] !== keys[1]) fail("saved mini-program readback retry created another resource");
  dom.window.close();
}

// A dropped response can follow a committed create. The next click with the
// same frozen form must replay its original Idempotency-Key, so the server
// returns the one committed resource instead of creating another one.
{
  let creates = 0;
  const keys = [];
  let lost = true;
  const dom = pageFixture("mpLib.html", (window) => async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
    const method = (init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/miniprogram-library" && method === "GET") return json(mediaRows.mini);
    if (url.pathname === "/api/admin/miniprogram-library" && method === "POST") {
      const key = new Headers(init.headers).get("Idempotency-Key");
      keys.push(key);
      if (lost) { lost = false; creates += 1; throw new TypeError("response lost after commit"); }
      if (key !== keys[0]) return json({ code: "new_key_would_duplicate" }, 500);
      return json({ item: { id: 15 }, replayed: true });
    }
    return json({ code: "unexpected", path: url.pathname, method }, 500);
  });
  await sleep(100);
  button(dom.window.document, "新建小程序卡片").click();
  await sleep(10);
  setInput(dom.window, "fMpName", "丢响应卡片"); setInput(dom.window, "fMpAppid", "wx-lost"); setInput(dom.window, "fMpPath", "pages/lost");
  const create = button(dom.window.document, "创建");
  create.click();
  await waitFor(() => !create.disabled, "lost create response left the editor locked");
  if (!dom.window.document.body.textContent?.includes("素材保存结果未知")) fail("lost create response was presented as a definite failure");
  create.click();
  await waitFor(() => keys.length === 2, "unknown create was not retried");
  await waitFor(() => !create.disabled, "replayed create did not finish after its authoritative list read");
  if (creates !== 1 || keys[0] !== keys[1]) fail("lost create response retried with a new key and could duplicate the resource");
  dom.window.close();
}

// Attachments take an additional authoritative detail GET before PUT. Only the
// later collection GET may complete the write: a pre-write lookup cannot
// unlock the form or permit a duplicate mutation.
{
  let attachmentReads = 0;
  let attachmentWrites = 0;
  let releaseReadback;
  const dom = pageFixture("attach.html", (window) => async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
    const method = (init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/attachment-library" && method === "GET") {
      attachmentReads += 1;
      if (attachmentWrites > 0) return new Promise((resolve) => { releaseReadback = () => resolve(json(mediaRows.attachment)); });
      return json(mediaRows.attachment);
    }
    if (url.pathname === "/api/admin/attachment-library/12" && method === "GET") return json(mediaRows.attachment.items[0]);
    if (url.pathname === "/api/admin/attachment-library/12" && method === "PUT") { attachmentWrites += 1; return json({ item: { id: "12" } }); }
    return json({ code: "unexpected", path: url.pathname, method }, 500);
  });
  await sleep(100);
  button(dom.window.document, "编辑").click();
  await sleep(10);
  setInput(dom.window, "fAttName", "更新附件");
  const save = button(dom.window.document, "保存");
  save.click();
  save.click();
  await waitFor(() => attachmentWrites === 1, "attachment edit did not block the duplicate write after its pre-write lookup");
  if (!save.disabled) fail("attachment editor unlocked before delayed collection readback");
  await waitFor(() => typeof releaseReadback === "function", "attachment controller did not start the post-write collection readback");
  releaseReadback();
  await waitFor(() => !save.disabled && save.textContent?.trim() === "保存", "attachment editor did not release after delayed collection readback");
  if (!dom.window.document.body.textContent?.includes("附件已保存")) fail("attachment success feedback from the frozen controller disappeared");
  dom.window.close();
}

// Exercise the actual frozen upload dialogs and generated multipart clients,
// not just edit forms. File selection and cancellation must remain visible;
// a delayed POST must not let the same dialog submit a second upload.
for (const spec of [
  { page: 'images.html', open: '上传图片', input: 'fImgUpFile', field: 'image', name: 'launch-upload.png', mime: 'image/png', path: '/api/admin/image-library', rows: mediaRows.image, result: { item_id: 11, item: { id: 11 } } },
  { page: 'attach.html', open: '上传附件', input: 'fAttUpFile', field: 'attachment', name: 'launch-upload.pdf', mime: 'application/pdf', path: '/api/admin/attachment-library', rows: mediaRows.attachment, result: { id: 12, item: { id: 12 }, version: 1 } },
]) {
  let writes = 0;
  let releaseUpload;
  let postedFile;
  let postedKey;
  const dom = pageFixture(spec.page, (window) => async (input, init = {}) => {
    const url = new URL(typeof input === 'string' ? input : input.url, window.location.origin);
    const method = (init.method || 'GET').toUpperCase();
    if (url.pathname === spec.path && method === 'GET') return json(writes ? spec.rows : { items: [] });
    if (url.pathname === `${spec.path}/upload` && method === 'POST') {
      writes += 1;
      postedFile = init.body.get(spec.field);
      postedKey = new Headers(init.headers).get('Idempotency-Key');
      return new Promise((resolve) => { releaseUpload = () => resolve(json(spec.result)); });
    }
    return json({ code: 'unexpected', path: url.pathname, method }, 500);
  });
  await waitFor(() => [...dom.window.document.querySelectorAll('button')].some((el) => el.textContent.trim() === spec.open), 'upload opener missing');
  button(dom.window.document, spec.open).click();
  await waitFor(() => dom.window.document.getElementById(`${spec.input}-status`), 'file selection status missing');
  const input = dom.window.document.getElementById(spec.input);
  input.dispatchEvent(new dom.window.Event('cancel', { bubbles: true }));
  if (!dom.window.document.getElementById(`${spec.input}-status`).textContent.includes('已取消选择')) fail('cancelled file picker has no feedback');
  const file = new dom.window.File(['file-content'], spec.name, { type: spec.mime });
  Object.defineProperty(input, 'files', { configurable: true, value: [file] });
  input.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  if (!dom.window.document.getElementById(`${spec.input}-status`).textContent.includes(spec.name)) fail('selected file name missing');
  const upload = button(dom.window.document, '上传');
  upload.click(); upload.click();
  await waitFor(() => typeof releaseUpload === 'function', 'multipart upload never started');
  if (writes !== 1 || postedFile?.name !== spec.name || postedFile?.size !== file.size || !postedKey || !upload.disabled) fail('upload did not preserve actual file, stable key and duplicate lock');
  releaseUpload();
  await waitFor(() => !dom.window.document.getElementById(spec.input), 'successful upload did not close dialog');
  await waitFor(() => dom.window.document.getElementById('stage')?.textContent.includes(spec.page === 'images.html' ? '原图片' : '原附件'), 'uploaded resource list did not read back');
  dom.window.close();
}

// The actual embedded Media page carries the refresh contract inside its
// existing scroll region. Loading errors are retryable, the manual full
// refresh is async, and a dropped POST response replays the same operation
// key rather than creating a second round.
{
  let preparationReads = 0;
  let releasePreparation;
  let fullPosts = 0;
  let releaseFull;
  const fullKeys = [];
  let roundReads = 0;
  const csrfHeaders = [];
  const contract = {
    ok: true,
    items: [{
      source_ref: "image:11",
      source_type: "image",
      content_digest: "sha256:source",
      file_name: "cover.png",
      media_type: "image/png",
      size_bytes: 32,
      snapshot_version: 2,
      state: "outcome_unknown",
      credential_state: "expired",
      credential_usable: false,
      failure_code: "upload_outcome_unknown",
      media_id: "temporary-media-id",
      last_succeeded_at: "2026-09-09T01:00:00Z",
      expires_at: "2026-09-12T01:00:00Z",
    }, {
      source_ref: "image:12",
      source_type: "image",
      content_digest: "sha256:source-still-valid",
      file_name: "last-valid.png",
      media_type: "image/png",
      size_bytes: 32,
      snapshot_version: 2,
      state: "final_failed",
      credential_state: "ready",
      credential_usable: true,
      failure_code: "provider_rejected",
      media_id: "still-valid-media-id",
      last_succeeded_at: "2026-09-10T01:00:00Z",
      expires_at: "2099-09-13T01:00:00Z",
    }, {
      source_ref: "image:13",
      source_type: "image",
      content_digest: "sha256:source-expired-by-clock",
      file_name: "expired-by-clock.png",
      media_type: "image/png",
      size_bytes: 32,
      snapshot_version: 2,
      state: "executed",
      credential_state: "ready",
      credential_usable: true,
      failure_code: "uploaded",
      media_id: "expired-media-id",
      last_succeeded_at: "2020-09-09T01:00:00Z",
      expires_at: "2020-09-12T01:00:00Z",
    }, {
      source_ref: "attachment:14",
      source_type: "attachment",
      content_digest: "sha256:source-missing",
      file_name: "never-uploaded.pdf",
      media_type: "application/pdf",
      size_bytes: 32,
      snapshot_version: 2,
      state: "missing",
      credential_state: "missing",
      credential_usable: false,
      failure_code: "",
      media_id: "",
      last_succeeded_at: null,
      expires_at: null,
    }],
    failures: [{
      source_ref: "attachment:19",
      failure_code: "source_bytes_missing",
      state: "final_failed",
      credential_state: "missing",
      credential_usable: false,
    }],
    today_refresh_round: { id: 5, local_date: "2026-09-10", state: "outcome_unknown" },
    next_refresh_at: "2026-09-11T02:00:00+08:00",
    next_cursor: "",
    done: true,
  };
  const dom = pageFixture("images.html", (window) => async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
    const method = (init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/image-library" && method === "GET") return json(mediaRows.image);
    if (url.pathname === "/api/admin/media-preparations" && method === "GET") {
      preparationReads += 1;
      if (preparationReads === 1) {
        return new Promise((resolve) => { releasePreparation = () => resolve(json({ code: "read_unavailable" }, 503)); });
      }
      return json(contract);
    }
    if (url.pathname === "/api/admin/media-preparations/refresh-rounds" && method === "POST") {
      fullPosts += 1;
      fullKeys.push(new Headers(init.headers).get("Idempotency-Key"));
      csrfHeaders.push(new Headers(init.headers).get("X-CSRF-Token"));
      if (fullPosts === 1) {
        return new Promise((_, reject) => { releaseFull = () => reject(new window.TypeError("response lost after commit")); });
      }
      return json({ ok: true, refresh_round: { id: 7, local_date: "2026-09-10", state: "queued", total: 1, queued: 1, succeeded: 0, failed: 0, unknown: 0 } }, 202);
    }
    if (url.pathname === "/api/admin/media-preparations/refresh-rounds/5" && method === "GET") {
      roundReads += 1;
      return json({ ok: true, refresh_round: { id: 5, local_date: "2026-09-10", state: "outcome_unknown", total: 100, queued: 0, succeeded: 98, failed: 0, unknown: 2 } });
    }
    if (url.pathname === "/api/admin/media-preparations/refresh-rounds/7" && method === "GET") {
      roundReads += 1;
      return json({ ok: true, refresh_round: { id: 7, local_date: "2026-09-10", state: "completed", total: 1, queued: 0, succeeded: 1, failed: 0, unknown: 0 } });
    }
    return json({ code: "unexpected", path: url.pathname, method }, 500);
  });
  dom.window.document.cookie = "aicrm_csrf=fixture-csrf";
  await waitFor(() => typeof releasePreparation === "function", "material preparation loading state never started");
  if (!dom.window.document.querySelector("[data-material-refresh-status]")?.textContent?.includes("正在读取")) fail("material preparation loading state is not visible");
  releasePreparation();
  await waitFor(() => !![...dom.window.document.querySelectorAll("button")].find((item) => item.textContent?.trim() === "重试读取刷新状态"), "material preparation read error did not expose a retry action");
  if (!dom.window.document.querySelector("[data-material-refresh-status]")?.textContent?.includes("刷新请求失败（HTTP 503）")) fail("material preparation read error was not presented in Chinese");
  [...dom.window.document.querySelectorAll("button")].find((item) => item.textContent?.trim() === "重试读取刷新状态").click();
  await waitFor(() => dom.window.document.querySelector("#material-refresh-panel")?.textContent?.includes("cover.png"), "material preparation contract did not render after manual retry");
  const panelText = dom.window.document.querySelector("#material-refresh-panel")?.textContent || "";
  if (
    panelText.includes("成功 0") ||
    !panelText.includes("结果待核实 · 凭据已过期") ||
    !panelText.includes("刷新失败 · 旧凭据仍可用") ||
    !panelText.includes("已就绪 · 凭据已过期") ||
    !panelText.includes("待首次刷新 · 尚无可用凭据") ||
    panelText.includes("已就绪 · 当前凭据可用") ||
    !panelText.includes("当日刷新进度暂不可用") ||
    !panelText.includes("下次运行")
  ) fail("material credential availability was inferred from the refresh state or expiry incorrectly");
  if (!panelText.includes("需要补传的原文件") || !panelText.includes("attachment:19：原文件缺失，请补传") || panelText.includes("source_bytes_missing")) fail("missing source was not kept as a locateable Chinese re-upload item");
  const uploadedRow = [...dom.window.document.querySelectorAll("#material-refresh-panel tr")].find((row) => row.textContent?.includes("expired-by-clock.png"));
  if (!uploadedRow || uploadedRow.querySelectorAll("td")[6]?.textContent?.trim() !== "—") fail("a successful uploaded receipt was rendered as a failure reason");
  if (![...dom.window.document.querySelectorAll("button")].find((item) => item.textContent?.trim() === "立即刷新全部启用素材")?.classList.contains("admin-button--primary")) fail("manual refresh did not use the existing primary-button styling");
  const progressRefresh = [...dom.window.document.querySelectorAll("button")].find((item) => item.textContent?.trim() === "刷新进度");
  progressRefresh.click();
  await waitFor(() => dom.window.document.querySelector("#material-refresh-panel")?.textContent?.includes("成功 98"), "explicit refresh-round counts were not rendered");
  const countedPanel = dom.window.document.querySelector("#material-refresh-panel")?.textContent || "";
  if (!countedPanel.includes("成功 98") || !countedPanel.includes("待核实 2")) fail("explicit refresh-round counts were not preserved");
  const full = [...dom.window.document.querySelectorAll("button")].find((item) => item.textContent?.trim() === "立即刷新全部启用素材");
  if (full.disabled) fail("manual full refresh button was already disabled before clicking");
  full.click(); full.click();
  await waitFor(() => fullPosts === 1 && typeof releaseFull === "function", "manual full refresh issued no request");
  if (!full.disabled || full.textContent?.trim() !== "刷新中…") fail("manual full refresh was not locked against a double click");
  assert.equal(csrfHeaders[0], "fixture-csrf");
  releaseFull();
  await waitFor(() => !full.disabled && dom.window.document.body.textContent?.includes("同一操作 key"), "dropped full-refresh response did not expose a retryable error");
  full.click();
  await waitFor(() => fullPosts === 2 && roundReads === 2, "manual full-refresh retry did not read the async round");
  if (!fullKeys[0] || fullKeys[0] !== fullKeys[1]) fail("dropped full-refresh response retried with a new idempotency key");
  dom.window.close();
}

console.log("material save Host frozen-template interactions: ok");
