#!/usr/bin/env bash
set -euo pipefail

installer="deploy/install-release.sh"
start_line="$(grep -nE '^if ! systemctl enable aicrm-effects-worker\.service \|\| ! systemctl restart aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
test -n "$start_line" || { echo "effects worker enable and restart must be rollback guarded" >&2; exit 1; }
exit_line="$(grep -nE '^  exit 8$' "$installer" | cut -d: -f1)"
restart_line="$(grep -nE '^    systemctl restart aicrm-effects-worker\.service \|\| true$' "$installer" | cut -d: -f1)"
active_line="$(grep -nF 'if [[ "$effects_worker_ready" != true ]]; then' "$installer" | cut -d: -f1)"
active_exit_line="$(grep -nE '^  exit 9$' "$installer" | cut -d: -f1)"
test -n "$exit_line" && test -n "$restart_line" && test -n "$active_line" && test -n "$active_exit_line" || {
  echo "effects worker failure must restart the previous compatible worker and exit" >&2; exit 1;
}
test "$(sed -n "$((start_line + 1))p" "$installer")" = "  rollback" && test "$((start_line + 2))" -eq "$exit_line" || {
  echo "effects worker rollback order is invalid" >&2; exit 1;
}
test "$(sed -n "$((active_line + 1))p" "$installer")" = "  rollback" && test "$((active_line + 2))" -eq "$active_exit_line" || {
  echo "effects worker must be active on the activated release" >&2; exit 1;
}
grep -qF 'for _ in $(seq 1 30); do' "$installer" || { echo "effects worker activation must have a bounded readiness retry" >&2; exit 1; }
grep -qF '[[ "$(readlink -f "/proc/${effects_worker_pid}/exe")" == "$release_dir/bin/aicrm" ]]' "$installer" || {
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
grep -qx 'test -f "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service"' "$installer" || { echo "release must include the HXC rollout unit" >&2; exit 1; }
grep -qx 'test -f "$release_dir/deploy/rollout-hxc-identity-v2.sh"' "$installer" || { echo "release must include the HXC rollout gate" >&2; exit 1; }
grep -qx 'install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service" /etc/systemd/system/aicrm-hxc-dashboard-rollout.service' "$installer" || { echo "installer must install the HXC rollout unit" >&2; exit 1; }
grep -qxF 'ExecStart=/usr/bin/env AICRM_ROLE=worker AICRM_CUSTOMER_SYNC_TRIGGER=daily /opt/aicrm/current/bin/aicrm' deploy/aicrm-customer-sync-daily.service || {
  echo "daily customer sync must pin its role and trigger at exec time" >&2
  exit 1
}
grep -qxF 'ExecStart=/usr/bin/env AICRM_ROLE=worker AICRM_HXC_SYNC_TRIGGER=scheduled /opt/aicrm/current/bin/aicrm' deploy/aicrm-hxc-dashboard-refresh.service || { echo "HXC refresh must use the durable worker trigger" >&2; exit 1; }
grep -qxF 'OnCalendar=*-*-* 03,09,15,21:15:00 Asia/Shanghai' deploy/aicrm-hxc-dashboard-refresh.timer || { echo "HXC refresh timer must run at the four approved Beijing times" >&2; exit 1; }
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
  '0026_identity_history_receipts.sql:identity history receipts' \
  '0027_admin_access_login_compat.sql:Admin access login compatibility' \
  '0028_hxc_dashboard.sql:HXC dashboard' \
  '0029_channel_center.sql:Channel center' \
  '0030_config_definition_import.sql:configuration definition import' \
  '0031_channel_history_import.sql:Channel history import' \
  '0032_channel_acquisition_assets.sql:Channel acquisition assets' \
  '0033_wecom_welcome_grants.sql:WeCom welcome grants' \
  '0034_channel_entrant_actions.sql:Channel entrant actions' \
  '0035_channel_acquisition_links.sql:Channel acquisition links' \
  '0036_ai_assistant_review.sql:AI Assistant review' \
  '0037_outbound_private_messages.sql:Outbound private messages' \
  '0038_survey_oauth_phone_vault.sql:survey OAuth phone vault' \
  '0049_order_history_attribution.sql:order history attribution' \
  '0053_segment_audience_member_event_fact_kinds.sql:Segment audience member event fact kind repair' \
  '0061_product_public_purchase.sql:product public purchase' \
  '0063_identity_hxc_source_observations.sql:HXC identity source observations' \
  '0064_hxc_dashboard_identity_v2.sql:HXC dashboard identity v2' \
  '0068_payment_session_beneficiary_selection.sql:payment session beneficiary selection' \
  '0069_coupon_claim_redemption_lifecycle.sql:coupon claim redemption lifecycle' \
  '0070_service_period_entitlement_fulfillment.sql:service-period entitlement fulfillment' \
  '0076_order_checkout_snapshots.sql:Order checkout snapshots' \
  '0077_coupon_public_slug.sql:coupon public slug' \
  '0078_group_ops_provider_tasks.sql:Group Ops provider tasks' \
  '0079_service_period_member_grid.sql:service-period member grid' \
  '0081_group_ops_webhook_unconfigured_reference.sql:Group Ops unconfigured webhook repair' \
  '0082_group_ops_history_import.sql:Group Ops history import' \
  '0084_hxc_shared_facts.sql:HXC shared legacy facts' \
  '0088_order_service_entitlement_alliance.sql:service-period alliance'; do
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
grep -qx 'test -x "$release_dir/bin/migrate-automation-operations"' "$installer" || { echo "release must include Automation Operations migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/wecom-archive-sdk-runner"' "$installer" || { echo "release must include the WeCom archive SDK runner" >&2; exit 1; }
grep -qF 'scripts/build-wecom-archive-sdk-runner-linux.sh release/bin/wecom-archive-sdk-runner' .github/workflows/ci.yml || { echo "release workflow must build the real Linux cgo archive runner" >&2; exit 1; }
grep -qF 'CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOWORK=off' scripts/build-wecom-archive-sdk-runner-linux.sh || { echo "archive release runner must be a Linux amd64 cgo build" >&2; exit 1; }
grep -qF 'scripts/build-wecom-archive-sdk-runner-linux.sh "$work/runner"' scripts/check-wecom-message-archive-sdk.sh || { echo "official SDK ABI check must exercise the release runner builder" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-order-attribution"' "$installer" || { echo "release must include order history attribution tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-order-attribution ./cmd/migrate-order-attribution' .github/workflows/ci.yml || { echo "CI must build the order history attribution tool" >&2; exit 1; }
grep -qx 'test -f "$release_dir/migrations/0047_automation_operations_migration.sql"' "$installer" || { echo "release must require Automation Operations migration schema" >&2; exit 1; }
grep -qx 'test -f "$release_dir/migrations/0048_segment_audience_schedule_state.sql"' "$installer" || { echo "release must require Automation Operations schedule state" >&2; exit 1; }
config_migration="migrations/0015_config_adminops.sql"
for table in config_settings config_audits config_outbox adminops_release_projections adminops_diagnostic_snapshots; do
  grep -qE "^CREATE TABLE ${table}[[:space:]]*\\(" "$config_migration" || {
    echo "Config/AdminOps migration must define ${table}" >&2
    exit 1
  }
done
grep -qx 'test -x "$release_dir/bin/migrate-phone-identities"' "$installer" || { echo "release must include phone migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-v2-config-definitions"' "$installer" || { echo "release must include configuration definition migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-config-definitions ./cmd/migrate-v2-config-definitions' .github/workflows/ci.yml || { echo "CI must build the configuration definition migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-media-legacy-materials"' "$installer" || { echo "release must include legacy Media mapping migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-media-legacy-materials ./cmd/migrate-media-legacy-materials' .github/workflows/ci.yml || { echo "CI must build the legacy Media mapping migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-channel-history"' "$installer" || { echo "release must include channel history migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-radar-v2"' "$installer" || { echo "release must include Radar migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-radar-v2 ./cmd/migrate-radar-v2' .github/workflows/ci.yml || { echo "release workflow must build Radar migration tool" >&2; exit 1; }
for page in radar radarDetail radarForm; do
  grep -qx "test -f \"\$release_dir/web/dist/admin/$page.html\"" "$installer" || { echo "release must include Radar UI page $page" >&2; exit 1; }
done
grep -qx 'test -x "$release_dir/bin/migrate-sidebar-history"' "$installer" || { echo "release must include sidebar history migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-sidebar-history ./cmd/migrate-sidebar-history' .github/workflows/ci.yml || { echo "release workflow must build sidebar history migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/bootstrap-automation-operations"' "$installer" || { echo "release must include Automation Operations semantic bootstrap" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/bootstrap-automation-operations ./cmd/bootstrap-automation-operations' .github/workflows/ci.yml || { echo "CI must build Automation Operations semantic bootstrap" >&2; exit 1; }
grep -qxF 'ExecStart=/opt/aicrm/current/bin/bootstrap-automation-operations' deploy/aicrm-automation-bootstrap.service || { echo "Automation Operations bootstrap unit must execute the release binary" >&2; exit 1; }
grep -qxF '  systemctl kill --kill-whom=all --signal=KILL aicrm-automation-bootstrap.service 2>/dev/null || true' "$installer" || { echo "deployment must terminate a stale Automation Operations bootstrap before waiting for the host lock" >&2; exit 1; }
grep -qxF '  if ! timeout 15s systemctl stop aicrm-automation-bootstrap.service; then' "$installer" || { echo "deployment must bound stale bootstrap stop confirmation" >&2; exit 1; }
grep -qF '"${stale_args[1]:-}" =~ ^/tmp/install-release-[0-9a-f]{40}\.sh$' "$installer" || { echo "deployment must scope stale installer supersession to validated release commands" >&2; exit 1; }
grep -qxF '      if [[ "${stale_args[4]:-}" =~ ^[1-9][0-9]*$ ]]; then' "$installer" || { echo "deployment must validate competing release run numbers" >&2; exit 1; }
grep -qxF '        if ((stale_args[4] < release_run_number)); then' "$installer" || { echo "deployment must supersede only older release run numbers" >&2; exit 1; }
grep -qxF '        non_older_installer_found=true' "$installer" || { echo "deployment must fail closed for manual or malformed installers" >&2; exit 1; }
grep -qF 'read -r stale_children < "/proc/${stale_pid}/task/${stale_pid}/children"' "$installer" || { echo "deployment must release stale installer child waits before terminating their parent" >&2; exit 1; }
grep -qx 'stale_installer_found=false' "$installer" || { echo "deployment must prove an older validated installer exists before lock recovery" >&2; exit 1; }
grep -qxF '    target="$(readlink -f "$lock_fd" 2>/dev/null || true)"' "$installer" || { echo "deployment lock recovery must inspect exact open file descriptors" >&2; exit 1; }
grep -qxF '    [[ "$target" == "$release_lock" ]] || continue' "$installer" || { echo "deployment lock recovery must remain scoped to the release lock" >&2; exit 1; }
grep -qxF 'release_lock_recovery_allowed="$stale_installer_found"' "$installer" || { echo "deployment must retain validated stale-installer recovery evidence" >&2; exit 1; }
grep -qxF 'if [[ -d /proc && -x "$(command -v flock)" && -n "$release_run_number" ]]; then' "$installer" || { echo "orphan lock recovery must require Linux procfs, flock, and a numbered CI run" >&2; exit 1; }
grep -qxF '  if [[ ! -e "$last_successful_run_file" ]]; then' "$installer" || { echo "pre-ordering hosts must be able to recover detached release locks" >&2; exit 1; }
grep -qxF '    if ! run_is_not_newer "$release_run_number" "$deployed_run_number"; then' "$installer" || { echo "deployment must allow orphan recovery only for a run newer than the deployed marker" >&2; exit 1; }
grep -qxF 'if [[ "$non_older_installer_found" != true && "$release_lock_recovery_allowed" == true ]] && ! flock -w 15 9; then' "$installer" || { echo "deployment may recover lock holders only after a bounded wait with supersession evidence" >&2; exit 1; }
grep -qxF '  if ! flock -w 15 9; then' "$installer" || { echo "deployment must bound the recovered lock acquisition" >&2; exit 1; }
grep -qxF 'if ! systemctl start aicrm-automation-bootstrap.service; then' "$installer" || { echo "deployment must run Automation Operations semantic bootstrap" >&2; exit 1; }
bootstrap_start_line="$(grep -nF 'if ! systemctl start aicrm-automation-bootstrap.service; then' "$installer" | cut -d: -f1)"
bootstrap_status_line="$(grep -nF '  systemctl status --no-pager --full aicrm-automation-bootstrap.service || true' "$installer" | tail -n 1 | cut -d: -f1)"
bootstrap_rollback_line="$(sed -n "$((bootstrap_start_line + 3))p" "$installer")"
test -n "$bootstrap_status_line" && test "$bootstrap_status_line" -gt "$bootstrap_start_line" && test "$bootstrap_rollback_line" = "  rollback" || { echo "bootstrap failure must emit service evidence before rollback" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-identity-phone-vault"' "$installer" || { echo "release must include phone vault migration tool" >&2; exit 1; }
grep -qx 'test -f "$release_dir/release-files.sha256"' "$installer" || { echo "release must require its immutable file manifest" >&2; exit 1; }
for ai_assistant_asset in \
  list.html detail.html \
  group_chat_picker.css group_chat_picker.js \
  material_picker.css material_picker.js \
  send_content_composer.css send_content_composer.js \
  send_content_readonly_detail.css send_content_readonly_detail.js \
  cloud_plan_review.js; do
  grep -qF 'test -f "$release_dir/web/dist/aiassistant/$ai_assistant_asset"' "$installer" || {
    echo "release installer must require AI Assistant UI assets" >&2
    exit 1
  }
done
grep -qx '(cd "$release_dir" && sha256sum --strict --check release-files.sha256)' "$installer" || { echo "existing releases must pass their complete file manifest before resume" >&2; exit 1; }
grep -qF 'mv -T "$staging_dir" "$release_dir"' "$installer" || { echo "new releases must become visible only after staged verification" >&2; exit 1; }
grep -qF 'sha256sum --strict --check release-files.sha256' .github/workflows/ci.yml || { echo "CI must generate and verify the immutable release manifest" >&2; exit 1; }
grep -qx 'exec 9>"$release_lock"' "$installer" || { echo "installer must hold a host-side release lock" >&2; exit 1; }
grep -qx 'flock 9' "$installer" || { echo "installer must serialize the release critical section with flock" >&2; exit 1; }
grep -qx 'release_run_number="${3:-}"' "$installer" || { echo "installer must accept the CI run number" >&2; exit 1; }
grep -qF 'last_successful_run_file=/opt/aicrm/last-successful-run-number' "$installer" || { echo "installer must retain the successful CI run marker" >&2; exit 1; }
grep -qF '${GITHUB_RUN_NUMBER}' .github/workflows/ci.yml || { echo "CI must pass the GitHub run number to the installer" >&2; exit 1; }
grep -qF 'remote_installer="/tmp/install-release-${GITHUB_SHA}.sh"' .github/workflows/ci.yml || { echo "CI must upload each installer to a SHA-versioned remote path" >&2; exit 1; }
grep -qF 'deploy/upload-release-chunks.sh \' .github/workflows/ci.yml || { echo "CI must upload the release through the bounded chunk uploader" >&2; exit 1; }
grep -qF 'split -b 1m -a 4' deploy/upload-release-chunks.sh || { echo "release upload chunks must fit the slow production link attempt budget" >&2; exit 1; }
grep -qF 'timeout 300s scp' deploy/upload-release-chunks.sh || { echo "each release chunk upload must be time bounded" >&2; exit 1; }
grep -qF 'sha256sum --check --status' deploy/upload-release-chunks.sh || { echo "the reconstructed remote release must pass a SHA-256 check" >&2; exit 1; }
grep -qF 'sudo /usr/bin/bash ${remote_installer}' .github/workflows/ci.yml || { echo "CI must execute the uploaded SHA-versioned installer" >&2; exit 1; }
grep -qF 'if [[ "$0" == "/tmp/install-release-${release_sha}.sh" ]]; then' "$installer" || { echo "installer cleanup must be limited to its SHA-versioned path" >&2; exit 1; }
grep -qF 'AICRM_HXC_SOURCE_DSN: ${{ secrets.AICRM_HXC_SOURCE_DSN }}' .github/workflows/ci.yml || { echo "CI must read the HXC DSN from Actions secrets" >&2; exit 1; }
grep -qF 'AICRM_HXC_UNIONID_SCOPE: ${{ secrets.AICRM_HXC_UNIONID_SCOPE }}' .github/workflows/ci.yml || { echo "CI must read the HXC scope from Actions secrets" >&2; exit 1; }
grep -qF 'sudo /usr/bin/bash ${remote_configurer} ${remote_config} ${GITHUB_SHA}' .github/workflows/ci.yml || { echo "CI must apply HXC configuration through the audited runtime configurer" >&2; exit 1; }
grep -qF 'AICRM_WECHAT_PAY_H5_APP_ID: ${{ secrets.AICRM_WECHAT_PAY_H5_APP_ID }}' .github/workflows/ci.yml || { echo "CI must read the H5 OAuth AppID from Actions secrets" >&2; exit 1; }
grep -qF 'AICRM_WECHAT_PAY_H5_APP_SECRET: ${{ secrets.AICRM_WECHAT_PAY_H5_APP_SECRET }}' .github/workflows/ci.yml || { echo "CI must read the H5 OAuth AppSecret from Actions secrets" >&2; exit 1; }
grep -qF 'scp "${ssh_flags[@]}" deploy/configure-payment-h5-oauth-runtime.sh "${DEPLOY_USER}@${DEPLOY_HOST}:${remote_configurer}"' .github/workflows/ci.yml || { echo "CI must upload the audited H5 OAuth runtime configurer" >&2; exit 1; }
scripts/test-configure-hxc-runtime.sh
scripts/test-configure-payment-h5-oauth-runtime.sh
scripts/test-install-release-ordering.sh
