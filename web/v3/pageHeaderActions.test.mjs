import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const bundle = await build({
  stdin: {
    contents: "import { mountPageHeaderActions, setPageHeaderActionDisabled } from './web/v3/shared/ui/pageHeaderActions'; globalThis.mount = mountPageHeaderActions; globalThis.setDisabled = setPageHeaderActionDisabled;",
    resolveDir: process.cwd(), sourcefile: 'page-header-actions-test-entry.ts',
  }, bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, logLevel: 'warning',
});
const dom = new JSDOM('<!doctype html><header class="admin-topbar"><div class="admin-topbar-head"><h1>唯一标题</h1></div><div class="admin-topbar-meta"><a href="/existing">既有链接</a></div></header>', { runScripts: 'outside-only' });
try {
  dom.window.eval(bundle.outputFiles[0].text);
  let clicked = 0;
  const remove = dom.window.mount('distribution-admin', [
    { label: '打开申请页', href: '/distribution', target: '_blank', variant: 'secondary' },
    { label: '复制申请链接', onClick: () => { clicked += 1; } },
  ]);
  const topbar = dom.window.document.querySelector('.admin-topbar');
  assert.equal(topbar.querySelectorAll('h1').length, 1, 'helper must not introduce a second page title');
  assert.equal(topbar.querySelectorAll('.admin-topbar-meta').length, 1, 'helper reuses existing meta container');
  assert.equal(topbar.querySelector('[data-page-header-actions="distribution-admin"] a').target, '_blank');
  assert.equal(topbar.querySelector('[data-page-header-actions="distribution-admin"] a').rel, 'noopener');
  topbar.querySelector('[data-page-header-actions="distribution-admin"] button').click();
  assert.equal(clicked, 1, 'client action is bound exactly once');
  dom.window.mount('distribution-admin', [{ label: '申请二维码', onClick: () => { clicked += 10; } }]);
  assert.deepEqual([...topbar.querySelectorAll('[data-page-header-actions="distribution-admin"] button')].map((node) => node.textContent), ['申请二维码'], 're-render replaces only the owner actions');
  assert.equal(topbar.querySelector('.admin-topbar-meta > a').textContent, '既有链接', 'SSR link actions remain intact');
  remove();
  assert.ok(topbar.querySelector('[data-page-header-actions="distribution-admin"]'), 'stale cleanup cannot remove a replacement host');
  let rejected = 0;
  let rejectedError;
  let resolvePending;
  const pending = new Promise((resolve) => { resolvePending = resolve; });
  dom.window.mount('distribution-safe', [{
    label: '保存',
    onClick: () => pending,
    onError: (error) => { rejected += 1; rejectedError = error; },
  }]);
  const save = topbar.querySelector('[data-page-header-actions="distribution-safe"] button');
  save.click(); save.click();
  assert.equal(save.disabled, true, 'busy is set before an action can reenter');
  assert.equal(rejected, 0, 'a pending action does not report a failure');
  resolvePending(); await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(save.disabled, false, 'a fulfilled action restores its own control');
  dom.window.mount('distribution-safe', [{ label: '暂不可用', disabled: true, onClick: () => { clicked += 100; } }]);
  const disabled = topbar.querySelector('[data-page-header-actions="distribution-safe"] button');
  disabled.click();
  assert.equal(disabled.disabled, true, 'an explicit action-disabled state is preserved at mount');
  assert.equal(disabled.getAttribute('aria-disabled'), 'true', 'disabled actions expose their state');
  assert.equal(clicked, 1, 'a disabled action cannot invoke its page command');
  dom.window.mount('groupops', [{ id: 'create-plan', label: '创建计划', onClick: () => {} }]);
  const create = topbar.querySelector('[data-page-header-actions="groupops"] button');
  create.focus();
  assert.equal(dom.window.setDisabled('groupops', 'create-plan', true), true, 'stable API updates an existing action by id');
  assert.equal(topbar.querySelector('[data-page-header-actions="groupops"] button'), create, 'stable disabled update keeps the same control node');
  assert.equal(create.disabled, true, 'stable API exposes dynamic disabled state');
  assert.equal(dom.window.setDisabled('groupops', 'missing', true), false, 'stable API never creates an unknown action');
  const rejectedAction = dom.window.mount('distribution-safe', [{
    label: '重试',
    onClick: () => Promise.reject(new Error('expected rejection')),
    onError: (error) => { rejected += 1; rejectedError = error; },
  }]);
  const retry = topbar.querySelector('[data-page-header-actions="distribution-safe"] button');
  retry.click(); await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(rejected, 1, 'a rejected action is consumed by its page feedback hook');
  assert.equal(rejectedError?.message, 'expected rejection');
  assert.equal(retry.disabled, false, 'a rejected action restores its control without an unhandled promise');
  dom.window.mount('distribution-safe', [{ label: '同步失败', onClick: () => { throw new Error('sync'); }, onError: () => { rejected += 1; } }]);
  topbar.querySelector('[data-page-header-actions="distribution-safe"] button').click();
  assert.equal(rejected, 2, 'a synchronous action failure is also reported and contained');
  rejectedAction();
  const empty = new JSDOM('<!doctype html><header class="admin-topbar"><div class="admin-topbar-head"></div></header>', { runScripts: 'outside-only' });
  try {
    empty.window.eval(bundle.outputFiles[0].text);
    const removeImageActions = empty.window.mount('image-library', [{ label: '上传素材', onClick: () => {} }]);
    assert.equal(empty.window.document.querySelectorAll('.admin-topbar-meta').length, 1, 'helper creates the existing topbar action container only when absent');
    removeImageActions();
    assert.equal(empty.window.document.querySelectorAll('.admin-topbar-meta').length, 0, 'cleanup removes only an empty helper-created meta container');
  } finally { empty.window.close(); }
} finally { dom.window.close(); }
console.log('page header actions: PASS');
