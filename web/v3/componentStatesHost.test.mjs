import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><main id="root"></main>', { url: 'https://test.invalid' });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  HTMLInputElement: dom.window.HTMLInputElement,
  KeyboardEvent: dom.window.KeyboardEvent,
  Event: dom.window.Event,
  DOMException: dom.window.DOMException,
  AbortController: dom.window.AbortController,
});

const bundle = await build({
  stdin: { contents: "export { mountComponentStates } from './web/v3/componentStatesHost';", resolveDir: process.cwd(), sourcefile: 'component-states-host-test.ts' },
  bundle: true,
  format: 'esm',
  platform: 'node',
  write: false,
  target: 'es2020',
});
const { mountComponentStates } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const root = document.querySelector('#root');

mountComponentStates(root);
assert.equal(root.dataset.componentStatesReady, 'true');
for (const state of ['loading', 'empty', 'error', 'forbidden', 'readonly', 'invalid', 'ime']) {
  assert.ok(root.querySelector(`[data-state="${state}"]`), `state catalog lists ${state}`);
}

const mode = (name) => root.querySelector(`[data-component-states-mode="${name}"]`).click();
const groupTrigger = () => root.querySelector('[data-component-states-group-open]');
const materialTrigger = () => root.querySelector('[data-component-states-material-open]');
const formTrigger = () => root.querySelector('[data-component-states-form-open]');
const closeGroup = () => document.querySelector('[data-v3-selection-session="group"] [data-v3-group-cancel]').click();
const closeMaterial = () => document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-cancel]').click();

groupTrigger().focus();
groupTrigger().click();
await flush();
const readyGroup = document.querySelector('[data-v3-selection-session="group"]');
assert.ok(readyGroup, 'ready mode opens the real group session');
readyGroup.querySelector('[data-v3-group-key]').click();
readyGroup.querySelector('[data-v3-group-confirm]').click();
await flush();
await flush();
assert.equal(document.querySelector('[data-v3-selection-session="group"]'), null, 'confirmed group session closes');
assert.equal(document.activeElement, groupTrigger(), 'confirmed group session restores the original trigger');
assert.match(root.querySelector('[data-component-states-group-result]').textContent, /北区新品体验群/, 'confirmed selection updates only its summary node');

mode('error');
groupTrigger().focus();
groupTrigger().click();
await flush();
await flush();
let group = document.querySelector('[data-v3-selection-session="group"]');
assert.match(group.textContent, /群聊目录暂时不可用/, 'error mode comes from the live group loader');
group.querySelector('[data-v3-group-reload]').click();
await flush();
await flush();
assert.match(group.textContent, /北区新品体验群/, 'error mode refreshes in the same group session with its second local result');
assert.doesNotMatch(group.querySelector('[data-v3-group-status]').textContent, /暂时不可用/, 'successful retry clears the explicit error without reopening the dialog');
closeGroup();
assert.equal(document.activeElement, groupTrigger(), 'cancelled group session restores its trigger');

mode('loading');
groupTrigger().focus();
groupTrigger().click();
await flush();
group = document.querySelector('[data-v3-selection-session="group"]');
assert.match(group.textContent, /正在读取群目录/, 'loading mode leaves the real group loader pending');
closeGroup();
assert.equal(document.activeElement, groupTrigger(), 'cancelling the pending group loader restores focus');

mode('readonly');
groupTrigger().focus();
groupTrigger().click();
await flush();
group = document.querySelector('[data-v3-selection-session="group"]');
assert.match(group.textContent, /当前记录处于只读状态/, 'readonly mode preserves a local selected record');
assert.equal(group.querySelector('[data-v3-group-confirm]').disabled, true, 'readonly group session blocks confirmation');
closeGroup();

mode('empty');
materialTrigger().focus();
materialTrigger().click();
await flush();
await flush();
let material = document.querySelector('[data-v3-selection-session="material"]');
assert.match(material.textContent, /没有可选素材/, 'empty mode comes from the live material loader');
closeMaterial();
assert.equal(document.activeElement, materialTrigger(), 'cancelled material session restores its trigger');

mode('forbidden');
materialTrigger().focus();
materialTrigger().click();
await flush();
await flush();
material = document.querySelector('[data-v3-selection-session="material"]');
assert.match(material.textContent, /目录权限已收回/, '403 mode stays explicit in the material session');
assert.match(material.textContent, /秋日活动封面/, '403 mode retains the selected local draft for inspection');
assert.equal(material.querySelector('[data-v3-picker-confirm]').disabled, true, '403 material session blocks confirmation');
closeMaterial();

mode('invalid');
materialTrigger().focus();
materialTrigger().click();
await flush();
material = document.querySelector('[data-v3-selection-session="material"]');
assert.match(material.textContent, /待目录确认的失效初选/, 'invalid selected record remains visible without being declared usable');
assert.match(material.textContent, /当前不可用/, 'invalid selected record exposes its reason');
closeMaterial();

formTrigger().focus();
formTrigger().click();
await flush();
await flush();
const form = document.querySelector('[data-v3-selection-session="component-states"]');
const search = form.querySelector('[data-component-states-ime-input]');
assert.ok(form.querySelector('[data-component-states-form-select]'), 'form demo contains a real select');
assert.ok(form.querySelector('[data-component-states-form-textarea]'), 'form demo contains a real textarea');
assert.ok(form.querySelector('[data-component-states-form-editable]'), 'form demo contains a real contenteditable control');
search.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
search.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
const candidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
Object.defineProperty(candidateEnter, 'keyCode', { value: 229 });
search.dispatchEvent(candidateEnter);
assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate Enter remains native');
await flush();
const ordinaryEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
search.dispatchEvent(ordinaryEnter);
assert.equal(ordinaryEnter.defaultPrevented, true, 'ordinary Enter submits the local session loader');
await flush();
form.querySelector('[data-component-states-choice]').click();
form.querySelector('[data-component-states-confirm]').click();
assert.match(form.textContent, /已提交本地示例会话/, 'form confirmation commits only the local SelectionSession');
form.querySelector('[data-component-states-close]').click();
assert.equal(document.activeElement, formTrigger(), 'form cancel restores focus to the still-mounted trigger');

dom.window.close();
console.log('component state Host: real state loaders, local commit, IME, and focus restoration PASS');
