import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { JSDOM } from 'jsdom';

const hostSource = await readFile(new URL('./member_grid_host.js', import.meta.url), 'utf8');

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function installHost(payload) {
  const dom = new JSDOM('<!doctype html><html><body><table><tbody><tr data-record-id="spm_member"><td class="sp-col-renewal_count">0</td></tr></tbody></table></body></html>', {
    runScripts: 'dangerously', url: 'https://crm.test/shared/service-period-member-grid#token',
  });
  const { window } = dom;
  window.fetch = async () => ({
    ok: true,
    clone: () => ({ json: async () => payload }),
    json: async () => payload,
  });
  window.eval(hostSource);
  return { dom, window };
}

test('member-grid host renders only explicitly unavailable renewal counts as dash', async () => {
  const { window } = installHost({ rows: [{ unionid: 'spm_member', version: 1, values: { renewal_count_unavailable: true } }] });
  await window.fetch('/api/public/service-period-member-grid/query');
  const row = window.document.querySelector('tr');
  row.append(window.document.createElement('td'));
  await settle();
  assert.equal(window.document.querySelector('.sp-col-renewal_count').textContent, '—');
});

test('member-grid host retains a factual renewal zero', async () => {
  const { window } = installHost({ rows: [{ unionid: 'spm_member', version: 1, values: { renewal_count: 0 } }] });
  await window.fetch('/api/public/service-period-member-grid/query');
  const row = window.document.querySelector('tr');
  row.append(window.document.createElement('td'));
  await settle();
  assert.equal(window.document.querySelector('.sp-col-renewal_count').textContent, '0');
});
