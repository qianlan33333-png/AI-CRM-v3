package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// Embedding the contracts keeps these boundary tests focused: the requests
// below must be rejected before an application/runtime method is reachable.
type applicationStub struct{ groupopshttp.Application }
type runtimeStub struct {
	groupopshttp.RuntimeApplication
}

type executionRuntimeStub struct {
	runtimeStub
	page groupopsport.ExecutionPage
}

func (stub executionRuntimeStub) ListExecutions(context.Context, int64, int32, int32) (groupopsport.ExecutionPage, error) {
	return stub.page, nil
}

type historyStub struct {
	groupopshttp.HistoryApplication
}

func (historyStub) ListHistoricalPlans(context.Context, int32, int32) (groupopsport.HistoricalPlanPage, error) {
	return groupopsport.HistoricalPlanPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalPlan{}, Limit: 50}, nil
}
func (historyStub) ListHistoricalDirectory(context.Context, int32, int32) (groupopsport.HistoricalDirectoryPage, error) {
	return groupopsport.HistoricalDirectoryPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalDirectory{}, Limit: 50}, nil
}
func (historyStub) ListHistoricalGroups(context.Context, int64, int32, int32) (groupopsport.HistoricalGroupPage, error) {
	return groupopsport.HistoricalGroupPage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalGroup{}, Limit: 50, PlanID: 1}, nil
}
func (historyStub) ListHistoricalNodes(context.Context, int64, int32, int32) (groupopsport.HistoricalNodePage, error) {
	return groupopsport.HistoricalNodePage{Source: "v1_history", ReadOnly: true, Items: []groupopsport.HistoricalNode{}, Limit: 50, PlanID: 1}, nil
}

type unavailableHistoryStub struct {
	groupopshttp.HistoryApplication
}

func (unavailableHistoryStub) ListHistoricalPlans(context.Context, int32, int32) (groupopsport.HistoricalPlanPage, error) {
	return groupopsport.HistoricalPlanPage{}, groupopsapp.ErrUnavailable
}
func (unavailableHistoryStub) ListHistoricalDirectory(context.Context, int32, int32) (groupopsport.HistoricalDirectoryPage, error) {
	return groupopsport.HistoricalDirectoryPage{}, groupopsapp.ErrUnavailable
}
func (unavailableHistoryStub) ListHistoricalGroups(context.Context, int64, int32, int32) (groupopsport.HistoricalGroupPage, error) {
	return groupopsport.HistoricalGroupPage{}, groupopsapp.ErrUnavailable
}
func (unavailableHistoryStub) ListHistoricalNodes(context.Context, int64, int32, int32) (groupopsport.HistoricalNodePage, error) {
	return groupopsport.HistoricalNodePage{}, groupopsapp.ErrUnavailable
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

func newHistoryHandler(t *testing.T, security securityStub) http.Handler {
	t.Helper()
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(applicationStub{}, runtimeStub{}, historyStub{}, security, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newUnavailableHistoryHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(applicationStub{}, runtimeStub{}, unavailableHistoryStub{}, adminSecurity(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestGroupOpsHistoryUsesExactReadOnlyDonorURLs(t *testing.T) {
	handler := newHistoryHandler(t, adminSecurity(nil))
	for _, target := range []string{
		groupopshttp.HistoryPath + "/plans?limit=20&offset=0",
		groupopshttp.HistoryPath + "/directory?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/groups?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/nodes?limit=20&offset=0",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"source":"v1_history"`) || !strings.Contains(response.Body.String(), `"read_only":true`) {
			t.Fatalf("target=%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
}

func TestGroupOpsHistoryRejectsInvalidSubresources(t *testing.T) {
	handler := newHistoryHandler(t, adminSecurity(nil))
	for _, target := range []string{
		groupopshttp.HistoryPath + "/plans/not-a-plan/groups",
		groupopshttp.HistoryPath + "/plans/1/unknown",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("target=%s status=%d cache=%q body=%s", target, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
}

func TestGroupOpsHistoryUnavailablePageBoundaryReturns503(t *testing.T) {
	handler := newUnavailableHistoryHandler(t)
	for _, target := range []string{
		groupopshttp.HistoryPath + "/plans?limit=20&offset=0",
		groupopshttp.HistoryPath + "/directory?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/groups?limit=20&offset=0",
		groupopshttp.HistoryPath + "/plans/1/nodes?limit=20&offset=0",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"group_ops_unavailable"`) {
			t.Fatalf("target=%s status=%d cache=%q body=%s", target, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
}

func TestGroupOpsHistoryIsReadOnlyAndRequiresSession(t *testing.T) {
	handler := newHistoryHandler(t, adminSecurity(nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, groupopshttp.HistoryPath+"/plans", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler = newHistoryHandler(t, securityStub{})
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.HistoryPath+"/plans", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("anonymous status=%d body=%s", response.Code, response.Body.String())
	}
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

func TestGroupOpsExecutionPageAdaptsVerifiedDeliveryForFrozenDetailDTO(t *testing.T) {
	runtime := executionRuntimeStub{page: groupopsport.ExecutionPage{Items: []groupopsport.Execution{{
		ID: 71, PlanID: 9, State: groupopsport.ExecutionProviderAccepted,
		ProviderAccepted: true, DeliveryProven: true, ProviderReceiptPresent: true,
	}}}}
	handler, err := groupopshttp.NewHandlerWithRuntime(applicationStub{}, runtime, adminSecurity(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, groupopshttp.PlansPath+"/9/executions?limit=100&offset=0", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			State        string `json:"state"`
			RuntimeState string `json:"runtime_state"`
			Delivery     bool   `json:"delivery_proven"`
			Receipt      bool   `json:"provider_receipt_present"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Items) != 1 || payload.Items[0].State != "delivery_proven" || payload.Items[0].RuntimeState != "provider_accepted" || !payload.Items[0].Delivery || !payload.Items[0].Receipt {
		t.Fatalf("payload=%s decoded=%+v err=%v", response.Body.String(), payload, err)
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
