import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const page = fs.readFileSync(path.join(root, "web/dist/h5/all.html"), "utf8");
const authHost = await buildTestBrowserBundle(
  path.join(root, "web/v3/h5AuthAdapter.ts"),
);
const publicHost = await buildTestBrowserBundle(
  path.join(root, "web/v3/surveyPublicHost.ts"),
);
const dom = new JSDOM(page, {
  url: "https://test.invalid/h5/all.html?slug=survey",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});

dom.window.eval(authHost);
dom.window.eval(publicHost);
const document = dom.window.document;
const screen = document.getElementById("screen");
const template = document.getElementById("tpl");
assert.ok(screen && template, "release H5 screen and template must remain");
assert.equal(
  document.body.dataset.v3PublicSurvey,
  "all",
  "public Survey Host scopes itself to the actual answer page",
);
const inNestedTemplate = (root, selector) => {
  const found = [...root.querySelectorAll(selector)];
  for (const child of root.querySelectorAll("template"))
    found.push(...inNestedTemplate(child.content, selector));
  return found;
};
assert.ok(
  inNestedTemplate(template.content, "[data-question-id]")[0]?.hasAttribute(
    "data-v3-survey-question",
  ),
  "question cards receive a V3 presentation hook before the frozen runtime mounts",
);
assert.ok(
  inNestedTemplate(template.content, "[data-h5-submit]")[0]?.hasAttribute(
    "data-v3-survey-action",
  ),
  "existing submit action receives the V3 presentation hook",
);
assert.equal(
  inNestedTemplate(template.content, "[data-h5-error]")[0]?.getAttribute(
    "aria-live",
  ),
  "assertive",
  "existing validation and submission failures remain announced",
);
assert.ok(
  inNestedTemplate(template.content, "[data-v3-survey-submitting]")[0],
  "both frozen answer templates receive a stable submitting marker bound to the Owner state",
);

screen.innerHTML =
  '<button data-h5-submit>提交</button><p data-v3-survey-submitting role="status">正在提交，请勿重复操作…</p>';
await new Promise((resolve) => setTimeout(resolve, 0));
const submit = screen.querySelector("[data-h5-submit]");
assert.ok(
  submit instanceof dom.window.HTMLButtonElement,
  "rendered submit control must exist",
);
assert.equal(
  submit.disabled,
  true,
  "rendered submitting state disables the existing submit control",
);
assert.equal(
  submit.getAttribute("aria-busy"),
  "true",
  "rendered submitting state exposes an accessible busy state",
);
assert.equal(
  screen.dataset.v3SurveySubmitting,
  "true",
  "screen records only the existing submitting state",
);

screen.innerHTML =
  "<button data-h5-submit>提交</button><div data-h5-error>提交失败；未修改答案时可安全重试</div>";
await new Promise((resolve) => setTimeout(resolve, 0));
const recovered = screen.querySelector("[data-h5-submit]");
assert.ok(
  recovered instanceof dom.window.HTMLButtonElement,
  "retry submit control must exist",
);
assert.equal(
  recovered.disabled,
  false,
  "error recovery restores the existing retry action without clearing rendered inputs",
);
assert.equal(
  screen.querySelector("[data-h5-error]")?.getAttribute("aria-live"),
  "assertive",
  "failure recovery keeps the existing error announcement",
);

screen.innerHTML = "<button data-h5-submit disabled>提交</button>";
await new Promise((resolve) => setTimeout(resolve, 0));
const ownerDisabled = screen.querySelector("[data-h5-submit]");
assert.ok(
  ownerDisabled instanceof dom.window.HTMLButtonElement,
  "Owner-disabled submit control must exist",
);
assert.equal(
  ownerDisabled.disabled,
  true,
  "presentation never releases an Owner-disabled submit control",
);
assert.equal(
  ownerDisabled.getAttribute("aria-busy"),
  null,
  "no pending state is inferred from button copy or disabled state",
);

screen.innerHTML = "<button data-h5-submit>提交</button>";
await new Promise((resolve) => setTimeout(resolve, 0));
const retry = screen.querySelector("[data-h5-submit]");
assert.ok(
  retry instanceof dom.window.HTMLButtonElement,
  "fresh retry control must exist",
);
retry.dispatchEvent(
  new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }),
);
assert.equal(
  retry.disabled,
  true,
  "first user click receives an immediate visual duplicate-click lock",
);
assert.equal(
  retry.getAttribute("aria-disabled"),
  "true",
  "duplicate-click lock is visible to assistive technology",
);
dom.window.close();

console.log("public Survey H5 presentation Host: PASS");
