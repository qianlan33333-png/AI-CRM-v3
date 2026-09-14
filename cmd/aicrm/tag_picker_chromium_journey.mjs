import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from '../../internal/webshell/chromium_launch.mjs';

const baseURL = process.env.AICRM_TAG_PICKER_TEST_URL;
const username = process.env.AICRM_TAG_PICKER_TEST_USERNAME;
const password = process.env.AICRM_TAG_PICKER_TEST_PASSWORD;
const productID = process.env.AICRM_TAG_PICKER_TEST_PRODUCT_ID;
const channelID = process.env.AICRM_TAG_PICKER_TEST_CHANNEL_ID;
const tagID = process.env.AICRM_TAG_PICKER_TEST_TAG_ID;
const screenshotDir = process.env.AICRM_TAG_PICKER_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || '') || !username || !password || !/^[1-9]\d*$/.test(productID || '') || !/^[1-9]\d*$/.test(channelID || '') || !/^[1-9]\d*$/.test(tagID || '')) throw new Error('tag picker Chromium journey requires URL, credentials and fixture IDs');

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function browserBinary() {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, process.platform === 'darwin' ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : '', 'google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'].filter(Boolean);
  for (const candidate of candidates) {
    try {
      if (candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status === 0 : spawnSync('which', [candidate], { stdio: 'ignore' }).status === 0) return candidate;
    } catch {}
  }
  throw new Error('Chromium binary is unavailable');
}
class CDP {
  constructor(socket) {
    this.socket = socket; this.nextID = 0; this.pending = new Map();
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id); this.pending.delete(message.id);
        if (message.error) pending.reject(new Error(`CDP ${message.error.code || 'error'}: ${message.error.message || ''}`)); else pending.resolve(message.result || {});
      }
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { for (const { reject } of this.pending.values()) reject(new Error('CDP closed')); this.pending.clear(); this.socket.close(); }
}
async function devtoolsURL(profile, child, stderr) {
  const deadline = Date.now() + chromiumStartupTimeoutMS;
  while (Date.now() < deadline) {
    try {
      const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0];
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch {}
    if (child.exitCode !== null) break;
    await sleep(50);
  }
  throw new Error(chromiumStartupDiagnostic({ profile, exitCode: child.exitCode, signalCode: child.signalCode, stderr }));
}
async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(`page evaluation failed: ${result.exceptionDetails.text || result.exceptionDetails.exception?.description || 'unknown'}`);
  return result.result?.value;
}
async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await sleep(50);
  }
  throw new Error(message);
}
async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  await Promise.race([new Promise((resolve) => child.once('exit', resolve)), sleep(3000)]);
  if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
}
async function removeProfile(profile) { try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 2 }); } catch {} }

