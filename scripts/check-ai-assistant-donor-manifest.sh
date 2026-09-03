#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

shasum -a 256 -c docs/migration/ai-assistant/donor-sha256.txt

if rg -n --glob '!web/donors/ai-assistant-production/**' --glob '!docs/**' --glob '!scripts/check-ai-assistant-donor-manifest.sh' 'web/donors/ai-assistant-production' .; then
  echo 'frozen AI Assistant donor imported directly by runtime code' >&2
  exit 1
fi

echo 'AI Assistant donor manifest: OK'
