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

type countingSecurity struct {
	authenticated, csrf int
	role                accessdomain.Role
}

func (security *countingSecurity) principal() accessdomain.Principal {
	role := security.role
	if role == "" {
		role = accessdomain.RoleAdmin
	}
	return accessdomain.Principal{InternalID: 7, Roles: []accessdomain.Role{role}}
}

func (security *countingSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.authenticated++
	return security.principal(), nil
}
func (security *countingSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.csrf++
	return security.principal(), nil
}

func TestViewerCanReadHistoryButCannotMutateStrategy(t *testing.T) {
	security := &countingSecurity{role: accessdomain.RoleViewer}
	handler, err := NewHandler(&operationapp.Service{}, security, "")
	if err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/strategies/weekly.review/versions?limit=10&offset=0", nil))
	if read.Code != http.StatusBadRequest || security.authenticated != 1 {
		t.Fatalf("viewer read status/auth=%d/%d", read.Code, security.authenticated)
	}
	write := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategies/weekly.review/status", bytes.NewBufferString(`{"expected_version":1,"status":"active"}`))
	request.Header.Set("Idempotency-Key", "activate-weekly-review")
	handler.ServeHTTP(write, request)
	if write.Code != http.StatusForbidden || security.csrf != 1 {
		t.Fatalf("viewer write status/csrf=%d/%d", write.Code, security.csrf)
	}
}

func TestAdminStrategyDTORejectsUnknownFields(t *testing.T) {
	security := &countingSecurity{}
	handler, err := NewHandler(&operationapp.Service{}, security, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategies", bytes.NewBufferString(`{"strategy_key":"weekly.review","title":"每周复盘","definition":{"schedule":"每周一 09:00","indicator_color":"#2EA121","primary_action":"start_review","stages":[{"key":"retro","label":"复盘","color":"#2EA121","state":"current"}]},"arbitrary_json":true}`))
	request.Header.Set("Idempotency-Key", "create-weekly-review")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || security.csrf != 1 {
		t.Fatalf("unknown field status/csrf=%d/%d body=%s", response.Code, security.csrf, response.Body.String())
	}
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
