import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  DonorViewError,
  LOCK_PATH,
  applyMaterialization,
  cleanMaterialization,
  planMaterialization,
  recoverMaterializationLock,
  verifyMaterialization,
  verifySourceIndex,
} from './donor-source-views.mjs';

const REPOSITORY = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const SOURCE = Buffer.from('frozen canonical donor payload\n');
const SOURCE_SHA256 = crypto.createHash('sha256').update(SOURCE).digest('hex');
const SOURCE_BLOB = crypto.createHash('sha1').update(`blob ${SOURCE.byteLength}\0`).update(SOURCE).digest('hex');
const SECOND_SOURCE = Buffer.from('second frozen canonical donor payload\n');
const SECOND_SOURCE_SHA256 = crypto.createHash('sha256').update(SECOND_SOURCE).digest('hex');
const SOURCE_COMMIT = '89abcdef0123456789abcdef0123456789abcdef';

function writeJSON(absolute, value) {
  fs.writeFileSync(absolute, `${JSON.stringify(value, null, 2)}\n`);
}

function lockEntries(index) {
  return index.contents.map((content) => {
    const library = index.libraries.find((item) => item.id === content.library_id);
    return {
      id: content.id,
      library_id: content.library_id,
      source_repository: library.source_repository,
      source_commit: library.source_commit,
      canonical_path: content.canonical_path,
      source_path: content.source_path,
      source_git_blob_sha: content.source_git_blob_sha,
      content_sha256: content.content_sha256,
      bytes: content.bytes,
      mode: content.mode,
    };
  });
}

function makeFixture({ views = [{ target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true }], secondContent = false } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-donor-views-'));
  fs.mkdirSync(path.join(root, 'sources'), { recursive: true });
  fs.mkdirSync(path.join(root, 'tracked'), { recursive: true });
  fs.writeFileSync(path.join(root, 'sources', 'health.schemas.ts'), SOURCE, { mode: 0o644 });
  fs.writeFileSync(path.join(root, 'tracked', 'health.schemas.ts'), SOURCE, { mode: 0o644 });
  const library = {
    id: 'fixture-library', source_repository: 'https://example.invalid/frozen.git', source_commit: SOURCE_COMMIT,
    root: 'sources', immutable: true,
  };
  const health = {
    id: 'health', library_id: library.id, canonical_path: 'sources/health.schemas.ts', source_path: 'web/src/api/generated/health.schemas.ts',
    source_git_blob_sha: SOURCE_BLOB, content_sha256: SOURCE_SHA256, bytes: SOURCE.byteLength, mode: '100644',
  };
  const contents = [health];
  if (secondContent) {
    contents.push({
      id: 'missing', library_id: library.id, canonical_path: 'sources/missing.ts', source_path: 'web/src/api/generated/missing.ts',
      source_git_blob_sha: '1123456789abcdef0123456789abcdef01234567', content_sha256: SECOND_SOURCE_SHA256, bytes: SECOND_SOURCE.byteLength, mode: '100644',
    });
  }
  const index = {
    schema_version: 1, lock_path: 'source-lock.json', libraries: [library], contents,
    bindings: [{
      module: 'fixture', logical_path: 'tracked/health.schemas.ts', content_id: 'health',
      source_repository: library.source_repository, source_commit: library.source_commit,
      source_path: health.source_path, source_git_blob_sha: health.source_git_blob_sha,
      mode: '100644', usage: 'frozen_donor_compatibility_view', freeze_gate: 'scripts/check-fixture.sh',
      freeze_ledger: 'docs/fixture-ledger.txt', current_path_state: 'tracked_pre_p2',
    }],
    views,
  };
  if (secondContent) {
    index.views = [
      { target_path: 'views/first.ts', content_id: 'health', enabled: true },
      { target_path: 'views/second.ts', content_id: 'missing', enabled: true },
    ];
  }
  writeJSON(path.join(root, 'source-index.json'), index);
  writeJSON(path.join(root, 'source-lock.json'), { schema_version: 1, entries: lockEntries(index) });
  return { root, index };
}

function withFixture(options, callback) {
  const fixture = makeFixture(options);
  try {
    return callback(fixture);
  } finally {
    fs.rmSync(fixture.root, { recursive: true, force: true });
  }
}

function expectCode(code, callback) {
  assert.throws(callback, (error) => error instanceof DonorViewError && error.code === code);
}

