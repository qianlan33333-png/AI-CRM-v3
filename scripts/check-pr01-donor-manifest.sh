#!/usr/bin/env bash
set -euo pipefail

# This is deliberately a full source-set gate, not a spot check. The frozen
# V2 business frontend is immutable. The survey-scoped files listed below are
# explicit V3 adapter, generated-contract, H5-extension, and characterization
# boundaries; the dedicated survey manifest still byte-locks its 16 donor files.
manifest="docs/donor-manifests/pr01-web.sha256"
test -s "$manifest" || { echo "missing PR01 web donor manifest" >&2; exit 1; }

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

cat > "$scratch/v3-owned-survey" <<'EOF'
web/scripts/e2e.mjs
web/scripts/channel-center-characterization.mjs
web/scripts/survey-editor-characterization.mjs
web/scripts/survey-public-characterization.mjs
web/src/admin/sections/questionnaireEditor.ts
web/src/api/questionnaireEditorV3.ts
web/src/h5/controller.ts
web/src/h5/templates/all.html
web/src/h5/templates/one.html
web/src/h5/templates/result.html
web/src/api/channelCenter.test.ts
EOF

awk 'NF == 2 && $1 ~ /^[0-9a-f]{64}$/ { print $2 }' "$manifest" \
  | grep -Fvx -f "$scratch/v3-owned-survey" \
  | sort > "$scratch/expected"
test -s "$scratch/expected" || { echo "empty PR01 web donor manifest" >&2; exit 1; }
test "$(sort -u "$scratch/expected" | wc -l | tr -d ' ')" = "$(wc -l < "$scratch/expected" | tr -d ' ')" || {
  echo "duplicate path in PR01 web donor manifest" >&2; exit 1;
}

{
  printf '%s\n' package.json package-lock.json web/tsconfig.json
  find web/src web/scripts -type f -print
} | grep -Fvx -f "$scratch/v3-owned-survey" | sort > "$scratch/actual"

if ! diff -u "$scratch/expected" "$scratch/actual"; then
  echo "PR01 donor file set is incomplete or contains an unapproved file" >&2
  exit 1
fi

while read -r checksum path; do
  [[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || continue
  test -f "$path" || { echo "missing PR01 donor file: $path" >&2; exit 1; }
  actual="$(hash_file "$path")"
  test "$actual" = "$checksum" || { echo "PR01 donor checksum mismatch: $path" >&2; exit 1; }
done < <(grep -Fv -f "$scratch/v3-owned-survey" "$manifest")
