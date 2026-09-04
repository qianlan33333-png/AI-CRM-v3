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
  fs.mkdirSync(path.join(source, 'aiassistant'), { recursive: true });
  fs.mkdirSync(path.join(source, 'assets'), { recursive: true });
  // Media templates and the generated Tags page are private Go-renderer
  // inputs; Campaign HTML must never reach release as a second shell.
  fs.writeFileSync(path.join(source, 'admin', 'campaigns.html'), '<aside class="side"></aside>');
  fs.writeFileSync(path.join(source, 'admin', 'wecom-tags.html'), '<aside class="side">donor shell</aside><template id="tpl"><section data-page="tags">tags</section></template>');
  for (const page of ['images', 'attach', 'mpLib']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['products', 'productForm', 'spProducts', 'spProductForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['coupons', 'couponForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['orders', 'orderDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['groupops', 'groupopsDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['agents', 'agentEdit']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  for (const page of ['cycles', 'cyclesDetail']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<template id="tpl">${page}</template>`);
  for (const page of ['config', 'configDetail', 'apidocs']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  for (const page of ['channels', 'channelForm']) fs.writeFileSync(path.join(source, 'admin', `${page}.html`), `<aside class="side">donor shell</aside><template id="tpl"><section data-page="${page}">${page}</section></template>`);
  const aiAssistantFiles = [
    'list.html', 'detail.html',
    'group_chat_picker.css', 'group_chat_picker.js',
    'material_picker.css', 'material_picker.js',
    'send_content_composer.css', 'send_content_composer.js',
    'send_content_readonly_detail.css', 'send_content_readonly_detail.js',
    'cloud_plan_review.js',
  ];
  for (const file of aiAssistantFiles) fs.writeFileSync(path.join(source, 'aiassistant', file), `aiassistant:${file}`);
  for (const file of ['admin.js', 'tokens.css', 'labs.css', 'legacy.js', 'campaigns.js', 'adminAccess.js', 'adminAccess-runtime.js', 'setupWizard.js', 'setupWizard-runtime.js', 'groupOpsHistory.js', 'funnel.js', 'funnel-runtime.js', 'dormant.js', 'cycles-host.js', 'cycles-main.js', 'cycles-legacy.js', 'channel-host.js', 'channel-main.js', 'channel-legacy.js', 'ai-host.js', 'ai-runtime.js']) {
    fs.writeFileSync(path.join(source, 'assets', file), file);
  }
  const files = Object.fromEntries([
    ['assets/admin.js', { imports: [{ kind: 'dynamic-import', path: 'assets/legacy.js' }] }],
    ['assets/tokens.css', { imports: [] }],
    ['assets/labs.css', { imports: [] }],
    ['assets/legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [
      { kind: 'dynamic-import', path: 'assets/campaigns.js' },
      { kind: 'dynamic-import', path: 'assets/adminAccess.js' },
      { kind: 'dynamic-import', path: 'assets/setupWizard.js' },
      { kind: 'dynamic-import', path: 'assets/groupOpsHistory.js' },
      { kind: 'dynamic-import', path: 'assets/funnel.js' },
      { kind: 'dynamic-import', path: 'assets/dormant.js' },
    ] }],
    ['assets/campaigns.js', { inputs: ['web/src/admin/sections/campaigns.ts'], imports: [] }],
    ['assets/adminAccess.js', { inputs: ['web/src/admin/sections/adminAccess.ts'], imports: [{ kind: 'import-statement', path: 'assets/adminAccess-runtime.js' }] }],
    ['assets/adminAccess-runtime.js', { imports: [] }],
    ['assets/setupWizard.js', { inputs: ['web/src/admin/sections/setupWizard.ts'], imports: [{ kind: 'import-statement', path: 'assets/setupWizard-runtime.js' }] }],
    ['assets/setupWizard-runtime.js', { imports: [] }],
    ['assets/groupOpsHistory.js', { inputs: ['web/src/admin/sections/groupOpsHistory.ts'], imports: [] }],
    ['assets/funnel.js', { inputs: ['web/src/admin/sections/funnelGrid.ts'], imports: [{ kind: 'import-statement', path: 'assets/funnel-runtime.js' }] }],
    ['assets/funnel-runtime.js', { imports: [] }],
    ['assets/dormant.js', { inputs: ['web/src/admin/sections/campaignHistory.ts'], imports: [] }],
    ['assets/cycles-host.js', { inputs: ['web/v3/operationCyclesAdapter.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/cycles-main.js' }] }],
    ['assets/cycles-main.js', { inputs: ['web/src/admin/main.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/cycles-legacy.js' }] }],
    ['assets/cycles-legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [] }],
    ['assets/channel-host.js', { inputs: ['web/v3/channelCenterAdapter.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/channel-main.js' }] }],
    ['assets/channel-main.js', { inputs: ['web/src/admin/main.ts'], imports: [{ kind: 'dynamic-import', path: 'assets/channel-legacy.js' }] }],
    ['assets/channel-legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [] }],
    ['assets/ai-host.js', { inputs: ['web/v3/aiAssistantAdapter.ts'], imports: [{ kind: 'import-statement', path: 'assets/ai-runtime.js' }] }],
    ['assets/ai-runtime.js', { imports: [] }],
    ...aiAssistantFiles.map((file) => [`aiassistant/${file}`, { imports: [] }]),
  ]);
  const releaseFiles = Object.fromEntries([
    ...Object.keys(files),
    'admin/campaigns.html', 'admin/wecom-tags.html',
    ...['images', 'attach', 'mpLib', 'products', 'productForm', 'spProducts', 'spProductForm', 'coupons', 'couponForm', 'orders', 'orderDetail', 'groupops', 'groupopsDetail', 'agents', 'agentEdit', 'cycles', 'cyclesDetail', 'config', 'configDetail', 'apidocs', 'channels', 'channelForm'].map((page) => `admin/${page}.html`),
    ...aiAssistantFiles.map((file) => `aiassistant/${file}`),
  ].map((relative) => [relative, { sha256: relative }]));
  fs.writeFileSync(path.join(source, 'asset-manifest.json'), JSON.stringify({
    entries: { admin: 'assets/admin.js', tokens: 'assets/tokens.css', labs: 'assets/labs.css', operationCyclesHost: 'assets/cycles-host.js', channelCenterHost: 'assets/channel-host.js', aiAssistantHost: 'assets/ai-host.js' }, files, release_files: releaseFiles,
  }));

  execFileSync(process.execPath, ['scripts/stage-pr01-effects-ui.mjs', source, stage], { stdio: 'inherit' });
  const staged = fs.readdirSync(stage, { recursive: true }).map((entry) => String(entry).split(path.sep).join('/')).sort();
  assert.deepEqual(staged, ['admin', 'admin/agentEdit.html', 'admin/agents.html', 'admin/apidocs.html', 'admin/attach.html', 'admin/channelForm.html', 'admin/channels.html', 'admin/config.html', 'admin/configDetail.html', 'admin/couponForm.html', 'admin/coupons.html', 'admin/cycles.html', 'admin/cyclesDetail.html', 'admin/groupops.html', 'admin/groupopsDetail.html', 'admin/images.html', 'admin/mpLib.html', 'admin/orderDetail.html', 'admin/orders.html', 'admin/productForm.html', 'admin/products.html', 'admin/spProductForm.html', 'admin/spProducts.html', 'admin/tags.html', 'aiassistant', 'aiassistant/cloud_plan_review.js', 'aiassistant/detail.html', 'aiassistant/group_chat_picker.css', 'aiassistant/group_chat_picker.js', 'aiassistant/list.html', 'aiassistant/material_picker.css', 'aiassistant/material_picker.js', 'aiassistant/send_content_composer.css', 'aiassistant/send_content_composer.js', 'aiassistant/send_content_readonly_detail.css', 'aiassistant/send_content_readonly_detail.js', 'asset-manifest.json', 'assets', 'assets/admin.js', 'assets/adminAccess-runtime.js', 'assets/adminAccess.js', 'assets/ai-host.js', 'assets/ai-runtime.js', 'assets/campaigns.js', 'assets/channel-host.js', 'assets/channel-legacy.js', 'assets/channel-main.js', 'assets/cycles-host.js', 'assets/cycles-legacy.js', 'assets/cycles-main.js', 'assets/funnel-runtime.js', 'assets/funnel.js', 'assets/groupOpsHistory.js', 'assets/labs.css', 'assets/legacy.js', 'assets/setupWizard-runtime.js', 'assets/setupWizard.js', 'assets/tokens.css']);
  assert.equal(fs.existsSync(path.join(stage, 'admin', 'campaigns.html')), false, 'donor campaign HTML must not be released');
  assert.equal(fs.readFileSync(path.join(stage, 'admin', 'tags.html'), 'utf8'), fs.readFileSync(path.join(source, 'admin', 'wecom-tags.html'), 'utf8'), 'generated donor Tags page must be copied byte-for-byte as the private template source');
  assert.deepEqual(staged.filter((entry) => entry.endsWith('.html')), ['admin/agentEdit.html', 'admin/agents.html', 'admin/apidocs.html', 'admin/attach.html', 'admin/channelForm.html', 'admin/channels.html', 'admin/config.html', 'admin/configDetail.html', 'admin/couponForm.html', 'admin/coupons.html', 'admin/cycles.html', 'admin/cyclesDetail.html', 'admin/groupops.html', 'admin/groupopsDetail.html', 'admin/images.html', 'admin/mpLib.html', 'admin/orderDetail.html', 'admin/orders.html', 'admin/productForm.html', 'admin/products.html', 'admin/spProductForm.html', 'admin/spProducts.html', 'admin/tags.html', 'aiassistant/detail.html', 'aiassistant/list.html'], 'only approved private business templates may be staged');
  const stagedManifest = JSON.parse(fs.readFileSync(path.join(stage, 'asset-manifest.json'), 'utf8'));
  for (const page of ['orders', 'orderDetail']) {
    assert.equal(fs.readFileSync(path.join(stage, 'admin', `${page}.html`), 'utf8'), `<template id="tpl">${page}</template>`, `transaction template ${page} must be staged byte-for-byte`);
    assert.ok(stagedManifest.release_files[`admin/${page}.html`], `release manifest must include transaction template ${page}`);
  }
  const expectedReleaseFiles = staged.filter((entry) => entry !== 'asset-manifest.json' && fs.statSync(path.join(stage, entry)).isFile());
  assert.deepEqual(Object.keys(stagedManifest.release_files).sort(), expectedReleaseFiles, 'release manifest must describe exactly the staged release root');
  for (const relative of Object.keys(stagedManifest.release_files)) {
    assert.ok(fs.statSync(path.join(stage, relative)).isFile(), `release manifest references an absent file: ${relative}`);
  }
  assert.deepEqual(stagedManifest.files['assets/legacy.js'].imports, [
    { kind: 'dynamic-import', path: 'assets/campaigns.js' },
    { kind: 'dynamic-import', path: 'assets/adminAccess.js' },
    { kind: 'dynamic-import', path: 'assets/setupWizard.js' },
    { kind: 'dynamic-import', path: 'assets/groupOpsHistory.js' },
    { kind: 'dynamic-import', path: 'assets/funnel.js' },
    { kind: 'dynamic-import', path: 'assets/dormant.js' },
  ], 'the frozen legacy loader must retain its dynamic import metadata');
  for (const asset of ['assets/adminAccess.js', 'assets/adminAccess-runtime.js', 'assets/setupWizard.js', 'assets/setupWizard-runtime.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  for (const asset of ['assets/funnel.js', 'assets/funnel-runtime.js']) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  assert.equal(stagedManifest.entries.aiAssistantHost, 'assets/ai-host.js', 'AI Assistant host entry must be retained');
  for (const asset of ['assets/ai-host.js', 'assets/ai-runtime.js', ...aiAssistantFiles.map((file) => `aiassistant/${file}`)]) {
    assert.ok(stagedManifest.files[asset], `the staged manifest must include ${asset}`);
    assert.ok(stagedManifest.release_files[asset], `the staged release manifest must include ${asset}`);
    assert.ok(fs.existsSync(path.join(stage, asset)), `the staged release must include ${asset}`);
  }
  assert.equal(stagedManifest.files['assets/dormant.js'], undefined, 'unselected dormant chunks must not become fetchable');
  assert.equal(stagedManifest.release_files['assets/dormant.js'], undefined, 'unselected dormant chunks must not enter the release manifest');
  assert.equal(fs.existsSync(path.join(stage, 'assets', 'dormant.js')), false, 'unselected dormant chunks must not be copied');
  console.log('PR01 effects UI staging contract passed');
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
