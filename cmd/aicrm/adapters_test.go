package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
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
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || authentication.session != "valid" {
		t.Fatalf("status=%d session=%q", response.Code, authentication.session)
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
		"/healthz": "health", "/readyz": "health", "/login": "access", "/api/admin/access/users": "access",
		"/api/admin/oneid/conflicts": "identity", "/auth/wecom/start": "wecom", "/api/sidebar/jssdk-config": "wecom",
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

func TestExternalEffectsUIRequiresAdminAndExposesOnlyItsFrozenSurface(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "admin", "campaigns.html"), []byte("external-effects"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "campaign.js"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	authentication := &fakeAccessAuthentication{err: accessdomain.ErrAuthentication}
	marker := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	handler, err := routeApplicationWithEffects(marker, marker, marker, marker, marker, externaleffects.NewUIHandler(dist), marker, marker, authentication, "https://crm.example")
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
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/external-effects?view=external-effects" {
		t.Fatalf("query was not normalized status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	for path, want := range map[string]string{
		"/admin/external-effects?view=external-effects": "external-effects",
		"/assets/campaign.js":                           "asset",
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Fatalf("readable path=%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}

	ui := externaleffects.NewUIHandler(dist)
	for _, path := range []string{"/admin/campaigns.html", "/admin/customers.html", "/customers"} {
		response = httptest.NewRecorder()
		ui.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("effects UI exposed %s with status=%d", path, response.Code)
		}
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
