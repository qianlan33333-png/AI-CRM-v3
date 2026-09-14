import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle=await buildTestBrowserBundle(path.join(root,'web/v3/standardComponentsHost.ts'));
for (const pathname of ['/admin/channels/17/edit','/admin/customers/9']) {
 const calls=[];
 const dom=new JSDOM('<!doctype html><body></body>',{url:'https://test.invalid'+pathname,runScripts:'dangerously',beforeParse(w){
  w.Request=Request;w.Response=Response;w.Headers=Headers;
  w.fetch=async (input,init)=>{calls.push({input,init});return new Response('{}',{status:200});};
 }});
 try {
  dom.window.document.cookie='aicrm_admin_csrf=test-csrf';
  dom.window.eval(bundle);
  const unscoped={method:'POST',headers:{Accept:'application/json'},credentials:'same-origin'};
  await dom.window.fetch('/api/admin/common/operation-members/sync',unscoped);
  assert.equal(calls.length,1);
  assert.equal(calls[0].init,unscoped,'the loader must not assign an unrelated caller the group_ops scope');
  assert.equal(calls[0].init.body,undefined,'the loader must not manufacture a refresh body for an unscoped caller');
  assert.equal(calls[0].init.headers['X-CSRF-Token'],undefined,'the loader must not add a cross-page CSRF envelope');
  const explicit={method:'POST',body:'{"scope":"custom"}',headers:{'Idempotency-Key':'existing'}};
  await dom.window.fetch('/api/admin/common/operation-members/sync',explicit);
  assert.equal(calls[1].init,explicit,'explicit V3 envelopes must not be changed');
  const foreign={method:'POST',headers:{Accept:'application/json'}};
  await dom.window.fetch('https://other.invalid/api/admin/common/operation-members/sync',foreign);
  assert.equal(calls[2].init,foreign,'never attach CSRF to another origin');
 } finally {dom.window.close();}
}
console.log('standard component loader leaves every staff refresh scope to its caller: PASS');

const fs = await import('node:fs/promises');
const picker = await fs.readFile(path.join(root,'internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js'),'utf8');

// The shared picker accepts an explicit selection contract. A multi-select
// limit must stay a finite, positive integer and the owner context is always
// single-select, regardless of an accidental caller-supplied limit.
{
 const dom=new JSDOM('<!doctype html><body></body>',{url:'https://test.invalid/admin/channels/17/edit',runScripts:'dangerously',beforeParse(w){
  w.Request=Request;w.Response=Response;w.Headers=Headers;
  w.AdminApi={responseErrorMessage:(_r,_d,f)=>f,errorMessage:(e,f)=>e?.message||f};
  w.fetch=async()=>new Response(JSON.stringify({items:[{staff_id:12,user_id:'alice',display_name:'Alice'}]}),{status:200});
 }});
 try {
  dom.window.eval(picker);
  for (const invalidMax of [Infinity, 1.5, 0, -1]) {
   await dom.window.OperationMemberPicker.open({context:'channel_assignees',selection:{mode:'multiple',max:invalidMax}});
   assert.match(dom.window.document.querySelector('[data-operation-member-description]').textContent,/本次最多选择 5 人/);
   assert.equal(dom.window.document.querySelectorAll('input[type="checkbox"]').length,1);
  }
  await dom.window.OperationMemberPicker.open({context:'group_ops_owner',selection:{mode:'single',max:8}});
  assert.equal(dom.window.document.querySelector('[data-operation-member-title]').textContent,'选择负责人');
  assert.match(dom.window.document.querySelector('[data-operation-member-description]').textContent,/选择一位负责人/);
  assert.doesNotMatch(dom.window.document.querySelector('[data-operation-member-description]').textContent,/最多选择/);
  assert.equal(dom.window.document.querySelectorAll('input[type="checkbox"]').length,0);
  assert.equal(dom.window.document.querySelector('[data-operation-member-confirm]').textContent,'确认负责人');
 } finally {dom.window.close();}
}
console.log('frozen staff selector selection contract: PASS');
