package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	hxcdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// OneID decision: not involved. The fixture seeds a public-safe HXC projection
// only to exercise the existing dashboard UI; it neither resolves nor changes
// an identity. Persistence decision: this is a test-only PostgreSQL fixture;
// the production layout code is stateless and never writes or invokes a
// Provider.
type adminShellLayoutFixture struct {
	*productExternalPushChromiumFixture
	screenshots string
}

// TestPostgreSQLAdminShellLayoutCompositionPreflight keeps the real release
// artifact and outer Composition routes under the ordinary PostgreSQL check.
// Chromium is deliberately a separate mandatory Linux step below.
func TestPostgreSQLAdminShellLayoutCompositionPreflight(t *testing.T) {
	fixture := newAdminShellLayoutFixture(t)
	session, csrf := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	refresh := httptest.NewRequest(http.MethodPost, "/api/admin/hxc-dashboard/refreshes", nil)
	refresh.Header.Set("Idempotency-Key", "admin-shell-layout-hxc-refresh")
	refresh.Header.Set("X-CSRF-Token", csrf)
	refresh.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	refresh.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	refreshResponse := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusServiceUnavailable || !strings.Contains(refreshResponse.Body.String(), `"hxc_sync_disabled"`) {
		t.Fatalf("HXC refresh binding status=%d disabled=%t", refreshResponse.Code, strings.Contains(refreshResponse.Body.String(), `"hxc_sync_disabled"`))
	}

	navigation := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion")
	if navigation.Code != http.StatusOK {
		t.Fatalf("admin layout navigation status=%d", navigation.Code)
	}
	for _, href := range []string{
		"/admin/automation-conversion", "/admin/operation-cycles", "/admin/automation-conversion/group-ops/ui", "/admin/channels", "/admin/cloud-orchestrator/plans", "/admin/customers", "/admin/hxc-dashboard", "/admin/questionnaires", "/admin/radar-links", "/admin/wecom-tags", "/admin/orders", "/admin/wechat-pay/products", "/admin/service-period-products", "/admin/coupons", "/admin/image-library", "/admin/miniprogram-library", "/admin/attachment-library", "/admin/automation-agents", "/admin/owner-migration", "/admin/config", "/admin/oneid", "/admin/api-docs",
	} {
		if !strings.Contains(navigation.Body.String(), `href="`+href+`"`) {
			t.Fatalf("admin layout navigation href=%q is absent from the actual Webshell menu", href)
		}
	}

	for _, route := range []struct {
		path            string
		canonicalPath   string
		canonicalStatus int
		marker          string
		expectTopbar    bool
	}{
		// Main navigation: every entry remains its actual UI owner rather than a
		// static fallback. The representative Chromium journey below measures the
		// three distinct layout types.
		{path: "/admin/automation-conversion", marker: `class="admin-topbar"`, expectTopbar: true},
		{path: "/admin/external-effects?view=external-effects", canonicalPath: "/admin/campaigns.html?view=external-effects", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/operation-cycles", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/automation-conversion/group-ops/ui", canonicalPath: "/admin/groupops.html", canonicalStatus: http.StatusFound, marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/groupops.html", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/channels", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/cloud-orchestrator/plans", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/customers", marker: `class="admin-topbar"`, expectTopbar: true},
		{path: "/admin/hxc-dashboard", marker: `admin-workspace-stage--dynamic`, expectTopbar: true},
		{path: "/admin/questionnaires", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/radar-links", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/wecom-tags", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/orders", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/wechat-pay/products", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/service-period-products", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/coupons", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/image-library", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/miniprogram-library", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/attachment-library", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/automation-agents", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/owner-migration", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/config", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/config/releases", marker: `data-runtime-release-host`, expectTopbar: true},
		{path: "/admin/oneid", marker: `class="admin-topbar"`, expectTopbar: true},
		// Open Platform is an authenticated V3 Host injected into the built
		// apidocs document. The vanity route must canonicalize before that
		// document loads; do not mistake the deliberate 303 for a missing Host.
		{path: "/admin/api-docs", canonicalPath: "/admin/apidocs.html", marker: `openPlatformHost-`, expectTopbar: false},
		// Canonical detail/form aliases must keep the same owning Host and layout.
		{path: "/admin/productForm.html?id=" + strconv.FormatInt(fixture.productID, 10), marker: `data-page="productForm"`, expectTopbar: false},
		{path: "/admin/spProductForm.html?id=" + strconv.FormatInt(fixture.serviceProductID, 10), marker: `data-page="spProductForm"`, expectTopbar: false},
	} {
		response := authenticatedAdminGet(t, fixture.application.handler, session, route.path)
		if route.canonicalPath != "" {
			canonicalStatus := route.canonicalStatus
			if canonicalStatus == 0 {
				canonicalStatus = http.StatusSeeOther
			}
			if response.Code != canonicalStatus || response.Header().Get("Location") != route.canonicalPath {
				t.Fatalf("outer admin layout canonical route=%s status=%d location=%q expected_status=%d expected_location=%q", route.path, response.Code, response.Header().Get("Location"), canonicalStatus, route.canonicalPath)
			}
			response = authenticatedAdminGet(t, fixture.application.handler, session, route.canonicalPath)
		}
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, route.marker) || (strings.Count(body, `<header class="admin-topbar">`) == 1) != route.expectTopbar {
			t.Fatalf("outer admin layout route=%s canonical=%s status=%d marker=%t topbar_count=%d expected_topbar=%t", route.path, route.canonicalPath, response.Code, strings.Contains(body, route.marker), strings.Count(body, `<header class="admin-topbar">`), route.expectTopbar)
		}
	}
}

