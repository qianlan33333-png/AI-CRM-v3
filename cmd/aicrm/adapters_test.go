package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type fakeAccessAuthentication struct {
	principal accessdomain.Principal
	issued    accessapp.IssuedSession
	err       error
	session   string
	csrf      [3]string
	wecomID   string
}

func (fake *fakeAccessAuthentication) Authenticate(_ context.Context, token string) (accessdomain.Principal, error) {
	fake.session = token
	return fake.principal, fake.err
}

func (fake *fakeAccessAuthentication) AuthorizeCSRF(_ context.Context, session, cookie, request string) (accessdomain.Principal, error) {
	fake.csrf = [3]string{session, cookie, request}
	return fake.principal, fake.err
}

func (fake *fakeAccessAuthentication) LoginWithWeComUserID(_ context.Context, command accessapp.WeComLoginCommand) (accessapp.IssuedSession, error) {
	fake.wecomID = command.WeComUserID
	return fake.issued, fake.err
}

type directUnitOfWork struct{}

func (directUnitOfWork) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

func TestAllowedOAuthRedirectsIncludesHiddenExternalEffectsPage(t *testing.T) {
	if _, ok := allowedOAuthRedirects()["/admin/external-effects"]; !ok {
		t.Fatal("external effects page is not an allowed OAuth redirect")
	}
}

func TestMountSurveyAPIsIncludesLegacyOperationsLogRead(t *testing.T) {
	mux := http.NewServeMux()
	mountSurveyAPIs(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/questionnaires/11/external-push-logs?limit=50&offset=0", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" || response.Body.String() != `{"items":[],"total":0}` {
		t.Fatalf("legacy survey operations log route status=%d content_type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

type fakeUserReader struct{ user accessdomain.User }

func (reader fakeUserReader) UserByID(context.Context, int64, bool) (accessdomain.User, error) {
	return reader.user, nil
}

func TestRequestSecurityUsesOnlyAdminCookiesAndCSRFHeader(t *testing.T) {
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	security := requestAccessSecurity{authentication: authentication}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/oneid/resolve", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "session-cookie"})
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_csrf", Value: "csrf-cookie"})
	request.Header.Set("X-CSRF-Token", "csrf-header")
	request.Header.Set("Authorization", "Bearer forged")

	if _, err := security.Authenticate(request.Context(), request); err != nil || authentication.session != "session-cookie" {
		t.Fatalf("authenticate session=%q err=%v", authentication.session, err)
	}
	if _, err := security.AuthorizeCSRF(request.Context(), request); err != nil || authentication.csrf != [3]string{"session-cookie", "csrf-cookie", "csrf-header"} {
		t.Fatalf("csrf=%q err=%v", authentication.csrf, err)
	}
}

func TestAdminShellRedirectsWithoutSessionAndServesWithSession(t *testing.T) {
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	handler := requireAdminSession(authentication, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/config/login-access?tab=staff", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fconfig%2Flogin-access%3Ftab%3Dstaff" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: "csrf-current"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || authentication.session != "valid" {
		t.Fatalf("status=%d session=%q", response.Code, authentication.session)
	}
	var compat *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == accesshttp.CompatCSRFCookieName {
			compat = cookie
			break
		}
	}
	if compat == nil || compat.Value != "csrf-current" || !compat.Secure || compat.HttpOnly || compat.SameSite != http.SameSiteLaxMode {
		t.Fatalf("compat csrf cookie=%#v", compat)
	}
}

func TestWeComAdaptersIssueSharedSessionAndResolveBoundEmployee(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	authentication := &fakeAccessAuthentication{
		principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 11, Roles: []accessdomain.Role{accessdomain.RoleViewer}},
		issued:    accessapp.IssuedSession{SessionToken: "session", CSRFToken: "csrf", ExpiresAt: expires},
	}
	issuer := weComSessionIssuer{authentication: authentication}
	credentials, err := issuer.IssueWeComSession(context.Background(), wecom.OAuthSidebar, wecom.OAuthIdentity{CorpID: "ww-corp", EmployeeID: "employee-11"})
	if err != nil || credentials.SessionToken != "session" || authentication.wecomID != "employee-11" {
		t.Fatalf("credentials=%+v employee=%q err=%v", credentials, authentication.wecomID, err)
	}

	resolver := sidebarPrincipalResolver{authentication: authentication, users: fakeUserReader{user: accessdomain.User{
		ID: 11, Active: true, WeComUserID: "employee-11",
	}}, uow: directUnitOfWork{}, corpID: "ww-corp"}
	principal, err := resolver.SidebarPrincipal(context.Background(), "sidebar-session")
	if err != nil || principal.CorpID != "ww-corp" || principal.EmployeeID != "employee-11" || authentication.session != "sidebar-session" {
		t.Fatalf("principal=%+v session=%q err=%v", principal, authentication.session, err)
	}

	resolver.users = fakeUserReader{user: accessdomain.User{ID: 11, Active: true}}
	if _, err = resolver.SidebarPrincipal(context.Background(), "sidebar-session"); !errors.Is(err, accessdomain.ErrAuthentication) {
		t.Fatalf("unbound user error=%v", err)
	}
}

func TestApplicationRouterKeepsOwnershipAndProtectsAdminShell(t *testing.T) {
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := routeApplication(marker("health"), marker("access"), marker("identity"), marker("wecom"), marker("shell"), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"/healthz": "health", "/readyz": "health", "/login": "access", "/api/admin/access/users": "access", "/api/admin/admin-access": "access",
		"/api/admin/oneid/conflicts": "identity", "/auth/wecom/start": "wecom", "/api/sidebar/jssdk-config": "wecom",
		"/api/sidebar/v2/profile": "identity", "/api/sidebar/v2/send-intents": "identity",
		"/sidebar/bind-mobile": "shell", "/static/admin_console/admin_console.css": "shell",
	}
	for path, owner := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != owner {
			t.Fatalf("path=%s status=%d owner=%q", path, response.Code, response.Header().Get("X-Owner"))
		}
		if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("path=%s missing security headers", path)
		}
		if path == "/sidebar/bind-mobile" && response.Header().Get("X-Frame-Options") != "" {
			t.Fatalf("sidebar must remain embeddable by the WeCom client")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/orders", nil))
	if response.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated admin status=%d", response.Code)
	}
	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin/orders", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "shell" {
		t.Fatalf("authenticated admin status=%d owner=%q", response.Code, response.Header().Get("X-Owner"))
	}
}

func TestFullApplicationRouterExposesOrderImportAPI(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Owner", name)
			writer.WriteHeader(http.StatusNoContent)
		})
	}
	other := marker("other")
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		other, other, marker("identity"), other, other, other,
		other, other, other, other, other, other, other, other,
		other, other, other, other, other, other, other, other,
		other, other, &fakeAccessAuthentication{}, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/order-imports/inspect", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "identity" {
		t.Fatalf("status=%d owner=%q", response.Code, response.Header().Get("X-Owner"))
	}
}

