import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const materializedSrc = path.join(root, 'web/src');
const transform = (html) => html
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
  .replace(/<\/sc-for>/g, '</template>')
  .replace(/<sc-if\s+([^>]*?)value="([^"]*)"([^>]*)>/g, (_match, _a, value) => `<template data-sc-if="${value}">`)
  .replace(/<\/sc-if>/g, '</template>');

const bundle = await build({
  stdin: {
    contents: `import './web/v3/surveyAdapter';
      import { AdminController } from './web/src/admin/controller';
      import { mount } from './web/src/shared/ui/runtime';
      window.SurveyControllerFixture = AdminController;
      window.SurveyMountFixture = mount;`,
    resolveDir: root,
    loader: 'ts',
  },
  plugins: [{
    name: 'survey-adapter-materialized-view',
    setup(pluginBuild) {
      pluginBuild.onResolve({ filter: /^\.\.\/src\/admin\/main$/ }, () => ({ path: 'empty-admin-main', namespace: 'survey-test' }));
      pluginBuild.onLoad({ filter: /.*/, namespace: 'survey-test' }, () => ({ contents: 'export {};', loader: 'ts' }));
      pluginBuild.onResolve({ filter: /^\.\.\/src\// }, (args) => {
        const raw = path.join(materializedSrc, args.path.replace('../src/', ''));
        const resolved = ['.ts', '.tsx', '.js'].map((extension) => `${raw}${extension}`).find(existsSync) || raw;
        return { path: resolved };
      });
    },
  }],
  bundle: true,
  format: 'iife',
  platform: 'browser',
  write: false,
  logLevel: 'silent',
});

const dom = new JSDOM('<!doctype html><body data-page="questionnaires"><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/questionnaires.html',
  runScripts: 'outside-only',
});
const pause = () => new Promise((resolve) => setTimeout(resolve, 0));

try {
  const calls = [];
  let failOnce = true;
  dom.window.Headers = globalThis.Headers;
  Object.defineProperty(dom.window, 'crypto', { value: globalThis.crypto, configurable: true });
  dom.window.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method, headers: init.headers, body: init.body });
    if (failOnce) {
      failOnce = false;
      return { ok: false, status: 503, clone: () => ({ json: async () => ({ code: 'temporary_failure' }) }), text: async () => 'temporary_failure' };
    }
    return { ok: true, status: 200 };
  };
  dom.window.eval(bundle.outputFiles[0].text);
  await pause();
  const controller = new dom.window.SurveyControllerFixture({ mode: 'mock' }, 'questionnaires');
  const source = {
    resourceId: 8,
    name: '过期问卷标题',
    internalName: 'Imported questionnaire',
    title: '过期问卷标题',
    version: 4,
    off: false,
    action: 'active',
    created: '2026-09-14T00:00:00Z',
    count: '0',
  };
  controller.db.rows.questionnaires = [source];
  controller.state.questionnaireQuery = 'imported questionnaire';
  let rows = controller.renderVals().rows.questionnaires;
  assert.equal(rows.length, 1, 'management search must use questionnaire name');
  assert.equal(rows[0].name, 'Imported questionnaire', 'management primary label must use questionnaire name');
  assert.equal(source.name, '过期问卷标题', 'bridge must restore the server DTO after rendering');

  controller.state.questionnaireQuery = '过期问卷标题';
  rows = controller.renderVals().rows.questionnaires;
  assert.equal(rows.length, 0, 'management search must not use questionnaire title');

  controller.state.questionnaireQuery = '';
  controller.init = async () => {
    controller.db.rows.questionnaires = [];
    controller.__render?.();
  };
  dom.window.SurveyMountFixture(dom.window.document.getElementById('stage'), transform(readFileSync(path.join(root, 'web/src/admin/templates/questionnaires.html'), 'utf8')), controller);
  const deleteLink = [...dom.window.document.querySelectorAll('a')].find((node) => node.textContent === '删除');
  assert.ok(deleteLink, 'the rendered questionnaire table has the archive action');
  deleteLink.click();
  assert.equal(dom.window.document.getElementById('fb-head')?.textContent, '删除问卷', 'the rendered action opens the owner-visible delete confirmation');
  assert.match(dom.window.document.getElementById('fb-body')?.textContent || '', /过期问卷标题/, 'the confirmation identifies the frozen row');
  dom.window.document.getElementById('fb-ok').click();
  await pause();
  assert.equal(calls.length, 1, 'the first confirmed click sends one archive request');

  const retryLink = [...dom.window.document.querySelectorAll('a')].find((node) => node.textContent === '删除');
  assert.ok(retryLink, 'a recoverable failure keeps the rendered archive action available');
  retryLink.click();
  dom.window.document.getElementById('fb-ok').click();
  await pause();
  assert.equal(calls.length, 2, 'a retry sends exactly one more archive request');
  assert.equal(calls[0].url, '/api/admin/questionnaires/8');
  assert.equal(calls[0].method, 'DELETE');
  assert.equal(calls[0].body, JSON.stringify({ expected_version: 4 }));
  assert.equal(calls[1].body, calls[0].body, 'retry retains the exact frozen CAS body');
  assert.equal(calls[1].headers['Idempotency-Key'], calls[0].headers['Idempotency-Key'], 'retry retains the exact idempotency key');
  assert.equal(dom.window.document.querySelectorAll('tbody tr').length, 0, 'successful readback removes the archived questionnaire from the rendered list');
} finally {
  dom.window.close();
}

console.log('survey archive DOM action: PASS');
