#!/usr/bin/env node
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);
const readManifest = (root) => JSON.parse(fs.readFileSync(path.join(root, 'asset-manifest.json'), 'utf8'));
const sourceManifest = readManifest(source);
const stagedManifest = readManifest(stage);

const requiredEntries = ['h5', 'questionnaireEditor', 'questionnaireEditorStyles'];
for (const key of requiredEntries) {
  assert.equal(stagedManifest.entries?.[key], sourceManifest.entries?.[key], `staged manifest omits Survey entry ${key}`);
}

const required = new Set();
const includeStatic = (relative) => {
  if (required.has(relative)) return;
  required.add(relative);
  for (const imported of sourceManifest.files?.[relative]?.imports || []) {
    if (imported.kind !== 'dynamic-import') includeStatic(imported.path);
  }
};
for (const key of requiredEntries) includeStatic(sourceManifest.entries[key]);
for (const relative of required) {
  assert.deepEqual(stagedManifest.files?.[relative], sourceManifest.files?.[relative], `staged manifest metadata drifted for ${relative}`);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged asset drifted for ${relative}`);
}

const adminPages = ['questionnaires.html', 'questionnaireDetail.html', 'questionnaireDetail.fragment.html', 'questionnaireOps.html'];
for (const page of adminPages) {
  const relative = path.join('admin', page);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged private template drifted for ${relative}`);
}

const expectedH5 = ['active.html', 'all.html', 'auth.html', 'done.html', 'error.html', 'expired.html', 'index.html', 'loading.html', 'one.html', 'pay.html', 'qr.html', 'result.html', 'signup.html'];
assert.deepEqual(fs.readdirSync(path.join(stage, 'h5')).sort(), expectedH5, 'release contains a missing or unapproved Survey H5 page');
for (const page of expectedH5) {
  const relative = path.join('h5', page);
  assert.ok(fs.readFileSync(path.join(stage, relative)).equals(fs.readFileSync(path.join(source, relative))), `staged H5 page drifted for ${relative}`);
}
assert.equal(stagedManifest.entries?.sidebar, undefined, 'Survey stage exposed the donor sidebar entry');
assert.equal(stagedManifest.entries?.memberGridShare, undefined, 'Survey stage exposed an unrelated public entry');

console.log('Survey release asset closure passed');
