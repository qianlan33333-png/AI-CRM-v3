import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const origin = process.argv[2];
if (!origin) throw new Error('missing runtime origin');
const repository = path.dirname(path.dirname(path.dirname(fileURLToPath(import.meta.url))));
const source = fs.readFileSync(path.join(repository, 'internal/webshell/templates/admin_customers.html'), 'utf8');
const script = fs.readFileSync(path.join(repository, 'internal/webshell/static/admin_console/admin_customers.js'), 'utf8');
const template = source
  .replace('{{define "admin_customers"}}', '')
  .replace(/{{if eq \.RequestPath "\/admin\/customers"}}([\s\S]*?){{else}}([\s\S]*?){{end}}\s*<\/div>\s*{{end}}\s*$/, '$1\n</div>');
const calls = [];
const dom = new JSDOM(`<!doctype html><html><body>${template}<script>${script}</script></body></html>`, {
  url: `${origin}/admin/customers`, runScripts: 'dangerously', pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = Headers;
    window.confirm = () => true;
    window.fetch = async (input, options = {}) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === '/api/admin/customers') {
        return new Response(JSON.stringify({ items: [{ customer_id: 1, display_name: '运行时客户', oneid: 'CID-1', phone_masked: '138****0000', updated_at: '2026-09-06T00:00:00Z' }], total: 1, total_is_estimate: false }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.pathname.startsWith('/api/v1/customer-tag-commands') && String(options.method || 'GET').toUpperCase() !== 'GET') calls.push(url.pathname);
      return globalThis.fetch(url, options);
    };
  },
});
try {
  dom.window.document.cookie = 'aicrm_admin_csrf=runtime-csrf; path=/';
  await new Promise((resolve) => setTimeout(resolve, 30));
  const checkbox = dom.window.document.querySelector('input[aria-label="选择客户 1"]');
  if (!checkbox) throw new Error('actual Host list did not render selection');
  checkbox.checked = true;
  checkbox.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  const form = dom.window.document.querySelector('#customer-tag-batch');
  if (!form || form.querySelector('[name="add_tag_ids"] option')?.textContent !== '运行时分组 / 运行时标签') throw new Error('actual catalog route did not render the local tag name');
  const tagOptions = [...form.querySelector('[name="add_tag_ids"]').options];
  if (tagOptions.length !== 1 || tagOptions[0].textContent !== '运行时分组 / 运行时标签') throw new Error('actual catalog route did not render the local tag name');
  tagOptions[0].selected = true;
  form.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  let result = '';
  for (let remaining = 40; remaining > 0; remaining--) {
    await new Promise((resolve) => setTimeout(resolve, 10));
    result = dom.window.document.querySelector('#customer-tag-batch-result')?.textContent || '';
    if (calls.length === 2 && result.includes('已刷新执行结果：客户 #1：queued；观察标签：已观察标签（active）')) break;
  }
  if (calls.join(',') !== '/api/v1/customer-tag-commands/preview,/api/v1/customer-tag-commands' || !result.includes('已刷新执行结果：客户 #1：queued；观察标签：已观察标签（active）')) {
    throw new Error(`Host tag interaction calls=${calls.join(',')} result=${result}`);
  }
  console.log('customer-tag-command-runtime-host: PASS');
} finally { dom.window.close(); }
