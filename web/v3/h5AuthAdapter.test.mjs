import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/h5/auth.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/h5AuthAdapter.ts'));
const runtime = await buildTestBrowserBundle(path.join(root, 'web/src/h5/main.ts'));
const dom = new JSDOM(page, { url: 'https://test.invalid/h5/auth.html?slug=survey', runScripts: 'outside-only' });

dom.window.eval(host);
const document = dom.window.document;
const template = document.getElementById('tpl');
assert.ok(template, 'frozen H5 runtime template must remain');
assert.equal(document.querySelector('.phone'), null, 'auth page must not retain the phone demo frame');
assert.equal(document.querySelector('a[href="index.html"]'), null, 'auth page must not retain the demo screen navigation');
assert.equal(template.content.querySelector('[data-h5-blocked]'), null, 'auth error binding must not remain an always-visible top warning');
assert.equal([...template.content.querySelectorAll('span,p')].some((node) => node.textContent?.includes('微信身份验证') || node.textContent?.includes('验证 UnionID')), false, 'auth page must not retain the demo identity explanation');
assert.ok([...template.content.querySelectorAll('h1')].some((node) => node.textContent?.includes('正在验证微信身份')), 'auth title must remain');
assert.ok(template.innerHTML.includes('重试微信授权'), 'failed OAuth must offer retry');
assert.ok(template.innerHTML.includes('当前不在微信内打开'), 'non-WeChat guidance must remain');
const errorTemplate = [...template.content.querySelectorAll('template[data-sc-if]')].find((node) => node.getAttribute('data-sc-if') === '{{ error }}');
assert.ok(errorTemplate?.content.querySelector('[data-h5-blocked][role="status"]'), 'frozen controller error binding must move into the auth card');
assert.ok(errorTemplate?.innerHTML.includes('{{ error }}'), 'moved auth error must retain the frozen controller binding');
dom.window.close();

const errorDom = new JSDOM(page, {
  url: 'https://test.invalid/h5/auth.html?slug=survey', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    Object.defineProperty(window.navigator, 'userAgent', { configurable: true, value: 'MicroMessenger' });
    window.fetch = async () => new Response('', { status: 409 });
  },
});
errorDom.window.Response = Response;
errorDom.window.Headers = Headers;
errorDom.window.eval(host);
errorDom.window.eval(runtime);
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(errorDom.window.document.querySelector('#screen [data-h5-blocked]')?.textContent?.includes('当前微信身份存在冲突'), 'frozen auth 409 must remain visible beside the OAuth action');
assert.equal(errorDom.window.document.querySelector('#screen').textContent?.includes('授权仅用于识别本次问卷所属客户，不会发送短信。'), false, 'ordinary WeChat notice must not occupy the cleaned page');
errorDom.window.close();
console.log('h5 auth Host presentation journey: PASS');

// Run the real auth controller with only its renderer/API imports isolated.
// A session miss auto-starts once; callback failure or a lost cookie cannot loop.
const {build} = await import('esbuild');
const {runInNewContext} = await import('node:vm');
const compiled = await build({entryPoints:[path.join(root,'web/src/h5/controller.ts')],bundle:true,write:false,format:'iife',globalName:'AuthController',plugins:[{name:'auth-controller-dependencies',setup(b){b.onResolve({filter:/^\.\.\//},args=>({path:args.path,namespace:'auth-dependency'}));b.onLoad({filter:/.*/,namespace:'auth-dependency'},()=>({contents:'export class PageBase {} export class ApiError extends Error {} export const toast=()=>{}; export const readPublicSurvey=()=>{}; export const readSurveyResult=()=>{}; export const submitSurvey=()=>{};',loader:'js'}));}}]});
const marker = new Map();
async function authRun(status, search='?slug=survey') {
  const redirects=[], calls=[];
  const sandbox={URLSearchParams,navigator:{userAgent:'MicroMessenger'},location:{search,replace(url){redirects.push(url)}},sessionStorage:{getItem:k=>marker.get(k),setItem:(k,v)=>marker.set(k,v),removeItem:k=>marker.delete(k)},fetch:async url=>{calls.push(url);return {ok:status===200,status,json:async()=>({display:'one'})}}};
  runInNewContext(compiled.outputFiles[0].text,sandbox);
  const controller=new sandbox.AuthController.H5Controller('auth');await controller.init();
  return {controller,redirects,calls};
}
const fresh=await authRun(401);
assert.deepEqual(fresh.redirects,['/api/h5/surveys/oauth/start?slug=survey']);
assert.equal(fresh.calls.length,1);
const lostCookie=await authRun(401);
assert.equal(lostCookie.redirects.length,0);
assert.match(lostCookie.controller.renderVals().error,/请重试/);
lostCookie.controller.renderVals().act.authContinue();
assert.equal(lostCookie.redirects.length,1);
const failed=await authRun(401,'?slug=survey&oauth_error=1');
assert.equal(failed.redirects.length,0);
assert.equal(failed.calls.length,0);
const known=await authRun(200);
assert.deepEqual(known.redirects,['/h5/one.html?slug=survey']);
assert.equal(marker.size,0);
