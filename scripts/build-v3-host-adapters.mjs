#!/usr/bin/env node
import { build } from 'esbuild';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { memberGridPresentationPlugin } from './member-grid-presentation-source.mjs';

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
  materialSaveHost: path.join(repository, 'web', 'v3', 'materialSaveAdapter.ts'),
  imageLibraryFilterHost: path.join(repository, 'web', 'v3', 'imageLibraryFilterHost.ts'),
  orderHost: path.join(repository, 'web', 'v3', 'orderAdapter.ts'),
  productHost: path.join(repository, 'web', 'v3', 'productAdapter.ts'),
  radarHost: path.join(repository, 'web', 'v3', 'radarAdapter.ts'),
  couponHost: path.join(repository, 'web', 'v3', 'couponAdapter.ts'),
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
  h5AuthHost: path.join(repository, 'web', 'v3', 'h5AuthAdapter.ts'),
  surfaceFeedbackHost: path.join(repository, 'web', 'v3', 'surfaceFeedbackHost.ts'),
  surfaceFeedbackStyles: path.join(repository, 'web', 'v3', 'surfaceFeedback.css'),
  presentationStyles: path.join(repository, 'web', 'v3', 'presentation.css'),
  actionFeedbackStyles: path.join(repository, 'web', 'v3', 'actionFeedback.css'),
  memberGridFeedbackHost: path.join(repository, 'web', 'v3', 'memberGridFeedbackHost.ts'),
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
  plugins: [memberGridPresentationPlugin],
});

