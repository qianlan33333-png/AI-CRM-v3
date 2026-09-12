import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM, VirtualConsole } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(root, "dist", "admin");
const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json" },
});
const host = await buildTestBrowserBundle(path.join(root, "v3", "imageLibraryFilterHost.ts"));
const admin = await buildTestBrowserBundle(path.join(root, "src", "admin", "main.ts"));

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

const enabled = item(11, "默认启用素材");
const fresh = item(12, "新的搜索结果");
const stale = item(13, "旧的搜索结果");
const disabled = item(14, "已停用素材", false);
const calls = [];
let releaseStale;

const source = fs.readFileSync(path.join(dist, "images.html"), "utf8");
const html = source
  .replace(/<script type="module" src="[^"]+"><\/script>/g, "")
  .replace("</body>", () => `<script>${host}</script><script>${admin}</script></body>`);
const virtualConsole = new VirtualConsole();
virtualConsole.forwardTo(console);
const dom = new JSDOM(html, {
  url: "https://test.invalid/admin/image-library",
  runScripts: "dangerously",
  pretendToBeVisual: true,
  virtualConsole,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.Request = Request;
    window.fetch = async (input, init = {}) => {
      const isURL = typeof input === "object" && input !== null && "href" in input;
      const raw = typeof input === "string" ? input : isURL ? input.href : input.url;
      const url = new URL(raw, window.location.origin);
      const method = String(init.method || (typeof input === "string" || isURL ? "GET" : input.method)).toUpperCase();
      calls.push({ path: url.pathname, query: url.searchParams.toString(), method });
      if (url.pathname.startsWith("/api/admin/image-library/") && url.pathname.includes("/variants/"))
        return new Response("fixture-image", { status: 200, headers: { "Content-Type": "image/png" } });
      if (url.pathname !== "/api/admin/image-library" || method !== "GET")
        return json({ code: "unexpected", path: url.pathname, method }, 500);
      const query = url.searchParams.get("q") || "";
      const enabledOnly = url.searchParams.get("enabled_only");
      if (query === "旧")
        return new Promise((resolve) => {
          releaseStale = () => resolve(json({ items: [stale] }));
        });
      if (query === "失败") return json({ code: "unavailable" }, 503);
      if (query === "新") return json({ items: enabledOnly === "false" ? [fresh, disabled] : [fresh] });
      return json({ items: enabledOnly === "false" ? [enabled, disabled] : [enabled] });
    };
  },
});

async function waitFor(predicate, label) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (predicate()) return;
    await sleep(10);
  }
  throw new Error(`image-library filter Host regression: ${label}; stage=${dom.window.document.getElementById("stage")?.textContent?.trim()}; calls=${JSON.stringify(calls)}`);
}

function controls() {
  const input = dom.window.document.querySelector('input[data-image-library-query="true"]');
  const includeInactive = dom.window.document.querySelector('input[data-image-library-include-inactive="true"]');
  const reset = dom.window.document.querySelector('button[data-image-library-reset="true"]');
  assert.ok(input instanceof dom.window.HTMLInputElement, "actual image donor query input was not bound");
  assert.ok(includeInactive instanceof dom.window.HTMLInputElement, "actual image donor inactive checkbox was not bound");
  assert.ok(reset instanceof dom.window.HTMLButtonElement, "actual image donor reset button was not bound");
  return { input, includeInactive, reset };
}

await waitFor(() => Boolean(dom.window.document.querySelector('input[data-image-library-query="true"]')), "initial donor image page did not mount");
assert.equal(Object.prototype.hasOwnProperty.call(dom.window.Object.prototype, "__render"), false, "image Host did not restore the donor-controller capture seam");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query === "limit=100&offset=0&enabled_only=true"),
  `default image-library read did not explicitly request the bounded enabled-only page: ${JSON.stringify(calls)}`,
);
assert.ok(
  calls.filter((call) => call.path === "/api/admin/image-library").every((call) =>
    call.query.includes("limit=100") && call.query.includes("offset=0") && call.query.includes("enabled_only=")),
  `an image-library read escaped the bounded filter contract: ${JSON.stringify(calls)}`,
);
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "default enabled image was not rendered");

let current = controls();
current.input.value = "旧";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => typeof releaseStale === "function", "first search did not start");
current.input.value = "新";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("新的搜索结果"), "newer search result did not render");
releaseStale();
await sleep(30);
assert.ok(!dom.window.document.body.textContent.includes("旧的搜索结果"), "stale search response overwrote the newer result");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=true")),
  "search did not send q with enabled_only=true",
);

current = controls();
current.includeInactive.checked = true;
current.includeInactive.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("已停用素材"), "include-inactive read did not render a disabled image");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=false")),
  "include-inactive read did not send enabled_only=false",
);

current = controls();
current.reset.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "reset did not restore the default result");
current = controls();
assert.equal(current.input.value, "", "reset did not clear the donor search input");
assert.equal(current.includeInactive.checked, false, "reset did not restore enabled-only filtering");
assert.ok(
  calls.filter((call) => call.path === "/api/admin/image-library").at(-1).query === "limit=100&offset=0&enabled_only=true",
  "reset did not restore the default bounded query",
);

current.input.value = "失败";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
await waitFor(() => Boolean(dom.window.document.querySelector('[data-image-library-filter-feedback][role="alert"]')), "failed read did not show a retryable in-context error");
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "failed read discarded the last successful image list");
assert.equal(current.input.value, "失败", "failed read discarded the query being retried");

dom.window.close();
console.log("image-library filter Host actual donor DOM: PASS");
