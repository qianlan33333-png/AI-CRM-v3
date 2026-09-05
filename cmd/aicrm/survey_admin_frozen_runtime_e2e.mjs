import { JSDOM } from "jsdom";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../../web/scripts/test-browser-bundle.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const origin = process.env.AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN;
if (!origin) throw new Error("AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN is required");

const editorBundle = await buildTestBrowserBundle(path.join(root, "web/src/admin/sections/questionnaireEditor.ts"));
const adminBundle = await buildTestBrowserBundle(path.join(root, "web/src/admin/main.ts"));
const hostBridge = await readFile(path.join(root, "internal/webshell/static/admin_console/survey_operations.js"), "utf8");
let nextKey = 1;

async function waitFor(label, predicate, timeout = 4000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${label}`);
}

function input(window, element, value) {
  if (!element) throw new Error("expected input is missing");
  element.value = value;
  element.dispatchEvent(new window.Event("input", { bubbles: true }));
}

function change(window, element, checked) {
  if (!element) throw new Error("expected checkbox is missing");
  element.checked = checked;
  element.dispatchEvent(new window.Event("change", { bubbles: true }));
}

function click(element) {
  if (!element) throw new Error("expected control is missing");
  element.click();
}

function summarizeCalls(calls) {
  return JSON.stringify(calls.map(({ path, method, status, response }) => {
    const questionnaire = response?.questionnaire || response?.data?.questionnaire;
    return {
      path,
      method,
      status,
      questionnaire: questionnaire ? {
        id: questionnaire.id,
        version: questionnaire.version,
        status: questionnaire.status,
        enabled: questionnaire.enabled,
      } : undefined,
    };
  }));
}

async function openEditor(query, { requestDelayMs = 0 } = {}) {
  const response = await fetch(`${origin}/admin/questionnaireDetail.html${query}`);
  if (!response.ok) throw new Error(`actual Host editor response=${response.status} body=${(await response.text()).slice(0, 240)}`);
  const html = await response.text();
  if (!html.includes('data-admin-shell-source="v3_webshell"') || !html.includes('id="questionnaire-editor-config"')) {
    throw new Error("actual Survey Host did not render the frozen editor inside the v3 shell");
  }
  const dom = new JSDOM(html, {
    url: `${origin}/admin/questionnaireDetail.html${query}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = globalThis.Headers;
      window.Response = globalThis.Response;
      window.confirm = () => true;
      window.prompt = () => null;
      Object.defineProperty(window.crypto, "randomUUID", { configurable: true, value: () => `00000000-0000-4000-8000-${String(nextKey++).padStart(12, "0")}` });
      window.__surveyRuntimeDownloads = [];
      window.URL.createObjectURL = (blob) => {
        window.__surveyRuntimeDownloads.push({ blob, clicked: false, filename: "" });
        return `blob:survey-runtime-${window.__surveyRuntimeDownloads.length}`;
      };
      window.URL.revokeObjectURL = () => {};
      window.HTMLAnchorElement.prototype.click = function clickAnchor() {
        const download = window.__surveyRuntimeDownloads.at(-1);
        if (download) {
          download.clicked = true;
          download.filename = this.download || "";
        }
      };
    },
  });
  const calls = [];
  dom.window.fetch = async (inputValue, init = {}) => {
    const target = new URL(String(inputValue), origin);
    const call = { path: target.pathname, method: init.method || "GET", body: typeof init.body === "string" ? init.body : "", status: 0 };
    calls.push(call);
    // The Host must retain the exact adapter promise even when a browser-side
    // wrapper delays the request before it reaches the actual Survey HTTP API.
    if (requestDelayMs > 0) await new Promise((resolve) => setTimeout(resolve, requestDelayMs));
    const response = await globalThis.fetch(target, init);
    call.status = response.status;
    response.clone().json().then((value) => { call.response = value; }).catch(() => {});
    return response;
  };
  dom.window.eval(editorBundle);
  dom.window.eval(hostBridge);
  await waitFor("frozen editor initial render", () => dom.window.document.querySelector("#save-btn") !== null && dom.window.document.querySelector("#inspector-body") !== null);
  return { dom, calls };
}

