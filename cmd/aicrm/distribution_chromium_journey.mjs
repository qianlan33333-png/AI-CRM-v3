import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const base = process.env.AICRM_DISTRIBUTION_BROWSER_URL;
const disabledBase = process.env.AICRM_DISTRIBUTION_BROWSER_DISABLED_URL;
const distributorSession = process.env.AICRM_DISTRIBUTION_BROWSER_SESSION;
const product = process.env.AICRM_DISTRIBUTION_BROWSER_PRODUCT;
const productID = process.env.AICRM_DISTRIBUTION_BROWSER_PRODUCT_ID;
const applicationTargetID = process.env.AICRM_DISTRIBUTION_BROWSER_APPLICATION_TARGET_ID;
const csrf = process.env.AICRM_DISTRIBUTION_BROWSER_CSRF;
const detailAttribution = process.env.AICRM_DISTRIBUTION_BROWSER_DETAIL_ATTRIBUTION;
const detailException = process.env.AICRM_DISTRIBUTION_BROWSER_DETAIL_EXCEPTION;
const detailCreatedAt = process.env.AICRM_DISTRIBUTION_BROWSER_DETAIL_CREATED_AT;
const adminDisplayName = process.env.AICRM_DISTRIBUTION_BROWSER_ADMIN_DISPLAY_NAME;
const admin = process.env.AICRM_DISTRIBUTION_BROWSER_ADMIN;
const password = process.env.AICRM_DISTRIBUTION_BROWSER_PASSWORD;
const screenshotDir = process.env.AICRM_DISTRIBUTION_BROWSER_SCREENSHOT_DIR;
const phase = process.env.AICRM_DISTRIBUTION_BROWSER_PHASE;
const earningsProduct = process.env.AICRM_DISTRIBUTION_BROWSER_EARNINGS_PRODUCT;
const earningsOrder = process.env.AICRM_DISTRIBUTION_BROWSER_EARNINGS_ORDER;
const earningsGrossMinor = process.env.AICRM_DISTRIBUTION_BROWSER_EARNINGS_GROSS_MINOR;
const earningsCommissionMinor = process.env.AICRM_DISTRIBUTION_BROWSER_EARNINGS_COMMISSION_MINOR;
const orderCollisionReference = process.env.AICRM_DISTRIBUTION_BROWSER_ORDER_COLLISION_REFERENCE;
const orderSettledAt = process.env.AICRM_DISTRIBUTION_BROWSER_ORDER_SETTLED_AT;
const revision = process.env.AICRM_DISTRIBUTION_BROWSER_REVISION || 'working-tree';
if (!/^https:\/\//.test(base || "") || !/^https:\/\//.test(disabledBase || "") || !distributorSession || !product || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(applicationTargetID || "") || !csrf || !/^[1-9][0-9]*$/.test(detailAttribution || "") || !/^[1-9][0-9]*$/.test(detailException || "") || !detailCreatedAt || !adminDisplayName || !admin || !password || !['registration','ready'].includes(phase || '') || (phase === 'ready' && (!earningsProduct || !earningsOrder || !/^[1-9][0-9]*$/.test(earningsGrossMinor || '') || !/^[1-9][0-9]*$/.test(earningsCommissionMinor || '') || !orderCollisionReference || !orderSettledAt))) throw new Error("Distribution Chromium journey environment is incomplete");
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const money = minor => new Intl.NumberFormat('zh-CN',{style:'currency',currency:'CNY',minimumFractionDigits:2}).format(Number(minor)/100);
function browser() { for (const item of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) if ((item.includes("/") ? spawnSync(item,["--version"],{stdio:"ignore"}) : spawnSync("which",[item],{stdio:"ignore"})).status === 0) return item; throw new Error("Chromium is unavailable"); }
class CDP { constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); this.exceptions=[]; this.logs=[]; this.responses=[]; this.redirects=[]; socket.addEventListener("message", event => { const m=JSON.parse(String(event.data)); if(m.method==="Runtime.exceptionThrown"){ this.exceptions.push(m.params?.exceptionDetails?.exception?.description||m.params?.exceptionDetails?.text||"runtime exception"); return; } if(m.method==="Log.entryAdded"){ this.logs.push(m.params?.entry?.text||"log"); return; } if(m.method==="Network.responseReceived"){ const r=m.params?.response; this.responses.push({requestId:m.params?.requestId||"",url:r?.url||"",status:r?.status||0}); return; } if(m.method==="Network.requestWillBeSent"&&m.params?.redirectResponse){ const r=m.params.redirectResponse; this.redirects.push({from:r.url||"",to:m.params?.request?.url||"",status:r.status||0}); return; } const p=this.pending.get(m.id); if (!p) return; this.pending.delete(m.id); m.error?p.reject(new Error(`CDP ${m.error.code}`)):p.resolve(m.result||{}); }); } call(method, params={}) { return new Promise((resolve,reject)=>{const id=++this.id, timer=setTimeout(()=>{this.pending.delete(id);reject(new Error(`CDP ${method} timed out`));},8000);this.pending.set(id,{resolve:v=>{clearTimeout(timer);resolve(v)},reject});this.socket.send(JSON.stringify({id,method,params}));}); } }
async function endpoint(profile) { for(let i=0;i<160;i++){try { const p=(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]; if(/^\d+$/.test(p))return `http://127.0.0.1:${p}`; }catch{} await sleep(50);} throw new Error("Chromium DevTools did not start"); }
async function value(cdp, expression) { const r=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(r.exceptionDetails) throw new Error(`page evaluation failed: ${r.exceptionDetails.exception?.description||r.exceptionDetails.text||"unknown"}`); return r.result?.value; }
async function wait(cdp, expression, message) { for(let i=0;i<180;i++){if(await value(cdp,expression))return;await sleep(50);} throw new Error(`${message} exceptions=${JSON.stringify(cdp.exceptions)} logs=${JSON.stringify(cdp.logs)}`); }
async function addCookie(cdp,name,value,origin=base) { await cdp.call("Network.setCookie",{url:origin,name,value,secure:true}); }
async function setMobileViewport(cdp, width) { await cdp.call("Emulation.setDeviceMetricsOverride",{width,height:900,deviceScaleFactor:1,mobile:true,screenWidth:width,screenHeight:900}); await sleep(100); }
async function captureDistributionScreen(cdp, name, width) { if (!screenshotDir) return; await setMobileViewport(cdp,width); const layout=await value(cdp,"(()=>{const root=document.querySelector('#distribution-root');const box=node=>node?.getBoundingClientRect();return {viewport:document.documentElement.clientWidth,scrollWidth:document.documentElement.scrollWidth,root:box(root)?.width||0,dialog:box(document.querySelector('dialog .distribution-card'))?.width||0,input:box(document.querySelector('dialog input'))?.width||0}})()"); if(!layout||layout.viewport!==width||layout.scrollWidth>width||layout.root<width-30||(layout.dialog&&layout.dialog>width-32)||(layout.input&&layout.input>width-32))throw new Error(`mobile ${name} ${width}px layout=${JSON.stringify(layout)}`); await fs.mkdir(screenshotDir,{recursive:true}); const image=await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:false}); await fs.writeFile(path.join(screenshotDir,`${name}-${width}.png`),Buffer.from(image.data,"base64")); }
async function captureMobileCard(cdp, width) { if (!screenshotDir) return; await setMobileViewport(cdp,width); const layout=await value(cdp,"(()=>{const card=document.querySelector('.distribution-product');const body=document.querySelector('.distribution-product-body');const button=[...document.querySelectorAll('.distribution-product .distribution-button.primary')].at(0);const root=document.querySelector('#distribution-root');if(!card||!body||!button||!root)return null;const box=node=>node.getBoundingClientRect();return {viewport:document.documentElement.clientWidth,scrollWidth:document.documentElement.scrollWidth,card:box(card).width,body:box(body).width,button:box(button).width,buttonHeight:box(button).height,bodyScrollWidth:body.scrollWidth,bodyClientWidth:body.clientWidth,hasImage:Boolean(card.querySelector('img')),text:card.textContent}})()"); if(!layout||layout.viewport!==width||layout.scrollWidth>width||layout.body<Math.max(0,width-86)||layout.button<layout.body-1||layout.buttonHeight<44||layout.bodyScrollWidth>layout.bodyClientWidth||layout.hasImage||/等待.*天|分销员编号|协议：|已启用/.test(layout.text))throw new Error(`mobile promotion card ${width}px layout=${JSON.stringify(layout)}`); await fs.mkdir(screenshotDir,{recursive:true}); const image=await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:false}); await fs.writeFile(path.join(screenshotDir,`distribution-mobile-card-${width}.png`),Buffer.from(image.data,"base64")); }
async function setDesktopViewport(cdp, width) { await cdp.call("Emulation.setDeviceMetricsOverride",{width,height:900,deviceScaleFactor:1,mobile:false,screenWidth:width,screenHeight:900}); await sleep(100); }
async function captureOrderScreen(cdp, name, width) { if (!screenshotDir) return; await setDesktopViewport(cdp,width); const layout=await value(cdp,"(()=>{const table=document.querySelector('table');return {viewport:document.documentElement.clientWidth,scrollWidth:document.documentElement.scrollWidth,table:table?.getBoundingClientRect().width||0}})()"); if(!layout||layout.viewport>width||layout.viewport<width-20||layout.scrollWidth>width||layout.table>width)throw new Error(`order ${name} ${width}px layout=${JSON.stringify(layout)}`); await fs.mkdir(screenshotDir,{recursive:true}); const image=await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:true}); await fs.writeFile(path.join(screenshotDir,`${name}-${width}.png`),Buffer.from(image.data,"base64")); }
async function login(cdp) { const page=await fetch(`${base}/login`,{redirect:"manual"}); const html=await page.text(), csrf=/name="login_csrf_token" value="([^"]+)"/.exec(html)?.[1]; if(!csrf)throw new Error("login CSRF unavailable"); const cookies=(typeof page.headers.getSetCookie==="function"?page.headers.getSetCookie():[]).map(v=>v.split(";",1)[0]).join("; "); const response=await fetch(`${base}/login`,{method:"POST",redirect:"manual",headers:{"Content-Type":"application/x-www-form-urlencoded",Cookie:cookies},body:new URLSearchParams({username:admin,password,login_csrf_token:csrf})}); if(response.status!==303)throw new Error(`admin login status=${response.status}`); for(const raw of response.headers.getSetCookie?.()||[]){const pair=raw.split(";",1)[0], n=pair.indexOf("="); if(n>0)await addCookie(cdp,pair.slice(0,n),pair.slice(n+1));} }

