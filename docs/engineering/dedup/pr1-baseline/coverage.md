# PR-1 duplicate-source baseline coverage

Target commit: `5291366b9742030957f48ebf7464a040a3ab46db`
Target tree: `f7fb3e57accfa4c4afce08e6546fdd10661dbd7c`
Historical seed baseline: `05045c645f95d269b624771ceb215713e3300f59`
Historical tree: `0747d8a4165283fe15e5b7caa5663015eec2c41d`

## Classification

- OneID / external identity: not involved; this audit reads only Git objects.
- Persistence / internal tasks / provider effects: not involved; no database, queue, provider or build command ran.
- PR-1 action: audit only. No tracked source was removed, linked, imported, generated or rewritten.

## Layer A: exact Git-object inventory

- Target tracked entries: **1937**; blob paths verified: **1937/1937**.
- Target unique blob objects: **1781/1781**.
- Target exact groups: **74**; paths in groups: **230**; additional logical path bytes: **10,234,871**.
- Baseline tracked entries: **1817**; blob paths verified: **1817/1817**.
- Baseline exact groups: **74**; paths in groups: **230**; additional logical path bytes: **10,234,871**.
- Read errors: target `0`, baseline `0`. Target LFS pointers `0`, submodules `0`.

### Target top-level coverage

| Root | Entries | Blobs | Verified blobs |
|---|---:|---:|---:|
| `(root files)` | 12 | 12 | 12 |
| `.github` | 5 | 5 | 5 |
| `acceptance` | 1 | 1 | 1 |
| `api` | 1 | 1 | 1 |
| `cmd` | 155 | 155 | 155 |
| `deploy` | 22 | 22 | 22 |
| `docs` | 129 | 129 | 129 |
| `internal` | 939 | 939 | 939 |
| `journeys` | 5 | 5 | 5 |
| `migrations` | 101 | 101 | 101 |
| `modules` | 1 | 1 | 1 |
| `scripts` | 57 | 57 | 57 |
| `skills` | 1 | 1 | 1 |
| `web` | 508 | 508 | 508 |

## Seed evidence

- 05045 seed groups verified exactly: **39/39**.
- Those seed groups unchanged at target: **39/39**.
- Each group/path/size result is in `exact-duplicates.json`; this is a revalidation, not deletion authorization.

## Layers B and C: candidate-only analysis

- Mechanical candidates: **0**. Only line-ending and trailing-space/tab normalization were applied; comments, headers, encoding, modes and behavior remain meaningful.
- Lexical shared-block candidates: **411** across **1533** bounded unique UTF-8 source objects.
- C-layer exclusions: {"binary_or_non_utf8": 6, "fewer_than_window_lines": 25, "over_bounded_bytes": 4, "unsupported_suffix": 213}. This is not parser, AST, type, data-flow or semantic analysis.

## Consumer and decision closure

- Static target paths mapped: **497**; readable text blobs scanned: **1931**.
- Exact decisions: **74**, all retain every path in PR-1. Every group remains `dependency_unresolved`; no source-of-truth or deletion decision is approved by this report.
- The map includes only resolvable literals plus ambiguous lexical mentions. Dynamic imports, shell expansion, runtime routing, generated assets and release staging still need owner review before P1/P2.

## Artifacts

- `inventory.json`: all target tracked entries, including binary, empty files, modes and special-entry status.
- `exact-duplicates.json`: all exact groups and 39-seed verification across both pinned commits.
- `near-duplicate-candidates.json`: B/C candidate methods, results and explicit limits.
- `dependency-map.json`: per-target static consumer observations and unresolved boundary.
- `dedup-decisions.json`: every exact group has an owner role, proposed-only canonical path, exception and blocked action.
- `provenance.json`: direct GitHub clone, pinned commits/trees, object-scan inputs, tool hashes and pre-output checkout state.

**Closure state: PR-1 audit evidence is complete for Layer A and the documented bounded B/C/static-D methods, but no group is safe to delete. P1 is blocked on named owner confirmation and dynamic/build/release consumer closure.**