const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
const normalizeOutput = (output) => path.relative(dist, path.resolve(repository, output)).split(path.sep).join('/');
const metadataFor = (contents) => ({
  bytes: contents.byteLength,
  gzip_bytes: gzipSync(contents, { level: 9 }).byteLength,
  sha256: crypto.createHash('sha256').update(contents).digest('hex'),
});
// The original dd8 selection components are released once below /assets.
// Page Hosts receive only these manifest-verified URLs; they never import a
// donor directory or recreate a picker.  The first eight payloads already
// have canonical frozen homes, while the previously unshipped tag picker is
// registered in the standard-components donor ledger.
const standardComponents = [
  { name: 'operation_member_picker.js', source: 'internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js', sha256: '1b12b405d737794808dd1b998ccfa8c6eb77dd4d7c22e69380b428fb89a69e70' },
  { name: 'group_chat_picker.css', source: 'web/donors/ai-assistant-production/static/group_chat_picker.css', sha256: '99627d8e05be5419c53a5cfbc3c8d6d006b6e4efafec157dd412aa656e858481' },
  { name: 'group_chat_picker.js', source: 'web/donors/ai-assistant-production/static/group_chat_picker.js', sha256: 'da3de5fc5861f1b22e61bbc2726b3de4ebdab8420e4af3342ba3e0c478f2c1ed' },
  { name: 'material_picker.css', source: 'web/donors/ai-assistant-production/static/material_picker.css', sha256: '46deddd60fbbbf6a94603e689fda6830d1a5b1aa221d0af8223bb6855846f1e1' },
  { name: 'material_picker.js', source: 'web/donors/ai-assistant-production/static/material_picker.js', sha256: '8f3e63686ffdd029d8f15b6112b771372467527fd533aa688711e0e33bb6bd73' },
  { name: 'send_content_composer.css', source: 'web/donors/ai-assistant-production/static/send_content_composer.css', sha256: 'd542f246a1fb311040bb39329bb50d1b7383105269aa6bb0e6556d014d9700c1' },
  { name: 'send_content_composer.js', source: 'web/donors/ai-assistant-production/static/send_content_composer.js', sha256: 'f58c588c681079d1d16ae610e8662ef177acc94caf8e612c35f373de769a6b85' },
  { name: 'wecom_tag_picker.css', source: 'web/donors/standard-components-production/static/wecom_tag_picker.css', sha256: '00fd6603ece70aab098f606bf778281364b6dc4bc66b423598616173fbf4d147' },
  { name: 'wecom_tag_picker.js', source: 'web/donors/standard-components-production/static/wecom_tag_picker.js', sha256: '5c53adee7b65f1f2909cf1adf981b9d3b944b3bd15ebcd7dbf4e4cfe1f13d23d' },
  { name: 'coupon_form.html', source: 'web/donors/standard-components-production/coupons/coupon_form.html', sha256: 'f9116280af8e0c9f4702c54c3cac192012f4c8af7944713afb32b929380f8e86' },
  { name: 'coupon_styles.html', source: 'web/donors/standard-components-production/coupons/coupon_styles.html', sha256: '89d4d72fb3234fc67c630ba61ff5f4292feae2286656fe8bb4b12942aea554f0' },
];
const standardComponentsManifest = { version: 'standard-components-v2-1b12b405d7377948', css: [], scripts: [] };
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
  else if (component.name.endsWith('.js')) standardComponentsManifest.scripts.push(relative);
}
manifest.standard_components = standardComponentsManifest;
// The coupon donor keeps its executable behavior in an inline script. CSP
// permits the same bytes only as a separately served release asset, so extract
// the one inline body without adding a wrapper or changing a byte. CouponHost
// appends this URL only after it has mounted the frozen DOM.
const couponSource = fs.readFileSync(path.join(repository, 'web/donors/standard-components-production/coupons/coupon_form.html'), 'utf8');
const couponInlineScripts = [...couponSource.matchAll(/<script>([\s\S]*?)<\/script>/g)];
if (couponInlineScripts.length !== 1) throw new Error('coupon donor must contain exactly one extractable inline runtime');
const couponRuntime = Buffer.from(couponInlineScripts[0][1]);
const couponRuntimeMetadata = metadataFor(couponRuntime);
if (couponRuntimeMetadata.sha256 !== 'a3e15d50e97609d934a4edcab1adb2a2048d23e0dd7517e46ad4b9b677b6c88c') throw new Error('coupon inline runtime differs from the audited donor bytes');
const couponRuntimeRelative = 'assets/standard-components/coupon_form_runtime.js';
fs.writeFileSync(path.join(dist, couponRuntimeRelative), couponRuntime);
manifest.files[couponRuntimeRelative] = { ...couponRuntimeMetadata, entry_point: 'web/donors/standard-components-production/coupons/coupon_form.html#inline-script[1]', imports: [], inputs: ['web/donors/standard-components-production/coupons/coupon_form.html#inline-script[1]'] };
manifest.release_files[couponRuntimeRelative] = couponRuntimeMetadata;
standardComponentsManifest.passive = [couponRuntimeRelative];
// Page Hosts append these byte-frozen documents/scripts only after their own
// scoped DOM exists. They deliberately stay outside ready()'s auto-evaluation list.
const standardPassiveAssets = [
  { name: 'channel_code_form.html', source: 'web/donors/standard-components-production/channel/channel_code_form.html', sha256: '9ab90756f1b2c58bd96368559339ca61865ebdd1641ab2c8c2f1146d80d1f909' },
  { name: 'channel_admission_pages.js', source: 'web/donors/standard-components-production/channel/channel_admission_pages.js', sha256: 'ae1d9007dbd37757850d35ac25bd147cb09e7f576563122ec26cba7f4285832a' },
];
for (const component of standardPassiveAssets) {
  const contents = fs.readFileSync(path.join(repository, component.source));
  const metadata = metadataFor(contents);
  if (metadata.sha256 !== component.sha256) throw new Error(`standard passive asset differs from its audited donor bytes: ${component.name}`);
  const relative = `assets/standard-components/${component.name}`;
  fs.writeFileSync(path.join(dist, relative), contents);
  manifest.files[relative] = { ...metadata, entry_point: component.source, imports: [], inputs: [component.source] };
  manifest.release_files[relative] = metadata;
  standardComponentsManifest.passive.push(relative);
}
// The Channel Center owns this page-specific stylesheet.  Keep it a release
// asset rather than importing it into the Host bundle so the original CSS
// remains inspectable and its load order stays before the page Host.
const channelAdmissionStylesSource = 'web/v3/channelAdmissionStandard.css';
const channelAdmissionStyles = fs.readFileSync(path.join(repository, channelAdmissionStylesSource));
const channelAdmissionStylesRelative = 'assets/channelAdmissionStandard.css';
fs.writeFileSync(path.join(dist, channelAdmissionStylesRelative), channelAdmissionStyles);
manifest.files[channelAdmissionStylesRelative] = { ...metadataFor(channelAdmissionStyles), entry_point: channelAdmissionStylesSource, imports: [], inputs: [channelAdmissionStylesSource] };
manifest.release_files[channelAdmissionStylesRelative] = metadataFor(channelAdmissionStyles);
manifest.entries.channelAdmissionStyles = channelAdmissionStylesRelative;
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
  if (['surfaceFeedbackHost', 'surfaceFeedbackStyles', 'presentationStyles', 'actionFeedbackStyles', 'memberGridFeedbackHost'].includes(name) || name === 'adminSessionHost' || name === 'standardComponentsHost' || name === 'aiAssistantHost' || name === 'sidebarHost' || name === 'sidebarStandardOverlay' || name === 'sidebarStandardStyles' || name === 'customerHost' || name === 'materialSaveHost' || name === 'imageLibraryFilterHost' || name === 'orderHost' || name === 'couponHost' || name === 'radarHost' || name === 'openPlatformHost' || name === 'groupopsHost' || name === 'groupopsStyles' || name === 'h5AuthHost') continue;
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

