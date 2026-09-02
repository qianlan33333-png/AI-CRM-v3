#!/usr/bin/env bash
set -euo pipefail

installer="deploy/install-release.sh"
start_line="$(grep -nE '^if ! systemctl enable --now aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
test -n "$start_line" || { echo "effects worker start must be rollback guarded" >&2; exit 1; }
exit_line="$(grep -nE '^  exit 8$' "$installer" | cut -d: -f1)"
restart_line="$(grep -nE '^    systemctl restart aicrm-effects-worker\.service \|\| true$' "$installer" | cut -d: -f1)"
active_line="$(grep -nE '^if ! systemctl is-active --quiet aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
active_exit_line="$(grep -nE '^  exit 9$' "$installer" | cut -d: -f1)"
test -n "$exit_line" && test -n "$restart_line" && test -n "$active_line" && test -n "$active_exit_line" || {
  echo "effects worker failure must restart the previous compatible worker and exit" >&2; exit 1;
}
test "$(sed -n "$((start_line + 1))p" "$installer")" = "  rollback" && test "$((start_line + 2))" -eq "$exit_line" || {
  echo "effects worker rollback order is invalid" >&2; exit 1;
}
test "$(sed -n "$((active_line + 1))p" "$installer")" = "  rollback" && test "$((active_line + 2))" -eq "$active_exit_line" || {
  echo "effects worker must be active after enable" >&2; exit 1;
}

for contract in \
  "deploy/aicrm.service:api" \
  "deploy/aicrm-wecom-worker.service:worker" \
  "deploy/aicrm-effects-worker.service:effects-worker"; do
  unit="${contract%%:*}"
  role="${contract#*:}"
  grep -qxF "ExecStart=/usr/bin/env AICRM_ROLE=${role} /opt/aicrm/current/bin/aicrm" "$unit" || {
    echo "$unit must override the shared environment role at exec time" >&2
    exit 1
  }
done
if grep -qE '^AICRM_ROLE=' deploy/aicrm.env.example; then
  echo "the shared environment example must not assign a runtime role" >&2
  exit 1
fi