func TestMountHXCUIReplacesPlaceholderAndProtectsAssets(t *testing.T) {
	dashboard := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Owner", "hxc-ui")
		writer.WriteHeader(http.StatusNoContent)
	})
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Owner", "next")
		writer.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler := mountHXCUI(next, dashboard, authentication)
	for _, target := range []string{"/admin/hxc-dashboard", "/hxc-dashboard-assets/admin-HASH.js"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "hxc-ui" {
			t.Fatalf("target=%q status=%d owner=%q", target, response.Code, response.Header().Get("X-Owner"))
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/customers", nil))
	if response.Header().Get("X-Owner") != "next" {
		t.Fatalf("unrelated owner=%q", response.Header().Get("X-Owner"))
	}
}

func TestTransactionRouteKeepsShellButReportsBackendUnavailable(t *testing.T) {
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNotFound) })
	handler, err := routeApplication(marker, marker, marker, marker, webshell.MustHandler(), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/orders", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "交易后端尚未就绪") || !strings.Contains(body, "功能待接入") {
		t.Fatalf("status=%d body=%q", response.Code, body)
	}
	if strings.Contains(body, "orderTransactionId") || strings.Contains(body, "创建退款 intent") {
		t.Fatal("blocked transaction shell mounted donor business actions before backend readiness")
	}
}

func TestSecurityHeadersAllowBlobImagesOnlyOnMediaPages(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for path, allowsBlob := range map[string]bool{
		"/admin/image-library":                        true,
		"/admin/miniprogram-library":                  true,
		"/admin/attachment-library":                   true,
		"/admin/campaigns.html?view=external-effects": false,
		"/admin/orders":                               false,
		"/api/admin/image-library":                    false,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		policy := response.Header().Get("Content-Security-Policy")
		if allowsBlob && !strings.Contains(policy, "img-src 'self' data: blob:") {
			t.Fatalf("Media page CSP lacks blob image source for %s: %q", path, policy)
		}
		if !allowsBlob && strings.Contains(policy, "blob:") {
			t.Fatalf("non-Media page CSP unexpectedly permits blob images for %s: %q", path, policy)
		}
	}
}

