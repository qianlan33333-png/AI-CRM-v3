#!/usr/bin/env bash
set -euo pipefail

# Staging is the only release builder. It receives a local v3 git bundle and
# never depends on GitHub connectivity.
sha="${1:-}"
bundle="${AICRM_SOURCE_BUNDLE:-}"
staging_host="${STAGING_HOST:?STAGING_HOST is required}"
staging_user="${STAGING_USER:-ubuntu}"
key="${STAGING_KEY:?STAGING_KEY is required}"
known_hosts="${STAGING_KNOWN_HOSTS:?STAGING_KNOWN_HOSTS is required}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid release sha" >&2; exit 2; }
[[ -f "$bundle" ]] || { echo "AICRM_SOURCE_BUNDLE must point to a local git bundle" >&2; exit 2; }
git bundle verify "$bundle" >/dev/null
git bundle list-heads "$bundle" | awk '{print $1}' | grep -Fxq "$sha" || { echo "source bundle does not contain requested commit $sha" >&2; exit 2; }
chmod 600 "$key"
flags=(-i "$key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$known_hosts" -o ConnectTimeout=30)
remote_bundle="/opt/aicrm/source-bundles/$sha.bundle"
remote_root="/opt/aicrm/builds/$sha"
remote_archive="$remote_root/aicrm-$sha.tar.gz"
remote_receipt="$remote_root/staging-receipt.json"
scp "${flags[@]}" "$bundle" "$staging_user@$staging_host:$remote_bundle.tmp"
remote_helper="/tmp/build-release-on-staging-${sha}.sh"
scp "${flags[@]}" deploy/build-release-on-staging-remote.sh "$staging_user@$staging_host:$remote_helper"
ssh "${flags[@]}" "$staging_user@$staging_host" "set -euo pipefail; umask 022; sudo install -d -o $staging_user -g $staging_user -m 0755 /opt/aicrm/source-bundles /opt/aicrm/builds; sudo touch /opt/aicrm/staging-build.lock; sudo chown $staging_user:$staging_user /opt/aicrm/staging-build.lock; mv '$remote_bundle.tmp' '$remote_bundle'; chmod 755 '$remote_helper'; '$remote_helper' '$sha' '$remote_bundle' '$remote_root'"

local_archive="aicrm-$sha.tar.gz"
local_receipt="staging-receipt-$sha.json"
scp "${flags[@]}" "$staging_user@$staging_host:$remote_archive" "$local_archive"
scp "${flags[@]}" "$staging_user@$staging_host:$remote_receipt" "$local_receipt"
sha256sum "$local_archive"
printf '%s\n' "$local_receipt"