async function openManagement() {
  const response = await fetch(`${origin}/admin/questionnaires.html`);
  if (!response.ok) throw new Error(`actual Host management response=${response.status} body=${(await response.text()).slice(0, 240)}`);
  const html = await response.text();
  if (!html.includes('data-admin-shell-source="v3_webshell"') || !html.includes('data-page="questionnaires"')) {
    throw new Error("actual Survey Host did not render the frozen management page inside the v3 shell");
  }
  const dom = new JSDOM(html, {
    url: `${origin}/admin/questionnaires.html`, runScripts: "outside-only", pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = globalThis.Headers;
      window.Response = globalThis.Response;
      window.confirm = () => true;
      Object.defineProperty(window.crypto, "randomUUID", { configurable: true, value: () => `00000000-0000-4000-8000-${String(nextKey++).padStart(12, "0")}` });
    },
  });
  const calls = [];
  dom.window.fetch = async (inputValue, init = {}) => {
    const target = new URL(String(inputValue), origin);
    const call = { path: target.pathname, method: init.method || "GET", body: typeof init.body === "string" ? init.body : "", status: 0 };
    calls.push(call);
    const response = await globalThis.fetch(target, init);
    call.status = response.status;
    response.clone().json().then((value) => { call.response = value; }).catch(() => {});
    return response;
  };
  dom.window.eval(adminBundle);
  dom.window.eval(hostBridge);
  dom.window.document.dispatchEvent(new dom.window.Event("DOMContentLoaded"));
  return { dom, calls };
}

function managementRow(document, questionnaireID, title) {
  const idText = `#${questionnaireID}`;
  return [...document.querySelectorAll("tr")].find((item) => item.textContent?.includes(title)
    && [...item.querySelectorAll("span")].some((span) => span.textContent?.trim() === idText)) || null;
}

function managementAction(document, questionnaireID, title, label) {
  const row = managementRow(document, questionnaireID, title);
  if (!row) return null;
  return [...row.querySelectorAll("a,button")].find((item) => item.textContent?.trim() === label) || null;
}

const normal = await openEditor("");
const normalDocument = normal.dom.window.document;
input(normal.dom.window, normalDocument.querySelector("#field-name"), "冻结后台实际问卷");
input(normal.dom.window, normalDocument.querySelector("#field-title"), "冻结后台实际标题");
input(normal.dom.window, normalDocument.querySelector("#field-slug"), "frozen-admin-runtime");
click(normalDocument.querySelector("#add-single"));
input(normal.dom.window, normalDocument.querySelector("#question-title"), "第一道真实题");
input(normal.dom.window, normalDocument.querySelector('[data-option-field="option_text"]'), "第一题选项 A");
click(normalDocument.querySelector("#add-option-btn"));
await waitFor("first question second option", () => normalDocument.querySelectorAll('[data-option-field="option_text"]').length === 2);
input(normal.dom.window, normalDocument.querySelectorAll('[data-option-field="option_text"]')[1], "第一题选项 B");
click(normalDocument.querySelector("#add-single"));
input(normal.dom.window, normalDocument.querySelector("#question-title"), "第二道真实题");
input(normal.dom.window, normalDocument.querySelector('[data-option-field="option_text"]'), "第二题选项 A");
click(normalDocument.querySelector("#add-option-btn"));
await waitFor("second question second option", () => normalDocument.querySelectorAll('[data-option-field="option_text"]').length === 2);
input(normal.dom.window, normalDocument.querySelectorAll('[data-option-field="option_text"]')[1], "第二题选项 B");
if (!normalDocument.querySelector("#preview-questions")?.textContent.includes("第二道真实题")) {
  throw new Error("frozen preview did not render the edited question before save");
}
click(normalDocument.querySelector("#save-btn"));
await waitFor("normal editor create", () => /\?id=[1-9]\d*$/.test(normal.dom.window.location.search) && normalDocument.querySelector("#editor-duplicate-btn") !== null).catch((error) => {
  const toast = normalDocument.querySelector("#toast")?.textContent || "";
  throw new Error(`${error.message}; toast=${toast}; calls=${JSON.stringify(normal.calls)}`);
});
const normalID = Number(new URLSearchParams(normal.dom.window.location.search).get("id"));
if (!Number.isSafeInteger(normalID) || normalID < 1) throw new Error("created editor did not retain a server questionnaire id");
const frozenNormalPayload = JSON.parse(normal.calls.find((call) => call.path === "/api/admin/questionnaires" && call.method === "POST")?.body || "{}");
if (!frozenNormalPayload.assessment_config || !frozenNormalPayload.questions?.every((question) => question.options.every((option) => option.other_max_length === 80 && option.is_other === false))) {
  throw new Error(`fixture no longer captures the frozen hidden defaults: ${JSON.stringify(frozenNormalPayload)}`);
}

