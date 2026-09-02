package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type testRenderer struct{}

func (testRenderer) Render(_ context.Context, response http.ResponseWriter, status int, _ string, _ map[string]any) error {
	response.WriteHeader(status)
	return nil
}

type testAuth struct {
	csrfErr error
}

func (testAuth) Login(context.Context, app.LoginCommand) (app.IssuedSession, error) {
	return app.IssuedSession{SessionToken: "session-secret", CSRFToken: "csrf-secret",
		ExpiresAt: time.Now().Add(time.Hour), User: domain.User{ID: 1}}, nil
}
func (testAuth) Authenticate(context.Context, string) (domain.Principal, error) {
	return domain.Principal{}, domain.ErrAuthentication
}
func (auth testAuth) AuthorizeCSRF(context.Context, string, string, string) (domain.Principal, error) {
	if auth.csrfErr != nil {
		return domain.Principal{}, auth.csrfErr
	}
	return domain.Principal{Kind: domain.KindAdmin, InternalID: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}, nil
}
func (auth testAuth) Logout(context.Context, string, string, string) error { return auth.csrfErr }

type testManagement struct{}

func (testManagement) AddUser(context.Context, domain.Principal, app.AddUserInput) (domain.User, error) {
	return domain.User{ID: 2, Username: "employee", Active: true}, nil
}
func (testManagement) DisableUser(context.Context, domain.Principal, int64) error { return nil }
func (testManagement) BindWeComUserID(context.Context, domain.Principal, int64, string) error {
	return nil
}
func (testManagement) ChangeRoles(context.Context, domain.Principal, int64, []domain.Role) error {
	return nil
}
func (testManagement) ResetPassword(context.Context, domain.Principal, int64, string) error {
	return nil
}

func TestLoginCookieContractAndSafeNext(t *testing.T) {
	handler, err := NewHandler(Config{Renderer: testRenderer{}, Auth: testAuth{}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=operator&password=secret&next=https%3A%2F%2Fevil.example"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %#v", cookies)
	}
	if cookies[0].Name != SessionCookieName || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", cookies[0])
	}
	if cookies[1].Name != CSRFCookieName || !cookies[1].Secure || cookies[1].HttpOnly || cookies[1].SameSite != http.SameSiteLaxMode {
		t.Fatalf("csrf cookie = %#v", cookies[1])
	}
}

func TestManagementHandlerRejectsMissingCSRF(t *testing.T) {
	handler, err := NewHandler(Config{Renderer: testRenderer{}, Auth: testAuth{csrfErr: domain.ErrCSRFRequired}, Management: testManagement{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/access/users/2/disable", strings.NewReader(""))
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_required") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestSafeNextPath(t *testing.T) {
	for _, unsafe := range []string{"https://evil.example", "//evil.example", `\\evil`, "/api/admin/users", "/static/x"} {
		if got := SafeNextPath(unsafe, "/admin"); got != "/admin" {
			t.Errorf("SafeNextPath(%q) = %q", unsafe, got)
		}
	}
	if got := SafeNextPath("/admin/customers?q=1", "/admin"); got != "/admin/customers?q=1" {
		t.Fatalf("safe next = %q", got)
	}
}
