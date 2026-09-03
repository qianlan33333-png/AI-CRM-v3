#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_installer="${repository_root}/deploy/install-release.sh"
test_root="$(mktemp -d)"
archives=()

cleanup() {
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

{
  head -n 1 "$source_installer"
  cat <<'EOF'
test_lock_acquire() {
  while ! mkdir "$AICRM_TEST_LOCK_DIR" 2>/dev/null; do /bin/sleep 0.01; done
  trap 'rmdir "$AICRM_TEST_LOCK_DIR" 2>/dev/null || true' EXIT
}
EOF
  tail -n +2 "$source_installer"
} | sed \
  -e "s|/opt/aicrm|$test_root/aicrm|g" \
  -e "s|/etc/aicrm|$test_root/etc-aicrm|g" \
  -e "s|/etc/systemd/system|$test_root/systemd|g" \
  -e 's|flock 9|test_lock_acquire|' > "$test_root/install-release.sh"
chmod 0755 "$test_root/install-release.sh"

make_release() {
  local sha="$1"
  local release="$test_root/package-${sha}"
  local archive="/tmp/aicrm-${sha}.tar.gz"
  mkdir -p "$release/bin" "$release/migrations" "$release/web/dist" "$release/deploy"
  for binary in aicrm migrate-platform migrate-river migrate-phone-identities migrate-survey-v2; do
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
    0027_admin_access_login_compat.sql; do
    : > "$release/migrations/$migration"
  done
  : > "$release/web/dist/asset-manifest.json"
  for unit in \
    aicrm.service aicrm-migrate.service aicrm-wecom-worker.service aicrm-wecom-worker.timer \
    aicrm-effects-worker.service aicrm-customer-sync-daily.service aicrm-customer-sync-daily.timer; do
    : > "$release/deploy/$unit"
  done
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
  PATH="$test_root/bin:$PATH" \
    AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
    AICRM_TEST_LOG="$test_root/install.log" \
    AICRM_TEST_LABEL="$label" \
    AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha}/bin/aicrm" \
    "$test_root/install-release.sh" "/tmp/aicrm-${sha}.tar.gz" "$sha" "$run_number" >> "$test_root/installer.log" 2>&1
}

for sha in "$sha_one" "$sha_manual" "$sha_stale" "$sha_failed" "$sha_first" "$sha_second"; do make_release "$sha"; done

run_release "$sha_one" 100 initial
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "successful run did not persist its run number"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_one" ]] || fail "initial release was not activated"

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

echo "install release ordering contract passed"