click(normalDocument.querySelector("#editor-duplicate-btn"));
await waitFor("frozen duplicate", () => normal.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/duplicate` && call.method === "POST" && call.status === 200 && Number(call.response?.questionnaire?.id || call.response?.data?.questionnaire?.id) > normalID));
const duplicateCall = normal.calls.find((call) => call.path === `/api/admin/questionnaires/${normalID}/duplicate` && call.method === "POST" && call.status === 200);
const copyID = Number(duplicateCall?.response?.questionnaire?.id || duplicateCall?.response?.data?.questionnaire?.id);
if (!Number.isSafeInteger(copyID) || copyID <= normalID) {
  throw new Error(`frozen duplicate response did not identify its copy: ${JSON.stringify(duplicateCall)}`);
}
normal.dom.window.close();

// The frozen management page owns the actual enable/disable controls. A saved
// definition is shown there as disabled; the first enable must freeze and
// publish that exact draft, while stopping and re-enabling use the lifecycle.
const management = await openManagement();
const managementDocument = management.dom.window.document;
await waitFor("normal management action", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "启用") !== null);
click(managementAction(managementDocument, normalID, "冻结后台实际标题", "启用"));
await waitFor("normal list enable conflict", () => management.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/enable` && call.status === 409)).catch((error) => {
  const row = managementRow(managementDocument, normalID, "冻结后台实际标题");
  throw new Error(`${error.message}; row=${row?.textContent || "missing"}; calls=${JSON.stringify(management.calls)}`);
});
await waitFor("normal draft publish", () => management.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/public-publish` && call.status === 200));
const normalPublish = management.calls.find((call) => call.path === `/api/admin/questionnaires/${normalID}/public-publish` && call.status === 200);
if (!normalPublish || !Number.isSafeInteger(Number(JSON.parse(normalPublish.body || "{}").expected_questionnaire_version)) || Number(JSON.parse(normalPublish.body || "{}").expected_questionnaire_version) < 1) {
  throw new Error(`normal draft publish was not a versioned CAS: ${JSON.stringify(normalPublish)}`);
}
await waitFor("normal list stop action", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "停用") !== null);
click(managementAction(managementDocument, normalID, "冻结后台实际标题", "停用"));
await waitFor("normal stop confirmation", () => [...managementDocument.querySelectorAll("button")].some((item) => item.textContent?.trim() === "确认停用"));
click([...managementDocument.querySelectorAll("button")].find((item) => item.textContent?.trim() === "确认停用"));
await waitFor("normal list disable", () => management.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/disable` && call.status === 200));
await waitFor("normal list re-enable action", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "启用") !== null);
click(managementAction(managementDocument, normalID, "冻结后台实际标题", "启用"));
await waitFor("normal list re-enable", () => management.calls.filter((call) => call.path === `/api/admin/questionnaires/${normalID}/enable` && call.status === 200).length === 1);
// The frozen controller refreshes its own list after a successful toggle. Wait
// for that actual rendered state before closing this JSDOM realm so its pending
// refresh cannot observe a torn-down window.
await waitFor("normal re-enable refresh", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "停用") !== null);
management.dom.window.close();

