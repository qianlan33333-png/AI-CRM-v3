#!/usr/bin/env bash
set -euo pipefail

# Run a direct Go consumer with every declared compatibility view prepared in
# an otherwise clean disposable worktree. This is deliberately not a package
# hook: callers opt in at the process boundary and the wrapper restores every
# tracked target on exit.
repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$#" -gt 0 ]] || { echo "usage: scripts/run-go-with-donor-views.sh COMMAND [ARGUMENT ...]" >&2; exit 2; }
exec env AICRM_DEDUP_DISPOSABLE_WORKTREE=1 \
  node "$repository/scripts/run-with-donor-views.mjs" --root "$repository" -- "$@"
