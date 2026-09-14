import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import jsdom from 'jsdom';

const { JSDOM, VirtualConsole } = jsdom;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const channelsTemplate = await fs.readFile(path.join(root, 'web/src/admin/templates/channels.html'), 'utf8');
assert.match(channelsTemplate, /onInput="\{\{ rows\.setChannelQuery \}\}"/, 'the test must exercise the byte-frozen channel onInput seam');
assert.match(channelsTemplate, /aria-label="搜索渠道名称"/, 'the channel search control needs a stable, accessible V3 registration seam');

async function searchInstallerBundle() {
  const result = await build({
    stdin: {
      contents: "import { installCommittedTextSearch } from './web/v3/shared/ui/committedTextSearch'; installCommittedTextSearch();",
      resolveDir: root,
      sourcefile: 'committed-text-search-test-entry.ts',
    },
    bundle: true,
    format: 'iife',
    platform: 'browser',
    target: 'es2020',
    write: false,
    minify: true,
    logLevel: 'warning',
  });
  return result.outputFiles[0].text.replace(/<\/script/gi, '<\\/script');
}

const installer = await searchInstallerBundle();
const pause = (ms = 5) => new Promise((resolve) => setTimeout(resolve, ms));

function enter(window, input, properties = {}) {
  const event = new window.KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter', code: 'Enter' });
  Object.entries(properties).forEach(([name, value]) => Object.defineProperty(event, name, { value }));
  input.dispatchEvent(event);
  return event;
}

const dom = new JSDOM(`<!doctype html><body data-page="channels">
  <section id="channel-host"><input aria-label="搜索渠道名称" value=""><div data-channel-row data-search-text="alpha">Alpha</div><div data-channel-row data-search-text="中文渠道">中文渠道</div></section>
  <div class="aicrm-group-chat-picker-mask"><input data-group-picker-search></div>
  <div class="aicrm-tag-picker"><input data-role="search"></div>
  <div data-operation-member-picker><input data-operation-member-search></div>
  <div class="aicrm-material-picker-mask"><input data-picker-search></div>
</body>`, {
  url: 'https://test.invalid/admin/channels', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
});

try {
  const { window } = dom;
  const bindChannelDonor = () => {
    const input = window.document.querySelector('input[aria-label="搜索渠道名称"]');
    input.addEventListener('input', () => {
      window.__channelSearches = (window.__channelSearches || 0) + 1;
      const query = input.value.trim().toLocaleLowerCase('zh-CN');
      // This is the same render boundary as the frozen controller: it replaces
      // the input together with the filtered rows.
      const rows = [
        ['alpha', 'Alpha'],
        ['中文渠道', '中文渠道'],
      ].filter(([text]) => !query || text.toLocaleLowerCase('zh-CN').includes(query));
      window.document.getElementById('channel-host').innerHTML = `<input aria-label="搜索渠道名称" value="${input.value}">${rows.map(([text, label]) => `<div data-channel-row data-search-text="${text}">${label}</div>`).join('')}`;
      bindChannelDonor();
    });
  };
  bindChannelDonor();

  const calls = { group: 0, tag: 0, staffInput: 0, staffEnter: 0, materialEnter: 0 };
  window.document.querySelector('[data-group-picker-search]').addEventListener('input', () => { calls.group += 1; });
  window.document.querySelector('.aicrm-tag-picker [data-role="search"]').addEventListener('input', () => { calls.tag += 1; });
  window.document.querySelector('[data-operation-member-search]').addEventListener('input', () => { calls.staffInput += 1; });
  window.document.querySelector('[data-operation-member-search]').addEventListener('keydown', (event) => { if (event.key === 'Enter') calls.staffEnter += 1; });
  window.document.querySelector('[data-picker-search]').addEventListener('keydown', (event) => { if (event.key === 'Enter') calls.materialEnter += 1; });

  // The two production bundles both call the installer. The document-scoped
  // Symbol state keeps exactly one capture policy for forwarded events.
  window.eval(installer);
  window.eval(installer);

  let channelInput = window.document.querySelector('input[aria-label="搜索渠道名称"]');
  channelInput.value = '中';
  channelInput.setSelectionRange(1, 1);
  channelInput.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  channelInput.dispatchEvent(new window.Event('input', { bubbles: true, cancelable: true }));
  assert.equal(window.__channelSearches || 0, 0, 'typing must keep the channel query as a draft');
  assert.equal(window.document.querySelectorAll('[data-channel-row]').length, 2, 'typing must not redraw channel rows');
  channelInput.dispatchEvent(new window.CompositionEvent('compositionend', { bubbles: true }));
  const candidateEnter = enter(window, channelInput, { keyCode: 229 });
  assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate confirmation keeps the browser default behavior');
  assert.equal(window.__channelSearches || 0, 0, 'IME candidate confirmation must not submit a search');

  await pause();
  const committedEnter = enter(window, channelInput);
  assert.equal(committedEnter.defaultPrevented, true, 'a deliberate search Enter is consumed before the frozen handler');
  assert.equal(window.__channelSearches, 1, 'two bundles forward one committed channel query exactly once');
  assert.equal(window.document.querySelectorAll('[data-channel-row]').length, 1, 'Enter applies the current channel draft');
  channelInput = window.document.querySelector('input[aria-label="搜索渠道名称"]');
  assert.equal(window.document.activeElement, channelInput, 'a channel redraw restores focus to the rebuilt search field');
  assert.deepEqual([channelInput.selectionStart, channelInput.selectionEnd], [1, 1], 'a channel redraw restores the query selection');
  channelInput.value = '';
  enter(window, channelInput);
  assert.equal(window.__channelSearches, 2, 'empty Enter explicitly reloads all channel rows');
  assert.equal(window.document.querySelectorAll('[data-channel-row]').length, 2, 'empty Enter restores all channel rows');

  const group = window.document.querySelector('[data-group-picker-search]');
  const tag = window.document.querySelector('.aicrm-tag-picker [data-role="search"]');
  const staff = window.document.querySelector('[data-operation-member-search]');
  const material = window.document.querySelector('[data-picker-search]');
  for (const input of [group, tag, staff, material]) {
    input.value = '草稿';
    input.dispatchEvent(new window.Event('input', { bubbles: true }));
  }
  assert.deepEqual(calls, { group: 0, tag: 0, staffInput: 0, staffEnter: 0, materialEnter: 0 }, 'picker input events keep drafts and do not invoke their frozen searches');
  enter(window, group);
  enter(window, tag);
  enter(window, staff);
  enter(window, material);
  assert.deepEqual(calls, { group: 1, tag: 1, staffInput: 0, staffEnter: 1, materialEnter: 1 }, 'each picker forwards one Enter to its existing authoritative handler');

  staff.dispatchEvent(new window.CompositionEvent('compositionstart', { bubbles: true }));
  const staffImeEnter = enter(window, staff, { isComposing: true });
  assert.equal(staffImeEnter.defaultPrevented, false, 'staff picker IME confirmation keeps its default browser behavior');
  assert.equal(calls.staffEnter, 1, 'staff picker IME confirmation does not submit a directory read');
} finally {
  await pause(20);
  dom.window.close();
}

console.log('committed text search: PASS');
