import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const base = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_URL;
const username = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_USERNAME;
const password = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_PASSWORD;
const productID = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_PRODUCT_ID;
const serviceProductID = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_SERVICE_PRODUCT_ID;
if (!/^https:\/\//.test(base || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "")) throw new Error("Distribution policy Chromium journey environment is incomplete");
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function browser() { for (const item of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) if ((item.includes("/") ? spawnSync(item,["--version"],{stdio:"ignore"}) : spawnSync("which",[item],{stdio:"ignore"})).status === 0) return item; throw new Error("Chromium is unavailable"); }
class CDP { constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); socket.addEventListener("message", event => { const m=JSON.parse(String(event.data)); const p=this.pending.get(m.id); if (!p) return; this.pending.delete(m.id); m.error?p.reject(new Error(`CDP ${m.error.code}`)):p.resolve(m.result||{}); }); } call(method,params={}) { return new Promise((resolve,reject)=>{const id=++this.id,timer=setTimeout(()=>{this.pending.delete(id);reject(new Error(`CDP ${method} timed out`));},8000);this.pending.set(id,{resolve:v=>{clearTimeout(timer);resolve(v)},reject});this.socket.send(JSON.stringify({id,method,params}));}); } }
async function endpoint(profile) { for(let i=0;i<160;i++){try { const port=(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]; if(/^\d+$/.test(port))return `http://127.0.0.1:${port}`; }catch{} await sleep(50);} throw new Error("Chromium DevTools did not start"); }
async function value(cdp,expression) { const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails)throw new Error("page evaluation failed"); return result.result?.value; }
async function wait(cdp,expression,message) { for(let i=0;i<180;i++){if(await value(cdp,expression))return;await sleep(50);} throw new Error(message); }
async function addCookie(cdp,name,value) { await cdp.call("Network.setCookie",{url:base,name,value,secure:true}); }
async function login(cdp) { const page=await fetch(`${base}/login`,{redirect:"manual"}); const html=await page.text(), csrf=/name="login_csrf_token" value="([^"]+)"/.exec(html)?.[1]; if(!csrf)throw new Error("login CSRF unavailable"); const cookies=(typeof page.headers.getSetCookie==="function"?page.headers.getSetCookie():[]).map(value=>value.split(";",1)[0]).join("; "); const response=await fetch(`${base}/login`,{method:"POST",redirect:"manual",headers:{"Content-Type":"application/x-www-form-urlencoded",Cookie:cookies},body:new URLSearchParams({username,password,login_csrf_token:csrf})}); if(response.status!==303)throw new Error(`admin login status=${response.status}`); for(const raw of response.headers.getSetCookie?.()||[]){const pair=raw.split(";",1)[0],index=pair.indexOf("=");if(index>0)await addCookie(cdp,pair.slice(0,index),pair.slice(index+1));} }
async function saveAndReload(cdp, page, rate, waitDays) {
  await cdp.call("Page.navigate",{url:base+page});
  await wait(cdp,"Boolean(document.querySelector('[data-distribution-policy]'))",`policy controls did not load ${page}`);
  await value(cdp,"(() => { const prior=window.fetch; window.__distributionPolicyWrites=[]; window.fetch=(input,init) => { const request=input instanceof Request ? input : undefined; window.__distributionPolicyWrites.push({url:String(request?.url || input),method:String(init?.method || request?.method || 'GET'),body:String(init?.body || '')}); return prior(input,init); }; return true; })()");
  const before=await value(cdp,"document.querySelector('[data-distribution-policy]')?.dataset.distributionPolicyVersion");
  assert.equal(before,"0",`fresh fixture must load a revision-zero policy on ${page}`);
  await value(cdp,`(() => { const enabled=document.querySelector('[data-distribution-policy-enabled]'); const commission=document.querySelector('[data-distribution-policy-rate]'); const days=document.querySelector('[data-distribution-policy-wait-days]'); enabled.checked=true; enabled.dispatchEvent(new Event('change',{bubbles:true})); commission.value=${JSON.stringify(rate)}; commission.dispatchEvent(new Event('input',{bubbles:true})); days.value=${JSON.stringify(String(waitDays))}; days.dispatchEvent(new Event('input',{bubbles:true})); const save=[...document.querySelectorAll('button')].find(button=>button.textContent.trim()==='保存当前维度' && !button.closest('#product-push') && !button.closest('#sp-push')); if(!save) throw new Error('normal product save control missing'); save.click(); return true; })()`);
  await wait(cdp,"document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度')",`normal product save did not finish ${page}`);
  const writes = await value(cdp, "window.__distributionPolicyWrites");
  const serverPolicy = await value(cdp, "fetch(location.pathname.includes('spProductForm') ? '/api/admin/service-period-products/" + serviceProductID + "' : '/api/v1/products/" + productID + "').then(response => response.text())");
  await cdp.call("Page.navigate",{url:base+page});
  const readback = `(() => { const host=document.querySelector('[data-distribution-policy]'); return {version:host?.dataset.distributionPolicyVersion,enabled:host?.querySelector('[data-distribution-policy-enabled]')?.checked,rate:host?.querySelector('[data-distribution-policy-rate]')?.value,wait_days:host?.querySelector('[data-distribution-policy-wait-days]')?.value}; })()`;
  for (let attempt = 0; attempt < 180; attempt += 1) {
    const actual = await value(cdp, readback);
    if (actual?.version === "1" && actual.enabled === true && Number(actual.rate) === Number(rate) && actual.wait_days === String(waitDays)) return;
    await sleep(50);
  }
  throw new Error(`policy save/readback mismatch ${page} actual=${JSON.stringify(await value(cdp, readback))} writes=${JSON.stringify(writes)} server=${serverPolicy}`);
}
const profile=await fs.mkdtemp(path.join(os.tmpdir(),"aicrm-distribution-policy-chromium-")); let child,cdp;
try { child=spawn(browser(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:"ignore"}); const page=await (await fetch(`${await endpoint(profile)}/json/new?about:blank`,{method:"PUT"})).json(); const socket=new WebSocket(page.webSocketDebuggerUrl); await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",()=>reject(new Error("CDP connection failed")),{once:true});}); cdp=new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable"); await login(cdp); await saveAndReload(cdp,`/admin/wechat-pay/productForm.html?id=${productID}`,"12.34",8); await saveAndReload(cdp,`/admin/wechat-pay/spProductForm.html?id=${serviceProductID}`,"30.00",0); console.log("distribution_product_policy_chromium: PASS"); } finally { if(cdp)cdp.socket.close(); if(child&&child.exitCode===null){child.kill("SIGTERM");await Promise.race([new Promise(resolve=>child.once("exit",resolve)),sleep(3000)]);if(child.exitCode===null)child.kill("SIGKILL");} await fs.rm(profile,{recursive:true,force:true}).catch(()=>{}); }
