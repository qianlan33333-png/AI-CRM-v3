#!/usr/bin/env bash
set -euo pipefail
sha="${1:?sha}"
remote_bundle="${2:?bundle}"
remote_root="${3:?root}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]]
flock -x /opt/aicrm/staging-build.lock -c "
  rm -rf '$remote_root'
  git clone --no-checkout '$remote_bundle' '$remote_root'
  git -C '$remote_root' config core.fileMode true
  git -C '$remote_root' checkout --detach '$sha'
  cd '$remote_root'
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GITHUB_SHA='$sha' bash scripts/run-donor-view-consumers.sh release-fast
  python3 scripts/check-release-binaries.py release/bin
  python3 - '$remote_root' '$sha' <<'PY'
import hashlib, json, pathlib, subprocess, sys
root, sha = map(pathlib.Path, sys.argv[1:])
archive = root / ('aicrm-' + sha.name + '.tar.gz')
tree = subprocess.check_output(['git', '-C', str(root), 'rev-parse', sha.name + '^{tree}'], text=True).strip()
json.dump({'schema': 1, 'repository': 'AI-CRM-v3', 'environment': 'staging', 'status': 'built',
           'commit_sha': sha.name, 'tree_sha': tree,
           'package_sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
           'package_name': archive.name, 'capability': 'release', 'affected_modules': [],
           'required_routes': [], 'required_services': [], 'required_provider_dependencies': [],
           'business_readback': 'not run; build provenance only'},
          (root / 'staging-receipt.json').open('w'), indent=2)
PY
"
