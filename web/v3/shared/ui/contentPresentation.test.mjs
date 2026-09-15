import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><div id="target"></div>', { url: 'https://test.invalid' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Event: dom.window.Event });
const bundle = await build({
  stdin: { contents: "export * from './web/v3/shared/ui/contentPresentation';", resolveDir: process.cwd(), sourcefile: 'content-presentation-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);

const source = {
  content_text: ' 你好 {{客户名}} {{未知变量}} <script>ignored</script> ',
  image_library_ids: [7, 7], attachment_library_ids: [9], miniprogram_library_ids: [3], group_invite_library_ids: [],
};
const before = JSON.stringify(source);
const normalized = mod.normalizeContentPackage(source);
assert.equal(normalized.content_text.startsWith('你好'), true, 'normalisation follows the established trim-on-save contract');
assert.deepEqual(normalized.image_library_ids, [7], 'normalisation retains only established typed package fields');
assert.equal(JSON.stringify(source), before, 'normalisation does not mutate caller state');
const records = [
  { source: 'media-library', kind: 'attachment', id: 9, label: '活动说明', disabledReason: '素材已归档' },
  { source: 'media-library', kind: 'image', id: 7, label: '活动主图', thumbnailURL: '/thumb.png' },
  { source: 'other-source', kind: 'miniprogram', id: 3, label: '错误来源' },
];
const ordered = mod.recordsForContentPackage(normalized, records, 'caller_persisted');
assert.deepEqual(ordered.map((item) => item.kind), ['attachment', 'image', 'miniprogram'], 'only caller-proved Media records preserve persisted order');
assert.match(ordered[0].disabledReason, /已归档/);
assert.match(ordered[2].disabledReason, /待目录确认/, 'another source cannot decorate a same-numbered Media selection');
const rebuilt = mod.contentPackageFromRecords(' 欢迎 ', ordered);
assert.equal('material_order' in rebuilt, false, 'a content package never carries UI-only cross-kind order');
assert.equal(rebuilt.content_text, '欢迎');
assert.deepEqual(rebuilt.image_library_ids, [7]);
const preserveText = (value) => value;
const callerPreserved = mod.normalizeContentPackage({ content_text: ' 前后空白 ' }, preserveText);
assert.equal(callerPreserved.content_text, ' 前后空白 ', 'a caller may preserve visible text until its own validation resolves it');
assert.equal(mod.contentPackageFromRecords(' 前后空白 ', [], preserveText).content_text, ' 前后空白 ', 'confirmed package text uses the same explicit caller policy as preview');
const policy = {
  scan: (text) => text.match(/\{\{[^{}]+\}\}/g) || [],
  variables: [{ token: '{{客户名}}', label: '客户名称' }],
  unknownTokenReason: '当前场景未声明可用变量，将按原文展示',
};
assert.deepEqual(mod.contentVariableIssues(normalized.content_text, policy), [{ token: '{{未知变量}}', reason: '当前场景未声明可用变量，将按原文展示', blocking: false }]);
assert.deepEqual(mod.contentVariableIssues(normalized.content_text, undefined), [], 'without caller syntax, shared presentation invents no variable semantics');

let requests = 0;
globalThis.fetch = () => { requests += 1; throw new Error('presentation must not fetch'); };
const target = document.getElementById('target');
mod.renderContentPresentation(target, { mode: 'preview', package: normalized, selectedRecords: records, materialOrder: 'caller_persisted', variablePolicy: policy });
assert.equal(requests, 0, 'preview never invokes a browser fetch/API command');
assert.match(target.textContent, /预览不会发送内容/);
assert.match(target.textContent, /当前场景未声明可用变量/);
assert.match(target.textContent, /素材已归档/);
assert.equal(target.querySelector('script'), null, 'untrusted content remains text rather than source HTML');
assert.equal(target.querySelectorAll('[data-content-material-key]').length, 3, 'readonly/preview material order is represented once');
const thumbnail = target.querySelector('img');
thumbnail.dispatchEvent(new Event('error'));
assert.equal(target.textContent.includes('缩略图暂不可用'), true, 'a controlled thumbnail has an explicit local fallback');
mod.renderContentPresentation(target, { mode: 'preview', package: { content_text: ' 前后空白 ' }, normalizeText: preserveText });
assert.equal(target.querySelector('.aicrm-content-presentation__text').textContent, ' 前后空白 ', 'presentation uses the caller policy instead of silently applying the legacy trim default');
mod.renderContentPresentation(target, { mode: 'readonly', package: normalized, selectedRecords: records });
assert.match(target.textContent, /最近一次保存结果/);
assert.equal(requests, 0, 'readonly rendering has no side effect');
dom.window.close();
console.log('content presentation: safe preview, readonly, caller variables, controlled thumbnails, unavailable selected records, and order PASS');
