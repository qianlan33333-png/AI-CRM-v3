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
    window.AdminFmt = { localTime: (value) => value === "2026-09-05T00:00:00Z" ? "2026-09-05 08:00:00" : value === "2026-09-05T00:00:01Z" ? "2026-09-05 08:00:01" : value === "2026-09-05T00:00:02Z" ? "2026-09-05 08:00:02" : "时间暂时无法显示" };
  },
});
Object.defineProperty(dom.window.document, "cookie", { value: "aicrm_admin_csrf=csrf-proof", configurable: true });
Object.defineProperty(dom.window.crypto, "randomUUID", { value: () => "00000000-0000-4000-8000-000000000000" });
let testPosts = 0;
let metadataVersion = 3;
const httpCalls = [];
const savedCompletionBodies = [];
const savedPushBodies = [];
const operations = {
  completion: { navigation_target_id: "", channel_id: null },
  external_push: { enabled: true, configuration_reference: "push.v1", metadata: { custom_params: { campaign: "old" } } },
};
dom.window.fetch = async (url, options = {}) => {
  const target = String(url);
  httpCalls.push(target);
  if (target.startsWith("/api/admin/questionnaires?")) return reply({ items: [{ id: 7, name: "compiled-survey", title: "编译问卷", description: "", status: "published", is_disabled: false, public_path: "/q/compiled-survey", assessment_enabled: false, answer_display_mode: "all_in_one", submission_count: 0, slug: "compiled-survey", questions: [], score_rules: [], version: 1 }], total: 1 });
  if (target.startsWith("/api/admin/channels?")) return reply({ items: [{ id: 42, channel_name: "运营测试渠道", channel_code: "ops-test", channel_type: "qrcode", carrier_type: "qrcode", status: "active", channel_contact_count: 0 }], total: 1 });
  if (target === "/api/admin/questionnaires/7") return reply({ questionnaire: { id: 7, name: "compiled-survey", title: "编译问卷", description: "", status: "published", is_disabled: false, public_path: "/q/compiled-survey", assessment_enabled: false, answer_display_mode: "all_in_one", submission_count: 0, slug: "compiled-survey", questions: [], score_rules: [], version: 1 } });
  if (target.includes("/external-push/test")) {
    testPosts += 1;
    return reply({ questionnaire_id: 7, test_run_id: "questionnaire-test-0123456789abcdef0123456789abcdef", effect_id: "eer_7", status: "queued" }, 202);
  }
  if (target.endsWith("/completion")) {
    const body = JSON.parse(options.body);
    savedCompletionBodies.push(body);
    operations.completion = { navigation_target_id: body.navigation_target_id || "", channel_id: body.channel_id || null };
    return reply({ completion: operations.completion, external_push: operations.external_push, configuration_version: metadataVersion });
  }
  if (target.endsWith("/external-push")) {
    const body = JSON.parse(options.body);
    savedPushBodies.push(body);
    operations.external_push = { enabled: body.enabled, configuration_reference: body.configuration_reference, metadata: body.metadata === undefined ? operations.external_push.metadata : body.metadata };
    return reply({ configuration_version: ++metadataVersion, completion: operations.completion, external_push: operations.external_push });
  }
  if (target.includes("external-push-logs")) return reply({ items: [{ source_pk: "questionnaire-test-executed", status: "executed", attempt_count: 1, provider_result_received: true, occurred_at: "2026-09-05T00:00:00Z" }, { source_pk: "questionnaire-test-unknown", status: "outcome_unknown", attempt_count: 1, occurred_at: "2026-09-05T00:00:01Z" }, { source_pk: "questionnaire-test-unmapped", status: "provider_pending", attempt_count: 1, occurred_at: "2026-09-05T00:00:02Z", failure_category: "provider_outcome_unknown" }] });
  return reply({ items: [{ source_pk: "questionnaire-test-queued", status: "queued", occurred_at: "2026-09-05T00:00:00Z" }], configuration_version: metadataVersion, ...operations });
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
let form = document.querySelector("[data-survey-push-metadata]");
const logCard = document.querySelector("[data-survey-host-logs]");
if (!form || !logCard) throw new Error("Host did not attach; host error=" + document.querySelector('[data-survey-host-error]')?.dataset.surveyHostError + "; headings=" + [...document.querySelectorAll("h3")].map((node) => node.textContent).join("|"));

const originalTest = [...document.querySelectorAll("button")].find((button) => button.dataset.surveyHostTestPush === "true");
if (!originalTest || originalTest.textContent.includes("仅本地记录") || document.body.textContent.includes("没有 Provider 调用、接收或送达回执") || !document.querySelector('[data-survey-host-log-boundary]')?.textContent.includes("HTTP 受理不代表")) throw new Error("compiled donor test button or receipt boundary was not replaced by the Host");
async function confirmControlledTest() { originalTest.click(); await wait(5); const confirm = document.querySelector('[data-survey-host-test-confirmation] [data-survey-host-test-confirm]'); if (!confirm) throw new Error("Host controlled-test confirmation did not render"); confirm.click(); await wait(50); }
await confirmControlledTest();
await confirmControlledTest();
if (testPosts !== 2 || !originalTest.textContent.includes("受控外推测试已创建") || !logCard.textContent.includes("等待处理")) throw new Error("original test button did not run through the confirmed Host controlled-test receipt path");
document.querySelector('[data-survey-log-scope="global"]').click();
await wait(10);
if (!logCard.textContent.includes("已收到处理结果") || !logCard.textContent.includes("处理结果待确认（不会自动重复发送）") || !logCard.textContent.includes("历史记录：状态待确认") || !logCard.textContent.includes("外部处理结果待核对") || !logCard.textContent.includes("2026-09-05 08:00:00") || logCard.textContent.includes("provider_pending") || logCard.textContent.includes("provider_outcome_unknown") || logCard.textContent.includes("2026-09-05T00:00:00Z")) throw new Error("actual log card did not localize effect states and timestamps");

const pushTab = [...document.querySelectorAll("button")].find(button => button.textContent.includes('外部推送') && !button.textContent.includes('保存'));
if (!pushTab) throw new Error('compiled external push tab missing');
pushTab.click(); await wait(60);
form = document.querySelector('[data-survey-push-metadata]');
form.elements.type.value = 'assessment';
const toggle = document.querySelector('[data-survey-push-enabled]');
if (!toggle) throw new Error('compiled enabled switch missing');
toggle.checked = false; toggle.dispatchEvent(new dom.window.Event('change', {bubbles:true}));
const headerSave = [...document.querySelectorAll('button')].find(button => !form.contains(button) && button.textContent.trim() === '保存当前维度');
if (!headerSave || !form.querySelector('button[type="submit"]').hidden) throw new Error('single top save not installed');
headerSave.click(); headerSave.click(); await wait(40);
if (savedPushBodies.length !== 1 || savedPushBodies[0].enabled !== false || savedPushBodies[0].metadata.type !== 'assessment' || savedPushBodies[0].metadata.custom_params.campaign !== 'old') throw new Error('top save lost toggle/params or double submitted: '+JSON.stringify(savedPushBodies));
if (metadataVersion !== 4 || !form.querySelector('button[type="submit"]').textContent.includes("已保存")) throw new Error("metadata save did not advance the configuration version");

const visibleSaveStatus = form.querySelector('[data-survey-push-save-status]');
if (!visibleSaveStatus || visibleSaveStatus.textContent !== '已保存当前维度' || visibleSaveStatus.hidden || visibleSaveStatus.getAttribute('role') !== 'status') throw new Error('top save lacks visible success feedback');
const pushStyle = document.getElementById('survey-push-editor-style')?.textContent || '';
if (!pushStyle.includes('@media(max-width:600px)') || !pushStyle.includes('minmax(0,1fr) 44px minmax(0,1fr) 32px') || !pushStyle.includes('.survey-push-layout>section,.survey-push-layout>aside{min-width:0}')) throw new Error('narrow fields or preview retain overflowing minimum widths');

const postTab = [...document.querySelectorAll("button")].find((button) => button.textContent.includes("提交后动作"));
postTab.click();
await wait(10);
if (!stage.textContent.includes("提交后动作") || !document.querySelector("[data-survey-push-metadata]") || !document.querySelector("[data-survey-host-logs]")) throw new Error("switching the existing tab removed the Host or submission-after-action controls");

const postHeading = [...document.querySelectorAll("h3")].find((heading) => heading.textContent.trim() === "提交后动作");
const postToggle = postHeading?.parentElement?.parentElement?.querySelector("span");
if (!postToggle) throw new Error("compiled submission-after-action toggle is missing");
postToggle.click();
await wait(10);
const qrChoice = [...document.querySelectorAll("div")].filter((node) => node.textContent.includes("展示渠道二维码") && node.querySelector("p")).sort((left, right) => left.textContent.length - right.textContent.length)[0];
if (!qrChoice) throw new Error("compiled channel-QR action choice is missing");
qrChoice.click();
await wait(10);
const channelPicker = [...document.querySelectorAll("div")].find((node) => node.textContent.trim() === "不配置引流渠道码");
if (!channelPicker) throw new Error("compiled channel picker is missing");
channelPicker.click();
await wait(10);
const channelChoice = [...document.querySelectorAll("*")].find((node) => node.textContent?.trim() === "运营测试渠道");
if (!channelChoice) throw new Error("compiled channel selection did not show the HTTP channel fixture");
channelChoice.click();
await wait(10);
const savePost = [...document.querySelectorAll("button")].find((button) => button.textContent === "保存提交后动作");
if (!savePost) throw new Error("compiled submission-after-action save is missing");
savePost.click();
await wait(50);
if (savedCompletionBodies.length !== 1 || savedCompletionBodies[0].channel_id !== 42 || savedCompletionBodies[0].navigation_target_id !== undefined) throw new Error("compiled channel action was not saved through the existing completion endpoint: " + JSON.stringify(savedCompletionBodies));
if (!httpCalls.some((target) => target.endsWith("/operations/completion"))) throw new Error("compiled channel action did not call the completion operations endpoint");
if (operations.completion.channel_id !== 42) throw new Error("compiled channel action did not retain saved channel state");

const redirectChoice = [...document.querySelectorAll("div")].filter((node) => node.textContent.includes("直接跳转") && node.querySelector("p")).sort((left, right) => left.textContent.length - right.textContent.length)[0];
if (!redirectChoice) throw new Error("compiled redirect action choice is missing");
redirectChoice.click();
await wait(10);
const navigationTarget = document.getElementById("opsNavigationTarget");
if (!navigationTarget) throw new Error("compiled navigation target input is missing");
navigationTarget.value = "completion.thank-you";
const saveRedirect = [...document.querySelectorAll("button")].find((button) => button.textContent === "保存提交后动作");
saveRedirect.click();
await wait(50);
if (savedCompletionBodies.length !== 2 || savedCompletionBodies[1].navigation_target_id !== "completion.thank-you" || savedCompletionBodies[1].channel_id !== undefined) throw new Error("compiled redirect action was not saved through the existing completion endpoint: " + JSON.stringify(savedCompletionBodies));
if (operations.completion.navigation_target_id !== "completion.thank-you" || operations.completion.channel_id !== null) throw new Error("compiled redirect action did not retain saved navigation state");
dom.window.close();
console.log("survey-operations-compiled-donor-host-journey: PASS");
