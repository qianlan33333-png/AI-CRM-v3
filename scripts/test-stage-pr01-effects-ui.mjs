#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-pr01-stage-'));
const source = path.join(root, 'source');
const stage = path.join(root, 'stage');
try {
  fs.mkdirSync(path.join(source, 'admin'), { recursive: true });
  fs.mkdirSync(path.join(source, 'assets'), { recursive: true });
  // A donor HTML file exists in the build input but must never reach release.
  fs.writeFileSync(path.join(source, 'admin', 'campaigns.html'), '<aside class="side"></aside>');
  for (const file of ['admin.js', 'tokens.css', 'labs.css', 'legacy.js', 'campaigns.js']) {
    fs.writeFileSync(path.join(source, 'assets', file), file);
  }
  const files = Object.fromEntries([
    ['assets/admin.js', { imports: [{ kind: 'import-statement', path: 'assets/legacy.js' }] }],
    ['assets/tokens.css', { imports: [] }],
    ['assets/labs.css', { imports: [] }],
    ['assets/legacy.js', { inputs: ['web/src/admin/legacy.ts'], imports: [] }],
    ['assets/campaigns.js', { inputs: ['web/src/admin/sections/campaigns.ts'], imports: [] }],
  ]);
  fs.writeFileSync(path.join(source, 'asset-manifest.json'), JSON.stringify({
    entries: { admin: 'assets/admin.js', tokens: 'assets/tokens.css', labs: 'assets/labs.css' }, files,
  }));

  execFileSync(process.execPath, ['scripts/stage-pr01-effects-ui.mjs', source, stage], { stdio: 'inherit' });
  const staged = fs.readdirSync(stage, { recursive: true }).map((entry) => String(entry).split(path.sep).join('/')).sort();
  assert.deepEqual(staged, ['asset-manifest.json', 'assets', 'assets/admin.js', 'assets/campaigns.js', 'assets/labs.css', 'assets/legacy.js', 'assets/tokens.css']);
  assert.equal(fs.existsSync(path.join(stage, 'admin', 'campaigns.html')), false, 'donor campaign HTML must not be released');
  assert.equal(staged.some((entry) => entry.endsWith('.html')), false, 'no HTML surface belongs in the effects asset stage');
  console.log('PR01 effects UI staging contract passed');
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
