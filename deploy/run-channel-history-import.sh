#!/usr/bin/env bash
set -euo pipefail

snapshot="${1:-}"
key_file="${2:-}"
manifest_digest="${3:-}"
snapshot_id="${4:-}"
release_sha="${5:-}"

if [[ "$snapshot" != /tmp/aicrm-channel-history-*.enc || "$key_file" != /tmp/aicrm-channel-history-*.key ]]; then
  echo "invalid channel history input paths" >&2
  exit 2
fi
if [[ ! "$manifest_digest" =~ ^[0-9a-f]{64}$ || ! "$snapshot_id" =~ ^channel-[0-9a-f]{24}$ || ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid channel history import identity" >&2
  exit 2
fi
if [[ ! -f "$snapshot" || ! -f "$key_file" || ! -f /etc/aicrm/aicrm.env ]]; then
  echo "channel history import inputs unavailable" >&2
  exit 2
fi

# CI uploads the encrypted snapshot as the restricted deployment user while
# the migrator deliberately runs as the unprivileged aicrm account. Transfer
# ownership only after the exact, bounded input paths have been validated.
chown aicrm:aicrm "$snapshot"
chmod 0600 "$snapshot"

current_release="$(readlink -f /opt/aicrm/current)"
if [[ "$current_release" != "/opt/aicrm/releases/${release_sha}" ]]; then
  echo "requested release is not active" >&2
  exit 3
fi
migrator="${current_release}/bin/migrate-channel-history"
if [[ ! -x "$migrator" ]]; then
  echo "channel history migrator unavailable" >&2
  exit 3
fi

database_url="$(sed -n 's/^AICRM_DATABASE_URL=//p' /etc/aicrm/aicrm.env | tail -n 1)"
snapshot_key="$(tr -d '\r\n' < "$key_file")"
if [[ -z "$database_url" || ! "$snapshot_key" =~ ^[A-Za-z0-9+/]{43}=$ ]]; then
  echo "channel history runtime configuration invalid" >&2
  exit 3
fi

report_root="/var/lib/aicrm/channel-history-imports/${snapshot_id}"
backup_root=/var/backups/aicrm
install -d -m 0700 -o aicrm -g aicrm "$report_root" "$backup_root"
backup_file="${backup_root}/channel-history-pre-${snapshot_id}.dump"

cleanup() {
  unset database_url snapshot_key
  unlink "$snapshot" "$key_file" 2>/dev/null || true
  if [[ "$0" == /tmp/run-channel-history-import-*.sh ]]; then
    unlink "$0" 2>/dev/null || true
  fi
}
trap cleanup EXIT

run_migrator() {
  runuser -u aicrm -- env \
    AICRM_DATABASE_URL="$database_url" \
    AICRM_CHANNEL_SNAPSHOT_KEY="$snapshot_key" \
    "$migrator" "$@"
}

# The backup is the production restore point for this one import. It is kept
# outside the release tree and is never uploaded to CI artifacts.
runuser -u aicrm -- /usr/bin/pg_dump --format=custom --no-owner --no-privileges --file="$backup_file" "$database_url"
chmod 0600 "$backup_file"

run_migrator --mode validate --snapshot "$snapshot" > "$report_root/validate.json"
run_migrator --mode dry-run --snapshot "$snapshot" > "$report_root/dry-run.json"
run_migrator --mode import --snapshot "$snapshot" --manifest-sha256 "$manifest_digest" --confirm > "$report_root/import.json"
run_migrator --mode reconcile --snapshot "$snapshot" --manifest-sha256 "$manifest_digest" > "$report_root/reconcile.json"
run_migrator --mode replay-check --snapshot "$snapshot" --manifest-sha256 "$manifest_digest" > "$report_root/replay-check.json"
chmod 0600 "$report_root"/*.json

# Emit only safe aggregate evidence. Snapshot contents, DSNs and keys are
# deliberately excluded from stdout and from the durable report directory.
python3 - "$report_root" "$backup_file" <<'PY'
import json
import os
import sys

root, backup = sys.argv[1:]
payload = {}
for name in ("validate", "dry-run", "import", "reconcile", "replay-check"):
    with open(os.path.join(root, name + ".json"), encoding="utf-8") as handle:
        payload[name] = json.load(handle)
payload["backup_created"] = os.path.isfile(backup) and os.path.getsize(backup) > 0
print(json.dumps(payload, ensure_ascii=False, separators=(",", ":")))
PY
