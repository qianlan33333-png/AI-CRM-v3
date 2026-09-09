#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const readManifest = (root) => JSON.parse(fs.readFileSync(path.join(root, 'asset-manifest.json'), 'utf8'));
const sourceManifest = readManifest(source);
const stagedManifest = readManifest(stage);
const surfaceFeedbackHost = sourceManifest.entries?.surfaceFeedbackHost;
const surfaceFeedbackStyles = sourceManifest.entries?.surfaceFeedbackStyles;
assert.equal(sourceManifest.files?.[surfaceFeedbackHost]?.entry_point, 'web/v3/surfaceFeedbackHost.ts', 'surface feedback Host must be V3-owned');
assert.equal(sourceManifest.files?.[surfaceFeedbackStyles]?.entry_point, 'web/v3/surfaceFeedback.css', 'surface feedback styles must be V3-owned');
const entryKeys = [
  'admin', 'adminSessionHost', 'standardComponentsHost', 'standardComponentsStableHost', 'tokens', 'labs',
  'operationCyclesHost', 'materialSaveHost', 'orderHost', 'productHost', 'couponHost', 'channelCenterHost', 'aiAssistantHost', 'radarHost',
  'customerHost', 'sidebarHost', 'sidebarStandardOverlay', 'sidebarImageResourceLoader', 'sidebarStandardStyles', 'openPlatformHost', 'sidebarStyles', 'groupopsHost', 'groupopsStyles', 'channelAdmissionStyles', 'surfaceFeedbackHost', 'surfaceFeedbackStyles', 'presentationStyles', 'actionFeedbackStyles', 'memberGridFeedbackHost',
];
const standardComponentSupport = ['assets/standard-components/operation_member_picker.js', 'assets/standard-components/group_chat_picker.css', 'assets/standard-components/group_chat_picker.js', 'assets/standard-components/material_picker.css', 'assets/standard-components/material_picker.js', 'assets/standard-components/send_content_composer.css', 'assets/standard-components/send_content_composer.js', 'assets/standard-components/wecom_tag_picker.css', 'assets/standard-components/wecom_tag_picker.js', 'assets/standard-components/coupon_form.html', 'assets/standard-components/coupon_form_runtime.js', 'assets/standard-components/coupon_styles.html', 'assets/standard-components/channel_code_form.html', 'assets/standard-components/channel_admission_pages.js'];
const groupOpsSupport = [...standardComponentSupport, 'aiassistant/send_content_readonly_detail.css', 'aiassistant/send_content_readonly_detail.js'];
const selected = new Set();
const includeClosure = (relative) => {
  if (selected.has(relative)) return;
  const metadata = sourceManifest.files?.[relative];
  assert.ok(metadata, `source manifest lacks metadata for ${relative}`);
  selected.add(relative);
  for (const imported of metadata.imports || []) includeClosure(imported.path);
};
for (const key of entryKeys) {
  assert.equal(stagedManifest.entries?.[key], sourceManifest.entries?.[key], `staged manifest omits new-shell entry ${key}`);
  includeClosure(sourceManifest.entries[key]);
}
for (const relative of [...selected, ...groupOpsSupport]) {
  assert.deepEqual(stagedManifest.files?.[relative], sourceManifest.files?.[relative], `staged manifest metadata drifted for ${relative}`);
  assert.deepEqual(stagedManifest.release_files?.[relative], sourceManifest.release_files?.[relative], `staged release metadata drifted for ${relative}`);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged runtime asset drifted for ${relative}`);
}

const adminPages = fs.readdirSync(path.join(source, 'admin')).filter((name) => name.endsWith('.html')).sort();
assert.ok(adminPages.length >= 39, 'new shell release must carry the complete built admin document set');
assert.deepEqual(fs.readdirSync(path.join(stage, 'admin')).filter((name) => name.endsWith('.html')).sort(), [...adminPages, 'tags.html'].sort(), 'staged admin documents differ from the built new shell or removed the existing private tags carrier');
for (const page of adminPages) {
  const relative = `admin/${page}`;
  assert.deepEqual(stagedManifest.release_files?.[relative], sourceManifest.release_files?.[relative], `staged release metadata drifted for ${relative}`);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged admin document drifted for ${relative}`);
  const html = fs.readFileSync(path.join(stage, relative), 'utf8');
  assert.ok(html.includes('data-ui-surface="admin"'), `staged ${relative} does not identify its UI surface`);
  assert.ok(html.includes(`<link rel="stylesheet" href="../${surfaceFeedbackStyles}">`), `staged ${relative} does not load the surface feedback stylesheet`);
  assert.ok(html.includes(`<script async src="../${surfaceFeedbackHost}"></script>`), `staged ${relative} does not load the surface feedback Host`);
  if (html.includes('class="side-user"')) assert.ok(html.includes(`src="../${sourceManifest.entries.adminSessionHost}"`), `staged ${relative} has no working session Host`);
  assert.ok(!html.includes('href="cycles.html"'), `staged ${relative} still routes its Operation Cycles menu to the retired document`);
  if (html.includes('运营闭环')) assert.ok(html.includes('href="/admin/operation-cycles"'), `staged ${relative} omitted the canonical Operation Cycles menu route`);
}

