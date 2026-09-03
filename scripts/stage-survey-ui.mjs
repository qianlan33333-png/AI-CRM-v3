#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);

const fail = (message) => {
  console.error(`Survey UI staging: ${message}`);
  process.exit(1);
};
const readJSON = (filename) => JSON.parse(fs.readFileSync(filename, 'utf8'));
const sourceManifest = readJSON(path.join(source, 'asset-manifest.json'));
const stagedManifestPath = path.join(stage, 'asset-manifest.json');
const stagedManifest = readJSON(stagedManifestPath);
const copy = (relative) => {
  const from = path.join(source, relative);
  const to = path.join(stage, relative);
  if (!fs.existsSync(from) || !fs.statSync(from).isFile()) fail(`expected file is absent: ${relative}`);
  fs.mkdirSync(path.dirname(to), { recursive: true });
  if (fs.existsSync(to) && !fs.readFileSync(to).equals(fs.readFileSync(from))) {
    fail(`refusing to replace a different staged file: ${relative}`);
  }
  fs.copyFileSync(from, to);
};

if (!fs.existsSync(stage) || !fs.statSync(stage).isDirectory()) fail(`missing existing release stage: ${stage}`);

const entryKeys = ['h5', 'questionnaireEditor', 'questionnaireEditorStyles'];
const selected = new Set();
const includeStatic = (relative) => {
  if (selected.has(relative)) return;
  const file = sourceManifest.files?.[relative];
  if (!file) fail(`manifest lacks dependency metadata for ${relative}`);
  selected.add(relative);
  for (const imported of file.imports || []) {
    if (imported.kind !== 'dynamic-import') includeStatic(imported.path);
  }
};
for (const key of entryKeys) {
  const entry = sourceManifest.entries?.[key];
  if (typeof entry !== 'string') fail(`missing manifest entry: ${key}`);
  includeStatic(entry);
}

// Survey sharing is loaded with import('./sections/qr') from the shared admin
// controller.  esbuild therefore emits it outside the static entry closure.
// Locate that one source-owned dynamic chunk explicitly instead of copying all
// donor dynamic imports into this release slice.
const qrChunks = Object.entries(sourceManifest.files || {})
  .filter(([, metadata]) => (metadata.inputs || []).includes('web/src/admin/sections/qr.ts'))
  .map(([relative]) => relative);
if (qrChunks.length !== 1) fail(`expected exactly one Survey QR chunk, found ${qrChunks.length}`);
includeStatic(qrChunks[0]);
for (const relative of [...selected].sort()) copy(relative);

const adminPages = ['questionnaires.html', 'questionnaireDetail.html', 'questionnaireOps.html'];
for (const page of adminPages) copy(path.join('admin', page));

const h5Pages = ['active.html', 'all.html', 'auth.html', 'done.html', 'error.html', 'expired.html', 'index.html', 'loading.html', 'one.html', 'pay.html', 'qr.html', 'result.html', 'signup.html'];
const builtH5Pages = fs.readdirSync(path.join(source, 'h5')).filter((name) => name.endsWith('.html')).sort();
if (JSON.stringify(builtH5Pages) !== JSON.stringify(h5Pages)) fail('built H5 page allowlist drifted');
for (const page of h5Pages) copy(path.join('h5', page));

for (const key of entryKeys) stagedManifest.entries[key] = sourceManifest.entries[key];
for (const relative of selected) stagedManifest.files[relative] = sourceManifest.files[relative];
fs.writeFileSync(stagedManifestPath, `${JSON.stringify(stagedManifest, null, 2)}\n`);

console.log(`staged Survey UI: ${adminPages.length} private admin templates, ${h5Pages.length} public H5 pages, ${selected.size} asset files`);
