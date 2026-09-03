import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const adapter = fs.readFileSync(path.join(here, "survey_operations.js"), "utf8");
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

function page() {
  const dom = new JSDOM('<!doctype html><body data-page="questionnaires"></body>', {
    url: "https://test.invalid/admin/questionnaires",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  dom.window.eval(adapter);
  return dom;
}

const failed = page();
const emptyBox = failed.window.document.createElement("div");
emptyBox.id = "shareQrBox";
failed.window.document.body.appendChild(emptyBox);
await wait(1300);
const fallback = emptyBox.querySelector('[data-survey-qr-fallback="true"][role="alert"]');
if (!fallback || !fallback.textContent.includes("复制")) throw new Error("empty QR mount did not expose the copy-link fallback");
failed.window.close();

const rendered = page();
const renderedBox = rendered.window.document.createElement("div");
renderedBox.id = "shareQrBox";
rendered.window.document.body.appendChild(renderedBox);
await wait(100);
renderedBox.appendChild(rendered.window.document.createElementNS("http://www.w3.org/2000/svg", "svg"));
await wait(1200);
if (renderedBox.querySelector('[data-survey-qr-fallback="true"]')) throw new Error("fallback replaced a rendered QR SVG");
rendered.window.close();

console.log("survey-qr-bridge-browser: PASS");
