package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// Embedding the contracts keeps these boundary tests focused: the requests
// below must be rejected before an application/runtime method is reachable.
type applicationStub struct{ groupopshttp.Application }
type runtimeStub struct {
	groupopshttp.RuntimeApplication
}

type contentDeliveryStub struct {
	mediaport.ContentDeliveryService
	previewCalls int
}

func (s *contentDeliveryStub) Preview(context.Context, mediaport.ContentPackageCommand) (mediaport.ContentPackage, error) {
	s.previewCalls++
	return mediaport.ContentPackage{ID: 1, Name: "内容包", ContentText: "正文", Version: 1, Refs: []mediaport.ContentRef{}}, nil
}

type protocolStub struct{ called bool }

func (s *protocolStub) AuthenticateGroupOpsWebhook(context.Context, *http.Request, string, []byte) (string, error) {
	s.called = true
	return "group-ops-webhook-test-key", nil
}

type securityStub struct {
	principal accessdomain.Principal
	csrfErr   error
}

func (s securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}

func (s securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	if s.csrfErr != nil {
		return accessdomain.Principal{}, s.csrfErr
	}
	return s.principal, nil
}

func adminSecurity(csrfErr error) securityStub {
	return securityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, csrfErr: csrfErr}
}

func newBoundaryHandler(t *testing.T, security securityStub, protocols groupopshttp.ProtocolAuthenticator) http.Handler {
	t.Helper()
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtimeStub{}, security, protocols)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestGroupOpsWebhookWithoutProtocolAdapterFailsClosed(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, groupopshttp.BroadcastPath, strings.NewReader(`{"plan_id":1}`))
	request.URL.Path = "/api/automation/group-ops/webhooks/plan-hook"
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), nil).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"protocol_auth_unavailable"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsOperationMembersRejectsAudienceScope(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, groupopshttp.OperationMembersPath+"/sync", strings.NewReader(`{"scope":"audience","page_size":20}`))
	request.Header.Set("Idempotency-Key", "group-ops-operation-members-01")
	request.Header.Set("X-CSRF-Token", "csrf")
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsMutationRequiresCSRFBeforeApplication(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, groupopshttp.PlansPath, strings.NewReader(`{"name":"plan"}`))
	request.Header.Set("Idempotency-Key", "group-ops-create-plan-01")
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(errors.New("csrf required")), nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"permission_denied"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGroupOpsWebhookRejectsBodiesBeyondHMACBound(t *testing.T) {
	protocols := &protocolStub{}
	body := `{"value":"` + strings.Repeat("a", 64<<10) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(body))
	response := httptest.NewRecorder()
	newBoundaryHandler(t, adminSecurity(nil), protocols).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) || protocols.called {
		t.Fatalf("status=%d protocol_called=%v body=%s", response.Code, protocols.called, response.Body.String())
	}
}

func TestGroupOpsContentPackagePreviewUsesMediaPortAdapter(t *testing.T) {
	delivery := &contentDeliveryStub{}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtimeStub{}, adminSecurity(nil), nil, delivery)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, groupopshttp.ContentPackagesPath+"/preview", strings.NewReader(`{"name":"内容包","content_text":"正文","refs":[]}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || delivery.previewCalls != 1 || !strings.Contains(response.Body.String(), `"content_text":"正文"`) {
		t.Fatalf("status=%d preview_calls=%d body=%s", response.Code, delivery.previewCalls, response.Body.String())
	}
}
