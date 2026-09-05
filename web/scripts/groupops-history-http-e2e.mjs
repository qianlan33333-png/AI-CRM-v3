import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const base = process.argv[2];
if (!base) throw new Error('usage: groupops-history-http-e2e.mjs <host-base-url>');
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
const hostURL = new URL('/admin/groupopsDetail.html?history=1&id=901', base);
const host = await fetch(hostURL);
if (host.status !== 200) throw new Error(`host status=${host.status}`);
let html = await host.text();
const readonlyPath = '/groupops-assets/aiassistant/send_content_readonly_detail.js';
if (!html.includes(`src="${readonlyPath}"`) || !html.includes('send_content_readonly_detail.css')) throw new Error('actual Group Ops Host did not include the manifest-verified read-only renderer');
const readonly = fs.readFileSync(path.join(root, 'donors/ai-assistant-production/static/send_content_readonly_detail.js'), 'utf8');
html = html.replace(`<script defer src="${readonlyPath}"></script>`, `<script>${readonly}</script>`);
html = html.replace(/<script type="module" src="\/groupops-assets\/assets\/admin-test\.js"><\/script>/, `<script>${bundle}</script>`);
if (!html.includes(bundle)) throw new Error('actual Host admin entry was not replaced for the existing JSDOM harness');
const dom = new JSDOM(html, {
  url: hostURL.href,
  runScripts: 'dangerously',
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = Headers;
    window.fetch = (input, init = {}) => fetch(new URL(String(input), window.location.href), init);
  },
});
await new Promise((resolve) => setTimeout(resolve, 80));
const stage = dom.window.document.querySelector('#stage');
const text = stage?.textContent || '';
if (!stage || !stage.querySelector('.send-readonly-detail')) throw new Error('historical node did not mount through the actual Host renderer');
if (!text.includes('标题 <img src=x onerror=alert(1)>') || !text.includes('正文 <script>window.__groupopsHistoryXSS=1</script>') || !text.includes('image #m1')) throw new Error(`imported node facts missing from rendered Host: ${text}`);
if (stage.querySelector('img') || stage.querySelector('script') || dom.window.__groupopsHistoryXSS === 1) throw new Error('historical title/body executed as markup');
if ([...stage.querySelectorAll('button')].some((button) => /创建|同步|激活|发送/.test(button.textContent || ''))) throw new Error('history Host exposed a current-plan command');
dom.window.close();
console.log('groupops-history HTTP Host e2e: PASS');
