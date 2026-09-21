#!/usr/bin/env bash
set -euo pipefail

archive="${1:-}"
sha="${2:-}"
[[ -f "$archive" && "$archive" == *"${sha}.tar.gz" ]] || { echo "archive/sha mismatch" >&2; exit 2; }
expected_digest="${STAGING_PACKAGE_SHA256:?STAGING_PACKAGE_SHA256 is required}"
actual_digest="$(sha256sum "$archive" | awk '{print $1}')"
[[ "$actual_digest" == "$expected_digest" ]] || { echo "staging package digest mismatch" >&2; exit 3; }
DEPLOY_TARGET="${PRODUCTION_HOST:?PRODUCTION_HOST is required}" \
DEPLOY_USER="${PRODUCTION_USER:-ubuntu}" \
DEPLOY_KEY="${PRODUCTION_KEY:?PRODUCTION_KEY is required}" \
DEPLOY_KNOWN_HOSTS="${PRODUCTION_KNOWN_HOSTS:?PRODUCTION_KNOWN_HOSTS is required}" \
DEPLOY_ENVIRONMENT=production \
bash scripts/deploy-release-local.sh "$archive" "$sha"
