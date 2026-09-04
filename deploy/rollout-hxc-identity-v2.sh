#!/usr/bin/env bash
set -euo pipefail

release_sha="${1:-}"
runtime_env=/etc/aicrm/aicrm.env
current_link=/opt/aicrm/current
rollout_lock=/opt/aicrm/hxc-identity-v2-rollout.lock

if [[ ${EUID} -ne 0 || ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid HXC rollout invocation" >&2
  exit 2
fi
current_release="$(readlink -f "$current_link")"
if [[ "$current_release" != "/opt/aicrm/releases/${release_sha}" || ! -f "$runtime_env" ]]; then
  echo "requested HXC release is not active" >&2
  exit 3
fi
for required in \
  "$current_release/bin/aicrm" \
  "$current_release/migrations/0063_identity_hxc_source_observations.sql" \
  "$current_release/migrations/0064_hxc_dashboard_identity_v2.sql"; do
  [[ -f "$required" ]] || { echo "HXC rollout artifact incomplete" >&2; exit 3; }
done
grep -qx 'AICRM_HXC_SYNC_ENABLED=true' "$runtime_env"
grep -qx 'AICRM_HXC_UNIONID_VERIFIED=true' "$runtime_env"
grep -Eq '^AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:[^[:space:]]+$' "$runtime_env"
grep -Eq '^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=[A-Za-z0-9+/]{43}=$' "$runtime_env"

database_url="$(sed -n 's/^AICRM_DATABASE_URL=//p' "$runtime_env")"
if [[ -z "$database_url" || "$database_url" == *$'\n'* ]]; then
  echo "HXC rollout database configuration is invalid" >&2
  exit 3
fi
psql_bin="$(command -v psql)"
[[ -x "$psql_bin" ]]

exec 9>"$rollout_lock"
flock 9

run_sql() {
  runuser -u aicrm -- env PGDATABASE="$database_url" "$psql_bin" -X -v ON_ERROR_STOP=1 -Atqc "$1"
}

set_write_mode() {
  local enabled="$1" next_env
  next_env="$(mktemp /etc/aicrm/.aicrm.env.hxc-mode.XXXXXX)"
  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      AICRM_HXC_IDENTITY_WRITE_ENABLED=*) ;;
      *) printf '%s\n' "$line" >> "$next_env" ;;
    esac
  done < "$runtime_env"
  printf 'AICRM_HXC_IDENTITY_WRITE_ENABLED=%s\n' "$enabled" >> "$next_env"
  chmod 0600 "$next_env"
  chown --reference="$runtime_env" "$next_env"
  mv -f -- "$next_env" "$runtime_env"
}

restart_runtime() {
  systemctl restart aicrm.service
  systemctl restart aicrm-effects-worker.service
  local ready=false response
  for _ in $(seq 1 30); do
    response="$(curl --fail --silent --show-error http://127.0.0.1:8080/readyz 2>/dev/null || true)"
    if [[ "$response" == *"\"release_sha\":\"${release_sha}\""* && "$response" == *'"status":"ready"'* ]] && systemctl is-active --quiet aicrm-effects-worker.service; then
      ready=true
      break
    fi
    sleep 1
  done
  [[ "$ready" == true ]]
}

rollout_complete=false
disable_on_failure() {
  unset database_url
  if [[ "$rollout_complete" != true ]]; then
    set_write_mode false || true
    systemctl restart aicrm.service >/dev/null 2>&1 || true
    systemctl restart aicrm-effects-worker.service >/dev/null 2>&1 || true
    echo "HXC identity rollout stopped with writes disabled" >&2
  fi
}
trap disable_on_failure EXIT

wait_for_run() {
  local mode="$1" run_key="$2" status="" record
  for _ in $(seq 1 360); do
    record="$(run_sql "SELECT status||'|'||source_count||'|'||processed_count||'|'||identity_replay_verified_count||'|'||COALESCE(projection_id,0)||'|'||COALESCE(error_code,'') FROM hxc_dashboard_refresh_runs WHERE run_key='${run_key}'")"
    if [[ -n "$record" ]]; then
      IFS='|' read -r status run_source_count run_processed_count run_replay_count run_projection_id run_error_code <<< "$record"
      if [[ "$status" == succeeded ]]; then
        return 0
      fi
      if [[ "$status" == failed ]]; then
        echo "HXC ${mode} failed with safe code ${run_error_code}" >&2
        return 1
      fi
    fi
    sleep 5
  done
  echo "HXC ${mode} timed out" >&2
  return 1
}

trigger_run() {
  local mode="$1"
  systemctl reset-failed aicrm-hxc-dashboard-rollout.service >/dev/null 2>&1 || true
  systemctl start aicrm-hxc-dashboard-rollout.service
  wait_for_run "$mode" "initial:hxc-dashboard-v2:${mode}:${release_sha}"
}

for migration in 0063 0064; do
  [[ "$(run_sql "SELECT count(*) FROM platform_schema_migrations WHERE version='${migration}'")" == 1 ]]
done
[[ "$(run_sql "SELECT count(*) FROM (SELECT kind,scope_key,normalized_value_digest,normalized_value FROM customer_identities WHERE status='active' GROUP BY kind,scope_key,normalized_value_digest,normalized_value HAVING count(DISTINCT customer_id)>1) duplicate_keys")" == 0 ]]

