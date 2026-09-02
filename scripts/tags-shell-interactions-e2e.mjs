/**
 * PR03 browser regression for the real staged Tags template in PR10's shell.
 *
 * The staged file is the byte-identical generated donor document used only as
 * a private template carrier. This test extracts its actual template#tpl,
 * executes the frozen admin entry, and proves its visible interactions mount
 * under one v3 sidebar without returning the donor document shell.
 */
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const REPOSITORY = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const stagedPath = path.join(REPOSITORY, 'release', 'web', 'dist', 'admin', 'tags.html');
const ADMIN = await buildTestBrowserBundle(path.join(REPOSITORY, 'web', 'src', 'admin', 'main.ts'));
const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const fail = (message) => { throw new Error(`tags shell DOM regression: ${message}`); };

if (!fs.existsSync(stagedPath)) fail('real release/web/dist/admin/tags.html is missing');
const carrier = new JSDOM(fs.readFileSync(stagedPath, 'utf8'));
const fragment = carrier.window.document.querySelector('template#tpl')?.innerHTML;
carrier.window.close();
if (!fragment?.trim()) fail('staged donor template#tpl is empty');

const html = `<!doctype html><html lang="zh-CN"><body class="admin-shell" data-admin-shell-source="v3_webshell" data-page="tags">
  <div class="admin-layout">
    <aside class="admin-sidebar"><nav aria-label="客户管理后台导航"></nav></aside>
    <div class="admin-main-wrap"><main id="stage" class="stage rich"></main><template id="tpl">${fragment}</template></div>
  </div>
  <script>${ADMIN}</script>
</body></html>`;
const dom = new JSDOM(html, {
  url: 'https://test.invalid/admin/wecom-tags',
  runScripts: 'dangerously',
  pretendToBeVisual: true,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = true;
    window.confirm = () => true;
  },
});

try {
  await sleep(500);
  const { document } = dom.window;
  if (document.querySelectorAll('aside.admin-sidebar').length !== 1 || document.querySelectorAll('main#stage').length !== 1 || document.querySelector('.side,.shell,.side-nav')) fail('workspace did not remain in the sole PR10 shell');
  if (document.querySelectorAll('[data-tag-group-card]').length !== 5) fail('frozen tag groups did not mount from the staged fragment');

  const create = Array.from(document.querySelectorAll('button')).find((button) => button.textContent?.trim() === '新增标签');
  create?.click();
  await sleep(30);
  if (!document.querySelector('#fTagName')) fail('frozen create-tag interaction did not open');
  Array.from(document.querySelectorAll('button')).find((button) => button.textContent?.trim() === '取消')?.click();

  const sync = Array.from(document.querySelectorAll('button')).find((button) => button.textContent?.trim() === '同步企微标签');
  sync?.click();
  await sleep(850);
  const feedback = document.querySelector('#fb-toast')?.textContent || '';
  if (!feedback.includes('已受理') || !feedback.includes('尚未收到 Provider 同步结果')) fail('frozen sync acceptance feedback changed');
  console.log('tags shell DOM interactions: ok');
} finally {
  dom.window.close();
}
