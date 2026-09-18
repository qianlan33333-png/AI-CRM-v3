import { JSDOM } from "jsdom";
import fs from "node:fs";
import assert from "node:assert/strict";
const script = fs.readFileSync(
  new URL("./admin_core_operations.js", import.meta.url),
  "utf8",
);
const calls = [];
const dom = new JSDOM('<div id="coreOperationsRoot"></div>', {
  runScripts: "outside-only",
  url: "https://crm.test/admin/automation-conversion",
});
dom.window.AudienceOperationsHTTP = {
  errorState: () => ({ message: "读取失败" }),
  request: async (path, options = {}) => {
    calls.push({ path, options });
    if (path.endsWith("core/products")) return { data: [] };
    if (path.endsWith("core/prompt/history"))
      return { data: [{ id: 1, body: "历史判断规则" }] };
    if (path.endsWith("core/prompt"))
      return {
        data: {
          draft: "当前草稿",
          version: 2,
          published_id: 1,
          ...(options.body ? { version: 3 } : {}),
        },
      };
    if (path.includes("packages?"))
      return { items: [{ id: 8, name: "现有人群包" }] };
    if (path.endsWith("core/recommendations"))
      return { data: { items: [{ id: 11, customer_id: 7, state: "queued" }] } };
    throw new Error("unexpected " + path);
  },
};
dom.window.eval(script);
await new Promise((r) => setTimeout(r, 10));
const document = dom.window.document;
assert.equal(document.querySelectorAll("form").length, 5);
const history = document.querySelector("select.ai-select:not(form select)");
history.value = "1";
history.dispatchEvent(new dom.window.Event("change"));
assert.equal(
  [...document.querySelectorAll("textarea")].at(-1).value,
  "历史判断规则",
);
const customers = document.querySelector("input[placeholder]");
customers.value = "7";
const preview = [...document.querySelectorAll("button")].find(
  (b) => b.textContent === "试运行（不入包）",
);
preview.click();
await new Promise((r) => setTimeout(r, 10));
const mutations = calls.filter((c) => c.options.method === "POST");
assert.equal(mutations.length, 2);
assert.equal(mutations[0].options.body.publish, false);
assert.equal(mutations[0].options.body.body, "历史判断规则");
assert.equal(mutations[1].options.body.preview, true);
assert.deepEqual(Array.from(mutations[1].options.body.customer_ids), [7]);
assert.ok(!calls.some((c) => c.path.endsWith("core/assignments")));
dom.window.close();
console.log(
  "core operations host: five slots, prompt restore and preview-only submission passed",
);
