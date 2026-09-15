import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

const base = process.env.AICRM_PUBLIC_SURVEY_BROWSER_URL;
const session = process.env.AICRM_PUBLIC_SURVEY_BROWSER_SESSION;
const successSlug = process.env.AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG;
const failureSlug = process.env.AICRM_PUBLIC_SURVEY_BROWSER_FAILURE_SLUG;
const screenshots = process.env.AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR;
if (!/^https:\/\//.test(base || '') || !/^[A-Za-z0-9_-]{43}$/.test(session || '') || !/^[a-z0-9-]{1,128}$/.test(successSlug || '') || !/^[a-z0-9-]{1,128}$/.test(failureSlug || '') || !path.isAbsolute(screenshots || '')) throw new Error('public Survey Chromium journey configuration is invalid');

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const chrome = () => {
  const candidates = process.platform === 'darwin' ? ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'google-chrome', 'chromium'] : ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'];
  for (const candidate of candidates) if ((candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status : spawnSync('which', [candidate], { stdio: 'ignore' }).status) === 0) return candidate;
  throw new Error('Chromium binary is unavailable');
};
class CDP {
  constructor(socket) {
    this.socket = socket; this.id = 0; this.successSubmissions = 0; this.failureSubmissions = 0; this.pending = new Map();
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      if (message.method === 'Network.requestWillBeSent' && message.params?.request?.method === 'POST') {
        const requestURL = message.params.request.url || '';
        if (requestURL.includes('/api/public/questionnaires/' + successSlug + '/submissions')) this.successSubmissions += 1;
        if (requestURL.includes('/api/public/questionnaires/' + failureSlug + '/submissions')) this.failureSubmissions += 1;
      }
      const pending = this.pending.get(message.id); if (!pending) return;
      this.pending.delete(message.id); message.error ? pending.reject(new Error('CDP request failed')) : pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
}
const evaluate = async (cdp, expression, label) => {
  const result = await cdp.call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(`${label}: ${result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'evaluation failed'}`);
  return result.result?.value;
};
const waitFor = async (cdp, expression, label) => {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression, `${label} probe`)) return;
    await delay(50);
  }
  const evidence = await evaluate(cdp, `(() => ({path: location.pathname, state: document.body?.dataset.v3PublicSurvey || '', text: document.querySelector('#screen')?.textContent?.trim().slice(0, 500) || ''}))()`, `${label} evidence`);
  throw new Error(`${label}: ${JSON.stringify(evidence)}`);
};
const portURL = async (profile) => {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try { const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch (_) {}
    await delay(50);
  }
  throw new Error('Chromium DevTools did not start');
};
const closeBrowser = async (browser) => {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return;
  browser.kill('SIGTERM');
  await Promise.race([new Promise((resolve) => browser.once('exit', resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) browser.kill('SIGKILL');
};
const screenshot = async (cdp, width, filename) => {
  await cdp.call('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 1, mobile: true });
  await evaluate(cdp, 'document.fonts?.ready || Promise.resolve()', `${filename} fonts`);
  const layout = await evaluate(cdp, `(() => { const screen=document.querySelector('#screen'); const submit=document.querySelector('[data-h5-submit]'); return { viewport: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth, screen: screen?.getBoundingClientRect().width || 0, submit: submit?.getBoundingClientRect().height || 0 }; })()`, `${filename} layout`);
  if (!layout || layout.viewport !== width || layout.scrollWidth > width || layout.screen < width - 2 || (layout.submit && layout.submit < 44)) throw new Error(`${filename} layout=${JSON.stringify(layout)}`);
  const image = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await fs.writeFile(path.join(screenshots, filename), Buffer.from(image.data, 'base64'), { mode: 0o600 });
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-public-survey-chromium-'));
let browser;
let failed = false;
try {
  await fs.mkdir(screenshots, { recursive: true, mode: 0o700 });
  browser = spawn(chrome(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', '--user-data-dir=' + profile, '--no-first-run', '--ignore-certificate-errors', '--allow-insecure-localhost', '--user-agent=Mozilla/5.0 MicroMessenger/8.0', 'about:blank'], { stdio: 'ignore' });
  let address;
  try { address = await portURL(profile); } catch (error) { if (process.platform === 'darwin') { console.log('public_survey_chromium: SKIP_DEVTOOLS'); process.exit(0); } throw error; }
  const target = await (await fetch(address + '/json/new?about:blank', { method: 'PUT' })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  const cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable'); await cdp.call('Network.enable');
  await cdp.call('Network.setCookie', { name: '__Host-aicrm_survey_identity', value: session, url: base, path: '/', secure: true, httpOnly: true, sameSite: 'Lax' });

  await cdp.call('Page.navigate', { url: `${base}/q/${successSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/all.html' && document.body?.dataset.v3PublicSurvey === 'all'`, 'authorized public all-in-one route did not mount');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-question-id] label[data-option-id]')) && Boolean(document.querySelector('#screen [data-h5-submit]'))`, 'actual public answer form did not render');
  await screenshot(cdp, 375, 'public-survey-answer-375.png');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-submit]').click(); true`, 'submit incomplete required answer');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-h5-error]')) && document.querySelector('#screen [data-h5-submit]')?.disabled === false`, 'required-answer validation did not retain an editable retry state');
  if (cdp.successSubmissions !== 0) throw new Error(`required-answer validation submitted=${cdp.successSubmissions}`);
  await evaluate(cdp, `document.querySelector('#screen label[data-option-id]').click(); true`, 'select public answer');
  await waitFor(cdp, `document.querySelector('#screen label[data-option-id]')?.getAttribute('aria-pressed') === 'true'`, 'selected answer did not remain visible');
  await evaluate(cdp, `(() => { const button=document.querySelector('#screen [data-h5-submit]'); button.click(); button.click(); return true; })()`, 'double click public submit');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-h5-receipt] [data-h5-result-link]'))`, 'successful submission receipt did not render');
  if (cdp.successSubmissions !== 1) throw new Error(`success submission requests=${cdp.successSubmissions}`);
  await evaluate(cdp, `document.querySelector('#screen [data-h5-result-link]').click(); true`, 'open actual submission result');
  await waitFor(cdp, `location.pathname === '/h5/result.html' && Boolean(document.querySelector('#screen [data-h5-result]')) && document.querySelector('#screen')?.textContent?.includes('提交已确认')`, 'actual result GET did not render');
  const successfulResult = await evaluate(cdp, `(() => { const text=document.querySelector('#screen')?.textContent || ''; const id=text.match(/提交编号\\s*(\\d+)/)?.[1] || ''; return { submissionID: Number(id), time: text.includes('提交时间'), version: text.includes('问卷版本'), internalScope: /处理范围|仅本地处理|外部效果/.test(text) }; })()`, 'read rendered result receipt');
  if (!successfulResult?.submissionID || !successfulResult.time || !successfulResult.version || successfulResult.internalScope) throw new Error(`public result receipt presentation is incomplete: ${JSON.stringify(successfulResult)}`);
  await screenshot(cdp, 390, 'public-survey-result-390.png');

  await cdp.call('Page.navigate', { url: `${base}/q/${failureSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/one.html' && document.body?.dataset.v3PublicSurvey === 'one' && document.querySelector('#screen [data-h5-progress]')?.textContent?.includes('1 / 2')`, 'authorized one-by-one route or progress did not mount');
  await evaluate(cdp, `document.querySelector('#screen label[data-option-id]').click(); true`, 'select retry answer');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-next]').click(); true`, 'advance to final question');
  await waitFor(cdp, `document.querySelector('#screen [data-h5-submit]') && document.querySelector('#screen [data-h5-progress]')?.textContent?.includes('2 / 2')`, 'final one-by-one submit step did not render');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-submit]').click(); true`, 'submit controlled failure');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-v3-survey-submitting]')) && !document.querySelector('#screen [data-h5-submit]')`, 'one-by-one submission did not expose its stable pending feedback');
  await waitFor(cdp, `document.querySelector('#screen [data-v3-survey-recovery]')?.textContent === '暂时无法完成操作，请保留当前页面并稍后重试。' && document.querySelector('#screen [data-v3-survey-error-detail]')?.textContent === '问题详情：HTTP 503' && document.querySelector('#screen [data-h5-submit]')?.disabled === false`, 'failure did not expose a recoverable transport explanation and retry action');
  if (cdp.failureSubmissions !== 1) throw new Error(`first failure submission requests=${cdp.failureSubmissions}`);
  await screenshot(cdp, 430, 'public-survey-failure-430.png');
  await evaluate(cdp, `(() => { document.querySelector('#screen [data-h5-previous]')?.click(); return true; })()`, 'return to preserved answer');
  await waitFor(cdp, `document.querySelector('#screen label[data-option-id]')?.getAttribute('aria-pressed') === 'true'`, 'first failed submission cleared the selected answer');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-next]').click(); true`, 'return to final retry step');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-h5-submit]'))`, 'final retry submit step did not restore');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-submit]').click(); true`, 'retry reaches the real Survey Owner');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-h5-receipt] [data-h5-result-link]'))`, 'recovered one-by-one submission receipt did not render');
  if (cdp.failureSubmissions !== 2) throw new Error(`recovery submission requests=${cdp.failureSubmissions}`);
  await evaluate(cdp, `document.querySelector('#screen [data-h5-result-link]').click(); true`, 'open recovered submission result');
  await waitFor(cdp, `location.pathname === '/h5/result.html' && Boolean(document.querySelector('#screen [data-h5-result]')) && document.querySelector('#screen')?.textContent?.includes('提交已确认')`, 'recovered submission result GET did not render');
  const recoveredResult = await evaluate(cdp, `(() => { const text=document.querySelector('#screen')?.textContent || ''; return Number(text.match(/提交编号\\s*(\\d+)/)?.[1] || 0); })()`, 'read recovered result receipt');
  if (!recoveredResult) throw new Error('recovered result receipt has no submission ID');
  console.log(JSON.stringify({ submission_id: successfulResult.submissionID, recovery_submission_id: recoveredResult }));
  console.log('public_survey_chromium: PASS');
  socket.close();
} catch (error) {
  failed = true;
  throw error;
} finally {
  await closeBrowser(browser);
  // Chromium can finish a late profile write after its root process exits. Retry
  // only this temporary-profile removal; a successful journey still reports an
  // actual cleanup failure once the bounded retry window is exhausted.
  try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }); } catch (error) { if (!failed) throw error; }
}