test('PR-2 production health pilot verifies all eight original paths and does not enable a view', () => {
  const result = verifySourceIndex(REPOSITORY);
  assert.equal(result.bindings_verified, 8);
  assert.equal(result.enabled_views, 0);
  assert.equal(result.canonical_contents[0].content_sha256, '7f1bc1d05b3e012de46b1d53ef7b56319c0bc032a1c0389fa3fd138c7218b40d');
  assert.deepEqual(planMaterialization(REPOSITORY).materialized_view_targets, []);
  assert.equal(applyMaterialization(REPOSITORY).no_enabled_views, true);
});

test('materializes byte-identical untracked views atomically, reuses them, verifies and cleans only receipted paths', () => {
  withFixture({}, ({ root }) => {
    const first = applyMaterialization(root, 'source-index.json');
    assert.deepEqual(first.created, ['views/health.schemas.ts']);
    assert.deepEqual(fs.readFileSync(path.join(root, 'views', 'health.schemas.ts')), SOURCE);
    assert.equal(fs.statSync(path.join(root, 'views', 'health.schemas.ts')).mode & 0o777, 0o644);
    assert.deepEqual(applyMaterialization(root, 'source-index.json').reused, ['views/health.schemas.ts']);
    assert.deepEqual(verifyMaterialization(root, 'source-index.json').materialized_views_verified, ['views/health.schemas.ts']);
    assert.deepEqual(cleanMaterialization(root, 'source-index.json').removed, ['views/health.schemas.ts']);
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
    assert.equal(fs.existsSync(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json')), false);
  });
});

test('fails closed for a tampered or missing canonical source before it writes a view', () => {
  withFixture({}, ({ root }) => {
    fs.appendFileSync(path.join(root, 'sources', 'health.schemas.ts'), 'tamper');
    expectCode('CANONICAL_SIZE_MISMATCH', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
  });
  withFixture({}, ({ root }) => {
    fs.rmSync(path.join(root, 'sources', 'health.schemas.ts'));
    expectCode('MISSING_FILE', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
  });
});

test('rejects a valid-looking wrong source version and an index/lock coordinated-source mismatch', () => {
  withFixture({}, ({ root, index }) => {
    index.bindings[0].source_commit = 'fedcba9876543210fedcba9876543210fedcba98';
    writeJSON(path.join(root, 'source-index.json'), index);
    expectCode('SOURCE_VERSION_MISMATCH', () => verifySourceIndex(root, 'source-index.json'));
  });
  withFixture({}, ({ root }) => {
    const lockPath = path.join(root, 'source-lock.json');
    const lock = JSON.parse(fs.readFileSync(lockPath));
    lock.entries[0].source_commit = 'fedcba9876543210fedcba9876543210fedcba98';
    writeJSON(lockPath, lock);
    expectCode('SOURCE_LOCK_MISMATCH', () => verifySourceIndex(root, 'source-index.json'));
  });
  withFixture({}, ({ root, index }) => {
    const replacementBlob = '1123456789abcdef0123456789abcdef01234567';
    index.contents[0].source_git_blob_sha = replacementBlob;
    index.bindings[0].source_git_blob_sha = replacementBlob;
    writeJSON(path.join(root, 'source-index.json'), index);
    writeJSON(path.join(root, 'source-lock.json'), { schema_version: 1, entries: lockEntries(index) });
    expectCode('CANONICAL_GIT_BLOB_MISMATCH', () => verifySourceIndex(root, 'source-index.json'));
  });
});

test('rejects path escape, duplicate targets and tracked targets', () => {
  withFixture({ views: [{ target_path: '../outside.ts', content_id: 'health', enabled: true }] }, ({ root }) => {
    expectCode('PATH_ESCAPE', () => planMaterialization(root, 'source-index.json'));
  });
  withFixture({ views: [
    { target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true },
    { target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true },
  ] }, ({ root }) => {
    expectCode('DUPLICATE_DECLARATION', () => planMaterialization(root, 'source-index.json'));
  });
  withFixture({ views: [{ target_path: 'views/health.schemas.ts', content_id: 'health', enabled: true }] }, ({ root }) => {
    fs.mkdirSync(path.join(root, 'views'), { recursive: true });
    fs.writeFileSync(path.join(root, 'views', 'health.schemas.ts'), SOURCE);
    execFileSync('git', ['init', '--quiet', root]);
    execFileSync('git', ['-C', root, 'add', 'views/health.schemas.ts']);
    expectCode('TRACKED_TARGET', () => applyMaterialization(root, 'source-index.json'));
  });
});

test('rejects dirty output, held lock and a partial plan before any write', () => {
  withFixture({}, ({ root }) => {
    fs.mkdirSync(path.join(root, 'views'), { recursive: true });
    fs.writeFileSync(path.join(root, 'views', 'health.schemas.ts'), 'developer change');
    expectCode('DIRTY_TARGET', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.readFileSync(path.join(root, 'views', 'health.schemas.ts'), 'utf8'), 'developer change');
  });
  withFixture({}, ({ root }) => {
    fs.mkdirSync(path.join(root, '.aicrm-dedup', 'donor-views.lock'), { recursive: true });
    expectCode('CONCURRENT_MATERIALIZATION', () => applyMaterialization(root, 'source-index.json'));
  });
  withFixture({ secondContent: true }, ({ root }) => {
    expectCode('MISSING_FILE', () => applyMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'first.ts')), false);
  });
});

test('fails verification and cleanup when an enabled view is absent from the receipt', () => {
  withFixture({}, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    writeJSON(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json'), { schema_version: 1, targets: [] });
    expectCode('RECEIPT_INCOMPLETE', () => verifyMaterialization(root, 'source-index.json'));
    expectCode('RECEIPT_INCOMPLETE', () => cleanMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), true);
  });
});

test("rolls back only this invocation's generated views when receipt publication fails", () => {
  withFixture({}, ({ root }) => {
    assert.throws(
      () => applyMaterialization(root, 'source-index.json', { writeReceipt: () => { throw new Error('injected receipt write failure'); } }),
      /injected receipt write failure/,
    );
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), false);
    assert.equal(fs.existsSync(path.join(root, '.aicrm-dedup', 'donor-views-receipt.json')), false);
    assert.deepEqual(applyMaterialization(root, 'source-index.json').created, ['views/health.schemas.ts']);
  });
});

