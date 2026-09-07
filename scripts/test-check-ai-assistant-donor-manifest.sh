#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-ai-assistant-donor-manifest.sh"

test -x "$checker" || { echo "missing executable AI Assistant donor checker" >&2; exit 1; }
"$checker"

# These fragments deliberately create fixture contents at runtime: the fixture
# verifies the scanner boundary without becoming a real repository reference.
donor_prefix="web/donors"
donor_name="ai-assistant-production"
audit_fixture="$repo_root/scripts/audit/.ai-assistant-donor-gate-$$"
runtime_fixture="$repo_root/internal/audit_donor_gate_fixture_$$"
trap 'rm -rf "$audit_fixture" "$runtime_fixture"' EXIT
mkdir -p "$audit_fixture" "$runtime_fixture"
printf 'audit source record: %s/%s\n' "$donor_prefix" "$donor_name" > "$audit_fixture/source-record.txt"
"$checker"

printf 'const frozenDonor = "%s/%s"\n' "$donor_prefix" "$donor_name" > "$runtime_fixture/runtime_reference.go"
if "$checker" >/dev/null 2>&1; then
  echo "AI Assistant donor checker accepted a runtime reference" >&2
  exit 1
fi

echo "AI Assistant donor manifest boundary self-test passed"
