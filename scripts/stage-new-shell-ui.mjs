#!/usr/bin/env node
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

const [sourceArg = 'web/dist', stageArg = 'release/web/dist'] = process.argv.slice(2);
const source = path.resolve(sourceArg);
const stage = path.resolve(stageArg);

const fail = (message) => {
  console.error(`new shell UI staging: ${message}`);
  process.exit(1);
};
const readJSON = (filename) => JSON.parse(fs.readFileSync(filename, 'utf8'));
const sourceManifestPath = path.join(source, 'asset-manifest.json');
const stagedManifestPath = path.join(stage, 'asset-manifest.json');
if (!fs.existsSync(sourceManifestPath) || !fs.statSync(sourceManifestPath).isFile()) fail('source asset-manifest.json is absent');
if (!fs.existsSync(stage) || !fs.statSync(stage).isDirectory()) fail('existing release stage is absent');
if (!fs.existsSync(stagedManifestPath) || !fs.statSync(stagedManifestPath).isFile()) fail('existing release manifest is absent');

const sourceManifest = readJSON(sourceManifestPath);
const stagedManifest = readJSON(stagedManifestPath);
const entryKeys = [
  'admin', 'adminSessionHost', 'standardComponentsHost', 'standardComponentsStableHost', 'tokens', 'labs',
  'operationCyclesHost', 'productHost', 'channelCenterHost', 'aiAssistantHost',
  'customerHost', 'sidebarHost', 'sidebarStandardOverlay', 'sidebarImageResourceLoader', 'sidebarStandardStyles', 'openPlatformHost', 'sidebarStyles', 'groupopsHost', 'groupopsStyles',
];
const selected = new Set();
const includeClosure = (relative) => {
  if (selected.has(relative)) return;
  const metadata = sourceManifest.files?.[relative];
  if (!metadata) fail(`source manifest lacks dependency metadata for ${relative}`);
  selected.add(relative);
  for (const imported of metadata.imports || []) includeClosure(imported.path);
};
for (const key of entryKeys) {
  const entry = sourceManifest.entries?.[key];
  if (typeof entry !== 'string') fail(`required new-shell entry is absent: ${key}`);
  includeClosure(entry);
}

const sourceAdmin = path.join(source, 'admin');
if (!fs.existsSync(sourceAdmin) || !fs.statSync(sourceAdmin).isDirectory()) fail('built admin document directory is absent');
const adminPages = fs.readdirSync(sourceAdmin, { withFileTypes: true })
  .filter((entry) => entry.isFile() && entry.name.endsWith('.html'))
  .map((entry) => `admin/${entry.name}`)
  .sort();
if (adminPages.length === 0) fail('built admin document set is empty');
const groupOpsSupport = ['assets/standard-components/operation_member_picker.js', 'assets/standard-components/group_chat_picker.css', 'assets/standard-components/group_chat_picker.js', 'assets/standard-components/material_picker.css', 'assets/standard-components/material_picker.js', 'assets/standard-components/send_content_composer.css', 'assets/standard-components/send_content_composer.js', 'assets/standard-components/wecom_tag_picker.css', 'assets/standard-components/wecom_tag_picker.js', 'aiassistant/send_content_readonly_detail.css', 'aiassistant/send_content_readonly_detail.js'];
const documents = [...adminPages, 'sidebar/index.html'];

const sourceFile = (relative) => path.join(source, relative);
const copyUnchanged = (relative) => {
  const from = sourceFile(relative);
  if (!fs.existsSync(from) || !fs.statSync(from).isFile()) fail(`expected source release file is absent: ${relative}`);
  const to = path.join(stage, relative);
  fs.mkdirSync(path.dirname(to), { recursive: true });
  if (fs.existsSync(to) && !fs.readFileSync(to).equals(fs.readFileSync(from))) {
    fail(`refusing to replace a different staged file: ${relative}`);
  }
  fs.copyFileSync(from, to);
};
const releaseMetadata = (relative) => {
  const metadata = sourceManifest.release_files?.[relative];
  if (!metadata) fail(`source release manifest lacks metadata for ${relative}`);
  return metadata;
};

// Verify every source-side asset and document before changing the staged tree,
// so a partial/missing build cannot leave a release candidate that appears
// usable.  This stages the actual dependency closure, including dynamic
// imports, rather than relying on a local web/dist directory at runtime.
for (const relative of [...selected, ...groupOpsSupport, ...documents]) {
  const sourcePath = sourceFile(relative);
  if (!fs.existsSync(sourcePath) || !fs.statSync(sourcePath).isFile()) fail(`expected source release file is absent: ${relative}`);
  const expected = releaseMetadata(relative);
  const contents = fs.readFileSync(sourcePath);
  const actualSHA256 = crypto.createHash("sha256").update(contents).digest("hex");
  if (expected.bytes !== contents.byteLength || expected.sha256 !== actualSHA256) fail(`source release file differs from declared metadata: ${relative}`);
}
for (const relative of [...selected, ...groupOpsSupport].sort()) copyUnchanged(relative);
for (const relative of documents) copyUnchanged(relative);

stagedManifest.entries ||= {};
stagedManifest.files ||= {};
stagedManifest.release_files ||= {};
for (const key of entryKeys) stagedManifest.entries[key] = sourceManifest.entries[key];
for (const relative of [...selected, ...groupOpsSupport]) {
  stagedManifest.files[relative] = sourceManifest.files[relative];
  // validate-release treats every staged asset as a release file as well as a
  // fetchable manifest file. Keep both metadata maps from the same source
  // record; omitting release_files here leaves a package that cannot pass its
  // byte/hash closure after the new shell resources are copied.
  stagedManifest.release_files[relative] = sourceManifest.release_files[relative];
}
for (const relative of documents) stagedManifest.release_files[relative] = sourceManifest.release_files[relative];
fs.writeFileSync(stagedManifestPath, `${JSON.stringify(stagedManifest, null, 2)}\n`);

console.log(`staged new shell UI: ${adminPages.length} private admin documents, sidebar/index.html, ${selected.size} recursive runtime assets`);
