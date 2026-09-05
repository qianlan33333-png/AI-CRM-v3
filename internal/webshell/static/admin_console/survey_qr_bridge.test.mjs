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
if (!form || !ops.window.document.querySelector('[data-survey-test-records]').textContent.includes('暂无测试记录')) throw new Error('zero-receipt page lost push metadata editor');
form.elements.enabled.checked = false; form.elements.configuration_reference.value = 'push.v2';
form.elements.type.value = 'new';
form.querySelector('[data-param-name]').value = 'campaign'; form.querySelector('[data-param-value]').value = 'autumn';
form.dispatchEvent(new ops.window.Event('submit', {bubbles:true,cancelable:true}));
await wait(20);
const put = requests.find((item)=>item.options.method==='PUT');
if (!put || put.options.headers.get('X-CSRF-Token') !== 'csrf-proof' || JSON.parse(put.options.body).configuration_reference !== 'push.v2' || JSON.parse(put.options.body).enabled !== false || JSON.parse(put.options.body).configuration_version !== 3 || savedMetadata.custom_params.campaign !== 'autumn') throw new Error('metadata save did not preserve current config revision with CSRF');
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

const journey = new JSDOM('<!doctype html><body data-page="questionnaireOps"><div id="stage"></div></body>', {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(journey.window.document, 'cookie', {value:'aicrm_admin_csrf=csrf-proof', configurable:true});
Object.defineProperty(journey.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
let created = false;
const journeyRequests = [];
journey.window.fetch = async (url, options = {}) => {
  const path = String(url); journeyRequests.push({path, options});
  if (path.includes('/external-push/test')) {
    if (options.headers.get('X-CSRF-Token') !== 'csrf-proof') return {ok:false,status:403,json:async()=>({})};
    created = true;
    return {ok:true,status:202,json:async()=>({questionnaire_id:7,test_run_id:'questionnaire-test-0123456789abcdef0123456789abcdef',effect_id:'eer_7',status:'queued',synthetic_data:true,accepted:true})};
  }
  if (path.includes('/admin/questionnaires/external-push-logs')) return {ok:true,json:async()=>({items:[
    {test_run_id:'questionnaire-test-0123456789abcdef0123456789abcdef',status:'executed',provider_attempt_number:1,provider_result_received:true,updated_at:'2026-09-05T00:00:00Z'},
    {test_run_id:'questionnaire-test-unknown',status:'outcome_unknown',provider_attempt_number:1,provider_result_received:false,updated_at:'2026-09-05T00:00:01Z'},
    {test_run_id:9,status:'disabled',updated_at:'2026-08-01T00:00:00Z',read_only_legacy:true},
  ]})};
  return {ok:true,json:async()=>({provider_enabled:true,items:created ? [{source_pk:'questionnaire-test-0123456789abcdef0123456789abcdef',status:'executed',provider_attempt_number:1,provider_result_received:true,occurred_at:'2026-09-05T00:00:00Z'}] : [{source_pk:'questionnaire-test-0123456789abcdef0123456789abcdef',status:'queued',occurred_at:'2026-09-05T00:00:00Z'}],configuration_version:3,external_push:{enabled:true,configuration_reference:'push.v1',metadata:{}}})};
};
journey.window.eval(adapter);
await wait(30);
const testButton = journey.window.document.querySelector('[data-survey-push-test]');
if (!testButton || !journey.window.document.querySelector('[data-survey-test-records]') || !journey.window.document.querySelector('[data-survey-global-test-records]')) throw new Error('host did not expose test controls and both record views');
testButton.click();
await wait(40);
const post = journeyRequests.find((item) => item.path.includes('/external-push/test'));
const combined = journey.window.document.body.textContent;
if (!post || post.options.headers.get('X-CSRF-Token') !== 'csrf-proof' || !combined.includes('已收到测试结果') || !combined.includes('结果待确认（不会自动重复发送）') || !combined.includes('当时未启用外推配置')) throw new Error('test journey did not carry CSRF or render terminal and historical results');
journey.window.close();

const forbidden = new JSDOM('<!doctype html><body data-page="questionnaireOps"><div id="stage"></div></body>', {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(forbidden.window.document, 'cookie', {value:'aicrm_admin_csrf=wrong', configurable:true});
Object.defineProperty(forbidden.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
forbidden.window.fetch = async (url, options = {}) => {
  if (String(url).includes('/external-push/test')) return {ok:false,status:403,json:async()=>({})};
  if (String(url).includes('/external-push-logs')) return {ok:true,json:async()=>({items:[]})};
  return {ok:true,json:async()=>({items:[],configuration_version:1,external_push:{enabled:true,configuration_reference:'push.v1',metadata:{}}})};
};
forbidden.window.eval(adapter);
await wait(30);
forbidden.window.document.querySelector('[data-survey-push-test]').click();
await wait(20);
if (!forbidden.window.document.body.textContent.includes('无操作权限')) throw new Error('CSRF refusal was not shown to the operator');
forbidden.window.close();

console.log("survey-operations-host-journey: PASS");

const legacyShell = new JSDOM('<!doctype html><body data-page="questionnaireOps"><div id="stage"></div></body>', {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(legacyShell.window.document, 'cookie', {value:'aicrm_admin_csrf=csrf-proof', configurable:true});
Object.defineProperty(legacyShell.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
const rawCalls = []; let legacyPostCalls = 0;
legacyShell.window.fetch = async (url, options = {}) => {
  const path = String(url); rawCalls.push({path, options});
  if (path.includes('/external-push/test')) { legacyPostCalls += 1; return {ok:true,status:202,json:async()=>({questionnaire_id:7,test_run_id:'questionnaire-test-0123456789abcdef0123456789abcdef',effect_id:'eer_7',status:'queued',accepted:true,synthetic_data:true})}; }
  if (path.includes('/external-push-logs')) return {ok:true,json:async()=>({items:[{test_run_id:'questionnaire-test-unknown',status:'outcome_unknown',attempt_count:1,provider_result_received:false,updated_at:'2026-09-05T00:00:01Z'}]})};
  return {ok:true,json:async()=>({items:[{source_pk:'questionnaire-test-0123456789abcdef0123456789abcdef',status:'executed',provider_attempt_number:1,provider_result_received:true,occurred_at:'2026-09-05T00:00:00Z'}],configuration_version:5,external_push:{enabled:true,configuration_reference:'push.v1',metadata:{}}})};
};
legacyShell.window.eval(adapter);
await wait(30);
const guardedLogs = await legacyShell.window.fetch('/admin/questionnaires/7/external-push-logs');
const guardedPayload = await guardedLogs.json();
if (!guardedPayload.local_only || guardedPayload.items.length !== 0 || rawCalls.some((item) => item.path === '/admin/questionnaires/7/external-push-logs')) throw new Error('frozen queued-only mapper was not isolated from real records');
const legacyStage = legacyShell.window.document.getElementById('stage');
legacyStage.replaceChildren();
const oldButton = legacyShell.window.document.createElement('button'); oldButton.textContent = '测试推送（仅本地记录）'; let donorClick = 0; oldButton.addEventListener('click', () => { donorClick += 1; }); legacyStage.appendChild(oldButton);
oldButton.click();
await wait(30);
if (donorClick !== 0 || legacyShell.window.document.querySelector('button')?.textContent.includes('仅本地记录')) throw new Error('frozen test button remained active after Host takeover');
const hostButton = legacyShell.window.document.querySelector('[data-survey-push-test]');
if (!hostButton) throw new Error('Host did not replace the frozen questionnaire operations entry');
hostButton.click();
await wait(40);
const shellText = legacyShell.window.document.body.textContent;
if (legacyPostCalls !== 1 || !shellText.includes('已收到测试结果') || !shellText.includes('结果待确认（不会自动重复发送）')) throw new Error('Host did not use one real test request and readable current/global results');
legacyShell.window.close();

console.log("survey-operations-frozen-shell-takeover: PASS");
