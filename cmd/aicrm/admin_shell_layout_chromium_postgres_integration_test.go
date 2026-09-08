package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
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
	radarID     int64
	aiPlanID    int64
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

	radarList := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/radar-links")
	radarDetail := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/radar-links/"+strconv.FormatInt(fixture.radarID, 10))
	if radarList.Code != http.StatusOK || !strings.Contains(radarList.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)) || radarDetail.Code != http.StatusOK || !strings.Contains(radarDetail.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)) {
		t.Fatalf("admin layout radar read list_status=%d list_seeded=%t detail_status=%d detail_seeded=%t", radarList.Code, strings.Contains(radarList.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)), radarDetail.Code, strings.Contains(radarDetail.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)))
	}
	aiPlan := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/ai-assistant/plans/"+strconv.FormatInt(fixture.aiPlanID, 10))
	aiRecipients := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/ai-assistant/plans/"+strconv.FormatInt(fixture.aiPlanID, 10)+"/recipients?limit=50")
	if aiPlan.Code != http.StatusOK || !strings.Contains(aiPlan.Body.String(), `"id":`+strconv.FormatInt(fixture.aiPlanID, 10)) || aiRecipients.Code != http.StatusOK || !strings.Contains(aiRecipients.Body.String(), `"items"`) {
		t.Fatalf("admin layout native AI read plan_status=%d plan_seeded=%t recipients_status=%d recipients=%t", aiPlan.Code, strings.Contains(aiPlan.Body.String(), `"id":`+strconv.FormatInt(fixture.aiPlanID, 10)), aiRecipients.Code, strings.Contains(aiRecipients.Body.String(), `"items"`))
	}

	navigation := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion")
	if navigation.Code != http.StatusOK {
		t.Fatalf("admin layout navigation status=%d", navigation.Code)
	}
	for _, href := range []string{
		"/admin/automation-conversion", "/admin/operation-cycles", "/admin/automation-conversion/group-ops/ui", "/admin/channels", "/admin/cloud-orchestrator/plans", "/admin/customers", "/admin/hxc-dashboard", "/admin/questionnaires", "/admin/radar-links", "/admin/wecom-tags", "/admin/orders", "/admin/wechat-pay/products", "/admin/service-period-products", "/admin/coupons", "/admin/image-library", "/admin/miniprogram-library", "/admin/attachment-library", "/admin/automation-agents", "/admin/owner-migration", "/admin/config", "/admin/api-docs",
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
		{path: "/admin/automation-conversion/group-ops/ui", canonicalPath: "/admin/groupops.html", canonicalStatus: http.StatusFound, marker: `data-group-ops-standard-stage`, expectTopbar: true},
		{path: "/admin/groupops.html", marker: `data-group-ops-standard-stage`, expectTopbar: true},
		{path: "/admin/channels", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/cloud-orchestrator/plans", marker: `data-cloud-plan-root`, expectTopbar: true},
		{path: "/admin/cloud-orchestrator/plans/", marker: `data-cloud-plan-root`, expectTopbar: true},
		{path: "/admin/cloud-orchestrator/plans/" + strconv.FormatInt(fixture.aiPlanID, 10), marker: `data-plan-detail-state`, expectTopbar: true},
		{path: "/admin/ai.html", canonicalPath: "/admin/cloud-orchestrator/plans", canonicalStatus: http.StatusFound, marker: `data-cloud-plan-root`, expectTopbar: true},
		{path: "/admin/aiDetail.html?id=" + strconv.FormatInt(fixture.aiPlanID, 10), canonicalPath: "/admin/cloud-orchestrator/plans/" + strconv.FormatInt(fixture.aiPlanID, 10), canonicalStatus: http.StatusFound, marker: `data-plan-detail-state`, expectTopbar: true},
		{path: "/admin/customers", marker: `class="admin-topbar"`, expectTopbar: true},
		{path: "/admin/hxc-dashboard", marker: `admin-workspace-stage--dynamic`, expectTopbar: true},
		{path: "/admin/questionnaires", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/radar-links", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/radarDetail.html?id=" + strconv.FormatInt(fixture.radarID, 10), marker: `data-page="radarDetail"`, expectTopbar: true},
		{path: "/admin/radarForm.html", marker: `data-page="radarForm"`, expectTopbar: true},
		{path: "/admin/radarForm.html?id=" + strconv.FormatInt(fixture.radarID, 10), marker: `data-page="radarForm"`, expectTopbar: true},
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
		{path: "/admin/config", marker: `data-runtime-release-host`, expectTopbar: true},
		{path: "/admin/config/releases", marker: `data-runtime-release-host`, expectTopbar: true},
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
		if strings.HasPrefix(route.path, "/admin/cloud-orchestrator/plans") || strings.HasPrefix(route.path, "/admin/ai") {
			if !strings.Contains(body, `admin-workspace-stage--dynamic`) {
				t.Fatalf("native AI Assistant route=%s must use the standard topbar content inset", route.path)
			}
		}
	}
	detail := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/cloud-orchestrator/plans/"+strconv.FormatInt(fixture.aiPlanID, 10))
	for _, marker := range []string{`data-plan-detail-state`, `data-plan-approve`, `data-plan-reject`, `href="/admin/cloud-orchestrator/plans"`} {
		if !strings.Contains(detail.Body.String(), marker) {
			t.Fatalf("native AI Assistant detail action marker=%q is absent", marker)
		}
	}
	for _, path := range []string{"/admin/cloud-orchestrator/plans/0", "/admin/cloud-orchestrator/plans/unknown", "/admin/aiDetail.html?id=0"} {
		response := authenticatedAdminGet(t, fixture.application.handler, session, path)
		if response.Code != http.StatusNotFound {
			t.Fatalf("native AI Assistant invalid detail route=%s status=%d", path, response.Code)
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
	// Hold the two reads long enough for the Host to expose its loading root.
	// The browser script must wait on rendered semantic controls, not that root,
	// before it measures the static sidebar/header geometry.
	fixture.server.Config.Handler = delayedOpenPlatformDirectoryReads(fixture.application.handler, 150*time.Millisecond)
	script := filepath.Join(filepath.Dir(fixture.script), "..", "..", "internal", "webshell", "admin_layout_geometry.mjs")
	command := exec.CommandContext(fixture.ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_ADMIN_LAYOUT_TEST_URL="+fixture.server.URL,
		"AICRM_ADMIN_LAYOUT_TEST_USERNAME=product-browser-owner",
		"AICRM_ADMIN_LAYOUT_TEST_PASSWORD=product-browser-owner-password",
		"AICRM_ADMIN_LAYOUT_TEST_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_SERVICE_PRODUCT_ID="+strconv.FormatInt(fixture.serviceProductID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_HISTORICAL_ORDER="+fixture.historicalOrderReference,
		"AICRM_ADMIN_LAYOUT_TEST_RADAR_ID="+strconv.FormatInt(fixture.radarID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_AI_PLAN_ID="+strconv.FormatInt(fixture.aiPlanID, 10),
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
		"automation.png", "cycles.png", "groupops.png", "channels.png", "ai.png", "ai-detail.png", "customers.png", "hxc.png", "questionnaires.png", "radar.png", "radar-detail.png", "radar-form.png", "tags.png",
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

func delayedOpenPlatformDirectoryReads(next http.Handler, duration time.Duration) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && (request.URL.Path == "/api/admin/open-platform/clients" || request.URL.Path == "/api/admin/open-platform/routes") {
			time.Sleep(duration)
		}
		next.ServeHTTP(writer, request)
	})
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
	// The layout journey deliberately aggregates independent failures from the
	// full navigation matrix. Its bounded three-minute context includes release
	// staging and browser startup; individual browser waits remain unchanged.
	fixture := &adminShellLayoutFixture{productExternalPushChromiumFixture: newProductExternalPushChromiumFixtureWithTimeout(t, 3*time.Minute), screenshots: screenshots}
	seedAdminShellLayoutHXC(t, fixture.ctx, fixture.application)
	fixture.radarID = seedAdminShellLayoutRadar(t, fixture.ctx, fixture.application)
	fixture.aiPlanID = seedAdminShellLayoutAIAssistantPlan(t, fixture.ctx, fixture.application)
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

// seedAdminShellLayoutRadar provides an existing read-only radar record so the
// composed detail alias, as well as the empty new-form alias, are measured by
// the same Chromium geometry contract. It never submits a browser mutation.
func seedAdminShellLayoutRadar(t *testing.T, ctx context.Context, application *composedApplication) int64 {
	t.Helper()
	now := time.Date(2026, time.September, 7, 1, 2, 3, 0, time.UTC)
	var radarID int64
	err := application.pool.Native().QueryRow(ctx, `
		INSERT INTO radar_links(
			public_code,name,title,description,content_type,destination_url,
			auth_policy,status,created_by,updated_by,created_at,updated_at
		) VALUES(
			'rd_adminlayoutradar','Admin layout radar','Admin layout radar','layout fixture',
			'link','https://example.com/admin-layout-radar','unionid_required','enabled',1,1,$1,$1
		) RETURNING id`, now).Scan(&radarID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `
		INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at)
		VALUES($1,1,'{}'::jsonb,1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}
	return radarID
}

// seedAdminShellLayoutAIAssistantPlan uses the composed authenticated admin
// API to persist a pending-review plan and recipient. The fixture deliberately
// stops before approval, dispatch, or any Provider invocation; it proves only
// the native UI's read path against its normal AI Service and Unit of Work.
func seedAdminShellLayoutAIAssistantPlan(t *testing.T, ctx context.Context, application *composedApplication) int64 {
	t.Helper()
	now := time.Date(2026, time.September, 7, 1, 3, 4, 0, time.UTC)
	var actorID, customerID int64
	if err := application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='product-browser-owner'`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active','AI layout customer','CID-AI-LAYOUT','active','admin-layout-fixture',1,$2,$2)`, customerID, now); err != nil {
		t.Fatal(err)
	}
	input := struct {
		Name         string            `json:"name"`
		SourceKind   string            `json:"source_kind"`
		SourceDigest effectport.Digest `json:"source_digest"`
		Recipients   []struct {
			CustomerID int64 `json:"customer_id"`
			StaffID    int64 `json:"staff_id"`
			Content    []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"recipients"`
	}{
		Name: "AI layout detail fixture", SourceKind: "admin_shell_layout.fixture.v1", SourceDigest: effectport.Hash("admin-shell-layout-ai-plan"),
		Recipients: []struct {
			CustomerID int64 `json:"customer_id"`
			StaffID    int64 `json:"staff_id"`
			Content    []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"content"`
		}{{CustomerID: customerID, StaffID: actorID, Content: []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}{{Kind: "text", Text: "AI layout detail fixture"}}}},
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "product-browser-owner", "product-browser-owner-password")
	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-assistant/plans", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "admin-shell-layout-ai-plan-0001")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	var created struct {
		OK   bool `json:"ok"`
		Plan struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"plan"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil || !created.OK || created.Plan.ID < 1 || created.Plan.State != "pending_review" {
		t.Fatalf("admin layout AI fixture create status=%d response_valid=%t plan_id=%d state=%q", response.Code, json.Unmarshal(response.Body.Bytes(), &created) == nil, created.Plan.ID, created.Plan.State)
	}
	return created.Plan.ID
}
