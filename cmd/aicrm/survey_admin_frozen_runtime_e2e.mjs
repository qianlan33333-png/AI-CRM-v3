import { JSDOM } from "jsdom";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../../web/scripts/test-browser-bundle.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const origin = process.env.AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN;
if (!origin) throw new Error("AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN is required");

const editorBundle = await buildTestBrowserBundle(path.join(root, "web/src/admin/sections/questionnaireEditor.ts"));
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

async function openEditor(query) {
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
      window.URL.createObjectURL = () => "blob:survey-runtime";
      window.URL.revokeObjectURL = () => {};
      window.HTMLAnchorElement.prototype.click = function clickAnchor() {
        this.dataset.runtimeClicked = "true";
      };
    },
  });
  const calls = [];
  dom.window.fetch = async (inputValue, init = {}) => {
    const target = new URL(String(inputValue), origin);
    calls.push({ path: target.pathname, method: init.method || "GET" });
    return globalThis.fetch(target, init);
  };
  dom.window.eval(editorBundle);
  await waitFor("frozen editor initial render", () => dom.window.document.querySelector("#save-btn") !== null && dom.window.document.querySelector("#inspector-body") !== null);
  return { dom, calls };
}

const normal = await openEditor("");
const normalDocument = normal.dom.window.document;
input(normal.dom.window, normalDocument.querySelector("#field-name"), "冻结后台实际问卷");
input(normal.dom.window, normalDocument.querySelector("#field-title"), "冻结后台实际标题");
input(normal.dom.window, normalDocument.querySelector("#field-slug"), "frozen-admin-runtime");
click(normalDocument.querySelector("#add-single"));
input(normal.dom.window, normalDocument.querySelector("#question-title"), "第一道真实题");
click(normalDocument.querySelector("#add-single"));
input(normal.dom.window, normalDocument.querySelector("#question-title"), "第二道真实题");
if (!normalDocument.querySelector("#preview-questions")?.textContent.includes("第二道真实题")) {
  throw new Error("frozen preview did not render the edited question before save");
}
click(normalDocument.querySelector("#save-btn"));
await waitFor("normal editor create", () => /\?id=[1-9]\d*$/.test(normal.dom.window.location.search) && normalDocument.querySelector("#editor-duplicate-btn") !== null);
const normalID = Number(new URLSearchParams(normal.dom.window.location.search).get("id"));
if (!Number.isSafeInteger(normalID) || normalID < 1) throw new Error("created editor did not retain a server questionnaire id");

click(normalDocument.querySelector("#editor-export-btn"));
await waitFor("actual CSV export", () => normal.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/export`));
if (!normalDocument.querySelector('a[data-runtime-clicked="true"]')) throw new Error("download action did not create the browser download anchor");

change(normal.dom.window, normalDocument.querySelector("#field-is-disabled"), true);
click(normalDocument.querySelector("#save-btn"));
await waitFor("normal editor disabled save", () => normalDocument.querySelector("#toast")?.textContent.includes("问卷已更新"));
click(normalDocument.querySelector("#editor-duplicate-btn"));
await waitFor("frozen duplicate", () => {
  const id = Number(new URLSearchParams(normal.dom.window.location.search).get("id"));
  return Number.isSafeInteger(id) && id > normalID;
});
const copyID = Number(new URLSearchParams(normal.dom.window.location.search).get("id"));
if (!normal.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/duplicate` && call.method === "POST")) {
  throw new Error("frozen duplicate did not use the actual duplicate endpoint");
}
normal.dom.window.close();

const assessment = await openEditor("?mode=assessment");
const assessmentDocument = assessment.dom.window.document;
input(assessment.dom.window, assessmentDocument.querySelector("#v2-basic-name"), "冻结排序测评");
input(assessment.dom.window, assessmentDocument.querySelector("#v2-basic-title"), "冻结排序与预览");
input(assessment.dom.window, assessmentDocument.querySelector("#v2-basic-slug"), "frozen-admin-assessment");
click(assessmentDocument.querySelector('[data-assessment-step="dimensions"]'));
await waitFor("assessment question cards", () => assessmentDocument.querySelectorAll("[data-question-key]").length >= 2);
const before = [...assessmentDocument.querySelectorAll("[data-question-key]")].map((item) => item.querySelector(".question-title-input")?.value || "");
click(assessmentDocument.querySelectorAll('[data-move-question][data-direction="-1"]')[1]);
await waitFor("assessment reorder", () => assessmentDocument.querySelector("[data-question-key]")?.querySelector(".question-title-input")?.value === before[1]);
const firstTitle = assessmentDocument.querySelector("[data-question-key]")?.querySelector(".question-title-input")?.value;
click(assessmentDocument.querySelector('[data-assessment-step="preview"]'));
await waitFor("assessment preview", () => assessmentDocument.querySelector(".h5-question-v2 strong")?.textContent === firstTitle);
click(assessmentDocument.querySelector("#v2-publish-save"));
await waitFor("assessment save", () => /\?id=[1-9]\d*$/.test(assessment.dom.window.location.search));
const assessmentID = Number(new URLSearchParams(assessment.dom.window.location.search).get("id"));
if (!assessment.calls.some((call) => call.path === "/api/admin/questionnaires" && call.method === "POST")) {
  throw new Error("frozen assessment save did not use the actual create endpoint");
}
assessment.dom.window.close();

console.log(JSON.stringify({ normalID, copyID, assessmentID, firstTitle }));