const profile=await fs.mkdtemp(path.join(os.tmpdir(),"aicrm-distribution-chromium-")); let child, cdp;
try {
  child=spawn(browser(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:"ignore"});
  const page=await (await fetch(`${await endpoint(profile)}/json/new?about:blank`,{method:"PUT"})).json(); const socket=new WebSocket(page.webSocketDebuggerUrl); await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",()=>reject(new Error("CDP connection failed")),{once:true});}); cdp=new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Log.enable"); await cdp.call("Network.enable"); await cdp.call("Network.setUserAgentOverride",{userAgent:"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 MicroMessenger/8.0.0"});
  await cdp.call("Page.navigate",{url:`${base}/distribution?product_id=${productID}&product_type=standard_product`}); await sleep(200); await value(cdp,"import(document.querySelector('script')?.src).then(()=>true)"); await wait(cdp,"document.querySelector('a.distribution-button')?.textContent==='使用微信登录'",'public application login did not render');
  const oauthHref=await value(cdp,"document.querySelector('a.distribution-button')?.getAttribute('href')"); assert.equal(oauthHref,`/api/h5/wechat-pay/oauth/start?return_url=%2Fdistribution%3Fproduct_id%3D${productID}%26product_type%3Dstandard_product`,'public login must use the real Payment OAuth route');
  await value(cdp,"document.querySelector('a.distribution-button')?.click(); true"); await wait(cdp,"location.hostname==='open.weixin.qq.com'",'WeChat-UA login click did not receive the Payment OAuth provider redirect');
  await addCookie(cdp,"aicrm_distribution_session",distributorSession); await addCookie(cdp,"aicrm_distribution_csrf",csrf);
  await cdp.call("Page.navigate",{url:`${base}/distribution?product_id=${productID}&product_type=standard_product`}); await sleep(200); await value(cdp,"import(document.querySelector('script')?.src).then(()=>true)");
  if (phase === 'registration') {
    await wait(cdp,"document.querySelector('#distribution-root')?.textContent.includes('申请成为分销员')",`Distribution registration did not render ${await value(cdp,"(async()=>JSON.stringify({path:location.pathname,text:document.querySelector('#distribution-root')?.textContent||'',me:await fetch('/api/v1/distribution/me').then(async r=>[r.status,await r.text()])}))()")}`);
    await captureDistributionScreen(cdp,'distribution-registration',390);
    await value(cdp,"document.querySelector('#distribution-root input[type=checkbox]')?.click(); [...document.querySelectorAll('#distribution-root button')].find(x=>x.textContent==='同意并注册')?.click(); true");
    await wait(cdp,"[...document.querySelectorAll('#distribution-root button')].some(x=>x.textContent==='完成收款准备')",'registered distributor did not await the Payment worker readiness projection');
    console.log("distribution_chromium: REGISTERED");
  } else {
    await wait(cdp,"[...document.querySelectorAll('#distribution-root button')].some(x=>x.textContent==='复制分销链接')",'Payment worker completion did not project ready receiver into the public UI');
  for (const width of [320,375,390,430]) await captureMobileCard(cdp,width);
  await setMobileViewport(cdp,390);
  const publicProductState=await value(cdp,"(async()=>{const r=await fetch('/api/v1/distribution/products?limit=50');return {status:r.status,body:await r.json()}})()");
  assert.equal(publicProductState.status,200,'eligible promotion products HTTP status'); assert.deepEqual(publicProductState.body.items?.map(x=>String(x.product_id)),[productID],'only the actually eligible product may be serialized');
  assert.equal(await value(cdp,"[...document.querySelectorAll('#distribution-root a')].some(x=>x.getAttribute('href')?.startsWith('/p/'))"),false,'an eligible promotion card must not include a purchase CTA');
  assert.equal(await value(cdp,"document.querySelector('#distribution-root')?.textContent.includes('查看商品并购买')"),false,'public promotion UI must not offer repeat purchase');
  await cdp.call("Page.navigate",{url:`${base}/distribution?product_id=${applicationTargetID}&product_type=standard_product`}); await sleep(200); await value(cdp,"import(document.querySelector('script')?.src).then(()=>true)"); await wait(cdp,"document.querySelector('#distribution-root')?.textContent.includes('分销浏览器商品')",'unqualified application target replaced the eligible promotion list');
  assert.equal(await value(cdp,"document.querySelector('#distribution-root')?.textContent.includes('申请未购商品')"),false,'application URL context must not inject an unqualified product after registration');
  assert.equal(await value(cdp,"document.querySelector('.distribution-product img')!==null"),false,'promotion card must not reserve a cover column');
  assert.equal(await value(cdp,"/等待.*天|分销员编号|协议：|已启用/.test(document.querySelector('.distribution-product')?.textContent||'')"),false,'promotion card must retain only selling facts');
  await value(cdp,"Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{window.__distributionCopied=value;}},configurable:true}); true");
  await value(cdp, "[...document.querySelectorAll('button')].find(x=>x.textContent==='复制分销链接')?.click(); true");
  for (let i = 0; i < 40 && !cdp.responses.some(item => item.url.includes('promotion-credentials')); i++) await sleep(50);
  const credentialResponse = cdp.responses.filter(item => item.url.includes('promotion-credentials')).at(-1);
  if (!credentialResponse || credentialResponse.status !== 201) {
    const body = credentialResponse?.requestId ? await cdp.call('Network.getResponseBody', { requestId: credentialResponse.requestId }).then(v => v.body).catch(() => '') : '';
    throw new Error(`credential request status=${credentialResponse?.status || 0} body=${body}`);
  }
  const copiedCredential = await value(cdp,"window.__distributionCopied||''");
  if (!/^https:\/\/.+\/d\/dpc_[A-Za-z0-9_-]{16,}$/.test(copiedCredential)) {
    const page = await value(cdp, "JSON.stringify({text:document.querySelector('#distribution-root')?.textContent||document.body.textContent,message:document.querySelector('[data-distribution-message]')?.textContent||'',dialogs:[...document.querySelectorAll('dialog')].map(x=>x.outerHTML)})");
    throw new Error(`qualified distributor did not copy real credential page=${page} assets=${JSON.stringify(cdp.responses.filter(x => x.url.includes('/assets/')).slice(-8))}`);
  }
  const credentialURL=copiedCredential; assert.match(credentialURL, new RegExp(`^${base.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/d/dpc_[A-Za-z0-9_-]{16,}$`)); const generatedPromotion=new URL(credentialURL).pathname.slice(3);
  await value(cdp,"Object.defineProperty(navigator,'clipboard',{value:undefined,configurable:true}); true");
  const credentialResponseCount=cdp.responses.filter(item => item.url.includes('promotion-credentials')).length;
  await value(cdp, "[...document.querySelectorAll('button')].find(x=>x.textContent==='复制分销链接')?.click(); true");
  for (let i = 0; i < 180 && cdp.responses.filter(item => item.url.includes('promotion-credentials')).length <= credentialResponseCount; i++) await sleep(50);
  if (cdp.responses.filter(item => item.url.includes('promotion-credentials')).length <= credentialResponseCount) throw new Error('credential fallback did not issue a real HTTP request');
  const credentialDialog = "Boolean((document.querySelector('dialog input[readonly]'))?.value.includes('/d/dpc_'))";
  for (let i = 0; i < 180 && !(await value(cdp, credentialDialog)); i++) await sleep(50);
  if (!(await value(cdp, credentialDialog))) throw new Error('credential fallback did not expose the trusted link for manual copy');
  assert.equal(await value(cdp, "document.querySelector('dialog input[readonly]')?.value"),credentialURL,'manual-copy fallback must expose the exact credential URL');
  await wait(cdp, "Boolean(document.querySelector('dialog .distribution-qr svg'))", 'credential QR did not render');
  await captureDistributionScreen(cdp,'distribution-copy-fallback',390);
  await value(cdp, "[...document.querySelectorAll('dialog button')].find(x=>x.textContent==='关闭')?.click(); true");
  const credentialReplayCount=cdp.responses.filter(item => item.url.includes('promotion-credentials')).length;
  await value(cdp,"Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{window.__distributionCopiedReplay=value;}},configurable:true}); true");
  await value(cdp, "[...document.querySelectorAll('button')].find(x=>x.textContent==='复制分销链接')?.click(); true");
  for (let i = 0; i < 180 && cdp.responses.filter(item => item.url.includes('promotion-credentials')).length <= credentialReplayCount; i++) await sleep(50);
  if (cdp.responses.filter(item => item.url.includes('promotion-credentials')).length <= credentialReplayCount) throw new Error('credential replay did not issue a real HTTP request');
  const replayResponse=cdp.responses.filter(item => item.url.includes('promotion-credentials')).at(-1); assert.equal(replayResponse?.status,201,'credential replay status');
  await wait(cdp, "window.__distributionCopiedReplay===window.__distributionCopied", 'credential replay did not copy its original link');
  await value(cdp,"[...document.querySelectorAll('button')].find(x=>x.textContent==='我的收益')?.click(); true"); await wait(cdp,"document.querySelector('#distribution-root')?.textContent.includes('累计推广成交额')","new distributor earnings did not render its summary"); await wait(cdp,`document.querySelector('.distribution-list .distribution-card')?.textContent.includes(${JSON.stringify(earningsProduct)})`,'registered distributor did not render a real commission detail'); const earningsText=await value(cdp,"document.querySelector('#distribution-root')?.textContent||''"); if(!earningsText.includes(money(earningsGrossMinor))||!earningsText.includes(money(earningsCommissionMinor))||!earningsText.includes(earningsOrder))throw new Error(`registered distributor earnings must retain the non-zero fixture facts: ${earningsText}`); const commissionFilter=await value(cdp,"(()=>{const select=document.querySelector('select[name=commission-status]');return Boolean(select&&select.getAttribute('aria-label')==='筛选佣金状态'&&select.options.length===7)})()"); assert.equal(commissionFilter,true,'commission status filter must remain a compact accessible native select'); await captureDistributionScreen(cdp,'distribution-earnings',390); await value(cdp,"(()=>{const select=document.querySelector('select[name=commission-status]');if(!(select instanceof HTMLSelectElement))throw new Error('commission status select missing');select.value='pending';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()"); await wait(cdp,"document.querySelector('select[name=commission-status]')?.value==='pending'&&document.querySelector('.distribution-list .distribution-card')?.textContent.includes('订单')",'pending commission filter did not preserve the non-empty detail'); await captureDistributionScreen(cdp,'distribution-earnings-detail',390);
  // A first-time buyer reaches the generated /d URL without a Payment session.
  // The public product page must preserve exactly the credential context into
  // the real H5 OAuth start endpoint, which in turn must redirect to WeChat.
  // A provider callback is deliberately outside this local browser journey.
  const promotionReturnPath=`/p/${product}?promotion_context=${generatedPromotion}`;
  const promotionOAuth=`${base}/api/h5/wechat-pay/oauth/start`;
  const redirectsBeforePromotion=cdp.redirects.length;
  await cdp.call("Page.navigate",{url:`${base}/d/${generatedPromotion}`});
  let promotionOAuthRedirect;
  for(let i=0;i<180&&!promotionOAuthRedirect;i++){
    promotionOAuthRedirect=cdp.redirects.slice(redirectsBeforePromotion).find(item=>{
      if(!item.from.startsWith(promotionOAuth))return false;
      try{return new URL(item.from).searchParams.get('return_url')===promotionReturnPath&&new URL(item.to).hostname==='open.weixin.qq.com';}catch{return false;}
    });
    if(!promotionOAuthRedirect)await sleep(50);
  }
  if(!promotionOAuthRedirect){
    const snapshot=await value(cdp,"JSON.stringify({path:location.pathname,query:location.search,text:document.body.textContent})");
    throw new Error(`promotion /d did not reach the canonical H5 OAuth route ${snapshot} redirects=${JSON.stringify(cdp.redirects.slice(redirectsBeforePromotion))}`);
  }
  assert.equal(promotionOAuthRedirect.status,302,'promotion OAuth start must accept the exact product return path');
  await wait(cdp,"location.hostname==='open.weixin.qq.com'",'promotion product OAuth did not receive the WeChat provider redirect');
  await addCookie(cdp,"aicrm_distribution_session",distributorSession,disabledBase); await addCookie(cdp,"aicrm_distribution_csrf",csrf,disabledBase);
  await cdp.call("Page.navigate",{url:`${disabledBase}/distribution`}); await sleep(200); await value(cdp,"import(document.querySelector('script')?.src).then(()=>true)"); await wait(cdp,"document.querySelector('#distribution-root')?.textContent.includes('商户尚未启用分佣结算，分销员资格会保留')",'merchant-disabled settlement gate did not identify merchant responsibility');
  assert.equal(await value(cdp,"[...document.querySelectorAll('#distribution-root button')].some(x=>/收款准备/.test(x.textContent||''))"),false,'merchant-disabled state must not offer receiver preparation');
  const disabledPreparation=await value(cdp,"(async()=>{const csrf=document.cookie.split(';').find(x=>x.trim().startsWith('aicrm_distribution_csrf='))?.split('=').slice(1).join('')||'';const r=await fetch('/api/v1/distribution/receiver-preparation',{method:'POST',headers:{'X-Distribution-CSRF':csrf,'Idempotency-Key':'distribution-chromium-disabled-noop'}});return {status:r.status,body:await r.json()}})()");
  assert.equal(disabledPreparation.status,200,'merchant-disabled receiver preparation must be a truthful no-op'); assert.equal(disabledPreparation.body.setup?.state,'merchant_settlement_disabled','merchant-disabled preparation state');
  await login(cdp);
  await cdp.call("Page.navigate",{url:`${base}/admin/orders`}); await sleep(200); await value(cdp,"import(document.querySelector('script')?.src).then(()=>true)");
  await wait(cdp,`[...document.querySelectorAll('tbody tr')].filter(row=>row.textContent.includes(${JSON.stringify(orderCollisionReference)})).length===2`,'Orders list did not render both provider-local rows with the same merchant reference');
  await captureOrderScreen(cdp,'order-list',1280); await captureOrderScreen(cdp,'order-list',1440);
  const orderRows=await value(cdp,`[...document.querySelectorAll('tbody tr')].filter(row=>row.textContent.includes(${JSON.stringify(orderCollisionReference)})).map(row=>({text:row.textContent||'',detailURL:row.dataset.orderDetailUrl||'',paymentLabel:row.querySelectorAll('td')[6]?.textContent?.trim()||''}))`);
  assert.equal(orderRows.length,2,'two provider-scoped rows must remain visible');
  const wechatRow=orderRows.find(row=>new URL(row.detailURL,base).searchParams.get('provider')==='wechat');
  const alipayRow=orderRows.find(row=>new URL(row.detailURL,base).searchParams.get('provider')==='alipay');
  assert.ok(wechatRow&&alipayRow,'each list row must use a server-owned provider detail URL');
  assert.equal(wechatRow.paymentLabel,'微信支付','the WeChat row must retain its provider payment label after the frozen renderer projection');
  assert.equal(alipayRow.paymentLabel,'支付宝','the Alipay row must retain its provider payment label after the frozen renderer projection');
  assert.doesNotMatch(orderRows.map(row=>`${row.text}\n${row.paymentLabel}`).join('\n'),/aicrm-order-v3:/,'list presentation must never expose the retired correlation marker');
  assert.match(wechatRow.text,/分销：/, 'successful split row retains a distribution summary');
  assert.match(alipayRow.text,/分销：/, 'outcome-unknown row retains a separate distribution summary');
  const orderDetailEvidence=[];
  for (const detailCase of [
    {name:'succeeded',row:wechatRow,provider:'wechat',product:'分账成功商品',settlement:'dstl_browser_succeeded',confirmed:true,width:1280},
    {name:'outcome-unknown',row:alipayRow,provider:'alipay',product:'分账待核验商品',settlement:'dstl_browser_outcome_unknown',confirmed:false,width:1440},
  ]) {
    const responsesBefore=cdp.responses.length;
    await cdp.call("Page.navigate",{url:new URL(detailCase.row.detailURL,base).toString()}); await sleep(200);
    await wait(cdp,`document.body.textContent.includes(${JSON.stringify(detailCase.product)})&&document.body.textContent.includes(${JSON.stringify(detailCase.settlement)})`, `provider-scoped order detail did not render its selected distribution facts page=${await value(cdp,"JSON.stringify({path:location.pathname,query:location.search,text:document.body.textContent,scripts:[...document.scripts].map(x=>x.src)})")}`);
    const text=await value(cdp,"document.body.textContent||''");
    assert.match(text,/分销信息/,`${detailCase.name} detail must retain the Distribution fact section`);
    const exactOrder=await value(cdp,`(async()=>{const r=await fetch('/api/admin/orders/${encodeURIComponent(orderCollisionReference)}?provider=${detailCase.provider}');return {status:r.status,body:await r.json()}})()`);
    const exactSettlement=exactOrder.body?.distribution?.[0]?.settlements?.find(item=>item.reference===detailCase.settlement);
    assert.equal(exactOrder.status,200,`${detailCase.name} exact provider read status`);
    if(detailCase.confirmed){assert.match(text,/已分账佣金/, 'successful split must be presented as a confirmed split amount');assert.match(text,/分账成功确认时间/, 'successful split must retain its audit confirmation time');assert.equal(Date.parse(exactSettlement?.settlement_confirmed_at||''),Date.parse(orderSettledAt),'successful split must retain the exact audit confirmation instant');}
    else {assert.match(text,/分账结果待核验/, 'unconfirmed split must stay outcome-unknown');assert.match(text,/分账成功确认时间\s*未记录/, 'unconfirmed split must not borrow a settlement confirmation time');assert.equal(exactSettlement?.settlement_confirmed_at,null,'unconfirmed split API fact must not borrow a confirmation instant');}
    const apiResponse=cdp.responses.slice(responsesBefore).find(item=>{try{const u=new URL(item.url);return u.pathname===`/api/admin/orders/${orderCollisionReference}`&&u.searchParams.get('provider')===detailCase.provider;}catch{return false;}});
    assert.ok(apiResponse&&apiResponse.status===200,`${detailCase.name} detail must request the exact provider-scoped API row`);
    await captureOrderScreen(cdp,'order-detail',detailCase.width);
    orderDetailEvidence.push({provider:detailCase.provider,product:detailCase.product,settlement:detailCase.settlement,confirmed:detailCase.confirmed,url:detailCase.row.detailURL,screenshot:`order-detail-${detailCase.width}.png`});
  }
  if(screenshotDir)await fs.writeFile(path.join(screenshotDir,'order-browser-evidence.json'),JSON.stringify({revision,order_collision_reference:orderCollisionReference,settlement_confirmed_at:orderSettledAt,list_rows:orderRows.map(row=>({detail_url:row.detailURL,payment_label:row.paymentLabel})),details:orderDetailEvidence,screenshots:{lists:['order-list-1280.png','order-list-1440.png'],details:orderDetailEvidence.map(detail=>detail.screenshot)}},null,2));
  await cdp.call("Page.navigate",{url:`${base}/admin/distribution`}); await sleep(200); await value(cdp,"import(document.querySelector('script')?.src).then(()=>true)"); await wait(cdp,`document.querySelector('#distribution-admin-root')?.textContent.includes(${JSON.stringify(adminDisplayName)})`,'Distribution admin distributor view did not read the Customer directory nickname');
  assert.equal(await value(cdp,"document.querySelector('#distribution-admin-root .admin-table')?.textContent.includes('DISTBROWSER01')"),false,'admin distributor list must use the Customer directory nickname as its primary display');
  await value(cdp,"[...document.querySelectorAll('#distribution-admin-root button')].find(x=>x.textContent==='查看详情')?.click(); true"); await wait(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('关联推广订单')",'Distribution admin detail did not render related orders');
  await wait(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('DISTBROWSER01')",'admin detail must retain the distributor public number');
  const relatedCount=await value(cdp,"[...[...document.querySelectorAll('dialog')].at(-1).querySelectorAll('button')].filter(x=>x.textContent.includes('order-')).length"); assert.equal(relatedCount,10,'first detail page must render ten real attributed orders');
  await value(cdp,"[...[...document.querySelectorAll('dialog')].at(-1).querySelectorAll('button')].find(x=>x.textContent==='加载更多订单')?.click(); true"); await wait(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('order-9011')",'detail continuation did not append the eleventh attributed order');
  await value(cdp,"[...[...document.querySelectorAll('dialog')].at(-1).querySelectorAll('button')].find(x=>x.textContent.includes('order-9010'))?.click(); true"); await wait(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('dstl_browser_partial') && [...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('买家退款调整')",'order detail did not render non-empty refund adjustment and settlement facts');
  await wait(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('创建时间')",'order detail did not render settlement created time'); assert.equal(await value(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('worker:distribution-due')"),false,'admin detail must not expose raw actor scope');
  await value(cdp,"[...[...document.querySelectorAll('dialog')].at(-1).querySelectorAll('button')].find(x=>x.textContent.includes('结算结果待核验'))?.click(); true"); await wait(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('已记录异常') && [...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('系统结算检查')",'exception detail did not render readable audit facts'); assert.equal(await value(cdp,"[...document.querySelectorAll('dialog')].at(-1)?.textContent.includes('distribution.exception_opened.v1')"),false,'admin detail must not expose raw audit event');
  const detail = await value(cdp,`(async()=>{const r=await fetch('/api/admin/distribution/orders/${detailAttribution}');return {status:r.status,body:await r.json()}})()`); assert.equal(detail.status,200,'admin order detail HTTP status'); assert.equal(detail.body.adjustments?.[0]?.delta_minor,-495,'admin order detail must preserve refund adjustment amount'); assert.equal(detail.body.settlements?.[0]?.amount_minor,495,'admin order detail must preserve settlement amount'); assert.equal(Date.parse(detail.body.settlements?.[0]?.created_at),Date.parse(detailCreatedAt),'admin order detail must preserve settlement instant');
  const exception = await value(cdp,`(async()=>{const r=await fetch('/api/admin/distribution/exceptions/${detailException}');return {status:r.status,body:await r.json()}})()`); assert.equal(exception.status,200,'admin exception detail HTTP status'); assert.equal(exception.body.audit?.[0]?.event_type,'distribution.exception_opened.v1','admin exception audit event'); assert.equal(exception.body.audit?.[0]?.amount_minor,495,'admin exception audit amount'); assert.equal(Date.parse(exception.body.audit?.[0]?.occurred_at),Date.parse(detailCreatedAt),'admin exception audit instant');
  await value(cdp,"[...document.querySelectorAll('#distribution-admin-root button')].find(x=>x.textContent==='订单')?.click(); true"); await wait(cdp,"document.querySelector('#distribution-admin-root')?.textContent.includes('分销浏览器商品') && document.querySelector('#distribution-admin-root')?.textContent.includes('order-9001')",`Distribution admin page did not read back the real distributor and frozen order facts ${await value(cdp,"(async()=>JSON.stringify({text:document.querySelector('#distribution-admin-root')?.textContent||document.body.textContent,script:document.querySelector('script')?.src,distributors:await fetch('/api/admin/distribution/distributors?limit=50').then(async r=>[r.status,await r.text()]),orders:await fetch('/api/admin/distribution/orders?limit=50').then(async r=>[r.status,await r.text()])}))()")}`);
    console.log("distribution_chromium: PASS");
  }
} finally { if(cdp)cdp.socket.close(); if(child&&child.exitCode===null){child.kill("SIGTERM");await Promise.race([new Promise(resolve=>child.once("exit",resolve)),sleep(3000)]);if(child.exitCode===null)child.kill("SIGKILL");} await fs.rm(profile,{recursive:true,force:true}).catch(()=>{}); }