test('refuses to verify or clean a modified or later-tracked generated view', () => {
  withFixture({}, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    fs.appendFileSync(path.join(root, 'views', 'health.schemas.ts'), 'developer change');
    expectCode('DIRTY_TARGET', () => verifyMaterialization(root, 'source-index.json'));
    expectCode('DIRTY_TARGET', () => cleanMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), true);
  });
  withFixture({}, ({ root }) => {
    applyMaterialization(root, 'source-index.json');
    execFileSync('git', ['init', '--quiet', root]);
    execFileSync('git', ['-C', root, 'add', 'views/health.schemas.ts']);
    expectCode('TRACKED_TARGET', () => verifyMaterialization(root, 'source-index.json'));
    expectCode('TRACKED_TARGET', () => cleanMaterialization(root, 'source-index.json'));
    assert.equal(fs.existsSync(path.join(root, 'views', 'health.schemas.ts')), true);
  });
});

test('requires explicit and provably-safe lock recovery', () => {
  withFixture({}, ({ root }) => {
    const lock = path.join(root, ...LOCK_PATH.split('/'));
    const owner = path.join(lock, 'owner.json');
    fs.mkdirSync(lock, { recursive: true, mode: 0o700 });
    expectCode('CONCURRENT_MATERIALIZATION', () => applyMaterialization(root, 'source-index.json'));
    expectCode('LOCK_RECOVERY_REQUIRED', () => recoverMaterializationLock(root));

    writeJSON(owner, { schema_version: 1, host: os.hostname(), pid: process.pid, lock_id: 'active-lock', created_at: new Date().toISOString() });
    expectCode('LOCK_RECOVERY_REQUIRED', () => recoverMaterializationLock(root));

    writeJSON(owner, { schema_version: 1, host: 'another-host', pid: 2147483647, lock_id: 'remote-lock', created_at: new Date().toISOString() });
    expectCode('LOCK_RECOVERY_REQUIRED', () => recoverMaterializationLock(root));

    writeJSON(owner, { schema_version: 1, host: os.hostname(), pid: 2147483647, lock_id: 'dead-lock', created_at: new Date().toISOString() });
    assert.deepEqual(recoverMaterializationLock(root), { action: 'recover-lock', recovered: true, lock_id: 'dead-lock' });
    assert.equal(fs.existsSync(lock), false);
    assert.deepEqual(recoverMaterializationLock(root), { action: 'recover-lock', recovered: false, reason: 'lock_absent' });
  });
});
