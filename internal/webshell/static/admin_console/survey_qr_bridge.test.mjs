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

const ops = new JSDOM('<!doctype html><body data-page="questionnaireOps"><div id="stage"></div></body>', {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(ops.window.document, 'cookie', {value:'aicrm_admin_csrf=csrf-proof', configurable:true});
Object.defineProperty(ops.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
const requests = [];
let savedMetadata = {type:'old', custom_params:{legacy:'yes'}};
ops.window.fetch = async (url, options = {}) => {
  requests.push({url:String(url), options});
  if (!options.method || options.method === 'GET') return {ok:true, json:async()=>({provider_enabled:false,real_external_call_executed:false,items:[],configuration_version:3,external_push:{enabled:true,configuration_reference:'push.v1',metadata:savedMetadata}})};
  if (options.headers.get('X-CSRF-Token') !== 'csrf-proof') return {ok:false, status:403, json:async()=>({})};
  const body = JSON.parse(options.body); savedMetadata = body.metadata;
  return {ok:true, json:async()=>({external_push:{enabled:body.enabled,configuration_reference:body.configuration_reference,metadata:body.metadata}})};
};
ops.window.eval(adapter);
await wait(20);
const form = ops.window.document.querySelector('[data-survey-push-metadata]');
if (!form || !ops.window.document.querySelector('[role="status"] p').textContent.includes('暂无外部操作回执')) throw new Error('zero-receipt page lost push metadata editor');
form.elements.type.value = 'new';
form.querySelector('[data-param-name]').value = 'campaign'; form.querySelector('[data-param-value]').value = 'autumn';
form.dispatchEvent(new ops.window.Event('submit', {bubbles:true,cancelable:true}));
await wait(20);
const put = requests.find((item)=>item.options.method==='PUT');
if (!put || put.options.headers.get('X-CSRF-Token') !== 'csrf-proof' || JSON.parse(put.options.body).configuration_reference !== 'push.v1' || JSON.parse(put.options.body).configuration_version !== 3 || savedMetadata.custom_params.campaign !== 'autumn') throw new Error('metadata save did not preserve current config revision with CSRF');
ops.window.close();

const conflict = new JSDOM('<!doctype html><body data-page="questionnaireOps"><div id="stage"></div></body>', {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(conflict.window.document, 'cookie', {value:'aicrm_admin_csrf=csrf-proof', configurable:true});
Object.defineProperty(conflict.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
conflict.window.fetch = async (_url, options = {}) => {
  if (!options.method || options.method === 'GET') return {ok:true, json:async()=>({provider_enabled:false,real_external_call_executed:false,items:[],configuration_version:4,external_push:{enabled:true,configuration_reference:'push.v1',metadata:{}}})};
  return {ok:false, status:409, json:async()=>({ok:false,code:'configuration_conflict'})};
};
conflict.window.eval(adapter);
await wait(20);
const conflictForm = conflict.window.document.querySelector('[data-survey-push-metadata]');
conflictForm.elements.type.value = 'retry';
conflictForm.dispatchEvent(new conflict.window.Event('submit', {bubbles:true,cancelable:true}));
await wait(20);
const conflictSave = conflictForm.querySelector('button[type="submit"]');
if (conflictSave.textContent === '已保存' || !conflictSave.textContent.includes('重新打开')) throw new Error('configuration conflict was presented as saved');
conflict.window.close();

console.log("survey-qr-bridge-browser: PASS");
