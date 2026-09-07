#!/usr/bin/env bash
set -euo pipefail

# Prepare source views for a direct Go consumer without mutating tracked
# compatibility paths. Before PR-4 this validates exact tracked bytes; after
# an approved removal it materializes only the declared untracked views.
repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ "$#" -gt 0 ]] || { echo "usage: scripts/run-go-with-donor-views.sh COMMAND [ARGUMENT ...]" >&2; exit 2; }
node "$repository/scripts/prepare-donor-source-views.mjs" --root "$repository" >/dev/null
exec "$@"
