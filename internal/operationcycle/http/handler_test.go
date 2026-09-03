package http

import (
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
