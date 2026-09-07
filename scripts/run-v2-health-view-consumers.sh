#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
case "$mode" in
  check)
    scripts/check-pr01-donor-manifest.sh
    scripts/check-pr02-donor-manifest.sh
    scripts/check-pr03-frontend-donor-manifest.sh
    AICRM_SERVICE_PERIOD_MEMBER_GRID_DONOR_DIR="${AICRM_SIDEBAR_DONOR_DIR:?}" scripts/check-service-period-member-grid-donor.sh
    scripts/check-automationops-donor-manifest.sh
    scripts/check-channel-donor-manifest.sh
    donor_dir="$(mktemp -d)"
    git clone --quiet https://github.com/qianlan33333-png/AI-CRM-v2.git "$donor_dir"
    git -C "$donor_dir" checkout --quiet 6bfbe5816bb89913c70adaca87d6a486260e016e
    AICRM_SURVEY_DONOR_DIR="$donor_dir" scripts/check-survey-donor-manifest.sh
    AICRM_PR06_DONOR_DIR="$donor_dir" scripts/check-pr06-closure.sh
    rm -rf "$donor_dir"
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
    scripts/check-ai-assistant-donor-manifest.sh
    bash scripts/test-check-ai-assistant-donor-manifest.sh
    AICRM_SIDEBAR_DONOR_DIR="${AICRM_SIDEBAR_DONOR_DIR:?}" scripts/check-sidebar-customer360-contract.sh
    make radar-check
    node scripts/generate-ai-assistant-client.mjs
    npm run typecheck
    npx tsc -p web/v3/tsconfig.json --noEmit
    npm test
    npm run build
    node --test internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs
    node --test internal/webshell/chromium_launch.test.mjs
    node internal/webshell/owner_handoff_host.test.mjs
    node scripts/build-v3-host-adapters.mjs
    node scripts/operation-cycles-shell-e2e.mjs
    node scripts/ai-assistant-shell-e2e.mjs
    TZ=Asia/Shanghai node scripts/open-platform-host-e2e.mjs
    mkdir -p release
    node scripts/stage-pr01-effects-ui.mjs web/dist release/web/dist
    node scripts/test-stage-pr01-effects-ui.mjs
    node scripts/test-groupops-history-release.mjs web/dist release/web/dist
    node scripts/stage-survey-ui.mjs web/dist release/web/dist
    node scripts/test-stage-survey-ui.mjs web/dist release/web/dist
    node scripts/stage-new-shell-ui.mjs web/dist release/web/dist
    node scripts/test-stage-new-shell-ui.mjs web/dist release/web/dist
    scripts/check-install-release-contract.sh
    ;;
  release)
    scripts/check-pr01-donor-manifest.sh
    scripts/check-pr02-donor-manifest.sh
    scripts/check-pr03-frontend-donor-manifest.sh
    AICRM_SERVICE_PERIOD_MEMBER_GRID_DONOR_DIR="${AICRM_SIDEBAR_DONOR_DIR:?}" scripts/check-service-period-member-grid-donor.sh
    scripts/check-automationops-donor-manifest.sh
    scripts/check-channel-donor-manifest.sh
    scripts/check-survey-donor-manifest.sh
    node web/scripts/channel-center-characterization.mjs
    node web/scripts/survey-editor-characterization.mjs
    node web/scripts/survey-public-characterization.mjs
    node web/scripts/survey-unresolved-history-contract.mjs
    node --check internal/webshell/static/admin_console/survey_operations.js
    node --check internal/webshell/static/admin_console/admin_audience_detail.js
    scripts/check-pr08-frontend-donor-manifest.sh
    scripts/check-pr09-frontend-freeze.sh
    scripts/check-config-definition-import-boundary.sh
    scripts/check-ai-assistant-donor-manifest.sh
    bash scripts/test-check-ai-assistant-donor-manifest.sh
    AICRM_SIDEBAR_DONOR_DIR="${AICRM_SIDEBAR_DONOR_DIR:?}" scripts/check-sidebar-customer360-contract.sh
    make radar-check
    node scripts/generate-ai-assistant-client.mjs
    npm run typecheck
    npx tsc -p web/v3/tsconfig.json --noEmit
    npm run build
    node --test internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs
    node --test internal/webshell/chromium_launch.test.mjs
    node internal/webshell/owner_handoff_host.test.mjs
    node scripts/build-v3-host-adapters.mjs
    node scripts/operation-cycles-shell-e2e.mjs
    node scripts/ai-assistant-shell-e2e.mjs
    TZ=Asia/Shanghai node scripts/open-platform-host-e2e.mjs
    cp -R migrations deploy release/
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
    tar -C release -czf "aicrm-${GITHUB_SHA}.tar.gz" .
    ;;
  *)
    echo "usage: scripts/run-v2-health-view-consumers.sh check|release" >&2
    exit 2
    ;;
esac
