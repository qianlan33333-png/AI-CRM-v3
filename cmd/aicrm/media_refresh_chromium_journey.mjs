import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "../../internal/webshell/chromium_launch.mjs";

const baseURL = process.env.AICRM_MEDIA_REFRESH_TEST_URL;
const username = process.env.AICRM_MEDIA_REFRESH_TEST_USERNAME;
const password = process.env.AICRM_MEDIA_REFRESH_TEST_PASSWORD;
const screenshot = process.env.AICRM_MEDIA_REFRESH_SCREENSHOT;
const missingSourceRef = process.env.AICRM_MEDIA_REFRESH_MISSING_SOURCE_REF;
const xlsxPath = process.env.AICRM_MEDIA_REFRESH_XLSX;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !screenshot || !missingSourceRef || !xlsxPath) throw new Error("media refresh Chromium journey requires HTTPS URL, credentials, real XLSX, missing source, and screenshot path");
const xlsxBase64 = (await fs.readFile(xlsxPath)).toString("base64");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const asError = (error) => error instanceof Error ? error : new Error(String(error));
function binary() { for (const item of [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, process.platform === "darwin" ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" : "", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"].filter(Boolean)) { if (item.includes("/")) { try { if (spawnSync(item, ["--version"], { stdio: "ignore" }).status === 0) return item; } catch {} } else if (spawnSync("which", [item], { stdio: "ignore" }).status === 0) return item; } throw new Error("Chromium binary is unavailable"); }
class CDP { constructor(socket) { this.socket=socket; this.id=0; this.waiting=new Map(); socket.addEventListener("message", event => { const m=JSON.parse(String(event.data)); if (m.id && this.waiting.has(m.id)) { const p=this.waiting.get(m.id); this.waiting.delete(m.id); m.error ? p.reject(new Error(`CDP ${m.error.code}`)) : p.resolve(m.result||{}); } }); } call(method, params={}) { return new Promise((resolve,reject)=>{ const id=++this.id; this.waiting.set(id,{resolve,reject}); this.socket.send(JSON.stringify({id,method,params})); }); } close() { this.socket.close(); } }
let browser; let stderr="";
async function devtools(profile) { const until=Date.now()+chromiumStartupTimeoutMS; while(Date.now()<until) { try { const port=String(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]; if(/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch {} if(browser?.exitCode!==null) break; await sleep(50); } throw new Error(chromiumStartupDiagnostic({profile,exitCode:browser?.exitCode,signalCode:browser?.signalCode,stderr})); }
async function value(cdp, expression) { const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails) throw new Error(`page evaluation exception: ${expression.slice(0,100)}`); return result.result?.value; }
async function wait(cdp, expression, label) { for(let i=0;i<150;i++) { if(await value(cdp,expression)) return; await sleep(100); } throw new Error(`${label}: ${await value(cdp,"JSON.stringify({path:location.pathname,title:document.title,body:document.body.innerText.slice(-2400),requests:window.__mediaRefreshRequests||[]})")}`); }
async function assertImageLibraryLayout(cdp, width, height) {
 await cdp.call("Emulation.setDeviceMetricsOverride",{width,height,deviceScaleFactor:1,mobile:false});
 await wait(cdp,"Boolean(document.querySelector('[data-image-library-query]')&&document.querySelector('[data-image-library-cards]'))",`image library ${width}px controls`);
 const layout=await value(cdp,`(()=>{const root=document.documentElement;const toolbar=document.querySelector('[data-image-library-query]')?.closest('.admin-toolbar');const cards=document.querySelector('[data-image-library-cards]');const box=node=>{const rect=node?.getBoundingClientRect();return rect?{left:rect.left,right:rect.right,width:rect.width}:null};return {viewport:root.clientWidth,scrollWidth:root.scrollWidth,toolbar:box(toolbar),cards:box(cards)}})()`);
 if(!layout||layout.scrollWidth>layout.viewport+1||!layout.toolbar||!layout.cards||layout.toolbar.left<-.5||layout.toolbar.right>layout.viewport+1||layout.cards.left<-.5||layout.cards.right>layout.viewport+1) throw new Error(`image library ${width}px page overflow ${JSON.stringify(layout)}`);
 await value(cdp,"[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='上传图片').click();true");
 await wait(cdp,"Boolean(document.querySelector('form[data-image-library-dialog] [data-image-library-dialog-fields]'))",`image library ${width}px upload dialog`);
 const dialog=await value(cdp,`(()=>{const panel=document.querySelector('form[data-image-library-dialog]');const fields=panel?.querySelector('[data-image-library-dialog-fields]');const submit=panel?.querySelector('[data-image-library-dialog-submit]');if(!panel||!fields||!submit)return null;fields.scrollTop=fields.scrollHeight;const panelBox=panel.getBoundingClientRect();const submitBox=submit.getBoundingClientRect();return {viewport:window.innerHeight,panelTop:panelBox.top,panelBottom:panelBox.bottom,panelScrollHeight:panel.scrollHeight,panelClientHeight:panel.clientHeight,fieldsScrollHeight:fields.scrollHeight,fieldsClientHeight:fields.clientHeight,submitTop:submitBox.top,submitBottom:submitBox.bottom}})()`);
 if(!dialog||dialog.panelTop<-.5||dialog.panelBottom>dialog.viewport+.5||dialog.submitTop<-.5||dialog.submitBottom>dialog.viewport+.5||dialog.panelScrollHeight<dialog.panelClientHeight||dialog.fieldsScrollHeight<dialog.fieldsClientHeight) throw new Error(`image library ${width}px dialog overflow ${JSON.stringify(dialog)}`);
 await value(cdp,"document.querySelector('button[aria-label=关闭弹窗]').click();true");
 await wait(cdp,"!document.querySelector('form[data-image-library-dialog]')",`image library ${width}px close dialog`);
}
const waitForExit = (child, ms) => new Promise((resolve) => { if (child.exitCode !== null || child.signalCode !== null) return resolve(true); const timer = setTimeout(() => resolve(false), ms); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
const stopBrowser = async (child) => {
 if (!child || child.exitCode !== null || child.signalCode !== null) return null;
 try {
  child.kill("SIGTERM");
  if (await waitForExit(child, 3000)) return null;
  if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  if (await waitForExit(child, 3000)) return null;
  return new Error("Chromium process did not exit before profile cleanup");
 } catch (error) { return asError(error); }
};
const removeProfile = async (profile) => {
 let lastError;
 for (let attempt = 0; attempt < 40; attempt += 1) {
  try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return null; }
  catch (error) {
   lastError = asError(error);
   if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return lastError;
   await sleep(100);
  }
 }
 return new Error(`Chromium test profile cleanup did not complete after 40 attempts: ${lastError?.code || lastError?.message || "unknown error"}`);
};
const profile=await fs.mkdtemp(path.join(os.tmpdir(),"aicrm-media-refresh-chromium-")); let cdp; let journeyError;
try {
 browser=spawn(binary(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--no-first-run","--no-default-browser-check","--disable-background-networking","--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:["ignore","ignore","pipe"]}); browser.stderr.on("data",c=>{stderr=(stderr+c).slice(-2048);});
 const tab=await (await fetch(`${await devtools(profile)}/json/new?about:blank`,{method:"PUT"})).json(); const socket=new WebSocket(tab.webSocketDebuggerUrl); await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",reject,{once:true});}); cdp=new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
 await cdp.call("Page.navigate",{url:`${baseURL}/login?next=%2Fadmin%2Fimage-library`}); await wait(cdp,"Boolean(document.querySelector('form[action=\"/login\"] input[name=login_csrf_token]'))","login page");
 await value(cdp,`(()=>{document.querySelector('input[name=username]').value=${JSON.stringify(username)};document.querySelector('input[name=password]').value=${JSON.stringify(password)};document.querySelector('form[action="/login"]').requestSubmit();return true})()`);
 await wait(cdp,"location.pathname==='/admin/image-library'&&document.body?.dataset.page==='images'&&document.title.includes('图片素材库')","image Host title");
 await value(cdp,"(()=>{const fetcher=window.fetch.bind(window);window.__mediaRefreshRequests=[];window.fetch=async(...args)=>{const response=await fetcher(...args);window.__mediaRefreshRequests.push(`${args[1]?.method||'GET'} ${typeof args[0]==='string'?args[0]:args[0].url} ${response.status} ${await response.clone().text()}`);return response};return true})()");
 await wait(cdp,`Boolean(document.querySelector('#material-refresh-panel')&&document.querySelector('#material-refresh-panel').textContent.includes('素材刷新状态')&&document.querySelector('#material-refresh-panel').textContent.includes(${JSON.stringify(missingSourceRef)})&&document.querySelector('#material-refresh-panel').textContent.includes('原文件缺失，请补传'))`,`refresh panel and missing source ${missingSourceRef}`);
 await assertImageLibraryLayout(cdp,780,700); await assertImageLibraryLayout(cdp,390,420); await cdp.call("Emulation.clearDeviceMetricsOverride");
 await value(cdp,"(()=>{const region=[...document.querySelectorAll('main#stage div')].find(n=>n.style.overflow==='auto');if(!region)return false;region.scrollTop=180;return region.scrollTop>=0})()");
 await value(cdp,"[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='上传图片').click();true"); await wait(cdp,"Boolean(document.querySelector('#fImgUpFile'))","image upload dialog");
 await value(cdp,`(()=>{const png=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0));const input=document.querySelector('#fImgUpFile');const dt=new DataTransfer();dt.items.add(new File([png],'browser-source.png',{type:'image/png'}));input.files=dt.files;input.dispatchEvent(new Event('change',{bubbles:true}));document.querySelector('#fImgUpName').value='浏览器刷新素材';[...document.querySelectorAll('#stage button')].find(b=>b.textContent.trim()==='上传').click();return true})()`);
 await wait(cdp,"!document.querySelector('#fImgUpFile')&&document.querySelector('#material-refresh-panel')?.textContent.includes('browser-source.png')","image material upload/readback");
 await cdp.call("Page.navigate",{url:`${baseURL}/admin/image-library`}); await wait(cdp,"document.querySelector('#material-refresh-panel')?.textContent.includes('fixture-media-1')","first fake credential status readback");
 await value(cdp,"(()=>{const fetcher=window.fetch.bind(window);window.__mediaRefreshRequests=[];window.fetch=async(...args)=>{const response=await fetcher(...args);window.__mediaRefreshRequests.push(`${args[1]?.method||'GET'} ${typeof args[0]==='string'?args[0]:args[0].url} ${response.status}`);return response};return true})()");
 await value(cdp,"document.querySelector('[data-material-refresh-all=true]').click();true"); await wait(cdp,"window.__mediaRefreshRequests.some(value=>value.includes('POST /api/admin/media-preparations/refresh-rounds 202'))","manual refresh accepted");
 for(let i=0;i<40;i++){ await value(cdp,"document.querySelector('[data-material-refresh-round]')?.click();true"); await sleep(250); if(await value(cdp,"document.querySelector('#material-refresh-panel')?.textContent.includes('成功 1')")) break; if(i===39) throw new Error("manual refresh round did not complete"); }
 await cdp.call("Page.navigate",{url:`${baseURL}/admin/image-library`}); await wait(cdp,"document.querySelector('#material-refresh-panel')?.textContent.includes('fixture-media-2')&&document.querySelector('#material-refresh-panel')?.textContent.includes('当前凭据可用')","old credential refreshed into usable credential");
 await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:true}).then(async result=>fs.writeFile(screenshot,Buffer.from(result.data,"base64")));
 await cdp.call("Page.navigate",{url:`${baseURL}/admin/operation-cycles`}); await wait(cdp,"Boolean([...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情'))","operation cycles");
 await value(cdp,"[...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情').click();true"); await wait(cdp,"Boolean([...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次'))","strategy detail");
 await value(cdp,"[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次').click();true"); await wait(cdp,"Boolean(document.querySelector('dialog[open] input[type=file]'))","new Excel draft dialog");
 await value(cdp,`(()=>{const input=document.querySelector('dialog[open] input[type=file]');const dt=new DataTransfer();const xlsx=Uint8Array.from(atob(${JSON.stringify(xlsxBase64)}),c=>c.charCodeAt(0));dt.items.add(new File([xlsx],'media-refresh.xlsx',{type:'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'}));input.files=dt.files;input.dispatchEvent(new Event('change'));const fresh=document.querySelector('dialog[open] input[type=checkbox]');fresh.checked=true;fresh.dispatchEvent(new Event('change'));[...document.querySelectorAll('dialog[open] button')].find(b=>b.textContent==='上传并开始审核').click();return true})()`);
 await wait(cdp,"!document.querySelector('dialog[open]')&&document.querySelector('.xeb-detail-main')?.textContent.includes('真实解析第一条草稿')","unapproved Excel draft");
 if(!(await value(cdp,"[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').disabled"))) throw new Error("draft without cover unexpectedly became approvable");
 await value(cdp,`(()=>{const input=document.querySelector('input[aria-label="统一封面图片"]');const png=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0));const dt=new DataTransfer();dt.items.add(new File([png],'excel-cover.png',{type:'image/png'}));input.files=dt.files;[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='上传统一封面').click();return true})()`);
 await wait(cdp,"document.querySelector('.xeb-detail-main')?.textContent.includes('统一封面已更新')","Excel cover upload");
 if(await value(cdp,"document.querySelector('.xeb-detail-main')?.textContent.includes('企微任务意图已创建')")) throw new Error("draft created a message without approval");
 console.log(`media_refresh_chromium: PASS screenshot=${screenshot}`);
} catch(error) { journeyError=asError(error); } finally {
 const cleanupErrors=[];
 if(cdp) { try { cdp.close(); } catch(error) { cleanupErrors.push(asError(error)); } }
 const browserError=await stopBrowser(browser); if(browserError) cleanupErrors.push(browserError);
 const profileError=await removeProfile(profile); if(profileError) cleanupErrors.push(profileError);
 if(journeyError&&cleanupErrors.length) throw new AggregateError([journeyError,...cleanupErrors],"Media refresh Chromium journey and cleanup failed");
 if(journeyError) throw journeyError;
 if(cleanupErrors.length) throw new AggregateError(cleanupErrors,"Media refresh Chromium cleanup failed");
}
