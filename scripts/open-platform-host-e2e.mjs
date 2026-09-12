#!/usr/bin/env node
import { JSDOM, VirtualConsole } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = await buildTestBrowserBundle(path.join(repository, 'web', 'v3', 'openPlatformAdapter.ts'));
const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const requests = [];
const browserErrors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => browserErrors.push(String(error?.message || error)));

const capabilities = [
  'platform.capabilities.read', 'customer.resolve', 'customer.read', 'customer.activity.read', 'ai.review_plan.create', 'operation.read',
];
const operations = capabilities.map((capability, index) => ({
  operation_id: `operation-${index + 1}`, rest_method: index === 4 ? 'POST' : 'GET', rest_path: `/open/v1/operation-${index + 1}`,
  mcp_tool: `operation_${index + 1}`, capability, required_scope: index === 4 ? 'write' : 'read', schema_version: 'open.v1',
}));
const expiresAt = '2026-09-06T10:11:12.611265Z';
let selected = {
  client_id: 'existing-agent', display_name: '现有调用方', purpose: 'external_agent', credential_hint: 'v1', audiences: ['external_integration'],
  scopes: ['read'], capabilities: ['platform.capabilities.read'], allowed_cidrs: [], owner_scope: { customer: ['read'] }, token_ttl_seconds: 1800,
  expires_at: expiresAt, enabled: true, reissue_required: false, auth_version: 3, created_at: '2026-09-06T09:00:00Z',
};
let created = null;
let activationAttempts = 0;

function json(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

const dom = new JSDOM(`<!doctype html><html><body data-page="apidocs"><main id="stage"></main><script>${bundle}</script></body></html>`, {
  url: 'https://open-platform.test/admin/api-docs', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.Headers = Headers;
    window.document.cookie = 'aicrm_admin_csrf=csrf-fixture; path=/';
    window.HTMLDialogElement.prototype.showModal = function showModal() { this.open = true; };
    window.HTMLDialogElement.prototype.close = function close() { this.open = false; this.dispatchEvent(new window.Event('close')); };
    Object.defineProperty(window.navigator, 'clipboard', { configurable: true, value: undefined });
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = (init.method || 'GET').toUpperCase();
      const body = init.body ? JSON.parse(String(init.body)) : undefined;
      requests.push({ path: url.pathname + url.search, method, headers: Object.fromEntries(new Headers(init.headers || {}).entries()), body });
      if (method === 'GET' && url.pathname === '/api/admin/open-platform/clients') return json({ items: [selected, ...(created ? [created] : [])] });
      if (method === 'GET' && url.pathname === '/api/admin/open-platform/routes') return json({ items: operations });
      if (method === 'GET' && url.pathname === `/api/admin/open-platform/clients/${selected.client_id}`) return json({ client: selected });
      if (method === 'GET' && created && url.pathname === `/api/admin/open-platform/clients/${created.client_id}`) return json({ client: created });
      if (method === 'GET' && /\/audit$/.test(url.pathname)) return json({ items: [{ action: 'machine_client_grants_updated', outcome: 'revoked_prior_bearers', details: {}, created_at: '2026-09-06T10:11:12Z' }, { action: 'unmapped_action', outcome: 'unmapped_outcome', details: {}, created_at: '2026-09-06T10:11:13Z' }] });
      if (method === 'PATCH' && url.pathname === `/api/admin/open-platform/clients/${selected.client_id}`) {
        selected = { ...selected, ...body, auth_version: selected.auth_version + 1 };
        return json({ client: selected });
      }
      if (method === 'POST' && url.pathname === '/api/admin/open-platform/clients') {
        created = { ...selected, ...body, client_id: body.client_id, display_name: body.display_name, enabled: false, reissue_required: false, auth_version: 1, created_at: '2026-09-06T10:00:00Z' };
        return json({ client: created, secret: 'one-time-secret-fixture' }, 201);
      }
      if (method === 'POST' && created && url.pathname === `/api/admin/open-platform/clients/${created.client_id}/rotate`) {
        created = { ...created, enabled: false, auth_version: created.auth_version + 1 };
        return json({ client: created, secret: 'rotated-secret-fixture' });
      }
      if (method === 'POST' && created && url.pathname === `/api/admin/open-platform/clients/${created.client_id}/activate`) {
        activationAttempts += 1;
        if (activationAttempts === 2) return json({ error: 'response_unavailable' }, 503);
        created = { ...created, enabled: true, auth_version: created.auth_version + 1 };
        return json({ client: created });
      }
      return json({ error: 'unexpected_request' }, 500);
    };
  },
});

function action(label) {
  return [...dom.window.document.querySelectorAll('button[data-open-platform-action]')].find((node) => node.dataset.openPlatformAction === label);
}

