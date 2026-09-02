#!/usr/bin/env bash

# PR06 closure gate: verify the fixed donor build/runtime contract and the
# v3 preparation boundary without compiling or rewriting donor frontend files.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
DONOR_DIR="$(printenv AICRM_PR06_DONOR_DIR 2>/dev/null || true)"
DONOR_SHA="$(printenv AICRM_PR06_DONOR_SHA 2>/dev/null || true)"
test -n "$DONOR_DIR" || DONOR_DIR=/private/tmp/aicrm-v2-pr04-donor
test -n "$DONOR_SHA" || DONOR_SHA=6bfbe5816bb89913c70adaca87d6a486260e016e
TARGET_ROOT="$REPO_ROOT/web/donors/groupops-v2/src"
ADMIN_BASE="$REPO_ROOT/internal/webshell/templates/admin_base.html"

fail() {
  printf 'PR06 closure check: FAIL: %s\n' "$*" >&2
  exit 1
}

pass() {
  printf 'PR06 closure check: PASS: %s\n' "$*"
}

require_file() {
  test -f "$1" || fail "missing file: $1"
}

require_text() {
  local needle="$1"
  local file="$2"
  rg -F -- "$needle" "$file" >/dev/null || fail "missing marker '$needle' in $file"
}

command -v git >/dev/null 2>&1 || fail "git is required"
command -v rg >/dev/null 2>&1 || fail "rg is required"
command -v cmp >/dev/null 2>&1 || fail "cmp is required"
command -v bash >/dev/null 2>&1 || fail "bash is required"
require_file "$ADMIN_BASE"
test -d "$DONOR_DIR" || fail "missing fixed donor checkout: $DONOR_DIR"
test -d "$TARGET_ROOT" || fail "missing donor archive: $TARGET_ROOT"

donor_head="$(git -C "$DONOR_DIR" rev-parse HEAD 2>/dev/null)" \
  || fail "unable to resolve donor HEAD"
[[ "$donor_head" == "$DONOR_SHA" ]] \
  || fail "donor HEAD $donor_head is not fixed SHA $DONOR_SHA"
pass "donor HEAD is fixed at $DONOR_SHA"

AICRM_PR06_ROOT="$REPO_ROOT" \
AICRM_PR06_DONOR_DIR="$DONOR_DIR" \
AICRM_PR06_DONOR_SHA="$DONOR_SHA" \
  bash "$REPO_ROOT/scripts/check-pr06-donor-manifest.sh"
pass "35/35 donor frontend files are SHA-256 and cmp byte-exact"

BUILD="$DONOR_DIR/web/scripts/build.mjs"
MAIN="$DONOR_DIR/web/src/admin/main.ts"
LEGACY="$DONOR_DIR/web/src/admin/legacy.ts"
REGISTRY="$DONOR_DIR/web/src/admin/registry.json"
NAV="$DONOR_DIR/web/src/admin/nav.json"
GROUPOPS_TEMPLATE="$TARGET_ROOT/admin/templates/groupops.html"
GROUPOPS_DETAIL_TEMPLATE="$TARGET_ROOT/admin/templates/groupopsDetail.html"

require_file "$BUILD"
require_file "$MAIN"
require_file "$LEGACY"
require_file "$REGISTRY"
require_file "$NAV"
require_text "admin: path.join(SRC, 'admin/main.ts')" "$BUILD"
require_text "for (const screen of registry.screens)" "$BUILD"
require_text "admin/templates" "$BUILD"
require_text 'import("./legacy")' "$MAIN"
require_text 'import { AdminController } from "./controller";' "$LEGACY"
require_text 'new AdminController(api, page)' "$LEGACY"
require_text 'import("./sections/groupOpsHistory")' "$LEGACY"
require_text '"key": "groupops"' "$REGISTRY"
require_text '"key": "groupopsDetail"' "$REGISTRY"
require_text '"key": "groupops"' "$NAV"
pass "actual browser chain is build.mjs -> main.ts -> legacy.ts -> AdminController"

template_count="$(find "$TARGET_ROOT/admin/templates" -maxdepth 1 -type f -print | wc -l | tr -d ' ')"
[[ "$template_count" == "2" ]] \
  || fail "expected exactly two archived Group Ops templates, found $template_count"
if find "$TARGET_ROOT" -type f \( -iname '*content*package*' -o -iname '*package*editor*' \) -print -quit | rg . >/dev/null; then
  fail "archive contains an unapproved independent content-package editor file"
fi
if rg -n -i \
    '/api/admin/content-packages|previewMediaContentPackage|createMediaContentPackage|updateMediaContentPackage|contentPackageEditor|content-package-editor' \
    "$TARGET_ROOT/admin/templates" \
    "$TARGET_ROOT/admin/sections" \
    "$TARGET_ROOT/api/admin.ts" >/dev/null; then
  fail "Group Ops active frontend contains a content-package editor/API wiring"
fi
require_text 'content_package' "$TARGET_ROOT/admin/sections/groupOpsHistory.ts"
pass "no independent Group Ops content-package editor; historical content_package remains read-only"

sidebar_count="$( (rg -o 'class="admin-sidebar"' "$ADMIN_BASE" || true) | wc -l | tr -d ' ')"
[[ "$sidebar_count" == "1" ]] \
  || fail "v3 admin_base must contain exactly one admin-sidebar, found $sidebar_count"
if rg -n '<aside|class="side"|\.side\b' "$GROUPOPS_TEMPLATE" "$GROUPOPS_DETAIL_TEMPLATE" >/dev/null; then
  fail "Group Ops business templates contain a second donor sidebar"
fi
pass "PR10 admin_base is the sole sidebar and Group Ops templates are business-only"

if test -d "$REPO_ROOT/web/src"; then
  if rg --files "$REPO_ROOT/web/src" | rg -i 'group.?ops|groupops|content.?package' >/dev/null; then
    fail "v3 active web/src contains a duplicate Group Ops/content-package frontend"
  fi
fi
pass "no duplicate v3 active Group Ops frontend runtime"

if rg -n 'github.com/[^"]+/AI-CRM-v3/internal/(customer|identity|audience|segment|campaign)' \
    "$REPO_ROOT/internal/groupops" --glob '*.go' >/dev/null; then
  fail "Group Ops imports a forbidden Customer/Identity/Audience/Segment/Campaign package"
fi
for deferred_dir in http store worker provider; do
  if test -e "$REPO_ROOT/internal/groupops/$deferred_dir"; then
    fail "prep boundary contains deferred donor implementation directory: internal/groupops/$deferred_dir"
  fi
done
pass "v3 Group Ops prep has no forbidden cross-domain import or copied donor backend"

printf 'PR06 closure check: PASS: frontend hard gates and preparation boundary verified; backend closure remains explicitly deferred.\n'
