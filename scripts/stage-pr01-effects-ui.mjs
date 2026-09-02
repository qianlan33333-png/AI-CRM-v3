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
const roots = ['admin', 'tokens', 'labs'].map((name) => manifest.entries?.[name]);
if (roots.some((entry) => typeof entry !== 'string')) fail('campaigns entry assets are absent from manifest');
const outputForInput = (input) => Object.entries(manifest.files || {}).find(([, file]) => file.inputs?.includes(input))?.[0];
const legacyEntry = outputForInput('web/src/admin/legacy.ts');
const campaignsEntry = outputForInput('web/src/admin/sections/campaigns.ts');
if (!legacyEntry || !campaignsEntry) fail('campaign runtime chunks are absent from manifest');

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
// release keeps the loader, its static dependencies, and only the campaigns
// chunk selected by the forced external-effects query; no other page chunk is
// staged or therefore fetchable. HTML stays v3-owned: the Go webshell renders
// the single admin shell and mounts the frozen stage, so no donor HTML is ever
// packaged.
for (const root of roots) includeStatic(root);
includeStatic(legacyEntry);
includeStatic(campaignsEntry);

for (const relative of [...selected].sort()) copy(relative);

const stagedManifest = {
  ...manifest,
  entries: Object.fromEntries(['admin', 'tokens', 'labs'].map((name) => [name, manifest.entries[name]])),
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
  const allowed = relative === 'asset-manifest.json' || relative.startsWith('assets/');
  if (!allowed) fail(`unapproved release file: ${relative}`);
  if (relative.endsWith('.html')) fail(`unapproved HTML surface: ${relative}`);
  if (/(^|\/)(h5|sidebar|public)(\/|$)/.test(relative)) fail(`unapproved public surface: ${relative}`);
}
for (const relative of selected) {
  if (!stagedFiles.includes(relative)) fail(`missing staged dependency: ${relative}`);
}
console.log(`staged ${selected.size} frozen External Effects assets in ${stage}`);
