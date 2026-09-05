import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../../../../web/scripts/test-browser-bundle.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../..");
const host = fs.readFileSync(path.join(root, "internal/webshell/static/admin_console/survey_operations.js"), "utf8");
const page = fs.readFileSync(path.join(root, "web/dist/admin/questionnaireOps.html"), "utf8");
const bundle = await buildTestBrowserBundle(path.join(root, "web/src/admin/main.ts"));
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });

const dom = new JSDOM(page, {
  url: "https://test.invalid/admin/questionnaireOps.html?id=7",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.confirm = () => true;
    window.Headers = globalThis.Headers;
    window.Response = globalThis.Response;
  },
});
Object.defineProperty(dom.window.document, "cookie", { value: "aicrm_admin_csrf=csrf-proof", configurable: true });
Object.defineProperty(dom.window.crypto, "randomUUID", { value: () => "00000000-0000-4000-8000-000000000000" });
let testPosts = 0;
let metadataVersion = 3;
const httpCalls = [];
dom.window.fetch = async (url, options = {}) => {
  const target = String(url);
  httpCalls.push(target);
  if (target.startsWith("/api/admin/questionnaires?")) return reply({ items: [{ id: 7, name: "compiled-survey", title: "编译问卷", description: "", status: "published", is_disabled: false, public_path: "/q/compiled-survey", assessment_enabled: false, answer_display_mode: "all_in_one", submission_count: 0, slug: "compiled-survey", questions: [], score_rules: [], version: 1 }], total: 1 });
  if (target.startsWith("/api/admin/channels?")) return reply({ items: [], total: 0 });
  if (target === "/api/admin/questionnaires/7") return reply({ questionnaire: { id: 7, name: "compiled-survey", title: "编译问卷", description: "", status: "published", is_disabled: false, public_path: "/q/compiled-survey", assessment_enabled: false, answer_display_mode: "all_in_one", submission_count: 0, slug: "compiled-survey", questions: [], score_rules: [], version: 1 } });
  if (target.includes("/external-push/test")) {
    testPosts += 1;
    return reply({ questionnaire_id: 7, test_run_id: "questionnaire-test-0123456789abcdef0123456789abcdef", effect_id: "eer_7", status: "queued" }, 202);
  }
  if (target.endsWith("/external-push")) {
    const body = JSON.parse(options.body);
    return reply({ configuration_version: ++metadataVersion, external_push: { enabled: body.enabled, configuration_reference: body.configuration_reference, metadata: body.metadata } });
  }
  if (target.includes("external-push-logs")) return reply({ items: [{ source_pk: "questionnaire-test-executed", status: "executed", attempt_count: 1, provider_result_received: true, occurred_at: "2026-09-05T00:00:00Z" }, { source_pk: "questionnaire-test-unknown", status: "outcome_unknown", attempt_count: 1, occurred_at: "2026-09-05T00:00:01Z" }] });
  return reply({ items: [{ source_pk: "questionnaire-test-queued", status: "queued", occurred_at: "2026-09-05T00:00:00Z" }], configuration_version: metadataVersion, external_push: { enabled: true, configuration_reference: "push.v1", metadata: { custom_params: { campaign: "old" } } } });
};

// This is the emitted donor page and its compiled controller, followed by the
// V3-owned Host. The controller sees the guarded empty legacy log projection;
// the Host then renders the real Survey records in the original log card.
dom.window.eval(bundle);
dom.window.eval(host);
dom.window.document.dispatchEvent(new dom.window.Event("DOMContentLoaded"));
await wait(150);
// jsdom's dynamic-import renderer does not notify a sibling observer for its
// synthetic module completion. A harmless real DOM mutation models the
// renderer's final stage write and lets the Host observe the compiled page.
dom.window.document.getElementById("stage").appendChild(dom.window.document.createComment("renderer settled"));
await wait(60);

const document = dom.window.document;
const stage = document.getElementById("stage");
if (!stage || !stage.textContent.includes("提交后动作") || !stage.textContent.includes("外部推送绑定") || !stage.textContent.includes("复制公开地址")) throw new Error("compiled questionnaire operations page did not retain its existing controls: " + httpCalls.join("|"));
const form = document.querySelector("[data-survey-push-metadata]");
const logCard = document.querySelector("[data-survey-host-logs]");
if (!form || !logCard) throw new Error("Host did not attach; host error=" + document.querySelector('[data-survey-host-error]')?.dataset.surveyHostError + "; headings=" + [...document.querySelectorAll("h3")].map((node) => node.textContent).join("|"));

const originalTest = [...document.querySelectorAll("button")].find((button) => button.textContent.includes("测试推送（仅本地记录）"));
if (!originalTest) throw new Error("compiled donor test button is missing");
originalTest.click();
await wait(50);
originalTest.click();
await wait(50);
if (testPosts !== 2 || !originalTest.textContent.includes("测试已创建") || !logCard.textContent.includes("等待处理")) throw new Error("original test button was not taken over by the Host on every click");
document.querySelector('[data-survey-log-scope="global"]').click();
await wait(10);
if (!logCard.textContent.includes("已收到处理结果") || !logCard.textContent.includes("处理结果待确认（不会自动重复发送）")) throw new Error("actual log card did not show true effect states");

form.elements.type.value = "assessment";
form.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
await wait(25);
if (metadataVersion !== 4 || !form.querySelector('button[type="submit"]').textContent.includes("已保存")) throw new Error("metadata save did not advance the configuration version");

const postTab = [...document.querySelectorAll("button")].find((button) => button.textContent.includes("提交后动作"));
postTab.click();
await wait(10);
if (!stage.textContent.includes("提交后动作") || !document.querySelector("[data-survey-push-metadata]") || !document.querySelector("[data-survey-host-logs]")) throw new Error("switching the existing tab removed the Host or submission-after-action controls");
dom.window.close();
console.log("survey-operations-compiled-donor-host-journey: PASS");
