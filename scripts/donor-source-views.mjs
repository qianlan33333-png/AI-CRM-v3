import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

export const INDEX_SCHEMA_VERSION = 1;
export const RECEIPT_SCHEMA_VERSION = 1;
export const RECEIPT_PATH = '.aicrm-dedup/donor-views-receipt.json';
export const LOCK_PATH = '.aicrm-dedup/donor-views.lock';

export class DonorViewError extends Error {
  constructor(code, message) {
    super(message);
    this.name = 'DonorViewError';
    this.code = code;
  }
}

function fail(code, message) {
  throw new DonorViewError(code, message);
}

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function requireString(value, label) {
  if (typeof value !== 'string' || value.length === 0) fail('INVALID_INDEX', `${label} must be a non-empty string`);
  return value;
}

function requireArray(value, label) {
  if (!Array.isArray(value)) fail('INVALID_INDEX', `${label} must be an array`);
  return value;
}

function isHex(value, length) {
  return typeof value === 'string' && new RegExp(`^[0-9a-f]{${length}}$`).test(value);
}

export function sha256(bytes) {
  return crypto.createHash('sha256').update(bytes).digest('hex');
}

export function gitBlobSHA1(bytes) {
  return crypto.createHash('sha1').update(`blob ${bytes.byteLength}\0`).update(bytes).digest('hex');
}

function normalizeLogicalPath(value, label) {
  requireString(value, label);
  if (value.includes('\\') || path.posix.isAbsolute(value) || value.startsWith('./') || value.includes('\0')) {
    fail('PATH_ESCAPE', `${label} must be a clean repository-relative POSIX path`);
  }
  const normalized = path.posix.normalize(value);
  if (normalized !== value || normalized === '.' || normalized === '..' || normalized.startsWith('../')) {
    fail('PATH_ESCAPE', `${label} escapes or normalizes outside its declared repository path`);
  }
  return normalized;
}

function assertNoSymlinkAncestor(root, absolute, label) {
  const relative = path.relative(root, absolute);
  if (relative === '' || relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    fail('PATH_ESCAPE', `${label} is outside the declared root`);
  }
  let current = root;
  for (const segment of relative.split(path.sep)) {
    current = path.join(current, segment);
    if (!fs.existsSync(current)) break;
    if (fs.lstatSync(current).isSymbolicLink()) fail('SYMLINK_PATH', `${label} traverses a symbolic link: ${relative}`);
  }
}

export function resolveLogicalPath(root, logicalPath, label = 'path') {
  const normalized = normalizeLogicalPath(logicalPath, label);
  const absolute = path.resolve(root, ...normalized.split('/'));
  assertNoSymlinkAncestor(root, absolute, label);
  return absolute;
}

function modeString(stat) {
  return `100${(stat.mode & 0o777).toString(8).padStart(3, '0')}`;
}

function readRegularFile(absolute, label) {
  if (!fs.existsSync(absolute)) fail('MISSING_FILE', `${label} is missing`);
  const stat = fs.lstatSync(absolute);
  if (!stat.isFile()) fail('NOT_REGULAR_FILE', `${label} must be a regular file`);
  return { bytes: fs.readFileSync(absolute), mode: modeString(stat) };
}

function parseJSON(absolute, label) {
  let parsed;
  try {
    parsed = JSON.parse(readRegularFile(absolute, label).bytes.toString('utf8'));
  } catch (error) {
    fail('INVALID_JSON', `${label} is not valid JSON: ${error.message}`);
  }
  if (!isPlainObject(parsed)) fail('INVALID_INDEX', `${label} must contain a JSON object`);
  return parsed;
}

function unique(items, selector, label) {
  const seen = new Set();
  for (const item of items) {
    const key = selector(item);
    if (seen.has(key)) fail('DUPLICATE_DECLARATION', `${label} repeats ${key}`);
    seen.add(key);
  }
}

