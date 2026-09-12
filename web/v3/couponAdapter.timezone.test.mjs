import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole, requestInterceptor } = jsdom;
const filePath = fileURLToPath(import.meta.url);
const root = path.resolve(path.dirname(filePath), '../..');

if (!process.env.AICRM_COUPON_TIMEZONE_PROBE) {
  for (const timezone of ['UTC', 'America/Los_Angeles']) {
    const result = spawnSync(process.execPath, [filePath], {
      env: { ...process.env, AICRM_COUPON_TIMEZONE_PROBE: '1', TZ: timezone }, encoding: 'utf8',
    });
    assert.equal(result.status, 0, `TZ=${timezone}: ${result.stderr || result.stdout}`);
    const body = JSON.parse(result.stdout.trim());
    assert.deepEqual(body, {
      claim_starts_at: '2026-09-08T02:00:00.000Z',
      claim_ends_at: '2026-09-08T04:00:00.000Z',
      use_starts_at: '2026-09-08T02:00:00.000Z',
      use_ends_at: '2026-09-08T05:00:00.000Z',
    }, `TZ=${timezone} must submit the same Shanghai instants through the Coupon Host`);
  }
  console.log('coupon Host Shanghai write boundary across browser timezones: PASS');
  process.exit(0);
}

const host = await buildTestBrowserBundle(path.join(root, 'web/v3/couponAdapter.ts'));
const donorForm = await fs.readFile(path.join(root, 'web/donors/standard-components-production/coupons/coupon_form.html'), 'utf8');
const donorStyle = await fs.readFile(path.join(root, 'web/donors/standard-components-production/coupons/coupon_styles.html'), 'utf8');
const runtimeBlock = donorForm.indexOf('{% block scripts_extra %}');
const runtimeOpen = donorForm.indexOf('<script>', runtimeBlock);
const runtimeClose = donorForm.indexOf('</script>', runtimeOpen);
const donorRuntime = donorForm.slice(runtimeOpen + '<script>'.length, runtimeClose);
const calls = [];
const pause = () => new Promise((resolve) => setTimeout(resolve, 10));
async function waitFor(check, message) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await pause();
  }
  throw new Error(message);
}
const dom = new JSDOM('<!doctype html><body data-page="couponForm"><main id="stage"><textarea id="coupon-target-refs"></textarea></main></body>', {
  url: 'https://test.invalid/admin/couponForm.html', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: new VirtualConsole(),
  resources: { interceptors: [requestInterceptor(async (request) => request.url.endsWith('/coupon_form_runtime.js') ? new Response(donorRuntime, { headers: { 'Content-Type': 'application/javascript' } }) : undefined)] },
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = String(init.method || (typeof input === 'string' ? 'GET' : input.method)).toUpperCase();
      if (url.pathname === '/assets/standard-components/coupon_form.html') return new Response(donorForm);
      if (url.pathname === '/assets/standard-components/coupon_styles.html') return new Response(donorStyle);
      if (url.pathname.endsWith('/product-options')) return Response.json({ total: 1, items: [{ target_ref: 'standard_product:9', name: '测试商品', price_minor: 2, currency: 'CNY' }] });
      if (url.pathname === '/api/admin/coupons' && method === 'POST') {
        calls.push(JSON.parse(String(init.body)));
        return Response.json({ coupon: { id: 9 } }, { status: 201 });
      }
      return Response.json({});
    };
  },
});
try {
  dom.window.eval(host);
  const document = dom.window.document;
  await waitFor(() => document.querySelector('#couponForm'), 'donor form did not mount');
  document.querySelector('#openProductSelector').click();
  await waitFor(() => document.querySelector('[data-product-option="standard_product:9"]'), 'product picker did not load');
  document.querySelector('[data-product-option="standard_product:9"]').click();
  document.querySelector('#confirmProductSelection').click();
  document.querySelector('#couponName').value = '时区验收券';
  document.querySelector('#couponAmount').value = '0.01';
  document.querySelector('#couponIssueLimit').value = '1';
  document.querySelector('#couponPerUserLimit').value = '1';
  document.querySelector('#couponClaimStart').value = '2026-09-08T10:00';
  document.querySelector('#couponClaimEnd').value = '2026-09-08T12:00';
  document.querySelector('#couponUseStart').value = '2026-09-08T10:00';
  document.querySelector('#couponUseEnd').value = '2026-09-08T13:00';
  document.querySelector('#saveCoupon').click();
  await waitFor(() => calls.length === 1, 'Coupon Host did not submit through its scoped write boundary');
  const body = calls[0];
  process.stdout.write(JSON.stringify({
    claim_starts_at: body.claim_starts_at,
    claim_ends_at: body.claim_ends_at,
    use_starts_at: body.use_starts_at,
    use_ends_at: body.use_ends_at,
  }));
} finally {
  dom.window.close();
}