const openPlatformEntry = sourceManifest.entries?.openPlatformHost;
const frozenAdminEntry = sourceManifest.entries?.admin;
assert.equal(typeof openPlatformEntry, 'string', 'Open Platform Host entry is absent from the source manifest');
assert.equal(typeof frozenAdminEntry, 'string', 'frozen admin entry is absent from the source manifest');
const openPlatformHTML = fs.readFileSync(path.join(stage, 'admin', 'apidocs.html'), 'utf8');
assert.match(openPlatformHTML, new RegExp(`<script type=\"module\" src=\"\.\./${openPlatformEntry.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\"></script>`), 'Open Platform document does not load the V3 Host');
assert.ok(!openPlatformHTML.includes(`../${frozenAdminEntry}`), 'Open Platform document still starts the retired frozen API-document runtime');

assert.deepEqual(stagedManifest.release_files?.['sidebar/index.html'], sourceManifest.release_files?.['sidebar/index.html'], 'staged release metadata omits sidebar/index.html');
assert.ok(fs.readFileSync(path.join(stage, 'sidebar', 'index.html')).equals(fs.readFileSync(path.join(source, 'sidebar', 'index.html'))), 'staged sidebar document drifted');
const sidebarHost = sourceManifest.entries?.sidebarHost;
const weComJSSDK = 'https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js';
assert.equal(sourceManifest.files?.[sidebarHost]?.entry_point, 'web/v3/sidebar/main.ts', 'sidebar Host must use the V3 sidebar entry');
const sidebarOverlay = sourceManifest.entries?.sidebarStandardOverlay;
const sidebarImageResourceLoader = sourceManifest.entries?.sidebarImageResourceLoader;
const sidebarStandardStyles = sourceManifest.entries?.sidebarStandardStyles;
assert.equal(sourceManifest.files?.[sidebarHost]?.entry_point, 'web/v3/sidebar/main.ts', 'sidebar Host must be the V3 trusted bridge entry');
assert.equal(sourceManifest.files?.[sidebarOverlay]?.entry_point, 'web/dist/sidebar/sidebar_workbench_v3_overlay.js', 'sidebar release manifest must contain the generated dd8 overlay');
assert.equal(sourceManifest.files?.[sidebarImageResourceLoader]?.entry_point, 'web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js', 'sidebar release manifest must contain the audited standard image loader');
assert.equal(sourceManifest.files?.[sidebarImageResourceLoader]?.sha256, '38090abd86d19b7027841e7035bb8e8b12548487914a98a893fd71a5ec51187d', 'sidebar image loader must retain its audited dd8 bytes');
assert.equal(sourceManifest.files?.[sidebarStandardStyles]?.entry_point, 'internal/webshell/static/sidebar_workbench/sidebar_workbench.css', 'sidebar release manifest must contain the standard stylesheet');
const sidebarHTML = fs.readFileSync(path.join(stage, 'sidebar', 'index.html'), 'utf8');
const sidebarScripts = [...sidebarHTML.matchAll(/<script(?: async| type="module")? src="([^"]+)"><\/script>/g)].map((match) => match[1]);
assert.deepEqual(sidebarScripts, [`../${surfaceFeedbackHost}`, weComJSSDK, `../${sidebarImageResourceLoader}`, `../${sidebarHost}`], 'staged sidebar document must preserve feedback, JSSDK, standard image loader, and V3 Host order');
assert.ok(sidebarHTML.includes('data-ui-surface="sidebar"'), 'staged sidebar document does not identify its UI surface');
assert.ok(sidebarHTML.includes(`<link rel="stylesheet" href="../${surfaceFeedbackStyles}">`), 'staged sidebar document does not load surface feedback styles');
assert.ok(sidebarHTML.includes(`data-overlay-url="../${sidebarOverlay}"`), 'staged sidebar document must pass the hashed dd8 overlay only to the V3 Host');
assert.ok(sidebarHTML.includes(`<link rel="stylesheet" href="../${sidebarStandardStyles}">`), 'staged sidebar document must load the hashed standard stylesheet');
assert.ok(!sidebarHTML.includes('https://res.wx.qq.com/open/js/jweixin-1.6.0.js'), 'staged sidebar document still loads the generic JSSDK that blocks agentConfig');
assert.equal(stagedManifest.entries?.h5, sourceManifest.entries?.h5, 'previous Survey stage was removed');
assert.ok(fs.existsSync(path.join(stage, 'h5', 'index.html')), 'previous Survey public stage was removed');

// validate-release treats files and release_files as a single immutable
// closure. Walk the actual stage after all three staging steps so no copied
// hashed asset or private document can escape metadata (and no stale metadata
// can name a missing release file).
const stagedReleaseFiles = [];
const collectReleaseFiles = (directory) => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) collectReleaseFiles(absolute);
    else if (entry.isFile() && entry.name !== 'asset-manifest.json') stagedReleaseFiles.push(path.relative(stage, absolute).split(path.sep).join('/'));
  }
};
collectReleaseFiles(stage);
assert.deepEqual(Object.keys(stagedManifest.release_files || {}).sort(), stagedReleaseFiles.sort(), 'staged release_files must describe the complete recursive release closure');

