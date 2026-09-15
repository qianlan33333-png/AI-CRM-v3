import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { JSDOM, VirtualConsole } from 'jsdom';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../..');
const template = await fs.readFile(path.join(root, 'internal/webshell/templates/admin_access.html'), 'utf8');
const host = await fs.readFile(path.join(root, 'internal/webshell/static/admin_console/admin_access.js'), 'utf8');
assert.match(template, /id="admin-access-search"/, 'test must use the actual access user-search seam');
assert.match(template, /id="admin-access-employee-search"/, 'test must use the actual access employee-directory seam');

const result = await build({
  stdin: { contents: "import { installCommittedTextSearch } from './web/v3/shared/ui/committedTextSearch'; installCommittedTextSearch();", resolveDir: root, sourcefile: 'admin-access-committed-search.ts' },
  bundle: true, format: 'iife', platform: 'browser', target: 'es2020', write: false, minify: true, logLevel: 'warning',
});
const sharedSearch = result.outputFiles[0].text.replace(/<\/script/gi, '<\\/script');
const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const reply = (payload, status = 200) => new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } });
const employee = (wecomUserID, displayName = wecomUserID) => ({ wecom_userid: wecomUserID, display_name: displayName, authorized_account: false });
const usersPayload = () => ({
  users: [
    { admin_user_id: 1, display_name: '管理员甲', wecom_userid: 'AdminFixtureID', role: 'super_admin', login_enabled: true, actions: { set_login_enabled: true, change_role: true, bind_wecom_userid: true, reset_password: true, transfer_super_admin: true } },
    { admin_user_id: 2, display_name: '只读乙', wecom_userid: 'ViewerFixtureID', role: 'viewer', login_enabled: true, actions: {} },
  ],
  actor: { admin_user_id: 1 }, capabilities: { provision_admin: true, provision_viewer: true, transfer_super_admin: true },
});
function enter(window, input, properties = {}) {
  const event = new window.KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter', code: 'Enter' });
  for (const [name, value] of Object.entries(properties)) Object.defineProperty(event, name, { value });
  input.dispatchEvent(event);
  return event;
}

const dom = new JSDOM(template, {
  url: 'https://test.invalid/admin/access', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    window.Headers = globalThis.Headers; window.Response = globalThis.Response; window.AbortController = globalThis.AbortController;
    Object.defineProperty(window.crypto, 'randomUUID', { value: () => '00000000-0000-4000-8000-000000000000', configurable: true });
    window.AdminFmt = { localTime: (value) => value };
    window.confirm = () => true;
  },
});
Object.defineProperty(dom.window.document, 'cookie', { value: 'aicrm_admin_csrf=csrf-proof', configurable: true });
const calls = [];
let resolveLate;
dom.window.fetch = (url, options = {}) => {
  const target = new URL(String(url), dom.window.location.href);
  calls.push(target.pathname + target.search);
  if (target.pathname === '/api/admin/access/users') return Promise.resolve(reply(usersPayload()));
  if (target.pathname !== '/api/admin/access/enterprise-employees') return Promise.resolve(reply({ error: 'not_found' }, 404));
  const query = target.searchParams.get('query') || '';
  if (query === 'failure') return Promise.resolve(reply({ error: 'directory_unavailable' }, 503));
  if (query === 'forbidden') return Promise.resolve(reply({ error: 'permission_denied' }, 403));
  if (query === 'lateA') return new Promise((resolve) => { resolveLate = () => resolve(reply({ items: [employee('LateA')], has_more: false })); });
  if (query === 'lateB') return Promise.resolve(reply({ items: [employee('LateB')], has_more: false }));
  if (query === 'next') return Promise.resolve(reply({ items: [employee('NextCandidate')], has_more: false }));
  return Promise.resolve(reply({ items: [employee('CurrentCandidate')], has_more: false }));
};