// Export is a readback operation on the published Owner definition. Re-open
// the actual frozen detail Host after the lifecycle transition so the browser
// waits for both the response and the completed Blob/download gesture.
const normalPublished = await openEditor(`?id=${normalID}`);
const normalPublishedDocument = normalPublished.dom.window.document;
await waitFor("published editor export action", () => normalPublishedDocument.querySelector("#editor-export-btn") !== null);
click(normalPublishedDocument.querySelector("#editor-export-btn"));
await waitFor("actual CSV download", () => {
  const downloads = normalPublished.dom.window.__surveyRuntimeDownloads || [];
  return normalPublished.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/export` && call.status === 200)
    && downloads.length === 1 && downloads[0].clicked
    && typeof downloads[0].blob?.arrayBuffer === "function" && typeof downloads[0].blob?.text === "function";
}).catch((error) => {
  throw new Error(`${error.message}; calls=${JSON.stringify(normalPublished.calls)}; downloads=${JSON.stringify((normalPublished.dom.window.__surveyRuntimeDownloads || []).map((value) => ({ clicked: value.clicked, filename: value.filename })))} `);
});
const csv = await normalPublished.dom.window.__surveyRuntimeDownloads[0].blob.text();
if (!csv.includes("submission_id") || !csv.includes("customer_id")) throw new Error(`downloaded CSV did not contain the Survey export contract: ${csv.slice(0, 160)}`);
normalPublished.dom.window.close();

// A local validation failure never reaches questionnaireEditorV3. Its pending
// publish gesture must be gone before a later ordinary draft save, otherwise a
// failed “保存并发布” could publish that unrelated save.
const validation = await openEditor("?mode=assessment");
const validationDocument = validation.dom.window.document;
input(validation.dom.window, validationDocument.querySelector("#v2-basic-name"), "冻结校验草稿");
input(validation.dom.window, validationDocument.querySelector("#v2-basic-title"), "冻结校验草稿标题");
input(validation.dom.window, validationDocument.querySelector("#v2-basic-slug"), "frozen-admin-validation-draft");
click(validationDocument.querySelector('[data-assessment-step="results"]'));
await waitFor("assessment result tabs", () => validationDocument.querySelector('[data-result-tab="overall"]') !== null);
click(validationDocument.querySelector('[data-result-tab="overall"]'));
await waitFor("assessment overall course URL", () => validationDocument.querySelector('[data-overall-field="course_url"]') !== null);
input(validation.dom.window, validationDocument.querySelector('[data-overall-field="course_url"]'), "not a URL");
click(validationDocument.querySelector('[data-assessment-step="preview"]'));
await waitFor("assessment validation publish control", () => validationDocument.querySelector("#v2-publish-save") !== null);
click(validationDocument.querySelector("#v2-publish-save"));
await waitFor("assessment validation failure", () => /课程链接格式不正确/.test(validationDocument.querySelector("#toast")?.textContent || ""));
if (validation.calls.some((call) => /^\/api\/admin\/questionnaires(?:\/\d+)?$/.test(call.path) && ["POST", "PUT"].includes(call.method))) {
  throw new Error(`local assessment validation unexpectedly started a save: ${summarizeCalls(validation.calls)}`);
}
click(validationDocument.querySelector('[data-assessment-step="results"]'));
await waitFor("assessment validation reset tabs", () => validationDocument.querySelector('[data-result-tab="overall"]') !== null);
click(validationDocument.querySelector('[data-result-tab="overall"]'));
await waitFor("assessment validation reset URL", () => validationDocument.querySelector('[data-overall-field="course_url"]') !== null);
input(validation.dom.window, validationDocument.querySelector('[data-overall-field="course_url"]'), "");
click(validationDocument.querySelector('[data-assessment-step="preview"]'));
await waitFor("assessment ordinary draft control", () => validationDocument.querySelector("#v2-save-draft") !== null);
click(validationDocument.querySelector("#v2-save-draft"));
await waitFor("assessment ordinary draft save", () => validation.calls.some((call) => call.path === "/api/admin/questionnaires" && call.method === "POST" && call.status === 200));
await new Promise((resolve) => setTimeout(resolve, 600));
if (validation.calls.some((call) => /\/public-publish$/.test(call.path))) {
  throw new Error(`ordinary draft save was incorrectly published after validation failure: ${summarizeCalls(validation.calls)}`);
}
validation.dom.window.close();

const assessment = await openEditor("?mode=assessment", { requestDelayMs: 550 });
const assessmentDocument = assessment.dom.window.document;
const assessmentTitle = "冻结排序与预览";
input(assessment.dom.window, assessmentDocument.querySelector("#v2-basic-name"), "冻结排序测评");
input(assessment.dom.window, assessmentDocument.querySelector("#v2-basic-title"), assessmentTitle);
input(assessment.dom.window, assessmentDocument.querySelector("#v2-basic-slug"), "frozen-admin-assessment");
click(assessmentDocument.querySelector('[data-assessment-step="dimensions"]'));
await waitFor("assessment question cards", () => assessmentDocument.querySelectorAll("[data-question-key]").length >= 2);
const before = [...assessmentDocument.querySelectorAll("[data-question-key]")].map((item) => item.querySelector(".question-title-input")?.value || "");
click(assessmentDocument.querySelectorAll('[data-move-question][data-direction="-1"]')[1]);
await waitFor("assessment reorder", () => assessmentDocument.querySelector("[data-question-key]")?.querySelector(".question-title-input")?.value === before[1]);
let firstTitle = assessmentDocument.querySelector("[data-question-key]")?.querySelector(".question-title-input")?.value;
click(assessmentDocument.querySelector('[data-assessment-step="preview"]'));
await waitFor("assessment preview", () => assessmentDocument.querySelector(".h5-question-v2 strong")?.textContent === firstTitle);
const publishButton = assessmentDocument.querySelector("#v2-publish-save");
const publishEventOrder = [];
assessmentDocument.addEventListener("click", (event) => { if (event.target === publishButton) publishEventOrder.push("capture"); }, { capture: true, once: true });
publishButton.addEventListener("click", () => publishEventOrder.push("target"), { once: true });
assessmentDocument.addEventListener("click", (event) => { if (event.target === publishButton) publishEventOrder.push("bubble"); }, { once: true });
click(publishButton);
if (publishEventOrder.join(",") !== "capture,target,bubble") throw new Error(`frozen publish click order=${publishEventOrder.join(",")}`);
await waitFor("assessment publish", () => assessmentDocument.querySelector('[data-survey-host-publish-status] a[data-survey-host-published-path]')?.getAttribute("href") === "/q/frozen-admin-assessment").catch((error) => {
  const toast = assessmentDocument.querySelector("#toast")?.textContent || "";
  const publishState = assessmentDocument.querySelector('[data-survey-host-publish-status]')?.textContent || "";
  throw new Error(`${error.message}; toast=${toast}; publish_state=${publishState}; calls=${summarizeCalls(assessment.calls)}`);
});
const publishedPath = assessmentDocument.querySelector('[data-survey-host-publish-status] a[data-survey-host-published-path]')?.getAttribute("href") || "";
if (publishedPath !== "/q/frozen-admin-assessment") throw new Error(`published Host share path=${publishedPath}`);
await waitFor("assessment save", () => /\?id=[1-9]\d*$/.test(assessment.dom.window.location.search));
const assessmentID = Number(new URLSearchParams(assessment.dom.window.location.search).get("id"));
if (!assessment.calls.some((call) => call.path === "/api/admin/questionnaires" && call.method === "POST")) {
  throw new Error("frozen assessment save did not use the actual create endpoint");
}
const frozenAssessmentPayload = JSON.parse(assessment.calls.find((call) => call.path === "/api/admin/questionnaires" && call.method === "POST")?.body || "{}");
const frozenDimension = frozenAssessmentPayload.assessment_config?.dimensions?.find((dimension) => dimension.key === "用户维护");
const frozenQuestion = frozenAssessmentPayload.questions?.find((question) => question.assessment_dimension_key === "用户维护");
if (!frozenDimension || !frozenDimension.type_priority?.includes("暖男/女型")
  || !frozenDimension.types?.some((type) => type.key === "暖男/女型")
  || !frozenQuestion?.options?.some((option) => option.assessment_type_key === "暖男/女型")) {
  throw new Error(`frozen assessment did not preserve its legacy Chinese/slash association keys: ${JSON.stringify(frozenAssessmentPayload)}`);
}
const firstAssessmentPublish = assessment.calls.find((call) => call.path === `/api/admin/questionnaires/${assessmentID}/public-publish` && call.method === "POST" && call.status === 200);
if (!firstAssessmentPublish
  || !Number.isSafeInteger(Number(JSON.parse(firstAssessmentPublish.body || "{}").expected_questionnaire_version))
  || Number(JSON.parse(firstAssessmentPublish.body || "{}").expected_questionnaire_version) < 1
  || assessment.calls.some((call) => call.path === `/api/admin/questionnaires/${assessmentID}/enable`)) {
  throw new Error(`Host publish bridge did not use the versioned V3 publish contract: ${summarizeCalls(assessment.calls)}`);
}
// Publishing updates the Owner version. Re-enter the frozen editor and save a
// changed title to prove the bridge reads the fresh save response instead of
// replaying the original version into the next public-publish request.
click(assessmentDocument.querySelector('[data-assessment-step="dimensions"]'));
await waitFor("assessment edit after publish", () => assessmentDocument.querySelectorAll("[data-question-key]").length >= 2);
firstTitle = `${assessmentDocument.querySelector("[data-question-key]")?.querySelector(".question-title-input")?.value || ""} 修订`;
input(assessment.dom.window, assessmentDocument.querySelector("[data-question-key]")?.querySelector(".question-title-input"), firstTitle);
click(assessmentDocument.querySelector('[data-assessment-step="preview"]'));
await waitFor("assessment revised preview", () => assessmentDocument.querySelector(".h5-question-v2 strong")?.textContent === firstTitle);
const priorPublishConfirmations = assessment.calls.filter((call) => call.path === `/api/admin/questionnaires/${assessmentID}` && call.method === "GET" && call.status === 200);
const priorPublishConfirmationCount = priorPublishConfirmations.length;
const priorPublishedVersion = Number((priorPublishConfirmations.at(-1)?.response?.questionnaire || priorPublishConfirmations.at(-1)?.response?.data?.questionnaire)?.version);
click(assessmentDocument.querySelector("#v2-publish-save"));
await waitFor("assessment republish", () => assessment.calls.filter((call) => call.path === `/api/admin/questionnaires/${assessmentID}/public-publish` && call.method === "POST" && call.status === 200).length === 2);
const revisedSave = assessment.calls.findLast((call) => call.path === `/api/admin/questionnaires/${assessmentID}` && call.method === "PUT" && call.status === 200);
const revisedQuestionID = Number(JSON.parse(revisedSave?.body || "{}").questions?.find((question) => question.title === firstTitle)?.id);
if (!Number.isSafeInteger(revisedQuestionID) || revisedQuestionID < 1) throw new Error(`revised frozen question did not retain an Owner id: ${summarizeCalls(assessment.calls)}`);
await waitFor("assessment republish Owner confirmation", () => {
  const confirmations = assessment.calls.filter((call) => call.path === `/api/admin/questionnaires/${assessmentID}` && call.method === "GET" && call.status === 200);
  const latestResponse = confirmations.at(-1)?.response;
  const latest = latestResponse?.questionnaire || latestResponse?.data?.questionnaire;
  const latestQuestions = latestResponse?.questions || latest?.questions || [];
  const latestQuestion = latestQuestions.find((question) => Number(question.id) === revisedQuestionID);
  return confirmations.length === priorPublishConfirmationCount + 1
    && latest?.status === "active" && latest?.enabled === true
    && latest?.title === assessmentTitle && Number(latest?.version) > priorPublishedVersion
    && latestQuestion?.title === firstTitle
    && assessmentDocument.querySelector('[data-survey-host-publish-status] a[data-survey-host-published-path]')?.getAttribute("href") === "/q/frozen-admin-assessment"
    && Number(assessmentDocument.querySelector('[data-survey-host-publish-status]')?.dataset.surveyHostPublishedVersion) === Number(latest?.version);
}).catch((error) => {
  throw new Error(`${error.message}; calls=${summarizeCalls(assessment.calls)}`);
});
if (!assessment.calls.some((call) => call.path === `/api/admin/questionnaires/${assessmentID}` && call.method === "PUT" && call.status === 200)) {
  throw new Error(`frozen assessment edit did not save through the current Owner version: ${summarizeCalls(assessment.calls)}`);
}
assessment.dom.window.close();

const h5 = await fetch(`${origin}/h5/all.html?slug=frozen-admin-assessment`);
if (!h5.ok || !h5.headers.get("content-type")?.includes("text/html") || (await h5.text()).length < 100) throw new Error("actual public H5 Host was unavailable after publish");
console.log(JSON.stringify({ normalID, copyID, assessmentID, firstTitle, publishedPath }));
