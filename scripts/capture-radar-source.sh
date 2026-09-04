#!/usr/bin/env bash
set -euo pipefail

output="${1:-}"
source_host="${AICRM_RADAR_SOURCE_SSH_HOST:-}"
source_user="${AICRM_RADAR_SOURCE_SSH_USER:-ubuntu}"
source_key="${AICRM_RADAR_SOURCE_SSH_KEY_FILE:-}"
known_hosts="${AICRM_RADAR_SOURCE_KNOWN_HOSTS_FILE:-}"

if [[ -z "$output" || -z "$source_host" || -z "$source_key" || -z "$known_hosts" ]]; then
  echo "usage: capture-radar-source.sh OUTPUT with pinned source SSH environment" >&2
  exit 2
fi
case "$source_host" in *[!A-Za-z0-9.-]*|"") echo "invalid source host" >&2; exit 2 ;; esac
if [[ ! -s "$source_key" || ! -s "$known_hosts" ]]; then
  echo "source SSH material is unavailable" >&2
  exit 2
fi

umask 077
sql_file="$(mktemp)"
cleanup() { [[ ! -f "$sql_file" ]] || unlink "$sql_file"; }
trap cleanup EXIT

cat > "$sql_file" <<'SQL'
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='10min';
SELECT '__AICRM_RADAR_SNAPSHOT__|' || to_char(transaction_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"');
SELECT '__AICRM_RADAR_LINK__|' || encode(convert_to(jsonb_build_object(
  'id',id,'public_code',public_code,'name',name,'title',title,
  'destination_url',destination_url,'status',status,
  'cover_image_id',cover_image_id,'attachment_id',attachment_id,
  'version',version,'created_by',created_by,'updated_by',updated_by,
  'created_at',created_at,'updated_at',updated_at
)::text,'UTF8'),'hex') FROM public.radar_links ORDER BY id;
SELECT '__AICRM_RADAR_EVENT__|' || encode(convert_to(jsonb_build_object(
  'id',id,'link_id',link_id,'stage',stage,'page',page_no,'created_at',created_at
)::text,'UTF8'),'hex') FROM public.radar_link_events ORDER BY id;
COMMIT;
SQL

ssh_flags=(-i "$source_key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known_hosts" -o ConnectTimeout=15)
ssh "${ssh_flags[@]}" "${source_user}@${source_host}" psql-stdin < "$sql_file" > "$output"
chmod 0600 "$output"
[[ "$(grep -c '__AICRM_RADAR_SNAPSHOT__|' "$output" || true)" == 1 ]]
echo "captured one consistent read-only Radar snapshot stream"
