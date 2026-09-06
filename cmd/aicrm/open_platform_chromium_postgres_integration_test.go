package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLOpenPlatformV1ChromiumJourney uses the real administrator
// session, CSRF bridge, V3-owned Host and Access-owned machine control plane.
// The browser creates a disabled V1 client, receives its one-time credential,
// activates it, changes a grant, rotates, activates again and disables it.
// It then proves OAuth, the REST catalog and MCP catalog at each applicable
// lifecycle boundary without ever writing a credential to test output.
//
// OneID decision: this control-plane browser journey does not resolve or
// provision a customer. Persistence decision: all control-plane writes are
// owned by Access and use its ordinary PostgreSQL UoW; no Provider is called.
func TestPostgreSQLOpenPlatformV1ChromiumJourney(t *testing.T) {
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Open Platform Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	t.Log("open platform Chromium: release artifact prepared")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: origin,
		ReleaseSHA:   "open-platform-v1-chromium-journey",
		WorkerOwner:  "open-platform-v1-chromium-journey",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "open-platform-v1-chromium-journey-webhook-secret"},
		WeCom:        platformconfig.WeCom{CorpID: "open-platform-browser"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: "01234567890123456789012345678901"},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
			OAuthOpenPlatformID:  "open-platform-browser",
		},
		Bootstrap: platformconfig.Bootstrap{
			Enabled: true, Username: "open-platform-browser-owner", Password: "open-platform-browser-owner-password", DisplayName: "Open Platform Browser Owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{
		Enabled: true, Username: "open-platform-browser-owner", Password: "open-platform-browser-owner-password", DisplayName: "Open Platform Browser Owner",
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("open platform Chromium: PostgreSQL composition ready")
	// Exercise the authenticated outer Composition route before Chromium opens
	// it. This proves the release artifact rendered from web/dist contains the
	// V3 Host asset, rather than allowing a package-relative missing artifact or
	// a stale frozen shell to turn into a generic browser timeout.
	outerSession, outerCSRF := adminAccessLogin(t, application.handler, "open-platform-browser-owner", "open-platform-browser-owner-password")
	if _, authenticateErr := application.authentication.Authenticate(ctx, outerSession); authenticateErr != nil {
		t.Fatal("test login did not issue a usable administrator session")
	}
	// An empty Postgres cidr[] can be projected as JSON null (and a future
	// Access normalizer may emit []). Read the actual composed management
	// endpoint before Chrome performs the same detail reload, so the Host's
	// null-tolerant path is tied to a real wire response rather than a DTO stub.
	probe := httptest.NewRequest(http.MethodPost, "/api/admin/open-platform/clients", strings.NewReader(`{"client_id":"browser-open-null-cidr-probe","display_name":"Browser Null CIDR Probe","purpose":"external_agent","audiences":["external_integration"],"scopes":["read"],"capabilities":["platform.capabilities.read"],"allowed_cidrs":[],"token_ttl_seconds":1800}`))
	probe.Header.Set("Content-Type", "application/json")
	probe.Header.Set("X-CSRF-Token", outerCSRF)
	probe.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	probe.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: outerCSRF})
	probeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(probeResponse, probe)
	if probeResponse.Code != http.StatusCreated {
		t.Fatalf("Open Platform null-CIDR probe create status=%d", probeResponse.Code)
	}
	probeRead := httptest.NewRequest(http.MethodGet, "/api/admin/open-platform/clients/browser-open-null-cidr-probe", nil)
	probeRead.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	probeReadResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(probeReadResponse, probeRead)
	var probeDetail struct {
		Client struct {
			AllowedCIDRs json.RawMessage `json:"allowed_cidrs"`
		} `json:"client"`
	}
	if probeReadResponse.Code != http.StatusOK || json.Unmarshal(probeReadResponse.Body.Bytes(), &probeDetail) != nil {
		t.Fatalf("Open Platform empty-CIDR detail status=%d response_valid=%t", probeReadResponse.Code, probeReadResponse.Code == http.StatusOK)
	}
	allowedCIDRShape := string(probeDetail.Client.AllowedCIDRs)
	if allowedCIDRShape != "null" && allowedCIDRShape != "[]" {
		t.Fatalf("Open Platform empty-CIDR detail nullable_shape=false")
	}
	outer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/apidocs.html", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	application.handler.ServeHTTP(outer, request)
	if outer.Code != http.StatusOK || !bytes.Contains(outer.Body.Bytes(), []byte(`data-page="apidocs"`)) || !bytes.Contains(outer.Body.Bytes(), []byte("openPlatformHost-")) {
		t.Fatalf("outer composed Open Platform Host status=%d api_docs=%t host_asset=%t", outer.Code, bytes.Contains(outer.Body.Bytes(), []byte(`data-page="apidocs"`)), bytes.Contains(outer.Body.Bytes(), []byte("openPlatformHost-")))
	}
	t.Log("open platform Chromium: outer route and management detail preflight passed")
	server.Config.Handler = application.handler
	server.StartTLS()
	// macOS desktop Chrome cannot reliably expose a remote-debugging endpoint
	// under this host's Crashpad policy. The preflight above still verifies the
	// real PostgreSQL Composition and release artifact locally; Linux CI must run
	// the CDP journey below and is the browser acceptance evidence.
	if goruntime.GOOS == "darwin" {
		t.Skip("Chromium CDP journey requires Linux CI; local outer-route PostgreSQL preflight passed")
	}

	t.Log("open platform Chromium: Linux CDP script launched")
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "open_platform_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_OPEN_PLATFORM_TEST_URL="+server.URL,
		"AICRM_OPEN_PLATFORM_TEST_USERNAME=open-platform-browser-owner",
		"AICRM_OPEN_PLATFORM_TEST_PASSWORD=open-platform-browser-owner-password",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Open Platform Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "open_platform_chromium: PASS") {
		t.Fatalf("Open Platform Chromium journey did not report success: %q", output)
	}

	var enabled bool
	var authVersion, audits int
	if err = application.pool.Native().QueryRow(ctx, `SELECT enabled,auth_version FROM access_machine_clients WHERE client_id='browser-open-agent'`).Scan(&enabled, &authVersion); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM access_machine_audit a JOIN access_machine_clients c ON c.id=a.machine_client_id WHERE c.client_id='browser-open-agent'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if enabled || authVersion < 5 || audits < 5 {
		t.Fatalf("Open Platform Chromium durable lifecycle enabled=%t auth_version=%d audits=%d", enabled, authVersion, audits)
	}
}