func TestSecurityHeadersAllowDashboardRuntimeStyles(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/hxc-dashboard", nil))
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("policy=%q", policy)
	}
}

func TestSecurityHeadersAllowFrozenOperationCycleInlineStylesOnBothPagesOnly(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for path, allowed := range map[string]bool{
		"/admin/operation-cycles":                        true,
		"/admin/operation-cycles/cyclesDetail.html?id=1": true,
		"/admin/operation-cycles/cycles.html":            true,
		"/admin/operation-cycles-unsafe":                 false,
		"/api/admin/operation-cycles/strategies":         false,
		"/assets/operationCyclesHost.js":                 false,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		policy := response.Header().Get("Content-Security-Policy")
		hasInlineStyle := strings.Contains(policy, "style-src 'self' 'unsafe-inline'")
		if hasInlineStyle != allowed {
			t.Fatalf("path=%s inline-style=%t policy=%q", path, hasInlineStyle, policy)
		}
		if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
			t.Fatalf("operation-cycle CSP relaxed scripts for %s: %q", path, policy)
		}
	}
}

func TestApplicationRouterOwnsEffectsAndPushCenterSeparately(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Owner", name)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := routeApplicationWithEffects(marker("health"), marker("access"), marker("identity"), marker("effects"), marker("push"), marker("ui"), marker("wecom"), marker("shell"), &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for path, owner := range map[string]string{"/api/admin/external-effects": "effects", "/api/admin/external-effects/eer_1/cancel": "effects", "/api/admin/push-center/jobs": "push", "/api/admin/push-center/jobs/1/retry": "push"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(response, request)
		if response.Header().Get("X-Owner") != owner {
			t.Fatalf("%s owner=%q", path, response.Header().Get("X-Owner"))
		}
	}
}

func TestApplicationRouterMountsWeChatShopCallbackAndReconciliation(t *testing.T) {
	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Owner", name)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler, err := routeApplication(marker("health"), marker("access"), marker("identity"), marker("wecom"), marker("shell"), &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/public/wechat-shop/callbacks/refund", "/api/admin/wechat-shop/refunds/9/reconcile"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNoContent || response.Header().Get("X-Owner") != "identity" {
			t.Fatalf("%s status=%d owner=%q", path, response.Code, response.Header().Get("X-Owner"))
		}
	}
}

