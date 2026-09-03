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
  channelCenterHost: path.join(repository, 'web', 'v3', 'channelCenterAdapter.ts'),
  aiAssistantHost: path.join(repository, 'web', 'v3', 'aiAssistantAdapter.ts'),
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
  if (name === 'aiAssistantHost') continue;
  const donorMain = manifest.files[entry].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/main.ts'))?.path;
  const donorLegacy = donorMain && manifest.files[donorMain].imports.find((item) => item.kind === 'dynamic-import' && manifest.files[item.path]?.inputs?.includes('web/src/admin/legacy.ts'))?.path;
  if (!donorMain || !donorLegacy) throw new Error(`${name} must start the frozen donor main -> legacy runtime`);
}

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
