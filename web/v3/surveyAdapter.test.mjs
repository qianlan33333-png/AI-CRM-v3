import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = await build({
  stdin: {
    contents: "import './web/v3/surveyAdapter'; import { AdminController } from './web/src/admin/controller'; window.SurveyControllerFixture = AdminController;",
    resolveDir: root,
    loader: 'ts',
  },
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

try {
  dom.window.eval(bundle.outputFiles[0].text);
  await new Promise((resolve) => setTimeout(resolve, 0));
  const controller = new dom.window.SurveyControllerFixture({ mode: 'mock' }, 'questionnaires');
  const source = {
    resourceId: 8,
    name: '过期问卷标题',
    internalName: 'Imported questionnaire',
    title: '过期问卷标题',
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
} finally {
  dom.window.close();
}

console.log('survey list name bridge: PASS');
