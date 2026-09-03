#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-pr01-stage-'));
const source = path.join(root, 'source');
const stage = path.join(root, 'stage');
try {
  fs.mkdirSync(path.join(source, 'admin'), { recursive: true });
  fs.mkdirSync(path.join(source, 'assets'), { recursive: true });
  // Media templates and the generated Tags page are private Go-renderer
  // inputs; Campaign HTML must never reach release as a second shell.
  fs.writeFileSync(path.join(source, 'admin', 'campaigns.html'), '<aside class="side"></aside>');
  fs.writeFileSync(path.join(source, 'admin', 'wecom-tags.html'), '<aside class="side">donor shell</aside><template id="tpl"><section data-page="tags">tags</section></template>');
  for (const page of ['images', 'attach', 'mpLib']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['products', 'productForm', 'spProducts', 'spProductForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['coupons', 'couponForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['groupops', 'groupopsDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['agents', 'agentEdit']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  for (const file of ['admin.js', 'tokens.css', 'labs.css', 'legacy.js', 'campaigns.js', 'groupOpsHistory.js']) {
    fs.writeFileSync(path.join(source, 'assets', file), file);
  }
  const files = Object.fromEntries([
    ['assets/admin.js', { imports: [{ kind: 'import-statement', path: 'assets/legacy.js' }] }],
    ['assets/tokens.css', { imports: [] }],
    ['assets/labs.css', { imports: [] }],
    ['assets/legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/groupOpsHistory.js' }] }],
    ['assets/campaigns.js', { inputs: ['web/src/admin/sections/campaigns.ts'], imports: [] }],
    ['assets/groupOpsHistory.js', { inputs: ['web/src/admin/sections/groupOpsHistory.ts'], imports: [] }],
  ]);
  fs.writeFileSync(path.join(source, 'asset-manifest.json'), JSON.stringify({
    entries: { admin: 'assets/admin.js', tokens: 'assets/tokens.css', labs: 'assets/labs.css' }, files,
  }));

  execFileSync(process.execPath, ['scripts/stage-pr01-effects-ui.mjs', source, stage], { stdio: 'inherit' });
  const staged = fs.readdirSync(stage, { recursive: true }).map((entry) => String(entry).split(path.sep).join('/')).sort();
  assert.deepEqual(staged, ['admin', 'admin/agentEdit.html', 'admin/agents.html', 'admin/attach.html', 'admin/couponForm.html', 'admin/coupons.html', 'admin/groupops.html', 'admin/groupopsDetail.html', 'admin/images.html', 'admin/mpLib.html', 'admin/productForm.html', 'admin/products.html', 'admin/spProductForm.html', 'admin/spProducts.html', 'admin/tags.html', 'asset-manifest.json', 'assets', 'assets/admin.js', 'assets/campaigns.js', 'assets/groupOpsHistory.js', 'assets/labs.css', 'assets/legacy.js', 'assets/tokens.css']);
  assert.equal(fs.existsSync(path.join(stage, 'admin', 'campaigns.html')), false, 'donor campaign HTML must not be released');
  assert.equal(fs.readFileSync(path.join(stage, 'admin', 'tags.html'), 'utf8'), fs.readFileSync(path.join(source, 'admin', 'wecom-tags.html'), 'utf8'), 'generated donor Tags page must be copied byte-for-byte as the private template source');
  assert.deepEqual(staged.filter((entry) => entry.endsWith('.html')), ['admin/agentEdit.html', 'admin/agents.html', 'admin/attach.html', 'admin/couponForm.html', 'admin/coupons.html', 'admin/groupops.html', 'admin/groupopsDetail.html', 'admin/images.html', 'admin/mpLib.html', 'admin/productForm.html', 'admin/products.html', 'admin/spProductForm.html', 'admin/spProducts.html', 'admin/tags.html'], 'only approved private Media, Tags, Product, Coupon, Group Ops, and Automation templates may be staged');
  const stagedManifest = JSON.parse(fs.readFileSync(path.join(stage, 'asset-manifest.json'), 'utf8'));
  assert.deepEqual(stagedManifest.files['assets/legacy.js'].imports, [{ kind: 'dynamic-import', path: 'assets/groupOpsHistory.js' }], 'the frozen Group Ops history dynamic module must remain fetchable from the staged manifest');
  console.log('PR01 effects UI staging contract passed');
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
