import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { build } from 'esbuild';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/radarAdapter.ts'));
const frozenRadar = (await build({ stdin: { contents: "import { mountRadar } from '../src/admin/sections/radar'; window.FrozenRadar = { mountRadar };", resolveDir: path.join(root, 'web/v3'), sourcefile: 'radar-frozen-renderer-entry.ts' }, bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning' })).outputFiles[0].text;
const picker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

const savedInputs = [];
let includeDirectoryOnlyItem = false;
const dom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => new Promise(() => {}) };
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return new Response(JSON.stringify({ items: [{ id: 37, name: '第一页图片', enabled: true }], has_more: true, next_offset: 1 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return new Response(JSON.stringify({ items: includeDirectoryOnlyItem ? [{ id: 39, name: '目录新图片', enabled: true }] : [{ id: 38, name: '后续页图片', enabled: true }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ code: 'unexpected' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
dom.window.eval(picker);
dom.window.eval(host);
dom.window.eval(frozenRadar);
let releaseLoad;
let firstLoad = true;
const pageDb = { radarLinks: [], rows: { images: [{ resourceId: '38', name: '后续页图片', enabled: true, originalUrl: 'https://cdn.example.test/later.png', size: '20KB' }] } };
const api = {
  mode: 'http',
  loadDb: () => firstLoad ? new Promise((resolve) => { firstLoad = false; releaseLoad = () => resolve(pageDb); }) : Promise.resolve(pageDb),
  saveRadarLink: async (input) => { savedInputs.push(input); return { id: 71 }; },
};
const mount = dom.window.FrozenRadar.mountRadar(dom.window.document.querySelector('#stage'), api, { view: 'form' });
await waitFor(() => typeof releaseLoad === 'function', 'the frozen renderer requested its scoped page data');
releaseLoad();
await mount;
const document = dom.window.document;
document.querySelector('[data-t="image"]').click();
document.querySelector('#fName').value = '实际冻结表单素材';
document.querySelector('#fUrl').value = 'https://example.test/radar-target';
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-picker-id="38"]'), 'the original picker rendered a later-page catalog item');
const legacyMask = document.querySelector('.pk-mask');
assert.ok(legacyMask, 'the frozen picker was created only after its delayed scoped load');
assert.equal(legacyMask.style.getPropertyValue('display'), 'none', 'the Host suppresses the frozen popup even though it has inline display styling');
assert.equal(legacyMask.style.getPropertyPriority('display'), 'important', 'the frozen popup cannot override the Host suppression');
document.querySelector('[data-picker-id="38"]').click();
await waitFor(() => document.querySelector('#mediaName').textContent === '后续页图片', 'the frozen renderer received the standard picker selection');
assert.equal(document.querySelector('.pk-mask'), null, 'the frozen picker callback cleans itself after a standard selection');
document.querySelector('#fSave').click();
await waitFor(() => savedInputs.length === 1, 'the actual frozen save handler wrote its selected material');
assert.equal(savedInputs[0].media_item_id, '38', 'the frozen save payload preserves the later-page material id');
includeDirectoryOnlyItem = true;
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-picker-id="39"]'), 'the standard directory may return an item absent from the frozen scoped snapshot');
document.querySelector('[data-picker-id="39"]').click();
await waitFor(() => document.querySelector('#mediaHelp').textContent.includes('素材目录已变化'), 'an unavailable frozen callback row gives a visible retry message');
assert.equal(document.querySelector('#mediaName').textContent, '后续页图片', 'a directory race preserves the existing frozen draft');
assert.equal(document.querySelector('.pk-mask'), null, 'a directory race resolves the hidden frozen picker without leaving a stale overlay');
includeDirectoryOnlyItem = false;
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('.aicrm-material-picker-mask'), 'the original picker reopened for cancellation');
document.querySelector('[data-picker-close]').click();
await waitFor(() => document.querySelector('.pk-mask') === null, 'cancel cleans the pending frozen picker promise');
assert.equal(document.querySelector('#mediaName').textContent, '后续页图片', 'cancelling preserves the frozen form draft');
assert.equal(savedInputs.length, 1, 'cancelling never writes a radar link');
dom.window.close();
console.log('radar Host actual frozen renderer material relay journey: PASS');
