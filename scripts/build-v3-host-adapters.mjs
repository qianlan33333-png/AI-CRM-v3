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
// The dd8 renderer is transformed only through its audited overlay generator.
// It must run after the frozen build (which recreates web/dist) and before
// esbuild fingerprints the overlay as a release asset.
const overlayBuild = await import('./build-sidebar-standard-overlay.mjs');
void overlayBuild;

const entryPoints = {
  adminSessionHost: path.join(repository, 'web', 'v3', 'adminSessionHost.ts'),
  standardComponentsHost: path.join(repository, 'web', 'v3', 'standardComponentsHost.ts'),
  operationCyclesHost: path.join(repository, 'web', 'v3', 'operationCyclesAdapter.ts'),
  productHost: path.join(repository, 'web', 'v3', 'productAdapter.ts'),
  channelCenterHost: path.join(repository, 'web', 'v3', 'channelCenterAdapter.ts'),
  aiAssistantHost: path.join(repository, 'web', 'v3', 'aiAssistantAdapter.ts'),
  // Customer pages retain their frozen templates and generated V2 client; this
  // adapter is injected before that client to map only its safe read DTOs.
  customerHost: path.join(repository, 'web', 'v3', 'customerAdapter.ts'),
  // The frozen sidebar template and stylesheet remain byte-exact. Its live
  // protocol adapter is V3-owned because the current Sidebar Owner exposes
  // narrower trusted DTOs than the donor-generated client.
  sidebarHost: path.join(repository, 'web', 'v3', 'sidebar', 'main.ts'),
  sidebarStandardOverlay: path.join(repository, 'web', 'dist', 'sidebar', 'sidebar_workbench_v3_overlay.js'),
  sidebarStandardStyles: path.join(repository, 'internal', 'webshell', 'static', 'sidebar_workbench', 'sidebar_workbench.css'),
  // The Open Platform catalog and caller lifecycle are V3-owned. The frozen
  // document only provides the authenticated admin shell around this Host.
  openPlatformHost: path.join(repository, 'web', 'v3', 'openPlatformAdapter.ts'),
  groupopsHost: path.join(repository, 'web', 'v3', 'groupOpsHostAdapter.ts'),
  groupopsStyles: path.join(repository, 'web', 'v3', 'groupOpsStandard.css'),
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
// Standard selector components are frozen donor files published as manifest
// assets. Hosts only provide scoped API transport and never import donor files.
const standardComponents = [
  { name: 'operation_member_picker.js', source: 'internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js', sha256: 'bd84ce78ccb834f170548dea76cb99f6434978bc21211a9ec843dd2bf7ebabea' },
  { name: 'group_chat_picker.css', source: 'web/donors/ai-assistant-production/static/group_chat_picker.css', sha256: '99627d8e05be5419c53a5cfbc3c8d6d006b6e4efafec157dd412aa656e858481' },
  { name: 'group_chat_picker.js', source: 'web/donors/ai-assistant-production/static/group_chat_picker.js', sha256: 'da3de5fc5861f1b22e61bbc2726b3de4ebdab8420e4af3342ba3e0c478f2c1ed' },
  { name: 'material_picker.css', source: 'web/donors/ai-assistant-production/static/material_picker.css', sha256: '46deddd60fbbbf6a94603e689fda6830d1a5b1aa221d0af8223bb6855846f1e1' },
  { name: 'material_picker.js', source: 'web/donors/ai-assistant-production/static/material_picker.js', sha256: '8f3e63686ffdd029d8f15b6112b771372467527fd533aa688711e0e33bb6bd73' },
  { name: 'send_content_composer.css', source: 'web/donors/ai-assistant-production/static/send_content_composer.css', sha256: 'd542f246a1fb311040bb39329bb50d1b7383105269aa6bb0e6556d014d9700c1' },
  { name: 'send_content_composer.js', source: 'web/donors/ai-assistant-production/static/send_content_composer.js', sha256: 'f58c588c681079d1d16ae610e8662ef177acc94caf8e612c35f373de769a6b85' },
  { name: 'wecom_tag_picker.css', source: 'web/donors/standard-components-production/static/wecom_tag_picker.css', sha256: '00fd6603ece70aab098f606bf778281364b6dc4bc66b423598616173fbf4d147' },
  { name: 'wecom_tag_picker.js', source: 'web/donors/standard-components-production/static/wecom_tag_picker.js', sha256: '5c53adee7b65f1f2909cf1adf981b9d3b944b3bd15ebcd7dbf4e4cfe1f13d23d' },
];
const standardComponentsManifest = { version: 'dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f', css: [], scripts: [] };
for (const component of standardComponents) {
  const contents = fs.readFileSync(path.join(repository, component.source));
  const metadata = metadataFor(contents);
  if (metadata.sha256 !== component.sha256) throw new Error(`standard component differs from its audited dd8 donor bytes: ${component.name}`);
  const relative = `assets/standard-components/${component.name}`;
  fs.mkdirSync(path.dirname(path.join(dist, relative)), { recursive: true });
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadata, entry_point: component.source, imports: [], inputs: [component.source] };
  manifest.release_files[relative] = metadata;
  if (component.name.endsWith('.css')) standardComponentsManifest.css.push(relative);
  else standardComponentsManifest.scripts.push(relative);
}
manifest.standard_components = standardComponentsManifest;
// Keep the standard renderer's paging helper as an audited byte-for-byte
// release asset. It owns the established scroll/observer behavior; the V3 Host
// only supplies the scoped request and thumbnail adapters.
const imageResourceLoaderSource = 'web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js';
const imageResourceLoaderPath = path.join(repository, imageResourceLoaderSource);
const imageResourceLoaderContents = fs.readFileSync(imageResourceLoaderPath);
const imageResourceLoaderMetadata = metadataFor(imageResourceLoaderContents);
const expectedImageResourceLoaderSHA256 = '38090abd86d19b7027841e7035bb8e8b12548487914a98a893fd71a5ec51187d';
if (imageResourceLoaderMetadata.sha256 !== expectedImageResourceLoaderSHA256) throw new Error('sidebar image resource loader differs from the audited dd8 donor asset');
const imageResourceLoaderEntry = `assets/sidebarImageResourceLoader-${expectedImageResourceLoaderSHA256.slice(0, 16)}.js`;
fs.writeFileSync(path.join(dist, imageResourceLoaderEntry), imageResourceLoaderContents);
manifest.files[imageResourceLoaderEntry] = { ...imageResourceLoaderMetadata, entry_point: imageResourceLoaderSource, imports: [], inputs: [imageResourceLoaderSource] };
manifest.release_files[imageResourceLoaderEntry] = imageResourceLoaderMetadata;
manifest.entries.sidebarImageResourceLoader = imageResourceLoaderEntry;
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
  if (name === 'adminSessionHost' || name === 'standardComponentsHost' || name === 'aiAssistantHost' || name === 'sidebarHost' || name === 'sidebarStandardOverlay' || name === 'sidebarStandardStyles' || name === 'customerHost' || name === 'openPlatformHost' || name === 'groupopsHost' || name === 'groupopsStyles') continue;
  const donorMain = manifest.files[entry].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/main.ts'))?.path;
  const donorLegacy = donorMain && manifest.files[donorMain].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/legacy.ts'))?.path;
  if (!donorMain || !donorLegacy) throw new Error(`${name} must start the frozen donor main -> legacy runtime`);
}

