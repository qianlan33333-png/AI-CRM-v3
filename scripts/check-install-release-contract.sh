#!/usr/bin/env bash
set -euo pipefail

installer="deploy/install-release.sh"
start_line="$(grep -nE '^if ! systemctl enable aicrm-effects-worker\.service \|\| ! systemctl restart aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
test -n "$start_line" || { echo "effects worker enable and restart must be rollback guarded" >&2; exit 1; }
exit_line="$(grep -nE '^  exit 8$' "$installer" | cut -d: -f1)"
restart_line="$(grep -nE '^    systemctl restart aicrm-effects-worker\.service \|\| true$' "$installer" | cut -d: -f1)"
active_line="$(grep -nF 'if ! systemctl is-active --quiet aicrm-effects-worker.service || \' "$installer" | cut -d: -f1)"
active_exit_line="$(grep -nE '^  exit 9$' "$installer" | cut -d: -f1)"
test -n "$exit_line" && test -n "$restart_line" && test -n "$active_line" && test -n "$active_exit_line" || {
  echo "effects worker failure must restart the previous compatible worker and exit" >&2; exit 1;
}
test "$(sed -n "$((start_line + 1))p" "$installer")" = "  rollback" && test "$((start_line + 2))" -eq "$exit_line" || {
  echo "effects worker rollback order is invalid" >&2; exit 1;
}
test "$(sed -n "$((active_line + 2))p" "$installer")" = "  rollback" && test "$((active_line + 3))" -eq "$active_exit_line" || {
  echo "effects worker must be active on the activated release" >&2; exit 1;
}
grep -qF '[[ "$(readlink -f "/proc/${effects_worker_pid}/exe")" != "$release_dir/bin/aicrm" ]]' "$installer" || {
  echo "effects worker executable must match the activated release" >&2; exit 1;
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
grep -qxF 'ExecStart=/usr/bin/env AICRM_ROLE=worker AICRM_CUSTOMER_SYNC_TRIGGER=daily /opt/aicrm/current/bin/aicrm' deploy/aicrm-customer-sync-daily.service || {
  echo "daily customer sync must pin its role and trigger at exec time" >&2
  exit 1
}
if grep -qE '^AICRM_ROLE=' deploy/aicrm.env.example; then
  echo "the shared environment example must not assign a runtime role" >&2
  exit 1
fi
grep -qx 'WantedBy=multi-user.target' deploy/aicrm-effects-worker.service || { echo "effects worker must be persistently enableable" >&2; exit 1; }
for migration_contract in \
  '0005_external_effects.sql:External Effects' \
  '0006_wecom_callback_channel_acquisition.sql:WeCom callback acquisition' \
  '0007_media.sql:Media' \
  '0008_tag_catalog.sql:Tag catalog' \
  '0009_customer_activation.sql:customer activation' \
  '0010_product.sql:Product' \
  '0011_coupon_rules.sql:Coupon rules' \
  '0012_group_ops.sql:Group Ops' \
  '0013_automation_agents.sql:Automation agents' \
  '0014_operation_cycles.sql:Operation Cycle' \
  '0015_config_adminops.sql:Config/AdminOps' \
  '0016_media_content_packages.sql:Media content packages' \
  '0017_group_ops_history.sql:Group Ops history' \
  '0019_tag_catalog_sync_projection.sql:Tag catalog sync projection' \
  '0020_order.sql:Order' \
  '0021_payment.sql:Payment' \
  '0024_order_product_version.sql:order product version' \
  '0025_payment_reconciliation.sql:payment reconciliation' \
  '0026_identity_history_receipts.sql:identity history receipts'; do
  migration="${migration_contract%%:*}"
  label="${migration_contract#*:}"
  test -f "migrations/${migration}" || {
    echo "${label} migration must exist in the source release" >&2
    exit 1
  }
  grep -qx "test -f \"\$release_dir/migrations/${migration}\"" "$installer" || {
    echo "release must require the current ${label} migration filename" >&2
    exit 1
  }
done
config_migration="migrations/0015_config_adminops.sql"
for table in config_settings config_audits config_outbox adminops_release_projections adminops_diagnostic_snapshots; do
  grep -qE "^CREATE TABLE ${table}[[:space:]]*\\(" "$config_migration" || {
    echo "Config/AdminOps migration must define ${table}" >&2
    exit 1
  }
done
grep -qx 'test -x "$release_dir/bin/migrate-phone-identities"' "$installer" || { echo "release must include phone migration tool" >&2; exit 1; }
grep -qx 'test -f "$release_dir/release-files.sha256"' "$installer" || { echo "release must require its immutable file manifest" >&2; exit 1; }
grep -qx '(cd "$release_dir" && sha256sum --strict --check release-files.sha256)' "$installer" || { echo "existing releases must pass their complete file manifest before resume" >&2; exit 1; }
grep -qF 'mv -T "$staging_dir" "$release_dir"' "$installer" || { echo "new releases must become visible only after staged verification" >&2; exit 1; }
grep -qF 'sha256sum --strict --check release-files.sha256' .github/workflows/ci.yml || { echo "CI must generate and verify the immutable release manifest" >&2; exit 1; }
