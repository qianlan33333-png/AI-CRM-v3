#!/usr/bin/env bash
# Read-only capture of the frozen legacy owner_migration_results rows. The
# output is intentionally not a V3 runtime dependency: feed it once to
# migrate-owner-handoff-history --mode=inspect-stream to create an AEAD
# protected snapshot before any V3 database write.
set -euo pipefail

output="${1:-}"
source_host="${AICRM_OWNER_HANDOFF_SOURCE_SSH_HOST:-}"
source_user="${AICRM_OWNER_HANDOFF_SOURCE_SSH_USER:-ubuntu}"
source_key="${AICRM_OWNER_HANDOFF_SOURCE_SSH_KEY_FILE:-}"
known_hosts="${AICRM_OWNER_HANDOFF_SOURCE_KNOWN_HOSTS_FILE:-}"
corp_scope="${AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE:-}"

if [[ -z "$output" || -z "$source_host" || -z "$source_key" || -z "$known_hosts" || -z "$corp_scope" ]]; then
  echo "usage: capture-owner-handoff-history-source.sh OUTPUT with pinned source SSH and AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE" >&2
  exit 2
fi
case "$source_host" in *[!A-Za-z0-9.-]*|"") echo "invalid source host" >&2; exit 2 ;; esac
case "$corp_scope" in wecom-corp:[A-Za-z0-9._-]*) ;; *) echo "invalid owner handoff corp scope" >&2; exit 2 ;; esac
if [[ ! -s "$source_key" || ! -s "$known_hosts" ]]; then
  echo "source SSH material is unavailable" >&2
  exit 2
fi

umask 077
sql_file="$(mktemp)"
cleanup() { [[ ! -f "$sql_file" ]] || unlink "$sql_file"; }
trap cleanup EXIT

# source rows are result rows, not a reconstruction from current follow
# relations. Ordinality is the frozen rows_json position under the old
# result_id, so a later capture cannot turn a changed row into a new source ID.
cat > "$sql_file" <<SQL
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='15min';
SELECT '__AICRM_OWNER_HANDOFF_HISTORY__|' || to_char(transaction_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"');
SELECT '__AICRM_OWNER_HANDOFF_HISTORY_ROW__|' || encode(convert_to(jsonb_build_object(
  'source_batch_id', r.result_id,
  'source_line_id', item.ordinality::text,
  'mode', CASE WHEN r.include_wecom_transfer THEN 'wecom_then_crm' ELSE 'local_only' END,
  'source_state', CASE
    WHEN COALESCE(NULLIF(item.row->>'external_userid',''), '')='' OR COALESCE(NULLIF(r.source_owner_userid,''), '')='' OR COALESCE(NULLIF(r.target_owner_userid,''), '')='' THEN 'invalid_source'
    ELSE COALESCE(NULLIF(item.row->>'status',''), NULLIF(item.row->>'crm_status',''), NULLIF(item.row->>'wecom_status',''), 'legacy_recorded')
  END,
  'occurred_at', COALESCE(r.executed_at,r.created_at),
  'corp_scope', '${corp_scope}',
  'external_userid', COALESCE(item.row->>'external_userid',''),
  'source_owner_userid', r.source_owner_userid,
  'target_owner_userid', r.target_owner_userid,
  'wecom_status', COALESCE(item.row->>'wecom_status',''),
  'crm_status', COALESCE(item.row->>'crm_status','')
)::text,'UTF8'),'hex')
FROM public.owner_migration_results r
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.rows_json,'[]'::jsonb)) WITH ORDINALITY AS item(row, ordinality)
ORDER BY r.executed_at,r.result_id,item.ordinality;
COMMIT;
SQL

ssh_flags=(-i "$source_key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known_hosts" -o ConnectTimeout=15)
ssh "${ssh_flags[@]}" "${source_user}@${source_host}" psql-stdin < "$sql_file" > "$output"
chmod 0600 "$output"
[[ "$(grep -c '__AICRM_OWNER_HANDOFF_HISTORY__|' "$output" || true)" == 1 ]]
echo "captured one consistent read-only owner handoff history stream"