func TestExternalEffectsUIRequiresAdminAndExposesOnlyItsFrozenSurface(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "campaign.js"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "campaign.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "labs.css"), []byte("#stage{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "asset-manifest.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"admin":"assets/campaign.js","tokens":"assets/campaign.css","labs":"assets/labs.css"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	renderer, err := webshell.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	ui := externaleffects.NewUIHandler(dist, func(writer http.ResponseWriter, request *http.Request, tokens, labs, admin string) error {
		return renderer.RenderExternalEffects(writer, webshell.AdminPageForRequest(request, "外部效果与 Push Center", "", "api.admin_cloud_orchestrator_workspace"), webshell.ExternalEffectsAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin})
	})
	handler, err := routeApplicationWithEffects(marker, marker, marker, marker, marker, ui, marker, marker, authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=external-effects", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fadmin%2Fexternal-effects%3Fview%3Dexternal-effects" {
		t.Fatalf("unauthenticated effects UI status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	authentication.err = nil
	request := httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=campaign&unexpected=1", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/campaigns.html?view=external-effects" {
		t.Fatalf("query was not normalized status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=external-effects&job=42", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/campaigns.html?job=42&view=external-effects" {
		t.Fatalf("frozen donor alias was not preserved status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/campaigns.html?view=external-effects&job=42", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Count(response.Body.String(), `class="admin-sidebar"`) != 1 || strings.Count(response.Body.String(), "<main") != 1 || !strings.Contains(response.Body.String(), `<main id="stage" class="stage rich"></main>`) || strings.Contains(response.Body.String(), `<aside class="side">`) || !strings.Contains(response.Body.String(), `src="/assets/campaign.js"`) || !strings.Contains(response.Header().Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") || strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("external effects shell mismatch status=%d body=%q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/external-effects?view=external-effects&job=0", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/campaigns.html?view=external-effects" {
		t.Fatalf("invalid job was not normalized status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	request = httptest.NewRequest(http.MethodGet, "/admin/campaigns.html?view=campaign", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("non-effects campaign alias status=%d", response.Code)
	}

	for path, want := range map[string]struct {
		body string
		mime string
	}{
		"/assets/campaign.js":         {body: "asset", mime: "text/javascript; charset=utf-8"},
		"/assets/campaign.css":        {body: "body{}", mime: "text/css; charset=utf-8"},
		"/assets/asset-manifest.json": {body: `{"version":1}`, mime: "application/json; charset=utf-8"},
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != want.body || response.Header().Get("Content-Type") != want.mime {
			t.Fatalf("readable path=%s status=%d body=%q mime=%q", path, response.Code, response.Body.String(), response.Header().Get("Content-Type"))
		}
		if strings.Contains(response.Header().Get("Content-Security-Policy"), "unsafe-inline") {
			t.Fatalf("asset CSP unexpectedly relaxed for %s: %q", path, response.Header().Get("Content-Security-Policy"))
		}
	}

	for _, path := range []string{"/admin/campaigns.html", "/admin/customers.html", "/customers"} {
		response = httptest.NewRecorder()
		ui.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("effects UI exposed %s with status=%d", path, response.Code)
		}
	}
}

func TestStagedTagsReleaseMountsFrozenWorkspaceInOnlyPR10Shell(t *testing.T) {
	dist := filepath.Join("..", "..", "release", "web", "dist")
	if _, err := os.Stat(filepath.Join(dist, "admin", "tags.html")); errors.Is(err, os.ErrNotExist) {
		t.Skip("real release stage is built by the CI frontend step")
	} else if err != nil {
		t.Fatal(err)
	}
	renderer, err := webshell.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	tagUI := tag.NewModuleRegistration().UIBinding(dist, func(writer http.ResponseWriter, request *http.Request, donorTemplate string, assets tag.TagsAssets) error {
		return renderer.RenderTags(writer, webshell.AdminPageForRequest(request, "企微标签管理", "", "api.admin_wecom_tags_page"), donorTemplate, webshell.TagsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := routeApplicationWithMediaTags(marker, marker, marker, marker, marker, marker, marker, marker, marker, tagUI, marker, webshell.MustHandler(), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/wecom-tags", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	for _, required := range []string{"新增标签组", "新增标签", "同步企微标签", "搜索标签组 / 标签 / tag_id", `data-page="tags"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("staged tags response missing %q", required)
		}
	}
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) {
		t.Fatalf("staged tags shell mismatch status=%d body=%q", response.Code, body)
	}

	for _, privatePath := range []string{"/admin/tags.html", "/admin/wecom-tags.html"} {
		request = httptest.NewRequest(http.MethodGet, privatePath, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("private donor input %s became routable: status=%d", privatePath, response.Code)
		}
	}
}

func TestCouponRoutesAreExplicitAndClaimPageFailsClosed(t *testing.T) {
	marker := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Coupon", "yes")
		w.WriteHeader(http.StatusNoContent)
	})
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := routeApplicationWithProductsCoupons(marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, marker, webshell.MustHandler(), authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/coupons", "/api/admin/coupons/7", "/admin/coupons", "/admin/coupons.html", "/admin/couponForm.html?id=7"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent || res.Header().Get("X-Coupon") != "yes" {
			t.Fatalf("coupon route %s status=%d owner=%q", path, res.Code, res.Header().Get("X-Coupon"))
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/couponData.html", nil)
	req.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("couponData=%d", res.Code)
	}
}

func TestApplicationRouterRejectsCrossSiteUnsafeRequests(t *testing.T) {
	authentication := &fakeAccessAuthentication{}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler, err := routeApplication(marker, marker, marker, marker, marker, authentication, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		origin    string
		fetchSite string
		want      int
	}{
		{name: "same origin", origin: "https://crm.example", fetchSite: "same-origin", want: http.StatusNoContent},
		{name: "same origin overrides inconsistent fetch metadata", origin: "https://crm.example", fetchSite: "cross-site", want: http.StatusNoContent},
		{name: "cross origin", origin: "https://evil.example", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "cross origin overrides misleading fetch metadata", origin: "https://evil.example", fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "opaque origin", origin: "null", fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "cross-site without origin", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "provider callback without browser headers", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestApplicationRouterDefersLoginPostToIndependentCSRFProtection(t *testing.T) {
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler, err := routeApplication(marker, marker, marker, marker, marker, &fakeAccessAuthentication{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.Header.Set("Origin", "null")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAIAssistantMountRejectsCrossSiteUnsafeRequests(t *testing.T) {
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := mountAIAssistant(marker, marker, marker, &fakeAccessAuthentication{}, true, "https://crm.example")

	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-assistant/plans/7/approve", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/integrations/ai-assistant/review-plans", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("machine request status=%d body=%s", response.Code, response.Body.String())
	}
}
