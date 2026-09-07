#!/usr/bin/env node
import { build } from 'esbuild';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(repository, 'web', 'dist');
const manifestPath = path.join(dist, 'asset-manifest.json');
if (!fs.existsSync(manifestPath)) throw new Error('run the frozen donor build before v3 host adapters');

const entryPoints = {
  operationCyclesHost: path.join(repository, 'web', 'v3', 'operationCyclesAdapter.ts'),
  productHost: path.join(repository, 'web', 'v3', 'productAdapter.ts'),
  surveyOperationsHost: path.join(repository, 'web', 'v3', 'surveyOperationsAdapter.ts'),
  channelCenterHost: path.join(repository, 'web', 'v3', 'channelCenterAdapter.ts'),
  aiAssistantHost: path.join(repository, 'web', 'v3', 'aiAssistantAdapter.ts'),
  // Customer pages retain their frozen templates and generated V2 client; this
  // adapter is injected before that client to map only its safe read DTOs.
  customerHost: path.join(repository, 'web', 'v3', 'customerAdapter.ts'),
  // The frozen sidebar template and stylesheet remain byte-exact. Its live
  // protocol adapter is V3-owned because the current Sidebar Owner exposes
  // narrower trusted DTOs than the donor-generated client.
  sidebarHost: path.join(repository, 'web', 'v3', 'sidebar', 'main.ts'),
  // The Open Platform catalog and caller lifecycle are V3-owned. The frozen
  // document only provides the authenticated admin shell around this Host.
  openPlatformHost: path.join(repository, 'web', 'v3', 'openPlatformAdapter.ts'),
};
const result = await build({
  entryPoints,
  bundle: true,
  format: 'esm',
  splitting: true,
  target: 'es2020',
  outdir: path.join(dist, 'assets'),
  entryNames: '[name]-[hash]',
  chunkNames: 'chunks/[name]-[hash]',
  assetNames: 'files/[name]-[hash]',
  minify: true,
  metafile: true,
  logLevel: 'warning',
});

const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
const normalizeOutput = (output) => path.relative(dist, path.resolve(repository, output)).split(path.sep).join('/');
const metadataFor = (contents) => ({
  bytes: contents.byteLength,
  gzip_bytes: gzipSync(contents, { level: 9 }).byteLength,
  sha256: crypto.createHash('sha256').update(contents).digest('hex'),
});
const entries = new Map();
for (const [output, metadata] of Object.entries(result.metafile.outputs)) {
  const relative = normalizeOutput(output);
  const contents = fs.readFileSync(path.join(dist, relative));
  const imports = metadata.imports.map((item) => {
    const absolute = path.isAbsolute(item.path)
      ? item.path
      : item.path.startsWith('web/dist/')
        ? path.resolve(repository, item.path)
        : path.resolve(path.dirname(path.resolve(repository, output)), item.path);
    return { path: path.relative(dist, absolute).split(path.sep).join('/'), kind: item.kind };
  });
  manifest.files[relative] = {
    ...metadataFor(contents),
    entry_point: metadata.entryPoint ? path.relative(repository, path.resolve(repository, metadata.entryPoint)).split(path.sep).join('/') : undefined,
    imports,
    inputs: Object.keys(metadata.inputs).map((input) => path.relative(repository, path.resolve(repository, input)).split(path.sep).join('/')).sort(),
  };
  manifest.release_files[relative] = metadataFor(contents);
  if (metadata.entryPoint) {
    const absoluteEntry = path.resolve(repository, metadata.entryPoint);
    for (const [name, source] of Object.entries(entryPoints)) {
      if (absoluteEntry === source) entries.set(name, relative);
    }
  }
}
for (const name of Object.keys(entryPoints)) {
  const entry = entries.get(name);
  if (!entry) throw new Error(`${name} adapter entry was not emitted`);
  manifest.entries[name] = entry;
  if (name === 'aiAssistantHost' || name === 'sidebarHost' || name === 'customerHost' || name === 'openPlatformHost') continue;
  const donorMain = manifest.files[entry].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/main.ts'))?.path;
  const donorLegacy = donorMain && manifest.files[donorMain].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/legacy.ts'))?.path;
  if (!donorMain || !donorLegacy) throw new Error(`${name} must start the frozen donor main -> legacy runtime`);
}

