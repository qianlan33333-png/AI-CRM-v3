# Duplicate-source governance

This document is the repository-maintained execution contract for
`PRD-ENG-DEDUP-001`. It is deliberately shorter than the approved attachment:
it records only the mechanics, invariants, gates, and rollback rules that later
changes must preserve.

## Scope and provenance

- Approved PRD attachment SHA-256: `f210ceb92e2ff711b95a7cf937f482dd6ae7b392fa1ed76e72b3522b416f3be7` (`/Users/qianlan/Downloads/2.md` when this contract was adopted).
- Approved seed-audit attachment SHA-256: `e0886cde7ab7deb7a7508beb8d10aff753f9afb482bb63128821ba4aedabf9ee` (`/Users/qianlan/Downloads/3.md`).
- P0 object baseline: `05045c645f95d269b624771ceb215713e3300f59`.
- P0 complete-tree audit target: `5291366b9742030957f48ebf7464a040a3ab46db`; see `docs/engineering/dedup/pr1-baseline/`.
- This work concerns Git-tracked source and build metadata only. It does not change OneID, persistence, jobs, Provider effects, API behavior, database migrations, or legacy runtime dependencies.

## Source and view invariants

1. A canonical payload has one declared authority. Frozen donor payloads live beneath `web/donor-sources/`; an active V3 contract may remain at its reviewed V3 source path when it is the authority (currently `api/openapi.yaml`). Its library records repository, immutable commit, path, Git blob SHA-1, SHA-256, bytes, and mode. Verification recomputes both hashes from the canonical bytes; a syntactically valid but different source commit/blob is rejected.
2. `web/donor-sources/source-index.json` binds every logical path to a canonical content ID. Bindings retain their module, logical path, source repository/commit/path/blob, usage, freeze gate, ledger, and mode.
3. `web/donor-sources/source-lock.json` repeats the immutable content identities. The verifier rejects an index/lock mismatch. A later P5 base-diff gate must require explicit review whenever either the index, lock, or canonical library payload changes; a coordinated edit cannot be treated as an ordinary consumer change.
4. Frozen consumers may never depend on mutable `web/src` content as their authority. If an active V3 behavior diverges, it must become an explicit V3 adapter or derived source with its own Owner and tests; it cannot edit a generated compatibility view.
5. Compatibility views are normal untracked files, copied only from a whitelist in the index. They are never symlinks or hard links. The materializer rejects `..`, absolute/backslash paths, symlink ancestors, duplicate targets, canonical targets, tracked targets, unknown targets, dirty targets, missing sources, stale receipts, and a held lock.
6. A receipt at `.aicrm-dedup/donor-views-receipt.json` records only files created by this tool. It is keyed by the source-index digest and canonical payload hashes. Cleanup verifies every recorded target before deleting only those paths; it never traverses a directory or cleans an unlisted file.
7. Writes use a same-directory temporary file and atomic publication. A view is published exclusively without overwriting a target that appeared after validation; the tool-owned receipt is atomically renamed while the lock is held. If the receipt write fails, the tool removes only views created in that invocation after rechecking their bytes and mode; it preserves any target that changed concurrently and reports rollback failure rather than deleting it. The prior receipt remains unchanged.
8. The lock directory contains a private owner record with host, PID, random lock ID, and creation time. Normal `apply` and `clean` never remove a pre-existing lock. `recover-lock` is an explicit operator action: it only removes a lock whose well-formed owner record is on this host and whose PID returns `ESRCH`; active, permission-denied, remote-host, missing, malformed, or changed owner records require manual inspection. It never performs automatic stale-lock cleanup.

PR-2 used `health.schemas.ts` as its mechanism pilot. PR-3 declares every P0-derived build view: 229 paths backed by 74 canonical contents. The sole active authority, `api/openapi.yaml`, remains tracked and is never a view; its package-local Go-embed copy is a declared derived view. All 229 derived paths remain tracked and byte-exact until PR-4 approves their exact removal. PR-3 only proves the transition in a clean disposable worktree by staging those exact deletions and materializing untracked replacements.

## Commands and phase rules

Read-only source validation:

```sh
node scripts/verify-donor-sources.mjs
node scripts/materialize-donor-views.mjs --mode plan
node scripts/materialize-donor-views.mjs --mode recover-lock  # only after local dead-PID proof
```

The materializer has `plan`, `apply`, `verify`, `clean`, `clean-stale`, and
explicit `recover-lock` modes. PR-3 adds `prepare-disposable` and
`restore-disposable`, which require `AICRM_DEDUP_DISPOSABLE_WORKTREE=1`; the
command runner uses them to remove exactly the selected tracked paths in a
disposable build worktree, materialize the views, run the selected consumers,
clean the receipt, and restore the tracked paths. This is the only permitted
PR-3 transition proof. It is not a normal developer checkout mutation or a
package-script hook.

`Makefile`, release builders, and direct build scripts automatically run the
non-destructive preparation command. It verifies the tracked derived bytes in
PR-3 and, after an approved PR-4 deletion, materializes only the declared
untracked views. It does not require a clean working tree and it never stages,
removes, or restores a tracked path. A bare `go` command remains an explicit
opt-in through this safe helper:

```sh
make check
scripts/build-linux.sh amd64
scripts/run-go-with-donor-views.sh go test ./cmd/aicrm
```

For an approved canonical-source update after PR-4, ordinary `clean` correctly
rejects the stale receipt. Run `clean-stale` only when the current index still
declares every old target and each target still exactly matches the old receipt;
it removes no user edit or tracked file. Then run `apply` with the reviewed new
source identity. Before switching branches, restore a disposable worktree. If a
normal worktree contains a stale receipt, use `clean-stale` only under those
same exact-byte conditions; otherwise preserve the files and recover manually.

The installer continues to consume only validated built binaries, `web/dist`,
and the release manifest; it must not require Node, Git, a donor checkout, or
source-view materialization.

| Phase | Permitted result | Required proof before advancing |
|---|---|---|
| P0 / PR-1 | Inventory, source/consumer decisions, no deletion | Full object scan, seed verification, no unknown target |
| P1 / PR-2 | Canonical library, lock/index, materializer, isolated pilot | Tamper, missing source, wrong source version, path escape, duplicate target, dirty target, lock, idempotence, cleanup and original-byte tests |
| P2 / PR-3 | Consumer/build/release wiring | Clean checkout runs each affected freeze gate, frontend/Host build, Go embed preparation, direct Go paths, CI artifact and release staging without a tracked-copy fallback |
| P3 / PR-4 | Only explicitly approved tracked payload removal | Pre/post hash, freeze and behavior evidence per group; required test contexts remain independent |
| P4 / PR-5 | Prevention and final ledger | Base-diff/injection gate rejects a new duplicate, new duplicate path, tracked generated view, unknown canonical payload, or unapproved source-index/lock change |

The original PR07 20-logical-file contract, every donor SHA comparison, test
context, template URL/MIME contract, and OpenAPI/Go-embed preparation remain
mandatory. No later phase may lower a count, replace a hash comparison with a
directory check, or add a broad `web/**` ignore rule.

## Rollback

Each phase is independently revertible. Before reverting a view-producing
change, run `clean` against its receipt; if any generated view is dirty, stop
and preserve the developer change. Reverting a source migration must restore
its canonical entry, source-index binding, lock entry, view manifest, consumer
wiring, and the old logical path together. Do not delete a source library or
receipt by broad glob. After rollback, rerun the prior freeze checks and the
relevant source/view verification.
