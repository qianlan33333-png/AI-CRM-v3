#!/usr/bin/env bash
set -euo pipefail

sha="${1:-}"
staging_host="${STAGING_HOST:?STAGING_HOST is required}"
staging_user="${STAGING_USER:-ubuntu}"
key="${STAGING_KEY:?STAGING_KEY is required}"
known_hosts="${STAGING_KNOWN_HOSTS:?STAGING_KNOWN_HOSTS is required}"
repo_url="${AICRM_V3_REPOSITORY:-https://github.com/qianlan33333-png/AI-CRM-v3.git}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid release sha" >&2; exit 2; }
chmod 600 "$key"
flags=(-i "$key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$known_hosts" -o ConnectTimeout=30)
remote_root="/opt/aicrm/builds/$sha"
remote_archive="$remote_root/aicrm-$sha.tar.gz"
ssh "${flags[@]}" "$staging_user@$staging_host" "sudo install -d -o $staging_user -g $staging_user -m 0755 /opt/aicrm/builds && rm -rf '$remote_root' && git clone --filter=blob:none '$repo_url' '$remote_root' && git -C '$remote_root' checkout --detach '$sha' && cd '$remote_root' && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GITHUB_SHA='$sha' bash scripts/run-donor-view-consumers.sh release-fast"
local_archive="aicrm-$sha.tar.gz"
scp "${flags[@]}" "$staging_user@$staging_host:$remote_archive" "$local_archive"
sha256sum "$local_archive"
