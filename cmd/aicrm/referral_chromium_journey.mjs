import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
const base = process.env.AICRM_REFERRAL_BROWSER_URL;
const actors = JSON.parse(process.env.AICRM_REFERRAL_BROWSER_ACTORS || '[]');
assert.match(base || '', /^https:\/\/127\.0\.0\.1:/);
assert.equal(actors.length, 3);
const sleep = ms => new Promise(r => setTimeout(r, ms));
const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'referral-browser-'));
const screenshots = process.env.AICRM_REFERRAL_SCREENSHOTS || path.join(profile, 'screenshots');
await fs.mkdir(screenshots, { recursive: true });
let chrome, socket;
const pending = new Map(); let sequence = 0;
try {
  const binary = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'chromium', 'google-chrome'].filter(Boolean).find(p => spawnSync(p, ['--version'], { stdio: 'ignore' }).status === 0);
  assert.ok(binary, 'Chromium is required');
  chrome = spawn(binary, ['--headless=new','--no-sandbox','--remote-debugging-port=0',`--user-data-dir=${profile}`,'--no-first-run','--ignore-certificate-errors','--disable-background-networking','about:blank'], { stdio: 'ignore' });
  let port;
  for (let i=0; i<300&&!port; i++) { try {port=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];}catch{} if(!port)await sleep(100); }
  assert.ok(port,'browser startup');
  const tab = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`,{method:'PUT'})).json();
  socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((r,j)=>{socket.addEventListener('open',r,{once:true});socket.addEventListener('error',j,{once:true});});
  const exceptions=[];
  socket.addEventListener('message',e=>{const m=JSON.parse(String(e.data));if(m.id){const p=pending.get(m.id);pending.delete(m.id);m.error?p?.reject(new Error(m.error.message)):p?.resolve(m.result);}else if(m.method==='Runtime.exceptionThrown')exceptions.push(m.params.exceptionDetails.text);});
  const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}));});
  const evaluate=async expression=>{const r=await call('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(r.exceptionDetails)throw new Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result?.value;};
  const wait=async expression=>{for(let i=0;i<180;i++){if(await evaluate(expression))return;await sleep(75);}throw new Error('UI condition not reached: '+expression+'; page='+await evaluate('location.pathname+location.search+" "+document.body.innerText'));};
  const cookie=async(name,value)=>call('Network.setCookie',{name,value,url:base,secure:true,sameSite:'Lax'});
  await call('Page.enable');await call('Runtime.enable');await call('Network.enable');
  const loginPage=await fetch(base+'/login');const html=await loginPage.text();const loginCSRF=/name="login_csrf_token" value="([^"]+)"/.exec(html)?.[1];assert.ok(loginCSRF);
  const login=await fetch(base+'/login',{method:'POST',redirect:'manual',headers:{'Content-Type':'application/x-www-form-urlencoded',Cookie:loginPage.headers.getSetCookie().map(c=>c.split(';')[0]).join('; ')},body:new URLSearchParams({username:'referral-admin',password:'referral-admin-password',login_csrf_token:loginCSRF})});assert.equal(login.status,303);
  const staffCookies=login.headers.getSetCookie().map(c=>c.split(';')[0]);
  for(const c of staffCookies){const n=c.indexOf('=');await cookie(c.slice(0,n),c.slice(n+1));}
  await call('Page.navigate',{url:base+'/admin/referral'});
  await wait("document.querySelector('#referral-admin-root') && document.body.textContent.includes('裂变活动')");
  const adminCSRF=await evaluate("decodeURIComponent(document.cookie.split('; ').find(v=>v.startsWith('aicrm_csrf='))?.split('=').slice(1).join('=')||'')");
  async function api(route, body, actor, method=body===undefined?'GET':'POST', key=crypto.randomUUID()) {
    const csrf=actor?'referral-fixture-csrf':adminCSRF;
    const cookies=actor?`aicrm_distribution_session=${actor.session}; aicrm_distribution_csrf=${csrf}`:staffCookies.join('; ');
    const response=await fetch(base+route,{method,headers:{Cookie:cookies,Origin:base,'Content-Type':'application/json','Idempotency-Key':key,'X-CSRF-Token':csrf,'X-Distribution-CSRF':csrf},body:body===undefined?undefined:JSON.stringify(body)});
    const value=await response.json();assert.ok(response.ok,`${method} ${route} -> ${response.status}: ${JSON.stringify(value)}`);return value;
  }
  const campaigns=[];
  for(let i=0;i<2;i++){
    const c=await api('/api/admin/referral/campaigns',{name:`同行邀请季 ${i+1}`,description:'邀请好友，和战队一起前进。',cover_url:'',reward_rules:'前 3 名可获得活动纪念礼物，由管理员核实后登记。',starts_at:new Date(Date.now()-3600000).toISOString(),ends_at:new Date(Date.now()+86400000).toISOString()});
    const team=await api(`/api/admin/referral/campaigns/${c.id}/teams`,{name:i?'向阳战队':'追光战队',logo_url:'',captain_customer_id:actors[i].id});
    await api(`/api/admin/referral/campaigns/${c.id}/state`,{expected_version:c.version,target:'active'});
    await api(`/api/v1/referral/campaigns/${c.id}/participations`,{team_id:team.id},actors[i]);
    const link=await api(`/api/v1/referral/campaigns/${c.id}/invite`,{},actors[i]);
    campaigns.push({c,team,link});
  }
  for(let index=0;index<2;index++){
    await cookie('aicrm_distribution_session',actors[2].session);await cookie('aicrm_distribution_csrf','referral-fixture-csrf');
    await call('Emulation.setDeviceMetricsOverride',{width:index?430:375,height:850,deviceScaleFactor:1,mobile:true});
    await call('Page.navigate',{url:campaigns[index].link.url});
    await wait("[...document.querySelectorAll('button')].some(b=>b.textContent.includes('接受邀请'))");
    await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.includes('接受邀请')).click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    await evaluate("document.querySelector('#referral-rule-check').click(); document.querySelector('[data-testid=referral-confirm-join]').click()");
    await wait("document.querySelector('[data-testid=referral-invite]')?.disabled === false && !document.querySelector('[data-testid=referral-accept-dialog]')");
    const pageImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`campaign-${index?430:375}.png`),Buffer.from(pageImage.data,'base64'));
    for(const period of ['day','week','total']) {
      await evaluate(`(()=>{const el=document.querySelector('[data-testid=referral-leaderboard-period]');el.value='${period}';el.dispatchEvent(new Event('change'));})()`);
      await sleep(200);
      await wait("document.querySelector('[data-testid=referral-leaderboard]')?.textContent.includes('1')");
      assert.equal(await evaluate("document.querySelector('[data-referral-message]')?.dataset.error === 'true'"),false,'period switch should not fail');
    }
    await evaluate("Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.__referralCopied=value;}}}); document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-copy-invite]')");
    await evaluate("document.querySelector('[data-testid=referral-copy-invite]').click()");
    await wait("typeof window.__referralCopied === 'string'");
    const copied = new URL(await evaluate('window.__referralCopied'));
    assert.equal(copied.origin,base);assert.match(copied.pathname,/^\/r\/rfi_[A-Za-z0-9_-]{43}$/);
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'),true,'mobile does not horizontally overflow');
    const image=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`mobile-${index?430:375}.png`),Buffer.from(image.data,'base64'));
  }
  // Repeated acceptance preserves the first campaign and does not steal the latest relationship back.
  const first=campaigns[0], token=new URL(first.link.url).pathname.split('/').at(-1);
  await api(`/api/v1/referral/campaigns/${first.c.id}/participations`,{invitation_token:token},actors[2]);
  for(const {c} of campaigns){const board=await api(`/api/v1/referral/campaigns/${c.id}/leaderboard?kind=personal&period=total`,undefined,actors[2]);assert.equal(board.items.reduce((sum,x)=>sum+x.score,0),1,'one independent invitation credit per campaign');}
  await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
  await call('Page.navigate',{url:base+'/admin/referral'});await wait("document.body.textContent.includes('同行邀请季')");
  assert.equal(await evaluate("document.querySelectorAll('main').length"),1,'one CRM main');
  const image=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'admin-1440.png'),Buffer.from(image.data,'base64'));
  // Use staff controls against real HTTP/SQL: history lookup, reversal, reward review.
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='归属历史').click()");
  await wait("document.querySelector('.referral-admin-picker input')");
  await evaluate("document.querySelector('.referral-admin-picker input').value='超长昵称'; [...document.querySelectorAll('button')].find(b=>b.textContent==='搜索客户').click()");
  await wait("document.querySelector('.referral-admin-picker__option')");
  await evaluate("document.querySelector('.referral-admin-picker__option').click(); [...document.querySelectorAll('button')].find(b=>b.textContent==='查看归属历史').click()");
  await wait("document.querySelector('.referral-admin-table')?.textContent.includes('队长阿青') && document.querySelector('.referral-admin-table')?.textContent.includes('队长小夏')");
  const referrals=await api(`/api/admin/referral/referrals?campaign_id=${first.c.id}`);
  const credited=referrals.items.find(r=>r.score_event_id>0);assert.ok(credited);
  await api('/api/admin/referral/rewards',{campaign_id:first.c.id,customer_id:actors[0].id,score_event_id:credited.score_event_id,period:'total',reward:'活动纪念礼物',evidence_reference:'fixture:manual-award'});
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='参与记录').click()");
  await wait("document.querySelector('[data-testid=referral-admin-campaign-select]')");
  await evaluate(`(()=>{const el=document.querySelector('[data-testid=referral-admin-campaign-select]');el.value='${first.c.id}';el.dispatchEvent(new Event('change'));})()`);
  await wait("[...document.querySelectorAll('.referral-admin-table button')].some(b=>b.textContent==='撤销')");
  await evaluate("[...document.querySelectorAll('.referral-admin-table button')].find(b=>b.textContent==='撤销').click()");
  await wait("document.querySelector('[data-testid=referral-admin-dialog] input')");
  await evaluate("document.querySelector('[data-testid=referral-admin-dialog] input').value='测试核实无效邀请'; [...document.querySelectorAll('dialog button')].find(b=>b.textContent==='确认撤销').click()");
  await wait("!document.querySelector('dialog') && document.querySelector('.referral-admin-table')?.textContent.includes('已撤销')");
  const corrected=await api(`/api/v1/referral/campaigns/${first.c.id}/leaderboard?kind=personal&period=total`,undefined,actors[2]);
  assert.equal(corrected.items.reduce((sum,x)=>sum+x.score,0),0,'reversal corrects real leaderboard');
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='人工发奖').click()");
  await wait("document.querySelector('.referral-admin-table')?.textContent.includes('待核查')");
  assert.deepEqual(exceptions,[],'no uncaught browser errors');
  console.log('referral_chromium: PASS');
} finally {
  socket?.close();chrome?.kill('SIGTERM');await sleep(500);
  if(chrome&&chrome.exitCode===null){chrome.kill('SIGKILL');await sleep(300);}
  if(!process.env.AICRM_REFERRAL_SCREENSHOTS)await fs.rm(profile,{recursive:true,force:true,maxRetries:5,retryDelay:150});
}
