#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const repository = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const readManifest = (root) => JSON.parse(fs.readFileSync(path.join(root, 'asset-manifest.json'), 'utf8'));
const sourceManifest = readManifest(source);
const stagedManifest = readManifest(stage);
const entryKeys = [
  'admin', 'tokens', 'labs',
  'operationCyclesHost', 'productHost', 'channelCenterHost', 'aiAssistantHost',
  'customerHost', 'sidebarHost', 'sidebarStyles',
];
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
for (const relative of selected) {
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
}
assert.deepEqual(stagedManifest.release_files?.['sidebar/index.html'], sourceManifest.release_files?.['sidebar/index.html'], 'staged release metadata omits sidebar/index.html');
assert.ok(fs.readFileSync(path.join(stage, 'sidebar', 'index.html')).equals(fs.readFileSync(path.join(source, 'sidebar', 'index.html'))), 'staged sidebar document drifted');
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
  const missing = sourceManifest.entries?.customerHost;
  assert.equal(typeof missing, 'string', 'customer Host entry must be declared before staging');
  assert.ok(selected.has(missing), 'customer Host must be included in the staged recursive closure');
  fs.rmSync(path.join(fixtureSource, missing));
  const rejected = spawnSync(process.execPath, [path.join(repository, 'scripts/stage-new-shell-ui.mjs'), fixtureSource, fixtureStage], { encoding: 'utf8' });
  assert.notEqual(rejected.status, 0, 'new shell stage accepted a missing customer Host asset');
  assert.match(`${rejected.stdout}\n${rejected.stderr}`, /expected source release file is absent/, 'new shell stage did not report the missing artifact safely');
  assert.ok(fs.readFileSync(path.join(fixtureStage, 'asset-manifest.json')).equals(before), 'missing source asset mutated the existing release stage');
} finally {
  fs.rmSync(sandbox, { recursive: true, force: true });
}

console.log('new shell release asset closure passed');