set_write_mode false
restart_runtime
trigger_run inspect
[[ "$run_source_count" == "$run_processed_count" && "$run_replay_count" == 0 && "$run_projection_id" =~ ^[1-9][0-9]*$ ]]
inspect_counts="$(run_sql "SELECT total_count||'|'||matched_count||'|'||unmatched_count||'|'||conflict_count||'|'||matched_by_unionid_count||'|'||matched_by_phone_count||'|'||matched_by_both_count||'|'||pending_observation_count||'|'||invalid_identity_count FROM hxc_dashboard_versions WHERE id=${run_projection_id} AND rule_version='hxc-current-v2' AND status='published'")"
IFS='|' read -r total_count matched_count unmatched_count conflict_count matched_union matched_phone matched_both pending_count invalid_count <<< "$inspect_counts"
((total_count == matched_count + unmatched_count + conflict_count))
((matched_count == matched_union + matched_phone + matched_both))
((unmatched_count == pending_count + invalid_count))
((total_count == run_source_count))

customer_count_before="$(run_sql 'SELECT count(*) FROM customers')"
subjects_before="$(run_sql 'SELECT count(*) FROM identity_source_subjects')"
observations_before="$(run_sql 'SELECT count(*) FROM identity_source_observations')"
receipts_before="$(run_sql 'SELECT count(*) FROM identity_source_resolution_receipts')"
conflicts_before="$(run_sql 'SELECT count(*) FROM identity_source_conflicts')"

set_write_mode true
restart_runtime
trigger_run apply
[[ "$run_source_count" == "$run_processed_count" && "$run_replay_count" == "$run_source_count" && "$run_projection_id" =~ ^[1-9][0-9]*$ ]]
apply_replay_count="$run_replay_count"
[[ "$(run_sql 'SELECT count(*) FROM customers')" == "$customer_count_before" ]]
apply_counts="$(run_sql "SELECT total_count||'|'||matched_count||'|'||unmatched_count||'|'||conflict_count||'|'||matched_by_unionid_count||'|'||matched_by_phone_count||'|'||matched_by_both_count||'|'||pending_observation_count||'|'||invalid_identity_count FROM hxc_dashboard_versions WHERE id=${run_projection_id} AND rule_version='hxc-current-v2' AND status='published'")"
IFS='|' read -r total_count matched_count unmatched_count conflict_count matched_union matched_phone matched_both pending_count invalid_count <<< "$apply_counts"
((total_count == matched_count + unmatched_count + conflict_count))
((matched_count == matched_union + matched_phone + matched_both))
((unmatched_count == pending_count + invalid_count))
((total_count == run_source_count))
[[ "$(run_sql "SELECT count(*) FROM identity_source_subjects WHERE source_system='hxc' AND status<>'retired'")" == "$run_source_count" ]]

systemctl enable --now aicrm-hxc-dashboard-refresh.timer
systemctl is-active --quiet aicrm-effects-worker.service
systemctl is-active --quiet aicrm-hxc-dashboard-refresh.timer
systemctl is-enabled --quiet aicrm-hxc-dashboard-refresh.timer

# Exercise the exact timer service path after persistence is enabled. The
# enabled timer remains responsible for subsequent natural refreshes.
scheduled_key="scheduled:$(TZ=Asia/Shanghai date +%Y-%m-%dT%H):hxc-dashboard-v2:apply"
systemctl start aicrm-hxc-dashboard-refresh.service
wait_for_run apply "$scheduled_key"
[[ "$run_source_count" == "$run_processed_count" && "$run_replay_count" == "$run_source_count" && "$run_projection_id" =~ ^[1-9][0-9]*$ ]]
[[ "$(run_sql 'SELECT count(*) FROM customers')" == "$customer_count_before" ]]
scheduled_counts="$(run_sql "SELECT total_count||'|'||matched_count||'|'||unmatched_count||'|'||conflict_count||'|'||matched_by_unionid_count||'|'||matched_by_phone_count||'|'||matched_by_both_count||'|'||pending_observation_count||'|'||invalid_identity_count FROM hxc_dashboard_versions WHERE id=${run_projection_id} AND rule_version='hxc-current-v2' AND status='published'")"
IFS='|' read -r total_count matched_count unmatched_count conflict_count matched_union matched_phone matched_both pending_count invalid_count <<< "$scheduled_counts"
((total_count == matched_count + unmatched_count + conflict_count))
((matched_count == matched_union + matched_phone + matched_both))
((unmatched_count == pending_count + invalid_count))
((total_count == run_source_count))
[[ "$(run_sql "SELECT count(*) FROM identity_source_subjects WHERE source_system='hxc' AND status<>'retired'")" == "$run_source_count" ]]

subjects_after="$(run_sql 'SELECT count(*) FROM identity_source_subjects')"
observations_after="$(run_sql 'SELECT count(*) FROM identity_source_observations')"
receipts_after="$(run_sql 'SELECT count(*) FROM identity_source_resolution_receipts')"
conflicts_after="$(run_sql 'SELECT count(*) FROM identity_source_conflicts')"
printf 'HXC identity v2 active: total=%s matched=%s unmatched=%s conflict=%s unionid=%s phone=%s both=%s pending=%s invalid=%s replay_verified=%s new_customers=0 subjects_delta=%s observations_delta=%s receipts_delta=%s conflicts_delta=%s\n' \
  "$total_count" "$matched_count" "$unmatched_count" "$conflict_count" "$matched_union" "$matched_phone" "$matched_both" "$pending_count" "$invalid_count" "$apply_replay_count" \
  "$((subjects_after-subjects_before))" "$((observations_after-observations_before))" "$((receipts_after-receipts_before))" "$((conflicts_after-conflicts_before))"

rollout_complete=true
unset database_url
trap - EXIT
