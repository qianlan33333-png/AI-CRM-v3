import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const host = await buildTestBrowserBundle(path.join(root, 'v3', 'materialLibraryPresentation.ts'));
const sleep = () => new Promise((resolve) => setTimeout(resolve, 0));

function response(payload) {
  return new Response(JSON.stringify(payload), { status: 200, headers: { 'content-type': 'application/json' } });
}

function domFor(page, markup, fetcher) {
  return new JSDOM(`<!doctype html><body data-page="${page}"><header class="admin-topbar"><div class="admin-topbar-head"><h1>素材库</h1></div><div class="admin-topbar-meta"></div></header><main id="stage" data-material-library-workspace="true">${markup}</main></body>`, {
    url: `https://test.invalid/admin/materials?tab=${page === 'attach' ? 'attachments' : 'miniprograms'}`,
    runScripts: 'outside-only', pretendToBeVisual: true,
    beforeParse(window) { window.Response = Response; window.Headers = Headers; window.Request = Request; window.fetch = fetcher; },
  });
}

async function settle() { await sleep(); await sleep(); }

{
  let reads = 0;
  const dom = domFor('attach', `
    <div style="height:52px"><button id="upload">上传附件</button></div>
    <section><div><input placeholder="搜索附件名"></div><table><thead><tr><th>附件名</th><th>标签</th><th>类型</th><th>大小</th><th>上传时间</th><th>操作</th></tr></thead><tbody><tr><td><span>PDF</span><span>课程表.pdf</span></td><td>课程</td><td>PDF</td><td>1 B</td><td>旧时间</td><td><button>编辑</button></td></tr></tbody></table></section>
    <details data-material-refresh-details><table><thead><tr><th>素材</th></tr></thead><tbody><tr id="diagnostic"><td>刷新诊断</td></tr></tbody></table></details>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/attachment-library') throw new Error(`unexpected attachment request ${url.pathname}`);
    reads += 1;
    return response({ items: [{ id: 7, name: '课程表.pdf', file_name: 'course.pdf', mime_type: 'application/pdf', file_size: 417430, description: '', tags: ['课程'], enabled: true, version: 3, created_by: 1, updated_by: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' }], total: 1, limit: 100, offset: 0 });
  });
  try {
    let uploads = 0;
    dom.window.document.querySelector('#upload').addEventListener('click', () => { uploads += 1; });
    dom.window.eval(host);
    dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    const topbarUpload = dom.window.document.querySelector('[data-page-header-actions="material-library-attach"] #upload');
    assert.equal(topbarUpload, dom.window.document.querySelector('#upload'), 'the original attachment upload control is moved, not recreated');
    topbarUpload.click(); assert.equal(uploads, 1, 'the original attachment upload callback remains connected');
    assert.equal(dom.window.getComputedStyle(dom.window.document.querySelector('#stage > div[style*="height"]')).display, 'none', 'the duplicate donor attachment title is not visible');
    assert.deepEqual([...dom.window.document.querySelectorAll('thead th')].slice(0, 8).map((cell) => cell.textContent), ['名称', '标签', '类型', '大小', '创建时间', '启用状态', '版本', '操作'], 'attachment table exposes its actual workspace fields');
    const row = dom.window.document.querySelector('tbody tr');
    assert.equal(row.cells[3].textContent, '408 KB');
    assert.equal(row.cells[5].textContent, '启用');
    assert.equal(row.cells[6].textContent, 'v3');
    const input = dom.window.document.querySelector('input[placeholder="搜索附件名"]');
    const beforeDraft = reads;
    input.value = '课程';
    input.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
    input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    assert.equal(reads, beforeDraft, 'an attachment IME draft cannot read or filter');
    input.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await sleep();
    assert.equal(reads, beforeDraft, 'the IME candidate Enter is not a committed search');
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle();
    assert.equal(reads, beforeDraft + 1, 'an explicit post-IME Enter reads the attachment owner list once');
    assert.equal(dom.window.document.querySelector('#diagnostic').hidden, false, 'attachment search never filters refresh diagnostics');
  } finally { dom.window.close(); }
}

{
  let reads = 0; let searches = 0;
  const dom = domFor('mpLib', `
    <div style="height:52px"><button id="create">新建小程序卡片</button></div>
    <section><input id="fMpQuery"><button id="mpSearch">查询</button><div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr))"><div><div><div style="height:112px"></div><div><div>报名卡片</div><div>● 可用</div><div>已启用</div><div><button>编辑</button><button>删除</button></div></div></div></div></div></section>`, async (input) => {
    const url = new URL(typeof input === 'string' ? input : input.url, 'https://test.invalid');
    if (url.pathname !== '/api/admin/miniprogram-library') throw new Error(`unexpected miniprogram request ${url.pathname}`);
    reads += 1;
    return response({ ok: true, items: [{ id: 9, name: '报名卡片', appid: 'wx-test', pagepath: 'pages/signup', page_path: 'pages/signup', title: '立即报名', thumb_image_url: '', thumb_image_base64: '', thumb_media_id: '', enabled: false, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', created_by: 1, updated_by: 1, version: 2 }], miniprograms: [], total: 1, limit: 100, offset: 0, local_only: true, provider_call_executed: false, real_external_call_executed: false });
  });
  try {
    dom.window.document.querySelector('#create').addEventListener('click', () => { searches += 10; });
    dom.window.document.querySelector('#mpSearch').addEventListener('click', () => { searches += 1; });
    dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    await settle();
    dom.window.document.querySelector('[data-page-header-actions="material-library-mpLib"] #create').click();
    assert.equal(searches, 10, 'the original mini-program create callback remains connected');
    const directory = dom.window.document.querySelector('[data-material-library-mini-directory]');
    assert.ok(directory?.textContent.includes('wx-test') && directory.textContent.includes('已停用') && directory.textContent.includes('v2'), 'mini-program directory exposes actual AppID, state, and version fields');
    const input = dom.window.document.querySelector('#fMpQuery'); const before = reads;
    input.value = '报名'; input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    assert.equal(reads, before, 'ordinary mini-program typing is a draft');
    input.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
    await settle();
    assert.equal(searches, 11, 'committed mini-program search retains the donor query callback');
    assert.equal(reads, before + 1, 'committed mini-program search reads only its own Media list');
  } finally { dom.window.close(); }
}

console.log('material library presentation: PASS');