const standardHostEntry = manifest.entries.standardComponentsHost;
if (typeof standardHostEntry !== 'string') throw new Error('standard Components Host entry is absent from manifest');
const stableStandardHost = 'assets/standard-components/standard_components_host.js';
const stableStandardHostContents = fs.readFileSync(path.join(dist, standardHostEntry));
fs.writeFileSync(path.join(dist, stableStandardHost), stableStandardHostContents);
const stableStandardHostMetadata = metadataFor(stableStandardHostContents);
manifest.files[stableStandardHost] = { ...stableStandardHostMetadata, entry_point: 'web/v3/standardComponentsHost.ts', imports: [], inputs: ['web/v3/standardComponentsHost.ts'] };
manifest.release_files[stableStandardHost] = stableStandardHostMetadata;
manifest.entries.standardComponentsStableHost = stableStandardHost;

const customerHost = manifest.entries.customerHost;
const frozenAdmin = manifest.entries.admin;
if (typeof customerHost !== 'string' || typeof frozenAdmin !== 'string') throw new Error('customer Host or frozen admin entry is absent from manifest');
const customerHostReference = `../${customerHost}`;
const frozenAdminReference = `<script type="module" src="../${frozenAdmin}"></script>`;
const standardCustomerReferences = `<link rel="stylesheet" href="../assets/standard-components/wecom_tag_picker.css">\n<script type="module" src="../${stableStandardHost}"></script>`;
for (const documentName of ['customers.html', 'customerDetail.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(customerHostReference)) throw new Error(`${documentName} already contains the customer Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `${standardCustomerReferences}\n<script type="module" src="${customerHostReference}"></script>\n${frozenAdminReference}`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

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
const adminSessionEntry = manifest.entries.adminSessionHost;
if (typeof adminSessionEntry !== 'string') throw new Error('Admin session Host entry is absent');
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) {
  const documentPath = path.join(adminOutput, documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes('class="side-user"')) continue;
  const script = `<script type="module" src="../${adminSessionEntry}"></script>`;
  if (documentHTML.includes(script)) throw new Error(`${documentName} already contains the Admin session Host`);
  if (!documentHTML.includes('</head>')) throw new Error(`${documentName} has no head for the Admin session Host`);
  documentHTML = documentHTML.replace('</head>', `${script}\n</head>`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}
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
const sidebarOverlay = manifest.entries.sidebarStandardOverlay;
const sidebarStyles = manifest.entries.sidebarStandardStyles;
const sidebarImageResourceLoader = manifest.entries.sidebarImageResourceLoader;
const weComJSSDK = 'https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js';
if (typeof sidebarHost !== 'string' || typeof sidebarOverlay !== 'string' || typeof sidebarStyles !== 'string' || typeof sidebarImageResourceLoader !== 'string') throw new Error('sidebar Host, overlay, image loader, or standard stylesheet is absent from manifest');
if (manifest.files[sidebarHost]?.entry_point !== 'web/v3/sidebar/main.ts') throw new Error('sidebar Host must be the V3 trusted bridge entry');
if (manifest.files[sidebarOverlay]?.entry_point !== 'web/dist/sidebar/sidebar_workbench_v3_overlay.js') throw new Error('sidebar standard overlay was not generated into the release manifest');
const sidebarHostScript = `<script type="module" src="../${sidebarHost}"></script>`;
const imageResourceLoaderScript = `<script src="../${sidebarImageResourceLoader}"></script>`;
const sidebarStylesheet = `<link rel="stylesheet" href="../${sidebarStyles}">`;
const sidebarTemplate = fs.readFileSync(path.join(repository, 'internal', 'webshell', 'static', 'sidebar_workbench', 'sidebar_customer_workbench_dd8d60d.html'), 'utf8');
let sidebarHTML = sidebarTemplate
  .replace(`{{ 'true' if debug_enabled else 'false' }}`, 'false')
  .replace('<link rel="stylesheet" href="/static/sidebar_workbench/sidebar_workbench.css?v=20260730-sidebar-material-search">', sidebarStylesheet)
  .replace('    data-other-staff-messages-url="/api/sidebar/v2/other-staff-messages"\n', '')
  .replace('    data-workbench-url="/api/sidebar/v2/workbench"\n', `    data-workbench-url="/api/sidebar/v2/workbench"\n    data-overlay-url="../${sidebarOverlay}"\n`)
  .replace('            <div class="meta" id="customer-external-userid"></div>\n', '')
  .replace('  <script src="https://res.wx.qq.com/open/js/jweixin-1.6.0.js"></script>\n  <script src="/static/admin_console/image_resource_loader.js?v=resource-governance-v2-pending-retry"></script>\n  <script src="/static/sidebar_workbench/sidebar_workbench.js?v=20260805-context-bootstrap"></script>', `  <script src="${weComJSSDK}"></script>\n  ${imageResourceLoaderScript}\n  ${sidebarHostScript}`);
if (sidebarHTML.includes('other-staff-messages') || sidebarHTML.includes('jweixin-1.6.0.js') || sidebarHTML.includes('sidebar_workbench.js')) throw new Error('standard sidebar overlay retained removed chat or retired runtime');
if (!sidebarHTML.includes(weComJSSDK) || !sidebarHTML.includes(imageResourceLoaderScript) || !sidebarHTML.includes(sidebarHostScript) || !sidebarHTML.includes(sidebarStylesheet) || !sidebarHTML.includes(`data-overlay-url="../${sidebarOverlay}"`)) throw new Error('standard sidebar overlay did not retain V3 bridge, image loader, generated renderer, and stylesheet closure');
const sidebarDocument = path.join(dist, 'sidebar', 'index.html');
fs.writeFileSync(sidebarDocument, sidebarHTML);
manifest.release_files['sidebar/index.html'] = metadataFor(Buffer.from(sidebarHTML));

const donor = path.join(repository, 'web', 'donors', 'ai-assistant-production');
const donorOut = path.join(dist, 'aiassistant');
fs.mkdirSync(donorOut, { recursive: true });
const donorAssets = ['group_chat_picker.css','group_chat_picker.js','material_picker.css','material_picker.js','send_content_composer.css','send_content_composer.js','send_content_readonly_detail.css','send_content_readonly_detail.js','cloud_plan_review.js'];
const groupOpsSupport = new Set(['group_chat_picker.css','group_chat_picker.js','material_picker.css','material_picker.js','send_content_composer.css','send_content_composer.js']);
for (const name of donorAssets) {
  const contents = fs.readFileSync(path.join(donor, 'static', name));
  const relative = `aiassistant/${name}`;
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadataFor(contents), inputs: [`web/donors/ai-assistant-production/static/${name}`], imports: [] };
  manifest.release_files[relative] = metadataFor(contents);
  if (groupOpsSupport.has(name)) {
    const groupOpsRelative = `groupops/${name}`;
    fs.mkdirSync(path.join(dist, 'groupops'), { recursive: true });
    fs.writeFileSync(path.join(dist, groupOpsRelative), contents);
    manifest.files[groupOpsRelative] = { ...metadataFor(contents), inputs: [`web/donors/ai-assistant-production/static/${name}`], imports: [] };
    manifest.release_files[groupOpsRelative] = metadataFor(contents);
  }
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