try {
  await sleep(120);
  const document = dom.window.document;
  const root = document.querySelector('[data-open-platform-host="v1"]');
  if (!root || !document.body.textContent.includes('V1 能力目录') || [...document.querySelectorAll('.open-platform-catalog tbody')].at(-1)?.querySelectorAll('tr').length !== capabilities.length) throw new Error('V1 Host did not render the controlled six-operation catalog');
  for (let attempt = 0; attempt < 10 && !document.body.textContent.includes('更新授权范围'); attempt += 1) await sleep(20);
  const auditText = [...document.querySelectorAll('.open-platform-catalog')].find((node) => node.querySelector('h2')?.textContent === '最近审计')?.textContent || '';
  if (!auditText.includes('更新授权范围') || !auditText.includes('已撤销旧访问凭据') || !auditText.includes('审计操作待确认') || !auditText.includes('审计结果待确认') || auditText.includes('machine_client_grants_updated') || auditText.includes('revoked_prior_bearers')) throw new Error(`audit action and outcome were not presented in Chinese: ${auditText}`);

  const expiry = document.querySelector('[data-open-platform-edit="expires_at"]');
  if (!expiry || !/^2026-09-06T18:11:12(?:\.000)?$/.test(expiry.value)) throw new Error(`UTC expiry was not rendered as Shanghai datetime with seconds: ${expiry?.value}`);
  const ttl = document.querySelector('[data-open-platform-edit="token_ttl_seconds"]');
  ttl.value = '3601';
  action('保存授权')?.click();
  await sleep(25);
  ttl.value = '60.5';
  action('保存授权')?.click();
  await sleep(25);
  if (requests.some((item) => item.method === 'PATCH')) throw new Error('out-of-range or fractional TTL bypassed client-side V1 validation');
  if (!document.body.textContent.includes('请保留显示名称、TTL、scope 与能力。')) throw new Error('invalid TTL did not show a controlled validation message');

  ttl.value = '3600';
  action('保存授权')?.click();
  await sleep(120);
  const patch = requests.find((item) => item.method === 'PATCH');
  if (!patch || patch.body.token_ttl_seconds !== 3600 || patch.body.expires_at !== expiresAt) throw new Error(`unchanged fractional expiry/TTL save drifted: ${JSON.stringify(patch?.body)}`);
  if (!patch.headers['x-csrf-token']) throw new Error('V1 grant mutation lost CSRF protection');

  const invalidExpiry = document.querySelector('[data-open-platform-edit="expires_at"]');
  invalidExpiry.type = 'text';
  invalidExpiry.value = '2026-02-29T00:00';
  action('保存授权')?.click();
  await sleep(25);
  if (requests.filter((item) => item.method === 'PATCH').length !== 1 || !document.body.textContent.includes('到期时间格式无效，请填写有效时间后保存。')) throw new Error('invalid Shanghai datetime-local expiry cleared or mutated an existing expiry');
  invalidExpiry.type = 'datetime-local';
  invalidExpiry.value = '2026-09-06T18:11:12';

  document.querySelector('[data-open-platform-create="client_id"]').value = 'new-agent';
  document.querySelector('[data-open-platform-create="display_name"]').value = '新建调用方';
  document.querySelector('[data-open-platform-create="token_ttl_seconds"]').value = '1800';
  action('创建并显示一次密钥')?.click();
  await sleep(50);
  const dialog = document.querySelector('dialog[data-open-platform-secret="new-agent"]');
  if (!dialog?.open) throw new Error('create did not show the one-time secret dialog');
  action('复制并确认启用')?.click();
  await sleep(25);
  const activationCalls = () => requests.filter((item) => item.method === 'POST' && /\/activate$/.test(item.path));
  if (activationCalls().length !== 0 || !dialog.textContent.includes('当前浏览器无法安全复制；请手动复制后再确认启用。')) throw new Error('unavailable clipboard activated or hid a disabled client');
  action('我已手动复制并确认启用')?.click();
  await sleep(120);
  const activations = activationCalls();
  if (activations.length !== 1 || activations[0].body?.client_secret !== 'one-time-secret-fixture' || activations[0].body?.copied_confirmed !== true) throw new Error('explicit manual confirmation did not use the one-time activation contract');
  if (!requests.some((item, index) => index > requests.indexOf(activations[0]) && item.method === 'GET' && item.path === '/api/admin/open-platform/clients')) throw new Error('closing the secret dialog did not refresh the created client list');

  action('轮换密钥')?.click();
  await sleep(50);
  const rotatedDialog = document.querySelector('dialog[data-open-platform-secret="new-agent"]');
  if (!rotatedDialog?.open || !rotatedDialog.textContent.includes('rotated-secret-fixture')) throw new Error('rotate did not show a new one-time secret');
  action('我已手动复制并确认启用')?.click();
  await sleep(60);
  if (!rotatedDialog.textContent.includes('未确认启用结果，请刷新核对状态。')) throw new Error('an uncertain activation was incorrectly described as disabled');
  const uncertainAttempts = activationAttempts;
  action('我已手动复制并确认启用')?.click();
  await sleep(25);
  if (activationAttempts !== uncertainAttempts) throw new Error('unknown activation outcome retried with the one-time secret');
  if (!requests.some((item, index) => index > requests.indexOf(activations.at(-1)) && item.method === 'GET' && item.path === '/api/admin/open-platform/clients')) throw new Error('unknown activation outcome did not refresh the authoritative client state');
  if (browserErrors.length) throw new Error(`Host emitted DOM errors: ${JSON.stringify(browserErrors)}`);
  console.log('open platform V1 Host DOM/HTTP journey: PASS');
} finally {
  dom.window.close();
}
