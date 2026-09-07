#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

shasum -a 256 -c docs/migration/ai-assistant/donor-sha256.txt

# The audit tools record frozen source paths as evidence. They inspect Git objects
# only and are not runtime/build consumers, so keep that explicit development-only
# boundary out of this runtime-import guard. All other scripts remain scanned.
if rg -n --glob '!web/donors/ai-assistant-production/**' --glob '!docs/**' --glob '!scripts/audit/**' --glob '!scripts/check-ai-assistant-donor-manifest.sh' --glob '!scripts/build-v3-host-adapters.mjs' 'web/donors/ai-assistant-production' .; then
  echo 'frozen AI Assistant donor imported directly by runtime code' >&2
  exit 1
fi

echo 'AI Assistant donor manifest: OK'
