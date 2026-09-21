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
remote_receipt="$remote_root/staging-receipt.json"
ssh "${flags[@]}" "$staging_user@$staging_host" "umask 022 && sudo install -d -o $staging_user -g $staging_user -m 0755 /opt/aicrm/builds && rm -rf '$remote_root' && git clone --filter=blob:none '$repo_url' '$remote_root' && git -C '$remote_root' config core.fileMode true && git -C '$remote_root' checkout --detach '$sha' && cd '$remote_root' && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GITHUB_SHA='$sha' bash scripts/run-donor-view-consumers.sh release-fast"
ssh "${flags[@]}" "$staging_user@$staging_host" "cd '$remote_root' && python3 scripts/check-release-binaries.py release/bin && python3 - '$remote_root' '$sha' <<'PY'
import hashlib, json, pathlib, subprocess, sys
root, sha = map(pathlib.Path, sys.argv[1:])
archive = root / ('aicrm-' + sha.name + '.tar.gz')
tree = subprocess.check_output(['git', '-C', str(root), 'rev-parse', sha.name + '^{tree}'], text=True).strip()
json.dump({'schema': 1, 'repository': 'AI-CRM-v3', 'environment': 'staging', 'status': 'built',
           'commit_sha': sha.name, 'tree_sha': tree,
           'package_sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
           'capability': 'release', 'affected_modules': [], 'required_routes': [],
           'required_services': [], 'required_provider_dependencies': [],
           'business_readback': 'not run; build provenance only'},
          (root / 'staging-receipt.json').open('w'), indent=2)
PY"
local_archive="aicrm-$sha.tar.gz"
local_receipt="staging-receipt-$sha.json"
scp "${flags[@]}" "$staging_user@$staging_host:$remote_archive" "$local_archive"
scp "${flags[@]}" "$staging_user@$staging_host:$remote_receipt" "$local_receipt"
sha256sum "$local_archive"
printf '%s\n' "$local_receipt"