async function setViewport(cdp, width) {
  await cdp.call('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
  await sleep(100);
}
async function assertDialogLayout(cdp, width, name) {
  const layout = await evaluate(cdp, `(() => { const mask=document.querySelector('[data-v3-selection-session="tag"]'); const dialog=mask?.querySelector('.aicrm-v3-tag-picker'); const selected=mask?.querySelector('[data-v3-tag-selected]'); const list=mask?.querySelector('[data-v3-tag-list]'); const confirm=mask?.querySelector('[data-v3-tag-confirm]'); const tools=mask?.querySelector('.aicrm-v3-tag-picker__tools'); const footer=mask?.querySelector('.aicrm-v3-tag-picker__footer'); const box=(node)=>{ if(!node)return null; const rect=node.getBoundingClientRect(); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height}; }; const style=(node)=>node&&getComputedStyle(node); const groups=Array.from(mask?.querySelectorAll('.aicrm-v3-tag-picker__group')||[]).map((group)=>({heading:box(group.querySelector('h4')),firstItem:box(group.querySelector('[data-v3-tag-key]'))})); return {documentWidth:document.documentElement.scrollWidth, dialog:box(dialog), selected:box(selected), list:box(list), confirm:box(confirm), selectedOverflow:style(selected)?.overflowY, listOverflow:style(list)?.overflowY, dialogStyle:{position:style(dialog)?.position,inset:style(dialog)?.inset,display:style(dialog)?.display}, toolsStyle:{display:style(tools)?.display,flexDirection:style(tools)?.flexDirection,flexWrap:style(tools)?.flexWrap}, footerStyle:{display:style(footer)?.display,flexDirection:style(footer)?.flexDirection}, toolButtons:Array.from(tools?.querySelectorAll('button')||[]).map(box), groups, css:Array.from(document.querySelectorAll('link[rel="stylesheet"]')).map((link)=>link.href).filter((href)=>href.includes('selectionDialogStyles-'))}; })()`);
  const centerError = layout?.dialog ? Math.abs((layout.dialog.left + layout.dialog.width / 2) - layout.documentWidth / 2) : Infinity;
  const collapsedTool = (layout?.toolButtons || []).some((button) => !button || button.width < 42 || button.height < 30);
  const groupStacked = layout?.groups?.length >= 2 && layout.groups.every((group) => group.heading && group.firstItem && group.heading.bottom <= group.firstItem.top) && layout.groups[1].heading.top > layout.groups[0].heading.top;
  if (!layout || layout.documentWidth > width + 1 || !layout.dialog || layout.dialog.width > width || layout.dialog.height < 420 || layout.dialog.bottom > 900 || centerError > 10 || layout.dialogStyle.position !== 'relative' || !layout.selected || !layout.list || layout.selectedOverflow !== 'auto' || layout.listOverflow !== 'auto' || !layout.confirm || layout.confirm.bottom > layout.dialog.bottom + 1 || layout.toolsStyle.display !== 'flex' || layout.toolsStyle.flexDirection !== 'row' || layout.footerStyle.display !== 'flex' || layout.footerStyle.flexDirection !== 'row' || collapsedTool || !groupStacked || !layout.css?.length) throw new Error(`${name} tag dialog ${width}px is not operable: ${JSON.stringify(layout)}`);
}
async function screenshot(cdp, file) {
  if (!screenshotDir) return;
  await fs.mkdir(screenshotDir, { recursive: true });
  const image = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  const target = path.join(screenshotDir, file);
  await fs.writeFile(target, Buffer.from(image.data, 'base64'));
  console.log(`tag_picker_chromium: SCREENSHOT ${target}`);
}
function tagDialogPresent() { return "Boolean(document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]'))"; }
const pickFirst = "(() => { const item=document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]'); if(!item)return false; item.click(); document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-confirm]').click(); return true; })()";

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-tag-picker-chromium-'));
let browser; let cdp; let failure;
try {
  let stderr = '';
  browser = spawn(browserBinary(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--ignore-certificate-errors', '--allow-insecure-localhost', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
  browser.stderr.on('data', (chunk) => { stderr += String(chunk).slice(-4000); });
  const target = await (await fetch(`${await devtoolsURL(profile, browser, stderr)}/json/new?about:blank`, { method: 'PUT' })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', () => reject(new Error('Chromium page connection failed')), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable'); await cdp.call('Network.enable');
  const responses = new Map(); const exceptions = [];
  cdp.socket.addEventListener('message', (event) => {
    const message = JSON.parse(String(event.data));
    if (message.method === 'Network.responseReceived') {
      try { const url = new URL(String(message.params.response?.url || '')); const value = { status: Number(message.params.response?.status), mime: String(message.params.response?.mimeType || '') }; responses.set(url.pathname, value); } catch {}
    }
    if (message.method === 'Runtime.exceptionThrown' && exceptions.length < 8) exceptions.push(String(message.params.exceptionDetails?.text || message.params.exceptionDetails?.exception?.description || 'runtime_exception'));
  });
  const cssStatus = () => [...responses.entries()].find(([pathname]) => pathname.includes('selectionDialogStyles-'))?.[1];
  const diagnostic = async () => JSON.stringify({ path: await evaluate(cdp, 'location.pathname'), scripts: await evaluate(cdp, 'Array.from(document.scripts).map((script) => ({ src: script.src, type: script.type }))'), responses: Object.fromEntries(responses), exceptions });

  await cdp.call('Page.navigate', { url: `${baseURL}/login?next=%2Fadmin%2Fcustomers` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", 'login form did not render');
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  try { await waitFor(cdp, "location.pathname === '/admin/customers' && Boolean(document.querySelector('#customer-tag-batch [name=\"add_tag_ids\"]'))", 'authenticated customer page did not render'); }
  catch (_) { throw new Error(`authenticated customer page did not render: ${await diagnostic()}`); }
  await waitFor(cdp, "Boolean(Array.from(document.querySelectorAll('#customer-tag-batch button')).find((button)=>button.textContent.trim()==='选择标签'))", 'customer tag draft entry did not mount');

  // The shared picker edits the original Customer command draft only. It must
  // never issue a preview or Provider command before the existing form submit.
  await evaluate(cdp, "Array.from(document.querySelectorAll('#customer-tag-batch button')).find((button)=>button.textContent.trim()==='选择标签').click(); true");
  await waitFor(cdp, tagDialogPresent(), 'customer tag dialog did not open');
  await evaluate(cdp, "(() => { const input=document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-picker-search-input]'); input.focus(); input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',keyCode:229,isComposing:true,bubbles:true})); return Boolean(document.querySelector('[data-v3-selection-session=\"tag\"]')); })()");
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-picker-search-input]').dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})); document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-cancel]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"tag\"]')", 'customer tag cancel did not close');
  if (await evaluate(cdp, "document.querySelector('#customer-tag-batch [name=\"add_tag_ids\"]').selectedOptions.length !== 0")) throw new Error('customer tag cancel changed the original command draft');
  await evaluate(cdp, "Array.from(document.querySelectorAll('#customer-tag-batch button')).find((button)=>button.textContent.trim()==='选择标签').click(); true");
  await waitFor(cdp, tagDialogPresent(), 'customer tag dialog did not reopen');
  await setViewport(cdp, 360); await assertDialogLayout(cdp, 360, 'customer'); await screenshot(cdp, 'customer-tag-picker-360.png');
  if (!await evaluate(cdp, pickFirst)) throw new Error('customer tag row was unavailable');
  await waitFor(cdp, `!document.querySelector('[data-v3-selection-session="tag"]') && Array.from(document.querySelector('#customer-tag-batch [name="add_tag_ids"]').selectedOptions).some((option)=>option.value === ${JSON.stringify(tagID)})`, 'customer tag confirmation did not update the original command draft');
  if (await evaluate(cdp, "Array.from(performance.getEntriesByType('resource')).some((entry)=>entry.name.includes('/api/v1/customer-tag-commands'))")) throw new Error('customer tag picker sent a command before the original preview form was submitted');

  await cdp.call('Page.navigate', { url: `${baseURL}/admin/customers/1` });
  await waitFor(cdp, "Boolean(document.querySelector('#customer-tag-single [name=\"add_tag_ids\"]')) && Boolean(Array.from(document.querySelectorAll('#customer-tag-single button')).find((button)=>button.textContent.trim()==='选择标签'))", `customer detail tag draft entry did not mount: ${await diagnostic()}`);
  await evaluate(cdp, "Array.from(document.querySelectorAll('#customer-tag-single button')).find((button)=>button.textContent.trim()==='选择标签').click(); true");
  await waitFor(cdp, tagDialogPresent(), 'customer detail tag dialog did not open');
  if (!await evaluate(cdp, pickFirst)) throw new Error('customer detail tag row was unavailable');
  await waitFor(cdp, `!document.querySelector('[data-v3-selection-session="tag"]') && Array.from(document.querySelector('#customer-tag-single [name="add_tag_ids"]').selectedOptions).some((option)=>option.value === ${JSON.stringify(tagID)})`, 'customer detail tag confirmation did not update the existing command draft');
  await evaluate(cdp, "Array.from(document.querySelectorAll('#customer-tag-single button')).find((button)=>button.textContent.trim()==='选择标签').click(); true");
  await waitFor(cdp, tagDialogPresent(), 'customer detail tag dialog did not reopen');
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-cancel]').click(); true");
  await waitFor(cdp, `!document.querySelector('[data-v3-selection-session="tag"]') && Array.from(document.querySelector('#customer-tag-single [name="add_tag_ids"]').selectedOptions).some((option)=>option.value === ${JSON.stringify(tagID)})`, 'customer detail tag cancellation did not retain the prior draft');

  await cdp.call('Page.navigate', { url: `${baseURL}/admin/productForm.html?id=${productID}` });
  await waitFor(cdp, "Boolean(document.querySelector('[data-product-tag-open]'))", `product V3 tag caller did not mount: ${await diagnostic()}`);
  await evaluate(cdp, "document.querySelector('[data-product-tag-enabled]').click(); document.querySelector('[data-product-tag-open]').click(); true");
  await waitFor(cdp, tagDialogPresent(), 'product tag dialog did not open');
  await setViewport(cdp, 420); await assertDialogLayout(cdp, 420, 'product'); await screenshot(cdp, 'product-tag-picker-420.png');
  if (!await evaluate(cdp, pickFirst)) throw new Error('product tag row was unavailable');
  await waitFor(cdp, `!document.querySelector('[data-v3-selection-session="tag"]') && document.querySelector('#pfWecomTagging').value.includes(${JSON.stringify(tagID)})`, 'product tag confirmation did not update the hidden form draft');
  await evaluate(cdp, "(() => { const save=Array.from(document.querySelectorAll('button')).find((button)=>button.textContent.trim()==='保存当前维度' && !button.closest('#product-push') && !button.closest('#sp-push')); if(!save) return false; save.click(); return true; })()");
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度')", 'normal product save did not complete');
  await waitFor(cdp, `fetch('/api/admin/wechat-pay/products/${productID}',{credentials:'same-origin'}).then((response)=>response.json()).then((body)=>body.admin_projection?.wecom_tagging?.enabled===true && body.admin_projection?.wecom_tagging?.tag_ids?.length===1 && String(body.admin_projection.wecom_tagging.tag_ids[0])===${JSON.stringify(tagID)})`, 'normal product save/readback did not persist the selected tag');

  await cdp.call('Page.navigate', { url: `${baseURL}/admin/channels/${channelID}/edit` });
  await waitFor(cdp, "Boolean(document.querySelector('[data-channel-admission-page] [data-open-tag-picker]'))", `channel entry tag button did not mount: ${await diagnostic()}`);
  await evaluate(cdp, "document.querySelector('[data-channel-admission-page] [data-open-tag-picker]').click(); true");
  await waitFor(cdp, tagDialogPresent(), 'channel V3 entry tag dialog did not open');
  await setViewport(cdp, 1440); await assertDialogLayout(cdp, 1440, 'channel-wide'); await screenshot(cdp, 'channel-tag-picker-1440.png');
  await setViewport(cdp, 1280); await assertDialogLayout(cdp, 1280, 'channel'); await screenshot(cdp, 'channel-tag-picker-1280.png');
  if (!await evaluate(cdp, pickFirst)) throw new Error('channel tag row was unavailable');
  await waitFor(cdp, `!document.querySelector('[data-v3-selection-session="tag"]') && document.querySelector('[data-entry-tag-id]').value === ${JSON.stringify(tagID)}`, 'channel V3 picker did not update the existing entry tag draft');
  await evaluate(cdp, "document.querySelector('[data-save-channel]').click(); true");
  await waitFor(cdp, `fetch('/api/admin/channels/${channelID}',{credentials:'same-origin'}).then((response)=>response.json()).then((body)=>String(body.channel?.entry_tag_id)===${JSON.stringify(tagID)} && body.channel?.entry_tag_name==='Chromium 标签' && body.channel?.entry_tag_group_name==='Chromium 新客')`, 'normal channel save/readback did not persist selected entry tag');

  const css = cssStatus();
  if (!css || css.status !== 200 || !/text\/css/i.test(css.mime)) throw new Error(`selection dialog CSS was not delivered as HTTP 200 text/css: ${JSON.stringify({ css, responses: Object.fromEntries(responses) })}`);
  if (exceptions.length) throw new Error(`page emitted runtime exceptions: ${JSON.stringify(exceptions)}`);
  console.log('tag_picker_chromium: PASS');
} catch (error) {
  if (/Chromium binary is unavailable|DevTools/i.test(error instanceof Error ? error.message : String(error))) console.log('tag_picker_chromium: SKIP_DEVTOOLS');
  else failure = error;
} finally {
  try { cdp?.close(); } catch {}
  await stopBrowser(browser);
  await removeProfile(profile);
  if (failure) throw failure;
}
