#!/usr/bin/env bash
set -euo pipefail

installer="deploy/install-release.sh"
start_line="$(rg -n '^if ! systemctl enable --now aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
test -n "$start_line" || { echo "effects worker start must be rollback guarded" >&2; exit 1; }
exit_line="$(rg -n '^  exit 8$' "$installer" | cut -d: -f1)"
restart_line="$(rg -n '^    systemctl restart aicrm-effects-worker\.service \|\| true$' "$installer" | cut -d: -f1)"
active_line="$(rg -n '^if ! systemctl is-active --quiet aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
active_exit_line="$(rg -n '^  exit 9$' "$installer" | cut -d: -f1)"
test -n "$exit_line" && test -n "$restart_line" && test -n "$active_line" && test -n "$active_exit_line" || {
  echo "effects worker failure must restart the previous compatible worker and exit" >&2; exit 1;
}
test "$(sed -n "$((start_line + 1))p" "$installer")" = "  rollback" && test "$((start_line + 2))" -eq "$exit_line" || {
  echo "effects worker rollback order is invalid" >&2; exit 1;
}
test "$(sed -n "$((active_line + 1))p" "$installer")" = "  rollback" && test "$((active_line + 2))" -eq "$active_exit_line" || {
  echo "effects worker must be active after enable" >&2; exit 1;
}
