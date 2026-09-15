package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLPublicCommerceChromiumJourney uses the real Composition Root,
// PostgreSQL Product facts, anonymous manifest closure, and Chromium. It
// deliberately stops before the checkout action: payments, OAuth, identity
// resolution, and Provider effects belong to their existing owners. The
// separate PostgreSQL response-loss journey remains the payment-recovery
// evidence for an accepted order that loses its initial response.
func TestPostgreSQLPublicCommerceChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newProductExternalPushChromiumFixtureWithTimeout(t, 110*time.Second)
	unavailableCode := seedPublicCommerceUnavailableServicePeriod(t, fixture.ctx, fixture.application)

	screenshots := t.TempDir()
	if configured := platformconfig.PublicCommerceScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "public_commerce_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_PUBLIC_COMMERCE_TEST_URL="+fixture.server.URL,
		"AICRM_PUBLIC_COMMERCE_STANDARD_CODE=browser-push-product",
		"AICRM_PUBLIC_COMMERCE_SERVICE_CODE=browser-push-service-period",
		"AICRM_PUBLIC_COMMERCE_UNAVAILABLE_SERVICE_CODE="+unavailableCode,
		"AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR="+screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "public_commerce_chromium: PASS") {
		t.Fatalf("public commerce Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	for _, name := range []string{
		"public-standard-detail-375.png",
		"public-standard-payment-390.png",
		"public-service-available-430.png",
		"public-service-available-payment-390.png",
		"public-service-unavailable-detail-375.png",
		"public-service-unavailable-payment-430.png",
	} {
		info, statErr := os.Stat(filepath.Join(screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("public commerce screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

func seedPublicCommerceUnavailableServicePeriod(t *testing.T, ctx context.Context, application *composedApplication) string {
	t.Helper()
	const code = "browser-public-service-unavailable"
	projection := `{"schema_version":1,"status":"service_period_disabled","enabled":false,"buy_button_text":"暂未开放","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	var id int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection)
		VALUES($1,'周期商品暂未开放','真实周期不可用公开页夹具',12800,'CNY',0,1,$2::jsonb) RETURNING id`, code, projection).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,30)`, id); err != nil {
		t.Fatal(err)
	}
	return code
}