const customerHost = manifest.entries.customerHost;
const frozenAdmin = manifest.entries.admin;
if (typeof customerHost !== 'string' || typeof frozenAdmin !== 'string') throw new Error('customer Host or frozen admin entry is absent from manifest');
const customerHostReference = `../${customerHost}`;
const frozenAdminReference = `<script type="module" src="../${frozenAdmin}"></script>`;
for (const documentName of ['customers.html', 'customerDetail.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(customerHostReference)) throw new Error(`${documentName} already contains the customer Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `<script type="module" src="${customerHostReference}"></script>\n${frozenAdminReference}`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const surveyOperationsHost = manifest.entries.surveyOperationsHost;
if (typeof surveyOperationsHost !== 'string') throw new Error('Survey Operations Host entry is absent from manifest');
const surveyOperationsReference = `../${surveyOperationsHost}`;
const surveyOperationsDocument = path.join(dist, 'admin', 'questionnaireOps.html');
let surveyOperationsHTML = fs.readFileSync(surveyOperationsDocument, 'utf8');
if (!surveyOperationsHTML.includes(frozenAdminReference)) throw new Error('questionnaireOps.html does not reference the declared frozen admin entry');
if (surveyOperationsHTML.includes(surveyOperationsReference)) throw new Error('questionnaireOps.html already contains the Survey Operations Host');
surveyOperationsHTML = surveyOperationsHTML.replace(frozenAdminReference, `<script type="module" src="${surveyOperationsReference}"></script>\n${frozenAdminReference}`);
fs.writeFileSync(surveyOperationsDocument, surveyOperationsHTML);
manifest.release_files['admin/questionnaireOps.html'] = metadataFor(Buffer.from(surveyOperationsHTML));

const openPlatformHost = manifest.entries.openPlatformHost;
if (typeof openPlatformHost !== 'string') throw new Error('Open Platform Host entry is absent from manifest');
const openPlatformReference = `../${openPlatformHost}`;
const openPlatformDocument = path.join(dist, 'admin', 'apidocs.html');
let openPlatformHTML = fs.readFileSync(openPlatformDocument, 'utf8');
if (!openPlatformHTML.includes(frozenAdminReference)) throw new Error('apidocs.html does not reference the declared frozen admin entry');
if (openPlatformHTML.includes(openPlatformReference)) throw new Error('apidocs.html already contains the Open Platform Host');
// This page formerly mounted the retired 56-route document. Keep its frozen
// static shell but replace that runtime with the V3 Host, so the legacy module
// cannot race the Host or render an obsolete API catalog before access control
// data arrives.
openPlatformHTML = openPlatformHTML.replace(frozenAdminReference, `<script type="module" src="${openPlatformReference}"></script>`);
fs.writeFileSync(openPlatformDocument, openPlatformHTML);
manifest.release_files['admin/apidocs.html'] = metadataFor(Buffer.from(openPlatformHTML));

// The frozen shell keeps its navigation markup byte-for-byte in the donor
// source. Adapt its generated release documents instead: Operation Cycles is
// V3-hosted at the canonical route, while /admin/cycles.html intentionally
// remains an unavailable retired document in the Composition Root.
const operationCyclesHref = '/admin/operation-cycles';
const adminOutput = path.join(dist, 'admin');
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) {
  const documentPath = path.join(adminOutput, documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes('href="cycles.html"')) continue;
  documentHTML = documentHTML.replaceAll('href="cycles.html"', `href="${operationCyclesHref}"`);
  if (documentHTML.includes('href="cycles.html"') || !documentHTML.includes(`href="${operationCyclesHref}"`)) throw new Error(`${documentName} did not receive the canonical Operation Cycles navigation link`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const sidebarHost = manifest.entries.sidebarHost;
const frozenSidebar = manifest.entries.sidebar;
const weComJSSDK = 'https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js';
if (typeof sidebarHost !== 'string' || typeof frozenSidebar !== 'string') throw new Error('sidebar Host or frozen entry is absent from manifest');
if (manifest.files[sidebarHost]?.entry_point !== 'web/v3/sidebar/main.ts' || !manifest.files[sidebarHost]?.inputs?.includes('web/v3/sidebarApi.ts')) throw new Error('sidebar Host must be the V3 sidebar entry and adapter closure');
const sidebarDocument = path.join(dist, 'sidebar', 'index.html');
let sidebarHTML = fs.readFileSync(sidebarDocument, 'utf8');
const frozenSidebarReference = `../${frozenSidebar}`;
const frozenSidebarScript = `<script type="module" src="${frozenSidebarReference}"></script>`;
const sidebarHostScript = `<script type="module" src="../${sidebarHost}"></script>`;
if (!sidebarHTML.includes(frozenSidebarScript)) throw new Error('frozen sidebar document does not reference its declared entry');
if (sidebarHTML.includes('https://res.wx.qq.com/open/js/jweixin-1.6.0.js')) throw new Error('sidebar Host must not load the generic JSSDK before the WeCom JSSDK');
sidebarHTML = sidebarHTML.replace(frozenSidebarScript, `<script src="${weComJSSDK}"></script>\n${sidebarHostScript}`);
if (!sidebarHTML.includes(weComJSSDK) || !sidebarHTML.includes(sidebarHostScript) || sidebarHTML.indexOf(weComJSSDK) > sidebarHTML.indexOf(sidebarHostScript)) throw new Error('sidebar document did not load the WeCom JSSDK before the V3 Host');
fs.writeFileSync(sidebarDocument, sidebarHTML);
const sidebarBytes = Buffer.from(sidebarHTML);
manifest.release_files['sidebar/index.html'] = metadataFor(sidebarBytes);

const donor = path.join(repository, 'web', 'donors', 'ai-assistant-production');
const donorOut = path.join(dist, 'aiassistant');
fs.mkdirSync(donorOut, { recursive: true });
const donorAssets = ['group_chat_picker.css','group_chat_picker.js','material_picker.css','material_picker.js','send_content_composer.css','send_content_composer.js','send_content_readonly_detail.css','send_content_readonly_detail.js','cloud_plan_review.js'];
for (const name of donorAssets) {
  const contents = fs.readFileSync(path.join(donor, 'static', name));
  const relative = `aiassistant/${name}`;
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadataFor(contents), inputs: [`web/donors/ai-assistant-production/static/${name}`], imports: [] };
  manifest.release_files[relative] = metadataFor(contents);
}
const template = fs.readFileSync(path.join(donor, 'templates', 'cloud_plan_review.html'), 'utf8');
const style = (template.match(/\{% block head_extra %\}[\s\S]*?(<style>[\s\S]*?<\/style>)[\s\S]*?\{% endblock %\}/) || [])[1];
const content = (template.match(/\{% block content %\}([\s\S]*?)\{% endblock %\}/) || [])[1];
if (!style || !content) throw new Error('AI Assistant donor template blocks missing');
const conditional = /\{% if page_mode == "list" %\}([\s\S]*?)\{% else %\}([\s\S]*?)\{% endif %\}/;
for (const [mode, index] of [['list',1],['detail',2]]) {
  let fragment = content.replace(conditional, (_all, list, detail) => index === 1 ? list : detail)
    .replaceAll('{{ page_mode }}', mode).replaceAll('{{ plan_id }}', mode === 'detail' ? '__PLAN_ID__' : '').replaceAll('{{ admin_action_token }}', '');
  fragment = `${style}\n${fragment.trim()}\n`;
  const relative = `aiassistant/${mode}.html`; const bytes = Buffer.from(fragment);
  fs.writeFileSync(path.join(dist, relative), bytes); manifest.files[relative] = { ...metadataFor(bytes), inputs: ['web/donors/ai-assistant-production/templates/cloud_plan_review.html'], imports: [] }; manifest.release_files[relative] = metadataFor(bytes);
}
manifest.entries = Object.fromEntries(Object.entries(manifest.entries).sort(([left], [right]) => left.localeCompare(right)));
manifest.files = Object.fromEntries(Object.entries(manifest.files).sort(([left], [right]) => left.localeCompare(right)));
manifest.release_files = Object.fromEntries(Object.entries(manifest.release_files).sort(([left], [right]) => left.localeCompare(right)));
fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`built v3 host adapters: ${[...entries.values()].join(', ')}`);
