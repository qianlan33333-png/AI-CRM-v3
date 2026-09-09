#!/usr/bin/env bash
set -euo pipefail

# This script always performs the non-destructive source-view preparation
# itself. After P4 the exact ignored views are materialized untracked for every consumer.
mode="${1:-}"
v2_donor="${AICRM_V2_FROZEN_DONOR_DIR:-${PR07_DONOR_DIR:?PR07_DONOR_DIR is required}}"
sidebar_donor="${AICRM_SIDEBAR_DONOR_DIR:?AICRM_SIDEBAR_DONOR_DIR is required}"
node scripts/check-donor-source-view-ignore.mjs >/dev/null
node scripts/prepare-donor-source-views.mjs >/dev/null

check_v2_donor() {
  [[ -d "$v2_donor/.git" ]] || { echo "missing V2 donor Git checkout: $v2_donor" >&2; exit 2; }
  [[ "$(git -C "$v2_donor" rev-parse HEAD)" == "6bfbe5816bb89913c70adaca87d6a486260e016e" ]] || {
    echo "V2 donor revision differs from the frozen source identity" >&2
    exit 2
  }
  git -C "$v2_donor" diff --quiet HEAD --
  git -C "$v2_donor" diff --cached --quiet
}

run_frozen_consumer_gates() {
  bash scripts/check-standard-components-donor-manifest.sh
  check_v2_donor
  scripts/check-pr01-donor-manifest.sh
  scripts/check-pr02-donor-manifest.sh
  scripts/check-pr03-frontend-donor-manifest.sh
  PR04_DONOR_ROOT="${PR04_DONOR_ROOT:-$v2_donor}" scripts/check-pr04-donor-manifest.sh
  PR05_DONOR_ROOT="${PR05_DONOR_ROOT:-$v2_donor}" scripts/check-pr05-closure.sh
  AICRM_PR06_DONOR_DIR="$v2_donor" scripts/check-pr06-closure.sh
  AICRM_SERVICE_PERIOD_MEMBER_GRID_DONOR_DIR="$sidebar_donor" scripts/check-service-period-member-grid-donor.sh
  scripts/check-automationops-donor-manifest.sh
  scripts/check-channel-donor-manifest.sh
  AICRM_SURVEY_DONOR_DIR="$v2_donor" scripts/check-survey-donor-manifest.sh
  scripts/check-pr07-frontend-freeze.sh
  node web/scripts/channel-center-characterization.mjs
  node web/scripts/survey-editor-characterization.mjs
  node web/scripts/survey-public-characterization.mjs
  node web/scripts/survey-unresolved-history-contract.mjs
  node --check internal/webshell/static/admin_console/survey_operations.js
  node --check internal/webshell/static/admin_console/admin_audience_detail.js
  scripts/check-pr08-frontend-donor-manifest.sh
  scripts/check-pr09-frontend-freeze.sh
  scripts/check-config-definition-import-boundary.sh
  node --test scripts/check-p4-donor-view-closure.test.mjs
  scripts/check-ai-assistant-donor-manifest.sh
  bash scripts/test-check-ai-assistant-donor-manifest.sh
  AICRM_SIDEBAR_DONOR_DIR="$sidebar_donor" scripts/check-sidebar-customer360-contract.sh
}

run_frontend_and_stage_checks() {
  make radar-donor-check
  bash scripts/check-radar-boundaries.sh
  node scripts/generate-ai-assistant-client.mjs
  npm run typecheck
  npx tsc -p web/v3/tsconfig.json --noEmit
  node scripts/sidebar-wecom-jssdk-contract.mjs
  if [[ "$mode" == check ]]; then npm test; fi
  # Some frontend journeys install Hosts into dist. Rebuild the inexpensive
  # raw frontend before staging so the final package cannot contain test edits.
  npm run build
  node --test internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs
  node --test internal/webshell/chromium_launch.test.mjs
  node internal/webshell/owner_handoff_host.test.mjs
  node scripts/build-v3-host-adapters.mjs
  node scripts/groupops-host-adapter-e2e.mjs
  node scripts/operation-cycles-shell-e2e.mjs
  node scripts/ai-assistant-shell-e2e.mjs
  TZ=Asia/Shanghai node scripts/open-platform-host-e2e.mjs
  node scripts/order-host-adapter-e2e.mjs
  node web/v3/channelCenterAdapter.test.mjs
  node web/v3/customerAdapter.test.mjs
  node web/v3/adminSessionHost.test.mjs
  node web/v3/h5AuthAdapter.test.mjs
  node web/v3/productAdapter.save_recovery.test.mjs
  node web/v3/productAdapter.sp_material.test.mjs
  node web/v3/orderAdapter.test.mjs
  node web/v3/couponAdapter.test.mjs
  node web/v3/channelAdmissionHost.test.mjs
  node web/v3/standardComponentsRefresh.test.mjs
  node web/v3/materialSaveAdapter.test.mjs
  node web/v3/actionFeedback.test.mjs
  node web/v3/productAdapter.upload_feedback.test.mjs
  node web/v3/radarAdapter.upload_feedback.test.mjs
  node web/v3/surfaceFeedbackHost.test.mjs
  node web/v3/memberGridFeedbackHost.test.mjs
  node web/v3/radarAdapter.test.mjs
  node web/v3/sidebar_send_recovery.test.mjs
  node internal/webshell/static/admin_console/tag_sync_bridge.test.mjs
  node internal/webshell/static/admin_console/survey_share_guard_real_host.test.mjs
}

