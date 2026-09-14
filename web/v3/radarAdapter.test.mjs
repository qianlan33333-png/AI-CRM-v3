import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/radarAdapter.ts'));
const picker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (check()) return;
    await wait(5);
  }
  throw new Error(`timed out: ${label}`);
}

let includeDirectoryOnlyItem = false;
const dom = new JSDOM(`<!doctype html><body data-page="radarForm"><main id="stage"></main></body>`, {
  url: 'https://test.invalid/admin/radarForm.html', runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    // Radar must wait for the actual standard-components lifecycle before it
    // installs the V3 Material adapter. A resolving lifecycle proves this
    // journey does not exercise the hidden donor popup by accident.
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.__AICRM_TEST_MOCK__ = true;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return new Response(JSON.stringify({ items: [{ id: 1, name: '直播预告主视觉.png', enabled: true }], has_more: true, next_offset: 1 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return new Response(JSON.stringify({ items: [{ id: 39, name: '目录新图片', enabled: true }], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ code: 'unexpected' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
dom.window.eval(picker);
dom.window.eval(host);
const document = dom.window.document;
await waitFor(() => document.querySelector('#btnPick'), 'the actual frozen main mounted the radar form after the V3 lifecycle');
document.querySelector('[data-t="image"]').click();
document.querySelector('#fName').value = '实际冻结表单素材';
document.querySelector('#fUrl').value = 'https://example.test/radar-target';
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the V3 picker replaced the frozen visual popup');
const legacyMask = document.querySelector('.pk-mask');
assert.ok(legacyMask, 'the frozen picker was created only after its delayed scoped load');
assert.equal(legacyMask.style.getPropertyValue('display'), 'none', 'the Host suppresses the frozen popup even though it has inline display styling');
assert.equal(legacyMask.style.getPropertyPriority('display'), 'important', 'the frozen popup cannot override the Host suppression');
await waitFor(() => document.querySelector('[data-v3-material-key$=":1"]'), 'the scoped V3 picker rendered only its first server page');
const search = document.querySelector('[data-v3-search-managed="true"]');
search.focus(); search.value = '中文草稿'; search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
assert.equal(document.activeElement, search, 'a native V3 search draft does not redraw or steal focus');
assert.equal(document.querySelectorAll('[data-v3-material-key]').length, 1, 'typing alone does not load another catalogue page');
document.querySelector('[data-v3-material-key$=":1"]').click();
assert.ok(document.querySelector('[data-v3-selection-session="material"]'), 'selection remains temporary until the operator confirms');
assert.equal(document.querySelector('#mediaPicked').hidden, true, 'a temporary picker selection cannot write the frozen form');
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('.pk-mask') === null, 'the frozen renderer received the standard picker selection and resolved its callback');
assert.equal(document.querySelector('.pk-mask'), null, 'the frozen picker callback cleans itself after a standard selection');
await waitFor(() => document.querySelector('#mediaName')?.textContent === '直播预告主视觉.png', 'the actual frozen form received its scoped material callback');
assert.equal(document.querySelector('#mediaPicked').hidden, false, 'confirmation applies the V3 selection through the frozen caller callback');
includeDirectoryOnlyItem = true;
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the V3 picker reopened for a directory race');
await waitFor(() => document.querySelector('[data-v3-material-key$=":1"]'), 'the V3 picker starts again from the scoped first page');
document.querySelector('[data-v3-picker-more]').click();
await waitFor(() => document.querySelector('[data-v3-material-key$=":39"]'), 'the standard directory may return an item absent from the frozen scoped snapshot');
document.querySelector('[data-v3-material-key$=":39"]').click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => document.querySelector('#mediaHelp').textContent.includes('素材目录已变化'), 'an unavailable frozen callback row gives a visible retry message');
assert.equal(document.querySelector('#mediaName').textContent, '直播预告主视觉.png', 'a directory race preserves the existing frozen draft');
assert.equal(document.querySelector('.pk-mask'), null, 'a directory race resolves the hidden frozen picker without leaving a stale overlay');
includeDirectoryOnlyItem = false;
document.querySelector('#btnPick').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the V3 picker reopened for cancellation');
document.querySelector('[data-v3-picker-close]').click();
await waitFor(() => document.querySelector('.pk-mask') === null, 'cancel cleans the pending frozen picker promise');
assert.equal(document.querySelector('#mediaName').textContent, '直播预告主视觉.png', 'cancelling preserves the frozen form draft');
dom.window.close();
console.log('radar Host actual frozen renderer material relay journey: PASS');
