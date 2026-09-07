import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

const base = process.env.AICRM_SURVEY_BROWSER_URL;
const username = process.env.AICRM_SURVEY_BROWSER_USERNAME;
const password = process.env.AICRM_SURVEY_BROWSER_PASSWORD;
const questionnaireID = process.env.AICRM_SURVEY_BROWSER_QUESTIONNAIRE_ID;
const target = process.env.AICRM_SURVEY_BROWSER_TARGET;
if (!/^https:\/\//.test(base || '') || !username || !password || !/^[1-9][0-9]*$/.test(questionnaireID || '') || !/^[A-Za-z0-9._:-]{1,128}$/.test(target || '')) throw new Error('survey Chromium journey configuration is invalid');

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const chrome = () => {
  const candidates = process.platform === 'darwin' ? ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'google-chrome', 'chromium'] : ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'];
  for (const candidate of candidates) if ((candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status : spawnSync('which', [candidate], { stdio: 'ignore' }).status) === 0) return candidate;
  throw new Error('Chromium binary is unavailable');
};
class CDP {
  constructor(socket) { this.socket = socket; this.id = 0; this.pending = new Map(); socket.addEventListener('message', (event) => { const message = JSON.parse(String(event.data)); const pending = this.pending.get(message.id); if (!pending) return; this.pending.delete(message.id); message.error ? pending.reject(new Error('CDP request failed')) : pending.resolve(message.result || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
}
const evaluate = async (cdp, expression) => { const result = await cdp.call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }); if (result.exceptionDetails) throw new Error('page evaluation failed'); return result.result?.value; };
const waitFor = async (cdp, expression, message) => { for (let i = 0; i < 180; i += 1) { if (await evaluate(cdp, expression)) return; await delay(50); } throw new Error(message); };
const portURL = async (profile) => { for (let i = 0; i < 160; i += 1) { try { const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; if (/^\d+$/.test(port)) return 'http://127.0.0.1:' + port; } catch (_) {} await delay(50); } throw new Error('Chromium DevTools did not start'); };

const waitForBrowserExit = async (child, timeoutMilliseconds) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    child.once('exit', () => { clearTimeout(timer); resolve(true); });
  });
};
const removeProfile = async (profile) => {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; }
    catch (error) { if (!error || !['ENOTEMPTY', 'EBUSY', 'EPERM'].includes(error.code)) return false; await delay(100); }
  }
  return false;
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-survey-chromium-'));
let browser;
let journeyFailed = false;
try {
  browser = spawn(chrome(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', '--user-data-dir=' + profile, '--no-first-run', '--ignore-certificate-errors', '--allow-insecure-localhost', 'about:blank'], { stdio: 'ignore' });
  let address;
  try { address = await portURL(profile); } catch (error) { if (process.platform === 'darwin') { console.log('survey_completion_chromium: SKIP_DEVTOOLS'); process.exit(0); } throw error; }
  const created = await (await fetch(address + '/json/new?about:blank', { method: 'PUT' })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  const cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable');
  const page = '/admin/questionnaireOps.html?id=' + questionnaireID;
  await cdp.call('Page.navigate', { url: base + '/login?next=' + encodeURIComponent(page) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"]'))", 'login did not render');
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/questionnaireOps.html'", 'login did not reach questionnaire operations');
  await evaluate(cdp, "(() => { const heading=[...document.querySelectorAll('h3')].find((item) => item.textContent.trim()==='外部推送绑定'); const toggle=[...heading.parentElement.parentElement.querySelectorAll('span')].find((item) => item.getAttribute('style')?.includes('cursor')); toggle.click(); return true; })()");
  await waitFor(cdp, "document.querySelector('#opsConfigurationReference')?.tagName === 'SELECT'", 'target selector did not replace frozen input');
  await evaluate(cdp, `(() => { const select=document.querySelector('#opsConfigurationReference'); if (![...select.options].some((item) => item.value===${JSON.stringify(target)})) throw new Error('target absent'); select.value=${JSON.stringify(target)}; select.dispatchEvent(new Event('change',{bubbles:true})); [...document.querySelectorAll('button')].find((item) => item.textContent.trim()==='保存外部推送').click(); return true; })()`);
  await waitFor(cdp, "String(document.querySelector('#fb-toast')?.textContent || '').includes('已保存')", 'configuration save did not finish');
  await evaluate(cdp, "location.reload(); true");
  await waitFor(cdp, `document.querySelector('#opsConfigurationReference')?.tagName === 'SELECT' && document.querySelector('#opsConfigurationReference').value===${JSON.stringify(target)}`, 'saved target did not reload');
  await evaluate(cdp, "[...document.querySelectorAll('button')].find((item) => item.textContent.includes('测试推送')).click(); true");
  await waitFor(cdp, "[...document.querySelectorAll('button')].some((item) => item.textContent.trim()==='确认创建')", 'test confirmation did not render');
  await evaluate(cdp, "[...document.querySelectorAll('button')].find((item) => item.textContent.trim()==='确认创建').click(); true");
  await waitFor(cdp, "String(document.querySelector('#fb-toast')?.textContent || '').includes('本地测试记录')", 'test receipt did not render');
  console.log('survey_completion_chromium: PASS');
  socket.close();
} catch (error) {
  journeyFailed = true;
  throw error;
} finally {
  if (browser && browser.exitCode === null && browser.signalCode === null) {
    browser.kill('SIGTERM');
    if (!await waitForBrowserExit(browser, 3000)) {
      browser.kill('SIGKILL');
      await waitForBrowserExit(browser, 1000);
    }
  }
  const removed = await removeProfile(profile);
  if (!removed && !journeyFailed) throw new Error('Chromium test profile cleanup did not complete');
}