try {
  dom.window.eval(sharedSearch);
  dom.window.eval(host);
  await delay(20);
  const document = dom.window.document;
  const userSearch = document.querySelector('#admin-access-search');
  assert.equal(document.querySelectorAll('#admin-access-users-body tr').length, 2, 'actual authorized users render before a local search');
  userSearch.value = '只读'; userSearch.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  assert.equal(document.querySelectorAll('#admin-access-users-body tr').length, 2, 'raw user input keeps the existing authorized list as a draft');
  const userCandidate = enter(dom.window, userSearch, { keyCode: 229, isComposing: true });
  assert.equal(userCandidate.defaultPrevented, false, 'user search IME candidate Enter keeps browser behavior');
  assert.equal(document.querySelectorAll('#admin-access-users-body tr').length, 2, 'user IME candidate Enter does not filter');
  enter(dom.window, userSearch);
  assert.equal(document.querySelectorAll('#admin-access-users-body tr').length, 1, 'ordinary Enter applies the existing user filter');
  assert.match(document.querySelector('#admin-access-users-body').textContent, /ViewerFixtureID/, 'user filter continues to match the existing wecom_userid field');
  userSearch.value = '管理员'; userSearch.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  document.querySelector('#admin-access-refresh').click(); await delay(10);
  assert.equal(document.querySelectorAll('#admin-access-users-body tr').length, 1, 'refresh retains the committed user query while a different draft is present');
  assert.equal(userSearch.value, '管理员', 'refresh does not overwrite a newer user-search draft');

  document.querySelector('#admin-access-provision').click(); await delay(20);
  const employeeSearch = document.querySelector('#admin-access-employee-search');
  assert.match(document.querySelector('#admin-access-employee-results').textContent, /CurrentCandidate/, 'initial authorized employee directory renders');
  document.querySelector('#admin-access-employee-results button[data-wecom-userid]').click();
  assert.equal(document.querySelector('#admin-access-provision-next').disabled, false, 'an actual directory choice is selected before a failed refresh');
  const beforeDraftCalls = calls.filter((item) => item.includes('/enterprise-employees')).length;
  employeeSearch.value = 'failure'; employeeSearch.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  await delay(280);
  assert.equal(calls.filter((item) => item.includes('/enterprise-employees')).length, beforeDraftCalls, 'raw employee input does not start a directory read');
  employeeSearch.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
  const employeeCandidate = enter(dom.window, employeeSearch, { keyCode: 229, isComposing: true });
  assert.equal(employeeCandidate.defaultPrevented, false, 'employee IME candidate Enter remains browser-owned');
  assert.equal(calls.filter((item) => item.includes('query=failure')).length, 0, 'employee IME candidate Enter does not read the directory');
  employeeSearch.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true })); await delay(5);
  enter(dom.window, employeeSearch); await delay(280);
  assert.match(document.querySelector('#admin-access-employee-search-status').textContent, /仍显示上次读取/, 'transient employee directory failure reports cached authorized rows');
  assert.match(document.querySelector('#admin-access-employee-results').textContent, /CurrentCandidate/, 'transient employee directory failure preserves candidate rows');
  assert.equal(document.querySelector('#admin-access-provision-next').disabled, false, 'transient employee directory failure preserves the selected candidate');

  employeeSearch.value = 'lateA'; employeeSearch.dispatchEvent(new dom.window.Event('input', { bubbles: true })); enter(dom.window, employeeSearch); await delay(260);
  employeeSearch.value = 'lateB'; employeeSearch.dispatchEvent(new dom.window.Event('input', { bubbles: true })); enter(dom.window, employeeSearch); await delay(280);
  assert.match(document.querySelector('#admin-access-employee-results').textContent, /LateB/, 'newer committed directory request renders first');
  resolveLate(); await delay(20);
  assert.match(document.querySelector('#admin-access-employee-results').textContent, /LateB/, 'late aborted directory response cannot replace the newer committed query');

  employeeSearch.value = 'forbidden'; employeeSearch.dispatchEvent(new dom.window.Event('input', { bubbles: true })); enter(dom.window, employeeSearch); await delay(280);
  assert.equal(document.querySelector('#admin-access-provision').hidden, true, '403 hides the Owner provisioning action');
  assert.equal(document.querySelectorAll('#admin-access-users-body tr').length, 0, '403 clears the previously authorized employee view');
  assert.equal(document.querySelector('#admin-access-search').disabled, true, '403 disables the read and Owner-write entry points in this stale page');
} finally {
  dom.window.close();
}
console.log('admin access committed search: PASS');
