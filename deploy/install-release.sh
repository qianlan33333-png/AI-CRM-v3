#!/usr/bin/env bash
set -euo pipefail

archive="${1:-}"
release_sha="${2:-}"
release_run_number="${3:-}"
release_root=/opt/aicrm/releases
current_link=/opt/aicrm/current
release_lock=/opt/aicrm/install-release.lock
last_successful_run_file=/opt/aicrm/last-successful-run-number

if [[ ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid release sha" >&2
  exit 2
fi
if [[ "$archive" != "/tmp/aicrm-${release_sha}.tar.gz" || ! -f "$archive" ]]; then
  echo "invalid release archive" >&2
  exit 2
fi
if [[ -n "$release_run_number" && ! "$release_run_number" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid release run number" >&2
  exit 2
fi
if ! id aicrm >/dev/null 2>&1 || [[ ! -f /etc/aicrm/aicrm.env ]]; then
  echo "aicrm runtime is not provisioned" >&2
  exit 3
fi
if ! grep -Eq '^AICRM_SURVEY_DATA_KEY=.{43}$' /etc/aicrm/aicrm.env; then
  survey_data_key="$(openssl rand -base64 32 | tr -d '\n=')"
  if grep -q '^AICRM_SURVEY_DATA_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_SURVEY_DATA_KEY=.*$|AICRM_SURVEY_DATA_KEY=${survey_data_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_SURVEY_DATA_KEY=%s\n' "$survey_data_key" >> /etc/aicrm/aicrm.env
  fi
  unset survey_data_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_IDENTITY_PHONE_DATA_KEY=.{43}$' /etc/aicrm/aicrm.env; then
  identity_phone_data_key="$(openssl rand -base64 32 | tr -d '\n=')"
  if grep -q '^AICRM_IDENTITY_PHONE_DATA_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_IDENTITY_PHONE_DATA_KEY=.*$|AICRM_IDENTITY_PHONE_DATA_KEY=${identity_phone_data_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_IDENTITY_PHONE_DATA_KEY=%s\n' "$identity_phone_data_key" >> /etc/aicrm/aicrm.env
  fi
  unset identity_phone_data_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_HXC_SUBJECT_HMAC_KEY=.{32,}$' /etc/aicrm/aicrm.env; then
  hxc_subject_hmac_key="$(openssl rand -base64 48 | tr -d '\n=')"
  if grep -q '^AICRM_HXC_SUBJECT_HMAC_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_HXC_SUBJECT_HMAC_KEY=.*$|AICRM_HXC_SUBJECT_HMAC_KEY=${hxc_subject_hmac_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_HXC_SUBJECT_HMAC_KEY=%s\n' "$hxc_subject_hmac_key" >> /etc/aicrm/aicrm.env
  fi
  unset hxc_subject_hmac_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=[A-Za-z0-9+/]{43}=$' /etc/aicrm/aicrm.env; then
  identity_observation_vault_key="$(openssl rand -base64 32 | tr -d '\n')"
  if grep -q '^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=.*$|AICRM_IDENTITY_OBSERVATION_VAULT_KEY=${identity_observation_vault_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_IDENTITY_OBSERVATION_VAULT_KEY=%s\n' "$identity_observation_vault_key" >> /etc/aicrm/aicrm.env
  fi
  unset identity_observation_vault_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_HXC_IDENTITY_WRITE_ENABLED=(true|false)$' /etc/aicrm/aicrm.env; then
  if grep -q '^AICRM_HXC_IDENTITY_WRITE_ENABLED=' /etc/aicrm/aicrm.env; then
    sed -i 's|^AICRM_HXC_IDENTITY_WRITE_ENABLED=.*$|AICRM_HXC_IDENTITY_WRITE_ENABLED=false|' /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_HXC_IDENTITY_WRITE_ENABLED=false\n' >> /etc/aicrm/aicrm.env
  fi
  chmod 0600 /etc/aicrm/aicrm.env
fi

release_dir="${release_root}/${release_sha}"

install -d -m 0755 "$release_root"
if [[ -e "$release_dir" ]]; then
  if [[ ! -d "$release_dir" || -L "$release_dir" ]]; then
    echo "existing release path is not a directory" >&2
    exit 4
  fi
  echo "resuming validated release ${release_sha}"
else
  staging_dir="$(mktemp -d "${release_root}/.${release_sha}.staging.XXXXXX")"
  cleanup_staging() {
    if [[ -n "${staging_dir:-}" && -d "$staging_dir" ]]; then
      rm -rf -- "$staging_dir"
    fi
  }
  trap cleanup_staging EXIT
  tar -xzf "$archive" -C "$staging_dir"
  test -f "$staging_dir/release-files.sha256"
  (cd "$staging_dir" && sha256sum --strict --check release-files.sha256)
  mv -T "$staging_dir" "$release_dir"
  staging_dir=""
  trap - EXIT
fi
test -x "$release_dir/bin/aicrm"
test -x "$release_dir/bin/migrate-platform"
test -x "$release_dir/bin/migrate-river"
test -x "$release_dir/bin/migrate-phone-identities"
test -x "$release_dir/bin/migrate-identity-phone-vault"
test -x "$release_dir/bin/migrate-survey-v2"
test -x "$release_dir/bin/migrate-order-attribution"
test -x "$release_dir/bin/migrate-automation-operations"
test -x "$release_dir/bin/migrate-v2-config-definitions"
test -x "$release_dir/bin/migrate-channel-history"
test -x "$release_dir/bin/migrate-radar-v2"
test -x "$release_dir/bin/migrate-sidebar-history"
test -x "$release_dir/bin/bootstrap-automation-operations"
test -f "$release_dir/migrations/0005_external_effects.sql"
test -f "$release_dir/migrations/0006_wecom_callback_channel_acquisition.sql"
test -f "$release_dir/migrations/0007_media.sql"
test -f "$release_dir/migrations/0008_tag_catalog.sql"
test -f "$release_dir/migrations/0009_customer_activation.sql"
test -f "$release_dir/migrations/0010_product.sql"
test -f "$release_dir/migrations/0011_coupon_rules.sql"
test -f "$release_dir/migrations/0012_group_ops.sql"
test -f "$release_dir/migrations/0013_automation_agents.sql"
test -f "$release_dir/migrations/0014_operation_cycles.sql"
test -f "$release_dir/migrations/0016_media_content_packages.sql"
test -f "$release_dir/migrations/0017_group_ops_history.sql"
test -f "$release_dir/migrations/0018_survey.sql"
test -f "$release_dir/migrations/0019_tag_catalog_sync_projection.sql"
test -f "$release_dir/migrations/0022_customer_profile_sections.sql"
test -f "$release_dir/migrations/0020_order.sql"
test -f "$release_dir/migrations/0021_payment.sql"
test -f "$release_dir/migrations/0024_order_product_version.sql"
test -f "$release_dir/migrations/0025_payment_reconciliation.sql"
test -f "$release_dir/migrations/0026_identity_history_receipts.sql"
test -f "$release_dir/migrations/0027_admin_access_login_compat.sql"
test -f "$release_dir/migrations/0028_hxc_dashboard.sql"
test -f "$release_dir/migrations/0029_channel_center.sql"
test -f "$release_dir/migrations/0030_config_definition_import.sql"
test -f "$release_dir/migrations/0031_channel_history_import.sql"
test -f "$release_dir/migrations/0032_channel_acquisition_assets.sql"
test -f "$release_dir/migrations/0033_wecom_welcome_grants.sql"
test -f "$release_dir/migrations/0034_channel_entrant_actions.sql"
test -f "$release_dir/migrations/0050_radar_core.sql"
test -f "$release_dir/migrations/0051_radar_sessions_events.sql"
test -f "$release_dir/migrations/0052_radar_legacy_import.sql"
test -f "$release_dir/web/dist/admin/radar.html"
test -f "$release_dir/web/dist/admin/radarDetail.html"
test -f "$release_dir/web/dist/admin/radarForm.html"
test -f "$release_dir/migrations/0035_channel_acquisition_links.sql"
test -f "$release_dir/migrations/0036_ai_assistant_review.sql"
test -f "$release_dir/migrations/0037_outbound_private_messages.sql"
test -f "$release_dir/migrations/0038_survey_oauth_phone_vault.sql"
test -f "$release_dir/migrations/0047_automation_operations_migration.sql"
test -f "$release_dir/migrations/0048_segment_audience_schedule_state.sql"
test -f "$release_dir/migrations/0049_order_history_attribution.sql"
test -f "$release_dir/migrations/0053_segment_audience_member_event_fact_kinds.sql"
test -f "$release_dir/migrations/0061_product_public_purchase.sql"
test -f "$release_dir/migrations/0063_identity_hxc_source_observations.sql"
test -f "$release_dir/migrations/0064_hxc_dashboard_identity_v2.sql"
test -f "$release_dir/migrations/0078_group_ops_provider_tasks.sql"
test -f "$release_dir/migrations/0081_group_ops_webhook_unconfigured_reference.sql"
test -f "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service"
test -f "$release_dir/deploy/rollout-hxc-identity-v2.sh"
test -f "$release_dir/migrations/0015_config_adminops.sql"
test -f "$release_dir/web/dist/asset-manifest.json"
for ai_assistant_asset in \
  list.html detail.html \
  group_chat_picker.css group_chat_picker.js \
  material_picker.css material_picker.js \
  send_content_composer.css send_content_composer.js \
  send_content_readonly_detail.css send_content_readonly_detail.js \
  cloud_plan_review.js; do
  test -f "$release_dir/web/dist/aiassistant/$ai_assistant_asset"
done
test -f "$release_dir/release-files.sha256"
(cd "$release_dir" && sha256sum --strict --check release-files.sha256)
printf 'AICRM_RELEASE_SHA=%s\n' "$release_sha" > "$release_dir/release.env"
chown -R aicrm:aicrm "$release_dir"

cleanup_release_artifacts() {
  rm -f -- "$archive"
  if [[ "$0" == "/tmp/install-release-${release_sha}.sh" ]]; then
    rm -f -- "$0"
  fi
}
trap cleanup_release_artifacts EXIT

# A workflow-level concurrency group cannot serialize every main deployment:
# GitHub retains only one pending run per group, and SHA-unique groups allow
# builds to overlap. The host therefore owns the release critical section.
# A cancelled SSH deployment can leave the durable bootstrap oneshot running
# while its installer still owns the host lock. A newer release may safely stop
# it before waiting: every staged batch is transactional and the command resumes
# through its idempotency receipts with the new release binary.
stale_installer_found=false
non_older_installer_found=false
if [[ -n "$release_run_number" ]]; then
  for stale_cmdline in /proc/[0-9]*/cmdline; do
    stale_args=()
    mapfile -d '' -t stale_args < "$stale_cmdline" 2>/dev/null || continue
    stale_pid="${stale_cmdline#/proc/}"
    stale_pid="${stale_pid%/cmdline}"
    if [[ "$stale_pid" != "$$" && "${stale_args[1]:-}" =~ ^/tmp/install-release-[0-9a-f]{40}\.sh$ ]]; then
      if [[ "${stale_args[4]:-}" =~ ^[1-9][0-9]*$ ]]; then
        if ((stale_args[4] < release_run_number)); then
          stale_installer_found=true
          stale_children=""
          read -r stale_children < "/proc/${stale_pid}/task/${stale_pid}/children" 2>/dev/null || true
          for stale_child_pid in $stale_children; do
            [[ "$stale_child_pid" =~ ^[1-9][0-9]*$ ]] && kill -TERM "$stale_child_pid" 2>/dev/null || true
          done
          kill -TERM "$stale_pid" 2>/dev/null || true
        else
          non_older_installer_found=true
        fi
      else
        # A manual or malformed installer has no comparable CI ordering proof.
        # Never recover its lock out from under it.
        non_older_installer_found=true
      fi
    fi
  done
fi
bootstrap_load_state="$(systemctl show aicrm-automation-bootstrap.service -p LoadState --value 2>/dev/null || true)"
if [[ "$bootstrap_load_state" == loaded ]]; then
  systemctl kill --kill-whom=all --signal=TERM aicrm-automation-bootstrap.service 2>/dev/null || true
  sleep 2
  systemctl kill --kill-whom=all --signal=KILL aicrm-automation-bootstrap.service 2>/dev/null || true
  if ! timeout 15s systemctl stop aicrm-automation-bootstrap.service; then
    systemctl status --no-pager --full aicrm-automation-bootstrap.service || true
    exit 14
  fi
fi
exec 9>"$release_lock"
# Terminating an obsolete installer is not sufficient when one of its deeper
# descendants inherited fd 9: that orphan can keep the kernel lock forever.
# Only a newer numbered release that actually found an older validated
# installer may recover this condition. The fd scan is scoped to the exact
# release lock and excludes this installer, which has opened but not yet locked
# fd 9.
terminate_stale_release_lock_holders() {
  local signal="$1" lock_fd holder_pid target
  for lock_fd in /proc/[0-9]*/fd/*; do
    target="$(readlink -f "$lock_fd" 2>/dev/null || true)"
    [[ "$target" == "$release_lock" ]] || continue
    holder_pid="${lock_fd#/proc/}"
    holder_pid="${holder_pid%%/*}"
    [[ "$holder_pid" =~ ^[1-9][0-9]*$ && "$holder_pid" != "$$" ]] || continue
    kill "-$signal" "$holder_pid" 2>/dev/null || true
  done
}
run_is_not_newer() {
  local candidate="$1"
  local deployed="$2"
  [[ ${#candidate} -lt ${#deployed} ]] || \
    ([[ ${#candidate} -eq ${#deployed} ]] && [[ "$candidate" < "$deployed" || "$candidate" == "$deployed" ]])
}

# A cancelled workflow can outlive the installer process while leaving only a
# descendant holding fd 9. A strictly newer CI run than the last successful
# release may recover that orphan even when no stale installer remains to be
# discovered. Manual installs and runs competing with an equal/newer installer
# fail closed.
release_lock_recovery_allowed="$stale_installer_found"
if [[ -d /proc && -x "$(command -v flock)" && -n "$release_run_number" ]]; then
  if [[ ! -e "$last_successful_run_file" ]]; then
    # Hosts deployed before run ordering was introduced have no marker. When no
    # active installer exists, an exact lock holder is necessarily detached
    # from an obsolete install and can be recovered by this numbered CI run.
    release_lock_recovery_allowed=true
  else
    deployed_run_number="$(<"$last_successful_run_file")"
    if [[ ! "$deployed_run_number" =~ ^[1-9][0-9]*$ ]]; then
      echo "invalid last successful release run number" >&2
      exit 11
    fi
    if ! run_is_not_newer "$release_run_number" "$deployed_run_number"; then
      release_lock_recovery_allowed=true
    fi
  fi
fi
if [[ "$non_older_installer_found" != true && "$release_lock_recovery_allowed" == true ]] && ! flock -w 15 9; then
  terminate_stale_release_lock_holders TERM
  sleep 2
  terminate_stale_release_lock_holders KILL
  if ! flock -w 15 9; then
    echo "timed out recovering stale release lock" >&2
    exit 15
  fi
fi
flock 9

if [[ -n "$release_run_number" && -e "$last_successful_run_file" ]]; then
  last_successful_run_number="$(<"$last_successful_run_file")"
  if [[ ! "$last_successful_run_number" =~ ^[1-9][0-9]*$ ]]; then
    echo "invalid last successful release run number" >&2
    exit 11
  fi
  if run_is_not_newer "$release_run_number" "$last_successful_run_number"; then
    echo "skipping stale release ${release_sha}: run ${release_run_number} is not newer than deployed run ${last_successful_run_number}"
    exit 0
  fi
elif [[ -z "$release_run_number" ]]; then
  echo "installing release ${release_sha} without a CI run number; serialized but not stale-run guarded" >&2
fi

previous=""
if [[ -L "$current_link" ]]; then
	previous="$(readlink -f "$current_link")"
fi

ln -sfn "$release_dir" "${current_link}.new"
mv -Tf "${current_link}.new" "$current_link"
install -m 0644 "$release_dir/deploy/aicrm.service" /etc/systemd/system/aicrm.service
install -m 0644 "$release_dir/deploy/aicrm-migrate.service" /etc/systemd/system/aicrm-migrate.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.service" /etc/systemd/system/aicrm-wecom-worker.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.timer" /etc/systemd/system/aicrm-wecom-worker.timer
install -m 0644 "$release_dir/deploy/aicrm-effects-worker.service" /etc/systemd/system/aicrm-effects-worker.service
install -m 0644 "$release_dir/deploy/aicrm-customer-sync-daily.service" /etc/systemd/system/aicrm-customer-sync-daily.service
install -m 0644 "$release_dir/deploy/aicrm-customer-sync-daily.timer" /etc/systemd/system/aicrm-customer-sync-daily.timer
install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-refresh.service" /etc/systemd/system/aicrm-hxc-dashboard-refresh.service
install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-refresh.timer" /etc/systemd/system/aicrm-hxc-dashboard-refresh.timer
install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service" /etc/systemd/system/aicrm-hxc-dashboard-rollout.service
install -m 0644 "$release_dir/deploy/aicrm-automation-bootstrap.service" /etc/systemd/system/aicrm-automation-bootstrap.service
systemctl daemon-reload

rollback() {
  if [[ -n "$previous" && -d "$previous" ]]; then
    ln -sfn "$previous" "${current_link}.rollback"
    mv -Tf "${current_link}.rollback" "$current_link"
    systemctl restart aicrm.service || true
    systemctl restart aicrm-wecom-worker.timer || true
    systemctl restart aicrm-effects-worker.service || true
    systemctl restart aicrm-customer-sync-daily.timer || true
    systemctl restart aicrm-hxc-dashboard-refresh.timer || true
  fi
}

if ! systemctl start aicrm-migrate.service; then
  rollback
  exit 5
fi
if ! systemctl restart aicrm.service; then
  rollback
  exit 6
fi

ready=false
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error http://127.0.0.1:8080/readyz >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  rollback
  exit 7
fi

systemctl enable --now aicrm-wecom-worker.timer
if ! systemctl enable aicrm-effects-worker.service || ! systemctl restart aicrm-effects-worker.service; then
  rollback
  exit 8
fi
effects_worker_ready=false
for _ in $(seq 1 30); do
  effects_worker_pid="$(systemctl show aicrm-effects-worker.service -p MainPID --value)"
  if systemctl is-active --quiet aicrm-effects-worker.service && \
    [[ "$effects_worker_pid" =~ ^[1-9][0-9]*$ ]] && \
    [[ "$(readlink -f "/proc/${effects_worker_pid}/exe")" == "$release_dir/bin/aicrm" ]]; then
    effects_worker_ready=true
    break
  fi
  sleep 1
done
if [[ "$effects_worker_ready" != true ]]; then
  rollback
  exit 9
fi
if ! systemctl start aicrm-automation-bootstrap.service; then
  systemctl status --no-pager --full aicrm-automation-bootstrap.service || true
  journalctl --no-pager -o cat -n 50 -u aicrm-automation-bootstrap.service || true
  rollback
  exit 13
fi
journalctl --no-pager -o cat -n 20 -u aicrm-automation-bootstrap.service || true
if ! systemctl enable --now aicrm-customer-sync-daily.timer; then
  rollback
  exit 10
fi
if ! systemctl enable --now aicrm-hxc-dashboard-refresh.timer; then
  rollback
  exit 12
fi
if [[ -n "$release_run_number" ]]; then
  next_run_file="$(mktemp "${last_successful_run_file}.XXXXXX")"
  printf '%s\n' "$release_run_number" > "$next_run_file"
  chmod 0644 "$next_run_file"
  mv -f "$next_run_file" "$last_successful_run_file"
fi
echo "release ${release_sha} active"
