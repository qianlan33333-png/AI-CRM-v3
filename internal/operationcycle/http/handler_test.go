package http

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
)

type deniedSecurity struct{}

func (deniedSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("no session")
}
func (deniedSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("no csrf")
}

func TestAdminRouteRequiresSessionBeforeApplicationCall(t *testing.T) {
	handler, err := NewHandler(&operationapp.Service{}, deniedSecurity{}, "")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/strategies", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunnerRouteIsDisabledWithoutServiceToken(t *testing.T) {
	handler, err := NewHandler(&operationapp.Service{}, deniedSecurity{}, "")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/operation-cycles/reports", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServiceTokenMustBeStrong(t *testing.T) {
	if _, err := NewHandler(&operationapp.Service{}, deniedSecurity{}, "short"); err == nil {
		t.Fatal("expected token validation failure")
	}
}

type countingSecurity struct{ authenticated, csrf int }

func (security *countingSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.authenticated++
	return accessdomain.Principal{InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (security *countingSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.csrf++
	return accessdomain.Principal{InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

func TestAdminWritesRequireCSRFAndReadsUseSessionAuthentication(t *testing.T) {
	security := &countingSecurity{}
	handler, err := NewHandler(&operationapp.Service{}, security, "")
	if err != nil {
		t.Fatal(err)
	}
	write := httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategies/weekly.review/actions/review/start", bytes.NewBufferString(`{"run_key":"weekly.review.001","parent_request_id":""}`))
	write.Header.Set("Idempotency-Key", "operation-cycle-http-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, write)
	if security.csrf != 1 || security.authenticated != 0 || response.Code != http.StatusBadRequest {
		t.Fatalf("write security/session/status=%d/%d/%d", security.csrf, security.authenticated, response.Code)
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/strategies", nil))
	if security.csrf != 1 || security.authenticated != 1 || read.Code != http.StatusBadRequest {
		t.Fatalf("read security/session/status=%d/%d/%d", security.csrf, security.authenticated, read.Code)
	}
}
