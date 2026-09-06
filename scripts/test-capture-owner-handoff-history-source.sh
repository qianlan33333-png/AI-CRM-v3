#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
: > "$tmp/key"
: > "$tmp/known_hosts"
chmod 600 "$tmp/key" "$tmp/known_hosts"
for scope in $'wecom-corp:ok\nSELECT' "wecom-corp:bad'quote" 'wecom-corp:semicolon;'; do
  if AICRM_OWNER_HANDOFF_SOURCE_HOST=source.example AICRM_OWNER_HANDOFF_SOURCE_USER=reader AICRM_OWNER_HANDOFF_SOURCE_SSH_KEY_FILE="$tmp/key" AICRM_OWNER_HANDOFF_SOURCE_KNOWN_HOSTS_FILE="$tmp/known_hosts" AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE="$scope" "$root/scripts/capture-owner-handoff-history-source.sh" "$tmp/out" >/dev/null 2>&1; then
    echo "unsafe corp scope was accepted" >&2
    exit 1
  fi
done
printf 'owner-handoff-history-source-scope: PASS\n'