// TestPostgreSQLAdminShellLayoutChromiumJourney measures the actual composed
// admin pages after Access login. It is deliberately mandatory on Linux CI:
// DOM shape or HTTP 200 cannot prove the shell geometry or asset layout.
func TestPostgreSQLAdminShellLayoutChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newAdminShellLayoutFixture(t)
	script := filepath.Join(filepath.Dir(fixture.script), "..", "..", "internal", "webshell", "admin_layout_geometry.mjs")
	command := exec.CommandContext(fixture.ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_ADMIN_LAYOUT_TEST_URL="+fixture.server.URL,
		"AICRM_ADMIN_LAYOUT_TEST_USERNAME=product-browser-owner",
		"AICRM_ADMIN_LAYOUT_TEST_PASSWORD=product-browser-owner-password",
		"AICRM_ADMIN_LAYOUT_TEST_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_SERVICE_PRODUCT_ID="+strconv.FormatInt(fixture.serviceProductID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_HISTORICAL_ORDER="+fixture.historicalOrderReference,
		"AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR="+fixture.screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("admin shell Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "admin_shell_layout_chromium: PASS") {
		t.Fatalf("admin shell Chromium journey did not report success: %q", output)
	}
	for _, name := range []string{
		"automation.png", "cycles.png", "groupops.png", "channels.png", "ai.png", "customers.png", "hxc.png", "questionnaires.png", "radar.png", "tags.png",
		"orders.png", "products.png", "service-period-products.png", "product.png", "service-period-product.png", "coupons.png", "image-library.png", "miniprogram-library.png", "attachment-library.png",
		"automation-agents.png", "owner-migration.png", "config.png", "runtime-config.png", "oneid.png", "api-docs.png", "order-detail-history.png", "external-effects.png",
	} {
		info, statErr := os.Stat(filepath.Join(fixture.screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("admin shell Chromium screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

func newAdminShellLayoutFixture(t *testing.T) *adminShellLayoutFixture {
	t.Helper()
	screenshots := t.TempDir()
	if configured := platformconfig.AdminLayoutScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatalf("AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatalf("create admin layout screenshot directory: %v", err)
		}
		screenshots = configured
	}
	fixture := &adminShellLayoutFixture{productExternalPushChromiumFixture: newProductExternalPushChromiumFixture(t), screenshots: screenshots}
	seedAdminShellLayoutHXC(t, fixture.ctx, fixture.application)
	return fixture
}

func authenticatedAdminGet(t *testing.T, handler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, session, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", path, nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	handler.ServeHTTP(response, request)
	return response
}

func seedAdminShellLayoutHXC(t *testing.T, ctx context.Context, application *composedApplication) {
	t.Helper()
	store := hxcstore.NewPostgreSQL(application.pool.Native())
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("admin-shell-layout-hxc-refresh"))
	var runID int64
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(
		run_key,request_digest,trigger,identity_mode,status,source_count,processed_count,identity_replay_verified_count
	) VALUES('admin-shell-layout-hxc',$1,'initial','inspect','publishing',30,30,0) RETURNING id`, digest[:]).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC)
	rows := make([]hxcdomain.ProjectionRow, 0, 30)
	for index := 0; index < 30; index++ {
		rows = append(rows, hxcdomain.ProjectionRow{
			SubjectDigest: [32]byte{byte(index + 1)},
			UserRef:       fmt.Sprintf("HXC-%012x", index+1),
			Stage:         hxcdomain.RegisteredNoActiveMembership,
			SourceRow: hxcdomain.SourceRow{
				MembershipAttribution: "none", CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`), SourceUpdatedAt: asOf,
			},
			IdentityState: hxcdomain.Unmatched, MatchedBy: "none", IdentityReasonCode: "no_match",
		})
	}
	projection := hxcdomain.Projection{
		AsOf:   asOf,
		Counts: hxcdomain.Counts{Total: 30, RegisteredNoActiveMembership: 30, Unmatched: 30, PendingObservation: 30},
		Rows:   rows,
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, publishErr := store.Publish(tx, runID, projection)
		return publishErr
	}); err != nil {
		t.Fatal(err)
	}
}