build_release_binaries() {
  mkdir -p release/bin
  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm ./cmd/aicrm
  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm-operation-cycle-runner ./cmd/operation-cycle-runner
  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm-operation-cycle-result ./cmd/operation-cycle-result
  scripts/build-wecom-archive-sdk-runner-linux.sh release/bin/wecom-archive-sdk-runner
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-platform ./cmd/migrate-platform
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-river ./cmd/migrate-river
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-phone-identities ./cmd/migrate-phone-identities
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-identity-phone-vault ./cmd/migrate-identity-phone-vault
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-survey-v2 ./cmd/migrate-survey-v2
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-commerce-history ./cmd/migrate-commerce-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-message-archive ./cmd/migrate-message-archive
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-order-attribution ./cmd/migrate-order-attribution
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-automation-operations ./cmd/migrate-automation-operations
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-config-definitions ./cmd/migrate-v2-config-definitions
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-runtime-config-releases ./cmd/migrate-v2-runtime-config-releases
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-commerce-external-push-history ./cmd/migrate-v2-commerce-external-push-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-open-platform ./cmd/migrate-open-platform
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-media-legacy-materials ./cmd/migrate-media-legacy-materials
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-channel-history ./cmd/migrate-channel-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-customer-tag-history ./cmd/migrate-v2-customer-tag-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-owner-handoff-history ./cmd/migrate-owner-handoff-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-radar-v2 ./cmd/migrate-radar-v2
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-sidebar-history ./cmd/migrate-sidebar-history
  go build -trimpath -ldflags "-s -w" -o release/bin/bootstrap-automation-operations ./cmd/bootstrap-automation-operations
}

# Pure compilation/staging is reusable by CI and deployment. Regression belongs
# to PR jobs, not the main-to-server path. Structural package checks stay here.
build_frontend() {
  node scripts/generate-ai-assistant-client.mjs
  npm run build
  node scripts/build-v3-host-adapters.mjs
}

stage_frontend() {
    mkdir -p release
    node scripts/stage-pr01-effects-ui.mjs web/dist release/web/dist
    node scripts/test-stage-pr01-effects-ui.mjs
    node scripts/test-groupops-history-release.mjs web/dist release/web/dist
    node scripts/stage-survey-ui.mjs web/dist release/web/dist
    node scripts/test-stage-survey-ui.mjs web/dist release/web/dist
    node scripts/stage-new-shell-ui.mjs web/dist release/web/dist
    node scripts/test-stage-new-shell-ui.mjs web/dist release/web/dist
}

case "$mode" in
  stage)
    build_frontend
    stage_frontend
    ;;
  release-fast)
    build_release_binaries
    build_frontend
    stage_frontend
    cp -R migrations deploy release/
    mkdir -p release/components/excel-batches
    cp components/excel-batches/batches.py components/excel-batches/requirements.txt components/excel-batches/aicrm-excel-batches.service release/components/excel-batches/
    (
      cd release
      LC_ALL=C find . -type f ! -name release-files.sha256 -print0 \
        | sort -z \
        | xargs -0 sha256sum > release-files.sha256
      sha256sum --strict --check release-files.sha256
    )
    tar -C release -czf "aicrm-${GITHUB_SHA:?GITHUB_SHA is required}.tar.gz" .
    ;;
  check)
    run_frozen_consumer_gates
    run_frontend_and_stage_checks
    stage_frontend
    scripts/check-install-release-contract.sh
    ;;
  release)
    build_release_binaries
    run_frozen_consumer_gates
    run_frontend_and_stage_checks
    cp -R migrations deploy release/
    mkdir -p release/components/excel-batches
    cp components/excel-batches/batches.py components/excel-batches/requirements.txt components/excel-batches/aicrm-excel-batches.service release/components/excel-batches/
    node scripts/stage-pr01-effects-ui.mjs web/dist release/web/dist
    node scripts/test-stage-pr01-effects-ui.mjs
    node scripts/test-groupops-history-release.mjs web/dist release/web/dist
    node scripts/stage-survey-ui.mjs web/dist release/web/dist
    node scripts/test-stage-survey-ui.mjs web/dist release/web/dist
    node scripts/stage-new-shell-ui.mjs web/dist release/web/dist
    node scripts/test-stage-new-shell-ui.mjs web/dist release/web/dist
    (
      cd release
      LC_ALL=C find . -type f ! -name release-files.sha256 -print0 \
        | sort -z \
        | xargs -0 sha256sum > release-files.sha256
      sha256sum --strict --check release-files.sha256
    )
    tar -C release -czf "aicrm-${GITHUB_SHA:?GITHUB_SHA is required}.tar.gz" .
    ;;
  *)
    echo "usage: scripts/run-donor-view-consumers.sh check|stage|release|release-fast" >&2
    exit 2
    ;;
esac