function requireMode(value, label) {
  if (value !== '100644') fail('INVALID_INDEX', `${label} must be 100644 for the pilot`);
  return value;
}

function validateIndex(index) {
  if (index.schema_version !== INDEX_SCHEMA_VERSION) fail('INVALID_INDEX', 'unsupported source-index schema_version');
  index.lock_path = normalizeLogicalPath(index.lock_path, 'lock_path');
  const libraries = requireArray(index.libraries, 'libraries');
  const contents = requireArray(index.contents, 'contents');
  const bindings = requireArray(index.bindings, 'bindings');
  const views = requireArray(index.views, 'views');
  unique(libraries, (item) => requireString(item.id, 'library.id'), 'library id');
  unique(contents, (item) => requireString(item.id, 'content.id'), 'content id');
  unique(bindings, (item) => `${requireString(item.module, 'binding.module')}\0${normalizeLogicalPath(item.logical_path, 'binding.logical_path')}`, 'binding');
  unique(views, (item) => normalizeLogicalPath(item.target_path, 'view.target_path'), 'view target');

  const libraryByID = new Map();
  for (const library of libraries) {
    if (!isPlainObject(library) || library.immutable !== true) fail('INVALID_INDEX', 'every source library must be an immutable object');
    requireString(library.source_repository, 'library.source_repository');
    if (!isHex(library.source_commit, 40)) fail('INVALID_INDEX', 'library.source_commit must be a 40-hex commit');
    library.root = normalizeLogicalPath(library.root, 'library.root');
    libraryByID.set(library.id, library);
  }

  const contentByID = new Map();
  const canonicalHashes = new Set();
  const canonicalPaths = new Set();
  for (const content of contents) {
    if (!isPlainObject(content) || !libraryByID.has(content.library_id)) fail('INVALID_INDEX', 'content must name an existing immutable library');
    content.canonical_path = normalizeLogicalPath(content.canonical_path, 'content.canonical_path');
    content.source_path = normalizeLogicalPath(content.source_path, 'content.source_path');
    if (!content.canonical_path.startsWith(`${libraryByID.get(content.library_id).root}/`)) {
      fail('INVALID_INDEX', `canonical path is outside immutable library: ${content.canonical_path}`);
    }
    if (!isHex(content.source_git_blob_sha, 40) || !isHex(content.content_sha256, 64)) fail('INVALID_INDEX', 'content hashes must be fixed SHA-1/SHA-256 values');
    if (!Number.isSafeInteger(content.bytes) || content.bytes < 0) fail('INVALID_INDEX', 'content.bytes must be a non-negative integer');
    requireMode(content.mode, 'content.mode');
    if (canonicalHashes.has(content.content_sha256) || canonicalPaths.has(content.canonical_path)) {
      fail('DUPLICATE_CANONICAL_CONTENT', 'one payload may have only one canonical entry');
    }
    canonicalHashes.add(content.content_sha256);
    canonicalPaths.add(content.canonical_path);
    contentByID.set(content.id, content);
  }

  for (const binding of bindings) {
    if (!isPlainObject(binding) || !contentByID.has(binding.content_id)) fail('INVALID_INDEX', 'binding must name an existing canonical content id');
    binding.logical_path = normalizeLogicalPath(binding.logical_path, 'binding.logical_path');
    binding.source_path = normalizeLogicalPath(binding.source_path, 'binding.source_path');
    requireString(binding.source_repository, 'binding.source_repository');
    if (!isHex(binding.source_commit, 40) || !isHex(binding.source_git_blob_sha, 40)) fail('INVALID_INDEX', 'binding must record exact source commit/blob');
    requireMode(binding.mode, 'binding.mode');
    requireString(binding.usage, 'binding.usage');
    requireString(binding.freeze_gate, 'binding.freeze_gate');
    requireString(binding.freeze_ledger, 'binding.freeze_ledger');
    if (binding.current_path_state !== 'tracked_pre_p2') fail('INVALID_INDEX', 'PR-2 pilot bindings must remain tracked_pre_p2');
    const content = contentByID.get(binding.content_id);
    const library = libraryByID.get(content.library_id);
    if (
      binding.source_repository !== library.source_repository
      || binding.source_commit !== library.source_commit
      || binding.source_path !== content.source_path
      || binding.source_git_blob_sha !== content.source_git_blob_sha
    ) {
      fail('SOURCE_VERSION_MISMATCH', `binding source identity differs from immutable canonical source: ${binding.logical_path}`);
    }
  }

  for (const view of views) {
    if (!isPlainObject(view) || !contentByID.has(view.content_id)) fail('INVALID_INDEX', 'view must name an existing canonical content id');
    view.target_path = normalizeLogicalPath(view.target_path, 'view.target_path');
    if (canonicalPaths.has(view.target_path)) fail('INVALID_INDEX', 'view target cannot be a canonical source path');
    if (typeof view.enabled !== 'boolean') fail('INVALID_INDEX', 'view.enabled must be boolean');
  }

  return { index, libraryByID, contentByID };
}

