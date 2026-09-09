import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
const code = readFileSync(
  new URL("./tag_sync_bridge.js", import.meta.url),
  "utf8",
);
const dom = new JSDOM(
  `<main id="stage"><button data-tag-group-card aria-pressed="true"><span>Group</span></button><table><tr><td>Known</td><td><button>复制 tag_id</button></td></tr><tr><td>Pending</td><td><button>复制 tag_id</button></td></tr></table><div><code>22</code><button>复制</button></div></main>`,
  { url: "https://test.invalid/admin/wecom-tags", runScripts: "outside-only" },
);
const { window } = dom;
const copies = [];
const retries = [];
Object.defineProperty(window.navigator, "clipboard", {
  value: { writeText: async (text) => copies.push(text) },
});
window.fetch = async (url, options) => {
  if (String(url).endsWith("/retry")) {
    retries.push(options);
    return { ok: true, json: async () => ({ ok: true }) };
  }
  if (String(url).endsWith("/sync-status"))
    return {
      ok: true,
      json: async () => ({ sync: { state: "idle", active: false } }),
    };
  return {
    ok: true,
    json: async () => ({
      groups: [{ group_id: 11, group_name: "Group" }],
      tags: [
        {
          tag_id: 22,
          group_id: 11,
          tag_name: "Known",
          provider_tag_id: "provider-real-tag",
        },
        { tag_id: 23, group_id: 11, tag_name: "Pending", provider_tag_id: "" },
      ],
      mutation_recoveries: [
        {
          id: 3,
          operation: "tag_create",
          name: "Pending",
          state: "final_failed",
        },
        {
          id: 4,
          operation: "tag_create",
          name: "Unknown",
          state: "outcome_unknown",
        },
      ],
    }),
  };
};
window.eval(code);
window.document.dispatchEvent(new window.Event("DOMContentLoaded"));
await new Promise((resolve) => setTimeout(resolve, 30));
const buttons = window.document.querySelectorAll("tr button");
buttons[0].click();
await Promise.resolve();
assert.deepEqual(copies, ["provider-real-tag"]);
buttons[1].click();
await Promise.resolve();
assert.equal(copies.length, 1, "unbound local ID must never be copied");
[...window.document.querySelectorAll("button")]
  .find((x) => x.textContent === "复制")
  .click();
await Promise.resolve();
assert.deepEqual(copies, ["provider-real-tag", "provider-real-tag"]);
const recover = window.document.querySelectorAll(
  "[data-tag-mutation-recovery] button",
);
assert.equal(recover.length, 1, "unknown outcome must not have retry");
window.document.cookie = "aicrm_admin_csrf=csrf-value";
recover[0].click();
await new Promise((resolve) => setTimeout(resolve, 20));
recover[0].click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(retries.length, 2);
assert.equal(
  retries[0].headers["Idempotency-Key"],
  retries[1].headers["Idempotency-Key"],
);
assert.equal(retries[0].headers["X-CSRF-Token"], "csrf-value");
assert.equal(
  window.document.querySelectorAll("[data-tag-mutation-recovery] button")
    .length,
  1,
);
dom.window.close();
console.log("tag Host Provider ID / explicit safe recovery: PASS");
