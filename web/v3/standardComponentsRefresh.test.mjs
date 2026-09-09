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
  await dom.window.fetch('/api/admin/common/operation-members/sync',{method:'POST',headers:{Accept:'application/json'},credentials:'same-origin'});
  assert.equal(calls.length,1);
  assert.equal(calls[0].init.headers.get('X-CSRF-Token'),'test-csrf');
  assert.match(calls[0].init.headers.get('Idempotency-Key'),/^operation-members-/);
  assert.deepEqual(JSON.parse(calls[0].init.body),{scope:'group_ops',page_size:100});
  const explicit={method:'POST',body:'{"scope":"custom"}',headers:{'Idempotency-Key':'existing'}};
  await dom.window.fetch('/api/admin/common/operation-members/sync',explicit);
  assert.equal(calls[1].init,explicit,'explicit V3 envelopes must not be changed');
  const foreign={method:'POST',headers:{Accept:'application/json'}};
  await dom.window.fetch('https://other.invalid/api/admin/common/operation-members/sync',foreign);
  assert.equal(calls[2].init,foreign,'never attach CSRF to another origin');
 } finally {dom.window.close();}
}
console.log('shared staff refresh envelopes on channel and customer Hosts: PASS');

// Exercise the real frozen picker, including its selection state. The Host
// intercepts refresh once and leaves rows intact when the Provider read fails.
const fs = await import('node:fs/promises');
const picker = await fs.readFile(path.join(root,'internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js'),'utf8');
const pause = (ms) => new Promise(resolve=>setTimeout(resolve,ms));
for (const syncStatus of [200,503]) {
 let posts=0;let reads=0;let selected;
 const dom=new JSDOM('<!doctype html><body></body>',{url:'https://test.invalid/admin/channels/17/edit',runScripts:'dangerously',beforeParse(w){
  w.Request=Request;w.Response=Response;w.Headers=Headers;
  w.AdminApi={responseErrorMessage:(_r,_d,f)=>f,errorMessage:(e,f)=>e?.message||f};
  w.fetch=async (input,init={})=>{
   if (init.method==='POST') { posts++;return new Response(JSON.stringify({ok:syncStatus===200}),{status:syncStatus}); }
   reads++;return new Response(JSON.stringify({items:[{staff_id:12,user_id:'alice',display_name:posts&&syncStatus===200?'刷新后昵称':'原昵称'}]}),{status:200});
  };
 }});
 try {
  dom.window.document.cookie='aicrm_admin_csrf=test-csrf';
  dom.window.eval(bundle);dom.window.eval(picker);
  await dom.window.OperationMemberPicker.open({scope:'channel_code',onSelect:m=>{selected=m;}});
  dom.window.document.querySelector('[data-operation-member-row-select]').click();
  dom.window.document.querySelector('[data-operation-member-refresh]').click();
  await pause(350);
  assert.equal(posts,1,'the Host and frozen picker must not issue duplicate refresh commands');
  if (syncStatus===503) {
   assert.equal(reads,1,'a failed refresh must keep the existing picker state');
   assert.match(dom.window.document.querySelector('[role="alert"]').textContent,/HTTP 503/);
   assert.match(dom.window.document.querySelector('[data-operation-member-list]').textContent,/原昵称/);
  } else {
   assert.equal(reads,2,'a successful refresh reloads through the existing search pathway');
   assert.match(dom.window.document.querySelector('[data-operation-member-list]').textContent,/刷新后昵称/);
  }
  dom.window.document.querySelector('[data-operation-member-confirm]').click();
  assert.equal(selected.user_id,'alice','selection remains confirmable after refresh');
 } finally {dom.window.close();}
}
console.log('frozen staff selector refresh success and failure retention: PASS');
