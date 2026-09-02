#!/usr/bin/env bash
set -euo pipefail

installer="deploy/install-release.sh"
start_line="$(rg -n '^if ! systemctl enable --now aicrm-effects-worker\\.service; then$' "$installer" | cut -d: -f1)"
test -n "$start_line" || { echo "effects worker start must be rollback guarded" >&2; exit 1; }
rollback_line="$(rg -n '^  rollback$' "$installer" | tail -1 | cut -d: -f1)"
exit_line="$(rg -n '^  exit 8$' "$installer" | cut -d: -f1)"
restart_line="$(rg -n '^    systemctl restart aicrm-effects-worker\\.service \\|\\| true$' "$installer" | cut -d: -f1)"
test -n "$rollback_line" && test -n "$exit_line" && test -n "$restart_line" || {
  echo "effects worker failure must restart the previous compatible worker and exit" >&2; exit 1;
}
test "$start_line" -lt "$rollback_line" && test "$rollback_line" -lt "$exit_line" || {
  echo "effects worker rollback order is invalid" >&2; exit 1;
}
