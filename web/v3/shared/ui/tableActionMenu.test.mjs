import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM(`<!doctype html><table><tbody><tr><td id="cell"><div id="actions"><button id="edit">编辑</button><button id="data">数据</button><button id="share">分享</button><button id="copy">复制</button><button id="disable">停用</button><button id="delete" disabled>删除</button></div></td></tr></tbody></table>`, { url: 'https://test.invalid', pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Node: dom.window.Node,
  HTMLElement: dom.window.HTMLElement, HTMLButtonElement: dom.window.HTMLButtonElement,
  HTMLAnchorElement: dom.window.HTMLAnchorElement, KeyboardEvent: dom.window.KeyboardEvent,
});
const bundle = await build({
  stdin: { contents: "export * from './web/v3/shared/ui/tableActionMenu';", resolveDir: process.cwd(), sourcefile: 'table-action-menu-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);

const container = document.getElementById('actions');
const share = document.getElementById('share');
const copy = document.getElementById('copy');
const disabledDelete = document.getElementById('delete');
let copied = 0;
copy.addEventListener('click', () => { copied += 1; });
const mounted = mod.mountTableActionMenu(container, { owner: 'product-list', primaryCount: 2 });
assert.ok(mounted, 'a six-action row receives the shared overflow control');
assert.equal(container.querySelectorAll(':scope > button').length, 0, 'source controls move under one stable presentation root');
assert.equal(container.querySelectorAll('[data-table-action-menu-trigger="product-list"]').length, 1, 'the row exposes one overflow trigger');
assert.equal(container.querySelector('#edit'), document.getElementById('edit'), 'primary source action identity is retained');
const trigger = container.querySelector('[data-table-action-menu-trigger="product-list"]');
const panel = document.querySelector('[data-table-action-menu-panel="product-list"]');
assert.equal(panel.hidden, true, 'overflow actions start closed');
assert.equal(disabledDelete.disabled, true, 'disabled source action remains disabled after being rehomed');

trigger.focus();
trigger.click();
assert.equal(panel.hidden, false, 'trigger opens the overflow panel');
assert.equal(document.activeElement, share, 'opening moves keyboard focus to the first available overflow action');
const escape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' });
document.dispatchEvent(escape);
assert.equal(escape.defaultPrevented, true, 'Escape is consumed while the action panel is open');
assert.equal(panel.hidden, true, 'Escape closes the panel');
assert.equal(document.activeElement, trigger, 'Escape restores focus to the trigger');

trigger.click();
document.body.dispatchEvent(new dom.window.Event('pointerdown', { bubbles: true }));
assert.equal(panel.hidden, true, 'an outside pointer close does not leave an orphan popup');
trigger.click();
copy.click();
assert.equal(copied, 1, 'the original owner click handler executes exactly once from the overflow panel');
assert.equal(panel.hidden, true, 'choosing an action closes the panel without changing its command semantics');

mounted.dispose();
assert.equal(document.querySelector('[data-table-action-menu-panel="product-list"]'), null, 'dispose removes the floating panel');
assert.equal(container.children.length, 6, 'dispose returns the original controls to their owner container');
assert.equal(container.querySelector('#copy'), copy, 'dispose retains original action identity and listeners');

const short = document.createElement('div');
short.append(document.createElement('button'), document.createElement('button'));
document.body.append(short);
assert.equal(mod.mountTableActionMenu(short, { owner: 'short-row', primaryCount: 2 }), undefined, 'permission-reduced rows keep their available actions directly visible');

dom.window.close();
console.log('table action menu: source actions, keyboard, Escape, outside close, focus restore and reduced-action rows PASS');
