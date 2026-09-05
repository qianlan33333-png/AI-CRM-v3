#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_installer="${repository_root}/deploy/install-release.sh"
test_root="$(mktemp -d)"
archives=()
stale_lock_pid=""
stale_lock_grandchild_pid_file=""

cleanup() {
  [[ -n "$stale_lock_pid" ]] && kill -KILL "$stale_lock_pid" 2>/dev/null || true
  if [[ -n "$stale_lock_grandchild_pid_file" && -f "$stale_lock_grandchild_pid_file" ]]; then
    kill -KILL "$(<"$stale_lock_grandchild_pid_file")" 2>/dev/null || true
  fi
  rm -rf -- "$test_root"
  if ((${#archives[@]})); then
    rm -f -- "${archives[@]}"
  fi
}
trap cleanup EXIT

fail() {
  echo "install release ordering test: $*" >&2
  exit 1
}

sha_one=1111111111111111111111111111111111111111
sha_manual=2222222222222222222222222222222222222222
sha_stale=3333333333333333333333333333333333333333
sha_failed=4444444444444444444444444444444444444444
sha_first=5555555555555555555555555555555555555555
sha_second=6666666666666666666666666666666666666666
sha_recovered=7777777777777777777777777777777777777777
sha_orphaned=8888888888888888888888888888888888888888
sha_orphan_only=9999999999999999999999999999999999999999

mkdir -p "$test_root/bin" "$test_root/aicrm" "$test_root/etc-aicrm" "$test_root/systemd"
printf 'AICRM_SURVEY_DATA_KEY=%043d\n' 0 > "$test_root/etc-aicrm/aicrm.env"

cat > "$test_root/bin/id" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == aicrm ]] && exit 0
exec /usr/bin/id "$@"
EOF
cat > "$test_root/bin/chown" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$test_root/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$test_root/bin/mv" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == -T* ]]; then
  options="$1"
  shift
  target="${!#}"
  /bin/rm -f "$target"
  [[ "$options" == *f* ]] && exec /bin/mv -f "$@"
fi
exec /bin/mv "$@"
EOF
cat > "$test_root/bin/readlink" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == -f && "${2:-}" == /proc/*/exe ]]; then
  calls=0
  [[ -f "$AICRM_TEST_EFFECTS_READLINK_STATE" ]] && calls="$(<"$AICRM_TEST_EFFECTS_READLINK_STATE")"
  calls=$((calls + 1))
  printf '%s\n' "$calls" > "$AICRM_TEST_EFFECTS_READLINK_STATE"
  if ((calls <= AICRM_TEST_EFFECTS_EXE_DELAY_CALLS)); then
    printf '/previous/aicrm\n'
    exit 0
  fi
  printf '%s\n' "$AICRM_TEST_EXPECTED_EXE"
  exit 0
fi
if [[ "${1:-}" == -f ]]; then
  exec /usr/bin/readlink "$2"
fi
exec /usr/bin/readlink "$@"
EOF
cat > "$test_root/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == start && "${2:-}" == aicrm-migrate.service ]]; then
  printf 'migration:%s\n' "${AICRM_TEST_LABEL:-unknown}" >> "$AICRM_TEST_LOG"
  [[ "${AICRM_TEST_FAIL_MIGRATION:-}" == 1 ]] && exit 1
  [[ -n "${AICRM_TEST_MIGRATION_DELAY:-}" ]] && /bin/sleep "$AICRM_TEST_MIGRATION_DELAY"
fi
if [[ "${1:-}" == show && "${2:-}" == aicrm-effects-worker.service ]]; then
  printf '4242\n'
fi
exit 0
EOF
chmod 0755 "$test_root/bin"/*

if command -v flock >/dev/null 2>&1; then
  cp "$source_installer" "$test_root/install-release.sh"
else
  {
    head -n 1 "$source_installer"
    cat <<'EOF'
test_lock_acquire() {
  while ! mkdir "$AICRM_TEST_LOCK_DIR" 2>/dev/null; do /bin/sleep 0.01; done
  trap 'cleanup_release_artifacts; rmdir "$AICRM_TEST_LOCK_DIR" 2>/dev/null || true' EXIT
}
EOF
    tail -n +2 "$source_installer"
  } | sed -e 's|flock 9|test_lock_acquire|' > "$test_root/install-release.sh"
fi
sed \
  -e "s|/opt/aicrm|$test_root/aicrm|g" \
  -e "s|/etc/aicrm|$test_root/etc-aicrm|g" \
  -e "s|/etc/systemd/system|$test_root/systemd|g" \
  "$test_root/install-release.sh" > "$test_root/install-release.rewritten.sh"
mv "$test_root/install-release.rewritten.sh" "$test_root/install-release.sh"
chmod 0755 "$test_root/install-release.sh"

make_release() {
  local sha="$1"
  local release="$test_root/package-${sha}"
  local archive="/tmp/aicrm-${sha}.tar.gz"
  mkdir -p "$release/bin" "$release/migrations" "$release/web/dist/admin" "$release/web/dist/aiassistant" "$release/deploy"
  for binary in aicrm wecom-archive-sdk-runner migrate-platform migrate-river migrate-phone-identities migrate-identity-phone-vault migrate-survey-v2 migrate-order-attribution migrate-automation-operations migrate-v2-config-definitions migrate-media-legacy-materials migrate-channel-history migrate-radar-v2 migrate-sidebar-history bootstrap-automation-operations; do
    printf '#!/usr/bin/env bash\nexit 0\n' > "$release/bin/$binary"
    chmod 0755 "$release/bin/$binary"
  done
  for migration in \
    0005_external_effects.sql 0006_wecom_callback_channel_acquisition.sql 0007_media.sql \
    0008_tag_catalog.sql 0009_customer_activation.sql 0010_product.sql 0011_coupon_rules.sql \
    0012_group_ops.sql 0013_automation_agents.sql 0014_operation_cycles.sql \
    0015_config_adminops.sql 0016_media_content_packages.sql 0017_group_ops_history.sql \
    0018_survey.sql 0019_tag_catalog_sync_projection.sql 0020_order.sql 0021_payment.sql \
    0022_customer_profile_sections.sql 0024_order_product_version.sql \
    0025_payment_reconciliation.sql 0026_identity_history_receipts.sql \
    0027_admin_access_login_compat.sql 0028_hxc_dashboard.sql \
    0029_channel_center.sql 0030_config_definition_import.sql \
    0031_channel_history_import.sql 0032_channel_acquisition_assets.sql \
    0033_wecom_welcome_grants.sql 0034_channel_entrant_actions.sql \
    0035_channel_acquisition_links.sql 0036_ai_assistant_review.sql \
    0037_outbound_private_messages.sql 0038_survey_oauth_phone_vault.sql \
    0047_automation_operations_migration.sql \
    0048_segment_audience_schedule_state.sql 0049_order_history_attribution.sql \
    0050_radar_core.sql 0051_radar_sessions_events.sql 0052_radar_legacy_import.sql \
    0053_segment_audience_member_event_fact_kinds.sql \
    0061_product_public_purchase.sql \
    0063_identity_hxc_source_observations.sql \
    0064_hxc_dashboard_identity_v2.sql \
    0068_payment_session_beneficiary_selection.sql \
    0078_group_ops_provider_tasks.sql \
    0081_group_ops_webhook_unconfigured_reference.sql \
    0082_group_ops_history_import.sql \
    0084_hxc_shared_facts.sql \
    0090_survey_oauth_state_redirect.sql \
    0091_survey_assessment_business_keys.sql; do
    : > "$release/migrations/$migration"
  done
  : > "$release/web/dist/asset-manifest.json"
  for radar_page in radar radarDetail radarForm; do
    : > "$release/web/dist/admin/$radar_page.html"
  done
  for ai_assistant_asset in \
    list.html detail.html \
    group_chat_picker.css group_chat_picker.js \
    material_picker.css material_picker.js \
    send_content_composer.css send_content_composer.js \
    send_content_readonly_detail.css send_content_readonly_detail.js \
    cloud_plan_review.js; do
    : > "$release/web/dist/aiassistant/$ai_assistant_asset"
  done
  for unit in \
    aicrm.service aicrm-migrate.service aicrm-wecom-worker.service aicrm-wecom-worker.timer \
    aicrm-effects-worker.service aicrm-customer-sync-daily.service aicrm-customer-sync-daily.timer \
    aicrm-hxc-dashboard-refresh.service aicrm-hxc-dashboard-refresh.timer aicrm-hxc-dashboard-rollout.service \
    aicrm-automation-bootstrap.service; do
    : > "$release/deploy/$unit"
  done
  : > "$release/deploy/rollout-hxc-identity-v2.sh"
  (
    cd "$release"
    LC_ALL=C find . -type f ! -name release-files.sha256 -print0 | sort -z | xargs -0 sha256sum > release-files.sha256
  )
  tar -C "$release" -czf "$archive" .
  archives+=("$archive")
}

run_release() {
  local sha="$1"
  local run_number="${2:-}"
  local label="$3"
  local effects_delay="${4:-0}"
  PATH="$test_root/bin:$PATH" \
    AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
    AICRM_TEST_LOG="$test_root/install.log" \
    AICRM_TEST_LABEL="$label" \
    AICRM_TEST_EFFECTS_EXE_DELAY_CALLS="$effects_delay" \
    AICRM_TEST_EFFECTS_READLINK_STATE="$test_root/effects-readlink-${sha}" \
    AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha}/bin/aicrm" \
    "$test_root/install-release.sh" "/tmp/aicrm-${sha}.tar.gz" "$sha" "$run_number" >> "$test_root/installer.log" 2>&1
}

for sha in "$sha_one" "$sha_manual" "$sha_stale" "$sha_failed" "$sha_first" "$sha_second" "$sha_recovered" "$sha_orphan_only"; do make_release "$sha"; done

run_release "$sha_one" 100 initial 1
[[ "$(<"$test_root/effects-readlink-${sha_one}")" == 2 ]] || fail "effects worker executable readiness was not retried"
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "successful run did not persist its run number"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_one" ]] || fail "initial release was not activated"
[[ ! -e "/tmp/aicrm-${sha_one}.tar.gz" ]] || fail "successful versioned install did not clean its archive"

run_release "$sha_manual" "" manual
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "manual compatibility install advanced the CI run number"
manual_current="$(readlink "$test_root/aicrm/current")"
[[ "$manual_current" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "manual compatibility install did not activate: ${manual_current}"

run_release "$sha_stale" 99 stale
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "stale run advanced the deployment marker"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "stale run replaced the active release"

if PATH="$test_root/bin:$PATH" \
  AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
  AICRM_TEST_LOG="$test_root/install.log" \
  AICRM_TEST_LABEL=failed \
  AICRM_TEST_FAIL_MIGRATION=1 \
  AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha_failed}/bin/aicrm" \
  "$test_root/install-release.sh" "/tmp/aicrm-${sha_failed}.tar.gz" "$sha_failed" 101 >> "$test_root/installer.log" 2>&1; then
  fail "migration failure unexpectedly succeeded"
fi
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "failed run advanced the deployment marker"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "failed run did not roll back"

PATH="$test_root/bin:$PATH" \
  AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
  AICRM_TEST_LOG="$test_root/install.log" \
  AICRM_TEST_LABEL=first \
  AICRM_TEST_MIGRATION_DELAY=1 \
  AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha_first}/bin/aicrm" \
  "$test_root/install-release.sh" "/tmp/aicrm-${sha_first}.tar.gz" "$sha_first" 102 > "$test_root/first.log" 2>&1 &
first_pid=$!
/bin/sleep 0.1
run_release "$sha_second" 103 second
wait "$first_pid"
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 103 ]] || fail "serialized deployments did not retain the newest run number"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_second" ]] || fail "serialized deployments did not retain the newest release"
[[ "$(grep '^migration:' "$test_root/install.log" | tail -n 2 | tr '\n' ' ')" == 'migration:first migration:second ' ]] || fail "second deployment entered the critical section before the first completed"

if [[ -d /proc && -x "$(command -v flock)" ]]; then
  stale_installer="/tmp/install-release-${sha_orphaned}.sh"
  stale_lock_grandchild_pid_file="$test_root/stale-lock-grandchild.pid"
  cat > "$stale_installer" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exec 9>"$AICRM_TEST_RELEASE_LOCK"
flock 9
bash -c 'sleep 300 & printf "%s\n" "$!" > "$AICRM_TEST_GRANDCHILD_PID_FILE"; wait' &
wait
EOF
  chmod 0755 "$stale_installer"
  AICRM_TEST_RELEASE_LOCK="$test_root/aicrm/install-release.lock" \
    AICRM_TEST_GRANDCHILD_PID_FILE="$stale_lock_grandchild_pid_file" \
    /usr/bin/bash "$stale_installer" ignored ignored 104 &
  stale_lock_pid=$!
  for _ in $(seq 1 100); do
    [[ -f "$stale_lock_grandchild_pid_file" ]] && break
    /bin/sleep 0.01
  done
  [[ -f "$stale_lock_grandchild_pid_file" ]] || fail "stale lock fixture did not start"
  run_release "$sha_recovered" 105 recovered
  wait "$stale_lock_pid" 2>/dev/null || true
  stale_lock_pid=""
  [[ "$(<"$test_root/aicrm/last-successful-run-number")" == 105 ]] || fail "orphan lock recovery did not persist the newer run number"
  [[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_recovered" ]] || fail "orphan lock recovery did not activate the newer release"
  [[ ! -e "/proc/$(<"$stale_lock_grandchild_pid_file")" ]] || fail "orphaned lock holder survived recovery"
  rm -f -- "$stale_installer"

  # Model the production failure after the obsolete installer has already
  # exited: only a detached descendant and its inherited lock fd remain.
  orphan_only_pid_file="$test_root/orphan-only.pid"
  (
    exec 9>"$test_root/aicrm/install-release.lock"
    flock 9
    /bin/sleep 300 &
    printf '%s\n' "$!" > "$orphan_only_pid_file"
  )
  for _ in $(seq 1 100); do
    [[ -f "$orphan_only_pid_file" ]] && break
    /bin/sleep 0.01
  done
  [[ -f "$orphan_only_pid_file" ]] || fail "orphan-only lock fixture did not start"
  stale_lock_grandchild_pid_file="$orphan_only_pid_file"
  rm -f -- "$test_root/aicrm/last-successful-run-number"
  run_release "$sha_orphan_only" 106 orphan-only
  [[ "$(<"$test_root/aicrm/last-successful-run-number")" == 106 ]] || fail "unmarked orphan-only recovery did not persist the newer run number"
  [[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_orphan_only" ]] || fail "orphan-only recovery did not activate the newer release"
  [[ ! -e "/proc/$(<"$orphan_only_pid_file")" ]] || fail "orphan-only lock holder survived recovery"
fi

echo "install release ordering contract passed"
