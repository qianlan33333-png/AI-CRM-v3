package main

import (
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The fixture uses the real admin session, composition, migrations and owner
// transactions. Provider calls remain disabled; model behavior has separate tests.
func TestPostgreSQLCoreOperationsChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f := newAudienceConfirmationChromiumFixture(t)
	command := exec.CommandContext(f.ctx, "node", filepath.Join(filepath.Dir(f.script), "core_operations_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_AUDIENCE_CONFIRMATION_TEST_URL="+f.server.URL, "AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME=groupops-browser-owner", "AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD=groupops-browser-owner-password", "AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID="+strconv.FormatInt(f.packageID, 10), "AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR="+f.screenshots)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "core_operations_chromium: PASS") {
		t.Fatalf("journey=%v output=%s", err, output)
	}
	var products, prompts, assignments int
	if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM segment_core_products),(SELECT count(*) FROM segment_core_prompt_versions),(SELECT count(*) FROM segment_core_assignments)`).Scan(&products, &prompts, &assignments); err != nil || products != 1 || prompts != 1 || assignments != 0 {
		t.Fatalf("products=%d prompts=%d assignments=%d err=%v", products, prompts, assignments, err)
	}
	t.Log(string(output))
}