// Execute the real installer's UI file checks against the actual release stage.
// This catches an installer retaining retired paths even when manifest hashes pass.
const installerSource = fs.readFileSync(path.join(repository, 'deploy/install-release.sh'), 'utf8');
const installerUIChecks = installerSource.match(/for ai_assistant_asset in[\s\S]*?done\nfor standard_component_asset in[\s\S]*?done/);
assert.ok(installerUIChecks, 'installer UI checks must include the unified components');
const verifyInstallerUI = (directory) => spawnSync('bash', ['-ec', 'release_dir="$1"; ' + installerUIChecks[0], 'installer-ui-check', directory], { encoding: 'utf8' });
const installerFixture = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-installer-ui-'));
try {
  fs.mkdirSync(path.join(installerFixture, 'web'), { recursive: true });
  fs.cpSync(stage, path.join(installerFixture, 'web/dist'), { recursive: true });
  const valid = verifyInstallerUI(installerFixture);
  assert.equal(valid.status, 0, `installer rejected the real UI release stage: ${valid.stderr}`);
  fs.rmSync(path.join(installerFixture, 'web/dist/assets/standard-components/group_chat_picker.js'));
  const missing = verifyInstallerUI(installerFixture);
  assert.notEqual(missing.status, 0, 'installer accepted a missing unified group picker');
  assert.match(missing.stderr, /missing standard component: group_chat_picker.js/);
} finally {
  fs.rmSync(installerFixture, { recursive: true, force: true });
}

// A build that loses one required hashed runtime asset must be rejected before
// the new shell step mutates its already-valid release stage.
const sandbox = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-stage-new-shell-'));
try {
  const fixtureSource = path.join(sandbox, 'source');
  const fixtureStage = path.join(sandbox, 'stage');
  fs.cpSync(source, fixtureSource, { recursive: true });
  execFileSync(process.execPath, [path.join(repository, 'scripts/stage-pr01-effects-ui.mjs'), fixtureSource, fixtureStage], { stdio: 'pipe' });
  execFileSync(process.execPath, [path.join(repository, 'scripts/stage-survey-ui.mjs'), fixtureSource, fixtureStage], { stdio: 'pipe' });
  const before = fs.readFileSync(path.join(fixtureStage, 'asset-manifest.json'));
  const requiredAssets = [
    ...[['customerHost', 'customer Host'], ['openPlatformHost', 'Open Platform Host'], ['sidebarStandardOverlay', 'sidebar standard overlay'], ['sidebarImageResourceLoader', 'sidebar standard image loader'], ['sidebarStandardStyles', 'sidebar standard stylesheet']].map(([entryKey, label]) => ({ relative: sourceManifest.entries?.[entryKey], label, entry: true })),
    ...['assets/standard-components/coupon_form.html', 'assets/standard-components/coupon_form_runtime.js', 'assets/standard-components/coupon_styles.html', 'assets/standard-components/channel_code_form.html', 'assets/standard-components/channel_admission_pages.js'].map((relative) => ({ relative, label: `passive standard asset ${relative}`, entry: false })),
  ];
  for (const { relative: missing, label, entry } of requiredAssets) {
    assert.equal(typeof missing, 'string', `${label} must be declared before staging`);
    if (entry) assert.ok(selected.has(missing), `${label} must be included in the staged recursive closure`);
    fs.rmSync(path.join(fixtureSource, missing));
    const rejected = spawnSync(process.execPath, [path.join(repository, 'scripts/stage-new-shell-ui.mjs'), fixtureSource, fixtureStage], { encoding: 'utf8' });
    assert.notEqual(rejected.status, 0, `new shell stage accepted a missing ${label} asset`);
    assert.match(`${rejected.stdout}\n${rejected.stderr}`, /expected source release file is absent/, `new shell stage did not report the missing ${label} artifact safely`);
    assert.ok(fs.readFileSync(path.join(fixtureStage, 'asset-manifest.json')).equals(before), `missing ${label} asset mutated the existing release stage`);
    fs.copyFileSync(path.join(source, missing), path.join(fixtureSource, missing));
  }
  const loaderPath = path.join(fixtureSource, sidebarImageResourceLoader);
  fs.appendFileSync(loaderPath, "\n// tampered fixture\n");
  const tampered = spawnSync(process.execPath, [path.join(repository, 'scripts/stage-new-shell-ui.mjs'), fixtureSource, fixtureStage], { encoding: 'utf8' });
  assert.notEqual(tampered.status, 0, 'new shell stage accepted a tampered sidebar image loader');
  assert.match(`${tampered.stdout}\n${tampered.stderr}`, /source release file differs from declared metadata/, 'new shell stage did not report the tampered image loader safely');
  assert.ok(fs.readFileSync(path.join(fixtureStage, 'asset-manifest.json')).equals(before), 'tampered sidebar image loader mutated the existing release stage');
} finally {
  fs.rmSync(sandbox, { recursive: true, force: true });
}

console.log('new shell release asset closure passed');
