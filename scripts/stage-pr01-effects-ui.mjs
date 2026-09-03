#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const manifestPath = path.join(source, 'asset-manifest.json');

const fail = (message) => {
  console.error(`PR01 effects UI staging: ${message}`);
  process.exit(1);
};
const copy = (relative) => {
  const from = path.join(source, relative);
  const to = path.join(stage, relative);
  if (!fs.statSync(from).isFile()) fail(`expected file is absent: ${relative}`);
  fs.mkdirSync(path.dirname(to), { recursive: true });
  fs.copyFileSync(from, to);
};

if (!fs.statSync(source).isDirectory()) fail(`missing build directory: ${source}`);
if (fs.existsSync(stage)) fail(`refusing to overwrite an existing stage: ${stage}`);
if (!fs.statSync(manifestPath).isFile()) fail('missing asset-manifest.json');

const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
const roots = ['admin', 'tokens', 'labs', 'operationCyclesHost'].map((name) => manifest.entries?.[name]);
if (roots.some((entry) => typeof entry !== 'string')) fail('campaigns entry assets are absent from manifest');
const dynamicOutputForInput = (from, input) => (manifest.files[from]?.imports || []).find((item) =>
  item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes(input)
)?.path;
const legacyEntry = dynamicOutputForInput(manifest.entries.admin, 'web/src/admin/legacy.ts');
const campaignsEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/campaigns.ts');
const adminAccessEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/adminAccess.ts');
const setupWizardEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/setupWizard.ts');
const groupOpsHistoryEntry = legacyEntry && dynamicOutputForInput(legacyEntry, 'web/src/admin/sections/groupOpsHistory.ts');
const operationHost = manifest.entries.operationCyclesHost;
const operationMainEntry = dynamicOutputForInput(operationHost, 'web/src/admin/main.ts');
const operationLegacyEntry = operationMainEntry && dynamicOutputForInput(operationMainEntry, 'web/src/admin/legacy.ts');
if (!legacyEntry || !campaignsEntry || !adminAccessEntry || !setupWizardEntry || !groupOpsHistoryEntry || !operationMainEntry || !operationLegacyEntry) fail('required frozen admin runtime chunks are absent from manifest');

const selected = new Set();
const includeStatic = (relative) => {
	if (selected.has(relative)) return;
	const file = manifest.files?.[relative];
	if (!file) fail(`manifest lacks dependency metadata for ${relative}`);
	selected.add(relative);
	for (const imported of file.imports || []) {
		if (imported.kind !== 'dynamic-import') includeStatic(imported.path);
	}
};
// V2's loader contains dormant dynamic imports for every legacy page. The
// release keeps the loader, its static dependencies, the campaigns chunk
// selected by the External Effects workspace, the Admin Access and Setup
// Wizard chunks selected by the Config host, and the Group Ops history chunk
// selected by the byte-frozen groupops.html?history=1 route. No other legacy
// page chunk is staged or fetchable. HTML stays v3-owned: the Go webshell
// renders the single admin shell and mounts the frozen stage, so no donor HTML
// is ever packaged.
for (const root of roots) includeStatic(root);
includeStatic(legacyEntry);
includeStatic(campaignsEntry);
includeStatic(adminAccessEntry);
includeStatic(setupWizardEntry);
includeStatic(groupOpsHistoryEntry);
includeStatic(operationMainEntry);
includeStatic(operationLegacyEntry);

for (const relative of [...selected].sort()) copy(relative);
// Media uses the same immutable admin bundle, mounted by the v3 shell. These
// templates are release-private inputs to the Go adapter, never HTTP-served
// donor pages; keeping exactly these three preserves the PR01 asset closure.
for (const page of ['images', 'attach', 'mpLib']) copy(`admin/${page}.html`);
// Product pages are byte-frozen private template carriers mounted by the v3
// admin shell; their donor documents are never independently routed.
for (const page of ['products', 'productForm', 'spProducts', 'spProductForm']) copy(`admin/${page}.html`);
// Coupon pages remain byte-frozen private template carriers in the v3 shell.
for (const page of ['coupons', 'couponForm']) copy(`admin/${page}.html`);
// Group Ops pages remain byte-frozen private template carriers in the v3
// shell. They are never routed as donor documents; the Go binding extracts
// only template#tpl into PR10's authenticated shell.
for (const page of ['groupops', 'groupopsDetail']) copy(`admin/${page}.html`);
// PR07 Agent pages are byte-frozen private template carriers. They are mounted
// by the existing v3 shell and must never become a second public donor shell.
for (const page of ['agents', 'agentEdit']) copy(`admin/${page}.html`);
// PR08 Operation Cycle pages are byte-frozen private template carriers.
for (const page of ['cycles', 'cyclesDetail']) copy(`admin/${page}.html`);
// Config pages are release-private template carriers for the v3 host adapter.
for (const page of ['config', 'configDetail', 'apidocs']) copy(`admin/${page}.html`);
// Tags also runs through the frozen donor admin entry. Keep the generated
// donor page only as a release-private template source under a non-routable
// filename; the Go adapter extracts template#tpl and mounts it in PR10's sole
// admin shell. The donor page's .shell/.side wrapper is therefore never sent.
const tagsSource = path.join(source, 'admin', 'wecom-tags.html');
const tagsTarget = path.join(stage, 'admin', 'tags.html');
if (!fs.statSync(tagsSource).isFile()) fail('expected file is absent: admin/wecom-tags.html');
fs.mkdirSync(path.dirname(tagsTarget), { recursive: true });
fs.copyFileSync(tagsSource, tagsTarget);

const stagedManifest = {
  ...manifest,
  entries: Object.fromEntries(['admin', 'tokens', 'labs', 'operationCyclesHost'].map((name) => [name, manifest.entries[name]])),
  files: Object.fromEntries([...selected].sort().map((relative) => [relative, manifest.files[relative]])),
};
fs.writeFileSync(path.join(stage, 'asset-manifest.json'), `${JSON.stringify(stagedManifest, null, 2)}\n`);

const stagedFiles = [];
const walk = (directory) => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) walk(absolute);
    else stagedFiles.push(path.relative(stage, absolute).split(path.sep).join('/'));
  }
};
walk(stage);
for (const relative of stagedFiles) {
  const allowedHTML = ['admin/images.html', 'admin/attach.html', 'admin/mpLib.html', 'admin/tags.html', 'admin/products.html', 'admin/productForm.html', 'admin/spProducts.html', 'admin/spProductForm.html', 'admin/coupons.html', 'admin/couponForm.html', 'admin/groupops.html', 'admin/groupopsDetail.html', 'admin/agents.html', 'admin/agentEdit.html', 'admin/cycles.html', 'admin/cyclesDetail.html', 'admin/config.html', 'admin/configDetail.html', 'admin/apidocs.html'];
  const allowed = relative === 'asset-manifest.json' || relative.startsWith('assets/') || allowedHTML.includes(relative);
  if (!allowed) fail(`unapproved release file: ${relative}`);
  if (relative.endsWith('.html') && !allowedHTML.includes(relative)) fail(`unapproved HTML surface: ${relative}`);
  if (/(^|\/)(h5|sidebar|public)(\/|$)/.test(relative)) fail(`unapproved public surface: ${relative}`);
}
for (const relative of selected) {
  if (!stagedFiles.includes(relative)) fail(`missing staged dependency: ${relative}`);
}
console.log(`staged ${selected.size} frozen admin assets and private Media, Tags, Product, Coupon, Group Ops, Automation, Operation Cycle, and Config templates in ${stage}`);