const orderHost = manifest.entries.orderHost;
if (typeof orderHost !== 'string') throw new Error('Order Host entry is absent from manifest');
const orderHostReference = `../${orderHost}`;
for (const documentName of ['orders.html', 'orderDetail.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(orderHostReference)) throw new Error(`${documentName} already contains the Order Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `<script type="module" src="${orderHostReference}"></script>`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[`admin/${documentName}`] = metadataFor(Buffer.from(documentHTML));
}

const materialSaveHost = manifest.entries.materialSaveHost;
if (typeof materialSaveHost !== 'string') throw new Error('Material Save Host entry is absent from manifest');
const materialSaveReference = `../${materialSaveHost}`;
for (const documentName of ['images.html', 'mpLib.html', 'attach.html']) {
  const documentPath = path.join(dist, 'admin', documentName);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  if (!documentHTML.includes(frozenAdminReference)) throw new Error(`${documentName} does not reference the declared frozen admin entry`);
  if (documentHTML.includes(materialSaveReference)) throw new Error(`${documentName} already contains the Material Save Host`);
  documentHTML = documentHTML.replace(frozenAdminReference, `<script type="module" src="${materialSaveReference}"></script>\n${frozenAdminReference}`);
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

const h5AuthHost = manifest.entries.h5AuthHost;
const frozenH5 = manifest.entries.h5;
if (typeof h5AuthHost !== 'string' || typeof frozenH5 !== 'string') throw new Error('H5 auth Host or frozen H5 entry is absent from manifest');
const frozenH5Reference = `<script type="module" src="../${frozenH5}"></script>`;
const h5AuthReference = `<script type="module" src="../${h5AuthHost}"></script>`;
for (const page of ['auth', 'all', 'one', 'result']) {
  const documentPath = path.join(dist, 'h5', `${page}.html`);
  let html = fs.readFileSync(documentPath, 'utf8');
  if (!html.includes(frozenH5Reference)) throw new Error(`${page}.html does not reference the declared frozen H5 entry`);
  if (html.includes(h5AuthReference)) throw new Error(`${page}.html already contains the H5 mobile Host`);
  // Remove demo chrome in the release HTML before first paint, not after mount.
  const demoShell = /<div class="h5-backdrop"><div><div class="phone"><div id="screen" class="phone-screen"><\/div><\/div><div style="[^"]*"><a href="index.html">← 全部屏幕<\/a><\/div><\/div><\/div>/;
  if (!demoShell.test(html)) throw new Error(`${page}.html H5 shell changed; inspect the mobile adaptation`);
  html = html.replace(demoShell, '<main id="screen" class="v3-survey-screen"></main>');
  html = html.replace('</head>', '<style>html,body{margin:0;min-height:100%;background:#F5F6F7}*{box-sizing:border-box}.v3-survey-screen{display:flex;flex-direction:column;width:100%;max-width:720px;min-height:100vh;min-height:100dvh;margin:0 auto;overflow-wrap:anywhere;padding-bottom:env(safe-area-inset-bottom)}.v3-survey-screen input,.v3-survey-screen textarea{max-width:100%;font-size:16px}</style></head>');
  html = html.replace(frozenH5Reference, `${h5AuthReference}\n${frozenH5Reference}`);
  fs.writeFileSync(documentPath, html);
  manifest.release_files[`h5/${page}.html`] = metadataFor(Buffer.from(html));
}

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

// The frozen documents remain their own authorities.  The V3 feedback layer
// is added only to the generated release views, after the donor build, so it
// can provide a scoped initial spinner and navigation hint without changing
// any donor bytes or business runtime.
const surfaceFeedbackHost = manifest.entries.surfaceFeedbackHost;
const surfaceFeedbackStyles = manifest.entries.surfaceFeedbackStyles;
const memberGridFeedbackHost = manifest.entries.memberGridFeedbackHost;
const memberGridShare = manifest.entries.memberGridShare;
if (typeof surfaceFeedbackHost !== 'string' || typeof surfaceFeedbackStyles !== 'string' || typeof memberGridFeedbackHost !== 'string' || typeof memberGridShare !== 'string') {
  throw new Error('surface feedback or member-grid Host entry is absent from manifest');
}
const injectSurfaceFeedback = (relative, surface) => {
  const documentPath = path.join(dist, relative);
  let documentHTML = fs.readFileSync(documentPath, 'utf8');
  const stylesheet = ['surfaceFeedbackStyles', 'actionFeedbackStyles', 'presentationStyles'].map((entry) => `<link rel="stylesheet" href="../${manifest.entries[entry]}">`).join('\n');
  const host = `<script async src="../${surfaceFeedbackHost}"></script>`;
  if (!documentHTML.includes('</head>') || !documentHTML.includes('<body')) throw new Error(`${relative} has no HTML shell for surface feedback`);
  if (documentHTML.includes(stylesheet) || documentHTML.includes(host)) throw new Error(`${relative} already contains surface feedback`);
  documentHTML = documentHTML.replace('<head>', `<head>\n${host}`).replace('</head>', `${stylesheet}\n</head>`);
  documentHTML = documentHTML.replace(/<body(\s|>)/, `<body data-ui-surface="${surface}"$1`);
  const placeholder = '<div class="surface-feedback__busy surface-feedback__busy--initial" data-surface-placeholder role="status" aria-live="polite"><span class="surface-feedback__spinner" aria-hidden="true"></span><span>正在加载页面…</span></div>';
  documentHTML = documentHTML.replace(/(<(?:main|div)[^>]*\bid="(?:stage|screen)"[^>]*>)(\s*)(<\/(?:main|div)>)/, `$1${placeholder}$3`);
  if (!documentHTML.includes(`data-ui-surface="${surface}"`)) throw new Error(`${relative} did not receive its surface marker`);
  fs.writeFileSync(documentPath, documentHTML);
  manifest.release_files[relative] = metadataFor(Buffer.from(documentHTML));
};
for (const documentName of fs.readdirSync(adminOutput).filter((name) => name.endsWith('.html'))) injectSurfaceFeedback(`admin/${documentName}`, 'admin');
for (const documentName of fs.readdirSync(path.join(dist, 'h5')).filter((name) => name.endsWith('.html'))) injectSurfaceFeedback(`h5/${documentName}`, 'h5');
injectSurfaceFeedback('sidebar/index.html', 'sidebar');
injectSurfaceFeedback('member-grid-share/index.html', 'share');
const memberGridShareDocument = path.join(dist, 'member-grid-share', 'index.html');
let memberGridShareHTML = fs.readFileSync(memberGridShareDocument, 'utf8');
const frozenMemberGridShareScript = `<script type="module" src="../${memberGridShare}"></script>`;
const memberGridFeedbackScript = `<script type="module" src="../${memberGridFeedbackHost}"></script>`;
if (!memberGridShareHTML.includes(frozenMemberGridShareScript) || memberGridShareHTML.includes(memberGridFeedbackScript)) throw new Error('member-grid share document does not have exactly one replaceable frozen entry');
memberGridShareHTML = memberGridShareHTML.replace(frozenMemberGridShareScript, memberGridFeedbackScript);
fs.writeFileSync(memberGridShareDocument, memberGridShareHTML);
manifest.release_files['member-grid-share/index.html'] = metadataFor(Buffer.from(memberGridShareHTML));

const donor = path.join(repository, 'web', 'donors', 'ai-assistant-production');
const donorOut = path.join(dist, 'aiassistant');
fs.mkdirSync(donorOut, { recursive: true });
const donorAssets = ['send_content_readonly_detail.css','send_content_readonly_detail.js','cloud_plan_review.js'];
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
