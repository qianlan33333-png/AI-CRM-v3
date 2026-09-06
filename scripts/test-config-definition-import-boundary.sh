#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/aicrm-config-definition-boundary.XXXXXX")"
sandbox="$temporary_root/repository"
cleanup() {
  git -C "$repository_root" worktree remove --force "$sandbox" >/dev/null 2>&1 || true
  rm -rf "$temporary_root"
}
trap cleanup EXIT

git -C "$repository_root" worktree add --detach "$sandbox" HEAD >/dev/null
# Exercise the gate under test, including its uncommitted change, while every
# fixture mutation remains isolated in the disposable worktree.
cp "$repository_root/scripts/check-config-definition-import-boundary.sh" "$sandbox/scripts/check-config-definition-import-boundary.sh"
chmod +x "$sandbox/scripts/check-config-definition-import-boundary.sh"
"$sandbox/scripts/check-config-definition-import-boundary.sh" >/dev/null

runtime_main="$sandbox/cmd/migrate-v2-runtime-config-releases/main.go"
python3 - "$runtime_main" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
old = "FROM config_releases ORDER BY id"
if old not in text:
    raise SystemExit("runtime history source query fixture missing")
path.write_text(text.replace(old, "FROM config_releases JOIN legacy_config_values ON TRUE ORDER BY id", 1), encoding="utf-8")
PY
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/source.out" 2>&1; then
  echo "runtime history gate accepted an unapproved source table" >&2
  exit 1
fi
grep -q 'runtime history read is outside its source/ledger allowlist: legacy_config_values' "$temporary_root/source.out"

git -C "$sandbox" checkout -- cmd/migrate-v2-runtime-config-releases/main.go
python3 - "$runtime_main" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
needle = "func apply(ctx context.Context, pool *pgxpool.Pool, s snapshot, manifestDigest [32]byte) (result, error) {"
if needle not in text:
    raise SystemExit("runtime history apply fixture missing")
path.write_text(text.replace(needle, needle + "\n\t_, _ = pool.Exec(ctx, `INSERT INTO config_runtime_releases(id) VALUES (1)`)", 1), encoding="utf-8")
PY
if "$sandbox/scripts/check-config-definition-import-boundary.sh" >"$temporary_root/target.out" 2>&1; then
  echo "runtime history gate accepted a live runtime write" >&2
  exit 1
fi
grep -q 'runtime history table is outside its approved scope: config_runtime_releases' "$temporary_root/target.out"

echo 'config-definition and runtime-history import boundaries passed'