export function loadSourceIndex(root, indexPath = 'web/donor-sources/source-index.json') {
  const normalizedRoot = path.resolve(root);
  if (!fs.existsSync(normalizedRoot) || !fs.lstatSync(normalizedRoot).isDirectory()) fail('INVALID_ROOT', 'root must be an existing directory');
  const normalizedIndex = normalizeLogicalPath(indexPath, 'index_path');
  const absoluteIndex = resolveLogicalPath(normalizedRoot, normalizedIndex, 'index_path');
  const index = parseJSON(absoluteIndex, 'source-index');
  const validated = validateIndex(index);
  const absoluteLock = resolveLogicalPath(normalizedRoot, validated.index.lock_path, 'lock_path');
  return { root: normalizedRoot, indexPath: normalizedIndex, absoluteIndex, absoluteLock, ...validated };
}

function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`;
  if (isPlainObject(value)) return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(value[key])}`).join(',')}}`;
  return JSON.stringify(value);
}

function lockedEntries(loaded) {
  return [...loaded.contentByID.values()].map((content) => {
    const library = loaded.libraryByID.get(content.library_id);
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
  }).sort((left, right) => left.id.localeCompare(right.id));
}

function verifySourceLock(loaded) {
  const lock = parseJSON(loaded.absoluteLock, 'source lock');
  if (lock.schema_version !== INDEX_SCHEMA_VERSION || !Array.isArray(lock.entries)) fail('INVALID_LOCK', 'source lock schema is invalid');
  const expected = lockedEntries(loaded);
  if (stableJSON(lock.entries) !== stableJSON(expected)) fail('SOURCE_LOCK_MISMATCH', 'source lock differs from immutable source-index entries');
  return lock;
}

function checkContent(root, content) {
  const absolute = resolveLogicalPath(root, content.canonical_path, `canonical:${content.id}`);
  const file = readRegularFile(absolute, `canonical:${content.id}`);
  if (file.bytes.byteLength !== content.bytes) fail('CANONICAL_SIZE_MISMATCH', `canonical:${content.id} has unexpected byte count`);
  if (sha256(file.bytes) !== content.content_sha256) fail('CANONICAL_HASH_MISMATCH', `canonical:${content.id} SHA-256 differs from the immutable index`);
  if (gitBlobSHA1(file.bytes) !== content.source_git_blob_sha) fail('CANONICAL_GIT_BLOB_MISMATCH', `canonical:${content.id} Git blob SHA-1 differs from the immutable index`);
  if (file.mode !== content.mode) fail('CANONICAL_MODE_MISMATCH', `canonical:${content.id} mode differs from the immutable index`);
  return { ...content, absolute, file_bytes: file.bytes };
}

function checkBinding(root, binding, content) {
  const absolute = resolveLogicalPath(root, binding.logical_path, `binding:${binding.logical_path}`);
  const file = readRegularFile(absolute, `binding:${binding.logical_path}`);
  if (sha256(file.bytes) !== content.content_sha256 || file.bytes.byteLength !== content.bytes || file.mode !== content.mode) {
    fail('BINDING_DRIFT', `binding:${binding.logical_path} differs from canonical ${content.id}`);
  }
}

function sourceSummary(loaded, checked) {
  return {
    index_path: loaded.indexPath,
    canonical_contents: checked.map((content) => ({ id: content.id, canonical_path: content.canonical_path, content_sha256: content.content_sha256, bytes: content.bytes })),
    bindings_verified: loaded.index.bindings.length,
    planned_views: loaded.index.views.filter((view) => !view.enabled).length,
    enabled_views: loaded.index.views.filter((view) => view.enabled).length,
  };
}

export function verifySourceIndex(root, indexPath) {
  const { loaded, checked } = loadValidated(root, indexPath);
  return sourceSummary(loaded, checked);
}

function receiptAbsolute(root) {
  return resolveLogicalPath(root, RECEIPT_PATH, 'receipt_path');
}

function loadReceipt(root) {
  const absolute = receiptAbsolute(root);
  if (!fs.existsSync(absolute)) return { absolute, receipt: { schema_version: RECEIPT_SCHEMA_VERSION, targets: [] } };
  const receipt = parseJSON(absolute, 'materialization receipt');
  if (receipt.schema_version !== RECEIPT_SCHEMA_VERSION || !Array.isArray(receipt.targets)) fail('INVALID_RECEIPT', 'materialization receipt schema is invalid');
  unique(receipt.targets, (item) => normalizeLogicalPath(item.target_path, 'receipt.target_path'), 'receipt target');
  return { absolute, receipt };
}

function writeAtomically(root, absolute, bytes, mode, { replaceOwnedFile = false } = {}) {
  assertNoSymlinkAncestor(root, absolute, 'atomic target');
  const parent = path.dirname(absolute);
  fs.mkdirSync(parent, { recursive: true, mode: 0o755 });
  assertNoSymlinkAncestor(root, absolute, 'atomic target');
  const temporary = path.join(parent, `.${path.basename(absolute)}.aicrm-dedup-${process.pid}-${crypto.randomUUID()}`);
  try {
    fs.writeFileSync(temporary, bytes, { mode: parseInt(mode.slice(-3), 8), flag: 'wx' });
    fs.chmodSync(temporary, parseInt(mode.slice(-3), 8));
    if (replaceOwnedFile) {
      fs.renameSync(temporary, absolute);
    } else {
      try {
        // An exclusive same-directory link publishes a normal file only when
        // the target remains absent. Removing the temporary name leaves one
        // link, so views are never persistent hard links to their source.
        fs.linkSync(temporary, absolute);
        fs.rmSync(temporary);
      } catch (error) {
        if (error?.code === 'EEXIST') fail('DIRTY_TARGET', `target appeared during materialization: ${path.basename(absolute)}`);
        throw error;
      }
    }
  } finally {
    if (fs.existsSync(temporary)) fs.rmSync(temporary, { force: true });
  }
}

function isTracked(root, logicalPath) {
  if (!fs.existsSync(path.join(root, '.git'))) return false;
  const result = spawnSync('git', ['-C', root, 'ls-files', '--error-unmatch', '--', logicalPath], { encoding: 'utf8' });
  return result.status === 0;
}

function indexDigest(loaded) {
  return sha256(readRegularFile(loaded.absoluteIndex, 'source-index').bytes);
}

function receiptRecord(view, content) {
  return {
    target_path: view.target_path,
    content_id: content.id,
    canonical_path: content.canonical_path,
    content_sha256: content.content_sha256,
    bytes: content.bytes,
    mode: content.mode,
  };
}

function matchesReceipt(record, content) {
  return isPlainObject(record)
    && record.content_id === content.id
    && record.canonical_path === content.canonical_path
    && record.content_sha256 === content.content_sha256
    && record.bytes === content.bytes
    && record.mode === content.mode;
}

function validateReceipt(loaded, receipt) {
  if (!isHex(receipt.index_sha256, 64) && receipt.targets.length !== 0) {
    fail('INVALID_RECEIPT', 'materialization receipt lacks an index SHA-256');
  }
  if (receipt.targets.length !== 0 && receipt.index_sha256 !== indexDigest(loaded)) {
    fail('STALE_RECEIPT', 'source index changed since materialized views were created');
  }
  const viewByTarget = new Map(loaded.index.views.map((view) => [view.target_path, view]));
  for (const record of receipt.targets) {
    if (!isPlainObject(record)) fail('INVALID_RECEIPT', 'materialization receipt target must be an object');
    const view = viewByTarget.get(normalizeLogicalPath(record.target_path, 'receipt.target_path'));
    if (!view || !view.enabled) fail('INVALID_RECEIPT', `receipt refers to an unknown or disabled view: ${record.target_path}`);
    const content = loaded.contentByID.get(view.content_id);
    if (!matchesReceipt(record, content)) fail('INVALID_RECEIPT', `receipt content does not match source index: ${record.target_path}`);
  }
}

function acquireLock(root) {
  const absolute = resolveLogicalPath(root, LOCK_PATH, 'lock_path');
  fs.mkdirSync(path.dirname(absolute), { recursive: true, mode: 0o755 });
  assertNoSymlinkAncestor(root, absolute, 'lock_path');
  try {
    fs.mkdirSync(absolute, { mode: 0o700 });
  } catch (error) {
    if (error?.code === 'EEXIST') fail('CONCURRENT_MATERIALIZATION', 'another donor-view materialization or cleanup holds the lock');
    throw error;
  }
  return () => fs.rmSync(absolute, { recursive: true, force: true });
}

function checkedContents(loaded) {
  const checked = [...loaded.contentByID.values()].map((content) => checkContent(loaded.root, content));
  const byID = new Map(checked.map((content) => [content.id, content]));
  for (const binding of loaded.index.bindings) checkBinding(loaded.root, binding, byID.get(binding.content_id));
  return { checked, byID };
}

function loadValidated(root, indexPath) {
  const loaded = loadSourceIndex(root, indexPath);
  verifySourceLock(loaded);
  const { checked, byID } = checkedContents(loaded);
  return { loaded, checked, byID };
}

export function planMaterialization(root, indexPath) {
  const { loaded, checked } = loadValidated(root, indexPath);
  return {
    ...sourceSummary(loaded, checked),
    action: 'plan',
    materialized_view_targets: loaded.index.views.filter((view) => view.enabled).map((view) => view.target_path).sort(),
  };
}

function assertWritableTarget(root, loaded, receipt, view, content) {
  const absolute = resolveLogicalPath(root, view.target_path, `view:${view.target_path}`);
  if (isTracked(root, view.target_path)) fail('TRACKED_TARGET', `refusing to overwrite tracked path: ${view.target_path}`);
  const known = receipt.targets.find((record) => record.target_path === view.target_path);
  if (!fs.existsSync(absolute)) {
    if (known) fail('MISSING_GENERATED_TARGET', `receipt target is missing: ${view.target_path}`);
    return { absolute, existing: false };
  }
  const actual = readRegularFile(absolute, `view:${view.target_path}`);
  if (!known || !matchesReceipt(known, content)) fail('DIRTY_TARGET', `target exists but is not a generated view: ${view.target_path}`);
  if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
    fail('DIRTY_TARGET', `generated target was modified: ${view.target_path}`);
  }
  return { absolute, existing: true };
}

function writeReceipt(root, loaded, absolute, targets) {
  const receipt = {
    schema_version: RECEIPT_SCHEMA_VERSION,
    index_sha256: indexDigest(loaded),
    targets: [...targets].sort((left, right) => left.target_path.localeCompare(right.target_path)),
  };
  writeAtomically(root, absolute, Buffer.from(`${JSON.stringify(receipt, null, 2)}\n`), '100644', { replaceOwnedFile: true });
}

export function applyMaterialization(root, indexPath) {
  const { loaded, checked, byID } = loadValidated(root, indexPath);
  const enabled = loaded.index.views.filter((view) => view.enabled);
  if (enabled.length === 0) return { ...sourceSummary(loaded, checked), action: 'apply', created: [], reused: [], no_enabled_views: true };
  const release = acquireLock(loaded.root);
  const created = [];
  try {
    const receiptState = loadReceipt(loaded.root);
    validateReceipt(loaded, receiptState.receipt);
    const prepared = enabled.map((view) => ({ view, content: byID.get(view.content_id), ...assertWritableTarget(loaded.root, loaded, receiptState.receipt, view, byID.get(view.content_id)) }));
    for (const item of prepared) {
      if (item.existing) continue;
      try {
        writeAtomically(loaded.root, item.absolute, item.content.file_bytes, item.content.mode);
        created.push(item);
      } catch (error) {
        for (const written of created) fs.rmSync(written.absolute, { force: true });
        throw error;
      }
    }
    const retained = receiptState.receipt.targets.filter((record) => !enabled.some((view) => view.target_path === record.target_path));
    const records = [...retained, ...enabled.map((view) => receiptRecord(view, byID.get(view.content_id)))];
    writeReceipt(loaded.root, loaded, receiptState.absolute, records);
    return {
      ...sourceSummary(loaded, checked), action: 'apply',
      created: created.map((item) => item.view.target_path).sort(),
      reused: prepared.filter((item) => item.existing).map((item) => item.view.target_path).sort(),
    };
  } finally {
    release();
  }
}

export function verifyMaterialization(root, indexPath) {
  const { loaded, checked, byID } = loadValidated(root, indexPath);
  const receiptState = loadReceipt(loaded.root);
  validateReceipt(loaded, receiptState.receipt);
  for (const record of receiptState.receipt.targets) {
    const content = byID.get(record.content_id);
    const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
    const actual = readRegularFile(absolute, `receipt:${record.target_path}`);
    if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
      fail('DIRTY_TARGET', `materialized view drifted: ${record.target_path}`);
    }
  }
  return { ...sourceSummary(loaded, checked), action: 'verify', materialized_views_verified: receiptState.receipt.targets.map((record) => record.target_path).sort() };
}

export function cleanMaterialization(root, indexPath) {
  const { loaded, checked, byID } = loadValidated(root, indexPath);
  const release = acquireLock(loaded.root);
  try {
    const receiptState = loadReceipt(loaded.root);
    validateReceipt(loaded, receiptState.receipt);
    for (const record of receiptState.receipt.targets) {
      const content = byID.get(record.content_id);
      const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
      const actual = readRegularFile(absolute, `receipt:${record.target_path}`);
      if (actual.bytes.byteLength !== content.bytes || sha256(actual.bytes) !== content.content_sha256 || actual.mode !== content.mode) {
        fail('DIRTY_TARGET', `refusing to clean a modified generated view: ${record.target_path}`);
      }
    }
    const removed = [];
    for (const record of receiptState.receipt.targets) {
      const absolute = resolveLogicalPath(loaded.root, record.target_path, `receipt:${record.target_path}`);
      fs.rmSync(absolute);
      removed.push(record.target_path);
    }
    if (fs.existsSync(receiptState.absolute)) fs.rmSync(receiptState.absolute);
    return { ...sourceSummary(loaded, checked), action: 'clean', removed: removed.sort() };
  } finally {
    release();
  }
}
