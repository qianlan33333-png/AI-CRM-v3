package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
)

type routeUOW struct{}

func (routeUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type routeEvents struct{}

func (routeEvents) Append(context.Context, operationport.Event) (operationport.EventID, error) {
	return 1, nil
}

type routeDeliveries struct{}

func (routeDeliveries) Accept(context.Context, operationport.EventID, string) error { return nil }

type routeStore struct {
	getRunKey, actionRequestID, versionKey, contextKey, contextMode string
	versionLimit, versionOffset, contextLimit, contextOffset        int32
	proposal                                                        operationapp.ProposalCommand
	decisionID, decision, decisionActor                             string
	err                                                             error
}

func (store *routeStore) Report(context.Context, operationapp.ReportCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) ListStrategies(context.Context, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) GetStrategy(context.Context, string) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) ListRuns(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) GetRun(_ context.Context, key string) (map[string]any, error) {
	store.getRunKey = key
	return map[string]any{"run_key": key}, store.err
}
func (store *routeStore) GetRunByOrdinal(context.Context, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) Start(context.Context, operationapp.StartCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) CurrentAction(context.Context, string) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) GetActionResult(_ context.Context, requestID string) (map[string]any, error) {
	store.actionRequestID = requestID
	return map[string]any{"request_id": requestID, "state": "completed"}, store.err
}
func (store *routeStore) Claim(context.Context, string, string, time.Time, time.Duration) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) RecordActionEvent(context.Context, operationapp.ActionEventCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) RenewActionLease(context.Context, operationapp.ActionLeaseRenewalCommand, time.Time, time.Duration) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) Heartbeat(context.Context, operationapp.RunnerHeartbeatCommand, time.Time) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) ContextIndex(_ context.Context, limit, offset int32) (map[string]any, error) {
	store.contextLimit, store.contextOffset = limit, offset
	return map[string]any{"items": []any{"context"}}, store.err
}
func (store *routeStore) StrategyContext(_ context.Context, key, mode string, limit, offset int32, _ map[string]string) (map[string]any, error) {
	store.contextKey, store.contextMode, store.contextLimit, store.contextOffset = key, mode, limit, offset
	return map[string]any{"strategy_key": key, "mode": mode}, store.err
}
func (store *routeStore) CreateProposal(_ context.Context, command operationapp.ProposalCommand, _ time.Time) (map[string]any, bool, error) {
	store.proposal = command
	return map[string]any{"proposal_id": "proposal-1", "state": "accepted"}, false, store.err
}
func (store *routeStore) ListProposals(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) DecideProposal(_ context.Context, id, decision, actor string, _ time.Time) (map[string]any, error) {
	store.decisionID, store.decision, store.decisionActor = id, decision, actor
	return map[string]any{"proposal_id": id, "decision": decision}, store.err
}
func (store *routeStore) CreateStrategy(context.Context, operationapp.CreateStrategyCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) UpdateStrategy(context.Context, operationapp.UpdateStrategyCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) TransitionStrategy(context.Context, operationapp.TransitionStrategyCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, store.err
}
func (store *routeStore) ListStrategyVersions(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, store.err
}
func (store *routeStore) ListRunVersions(_ context.Context, key string, limit, offset int32) (map[string]any, error) {
	store.versionKey, store.versionLimit, store.versionOffset = key, limit, offset
	return map[string]any{"run_key": key, "items": []any{}}, store.err
}

var _ operationapp.Store = (*routeStore)(nil)

type routeSecurityOp struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (security routeSecurityOp) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.authErr
}
func (security routeSecurityOp) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.csrfErr
}

func newRouteHandler(t *testing.T, store *routeStore, security routeSecurityOp, token string) *Handler {
	t.Helper()
	handler, err := NewHandler(operationapp.NewService(routeUOW{}, store, routeEvents{}, routeDeliveries{}), security, token)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func adminRouteSecurity() routeSecurityOp {
	return routeSecurityOp{principal: accessdomain.Principal{InternalID: 42, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
}

func TestOperationCycleAdminReadRouteContracts(t *testing.T) {
	security := adminRouteSecurity()
	store := &routeStore{}
	handler := newRouteHandler(t, store, security, "")
	for _, test := range []struct{ path, field, want string }{
		{"/api/admin/operation-cycles/runs/run.weekly.001", "run_key", "run.weekly.001"},
		{"/api/admin/operation-cycles/action-requests/request-9/result", "request_id", "request-9"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"`+test.field+`":"`+test.want+`"`) {
			t.Fatalf("path=%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}
	if store.getRunKey != "run.weekly.001" || store.actionRequestID != "request-9" {
		t.Fatalf("forwarded keys run=%q action=%q", store.getRunKey, store.actionRequestID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/runs/run.weekly.001?limit=0", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("run query status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/runs/run.weekly.001/versions?limit=2&offset=3", nil))
	if response.Code != http.StatusOK || store.versionKey != "run.weekly.001" || store.versionLimit != 2 || store.versionOffset != 3 {
		t.Fatalf("versions status=%d key=%q page=%d/%d body=%s", response.Code, store.versionKey, store.versionLimit, store.versionOffset, response.Body.String())
	}
	store.err = operationapp.ErrUnavailable
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/runs/run.weekly.001/versions", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("versions unavailable status=%d body=%s", response.Code, response.Body.String())
	}

	store.err = operationapp.ErrNotFound
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/operation-cycles/action-requests/missing/result", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
		t.Fatalf("not found status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOperationCycleAdminDecisionRouteContract(t *testing.T) {
	security := adminRouteSecurity()
	store := &routeStore{}
	handler := newRouteHandler(t, store, security, "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"accept"}`)))
	if response.Code != http.StatusOK || store.decisionID != "proposal-1" || store.decision != "accept" || store.decisionActor != "42" {
		t.Fatalf("status=%d decision=%q/%q/%q body=%s", response.Code, store.decisionID, store.decision, store.decisionActor, response.Body.String())
	}

	response = httptest.NewRecorder()
	newRouteHandler(t, &routeStore{}, routeSecurityOp{csrfErr: errors.New("csrf")}, "").ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"accept"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("csrf status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"hold"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid decision status=%d body=%s", response.Code, response.Body.String())
	}
	store.err = operationapp.ErrConflict
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategy-change-proposals/proposal-1/decision", strings.NewReader(`{"decision":"accept"}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"conflict"`) {
		t.Fatalf("decision conflict status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOperationCycleRunnerContextAndProposalRouteContracts(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	store := &routeStore{}
	handler := newRouteHandler(t, store, routeSecurityOp{}, token)
	request := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/context-index?limit=4&offset=2", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.contextLimit != 4 || store.contextOffset != 2 {
		t.Fatalf("index status=%d page=%d/%d body=%s", response.Code, store.contextLimit, store.contextOffset, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/operation-cycles/strategies/weekly.review/context?mode=review&limit=3&offset=1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.contextKey != "weekly.review" || store.contextMode != "review" || store.contextLimit != 3 || store.contextOffset != 1 {
		t.Fatalf("context status=%d values=%q/%q/%d/%d body=%s", response.Code, store.contextKey, store.contextMode, store.contextLimit, store.contextOffset, response.Body.String())
	}
	badToken := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/context-index", nil)
	badToken.Header.Set("Authorization", "Bearer wrong")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, badToken)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("token status=%d body=%s", response.Code, response.Body.String())
	}
	store.err = operationapp.ErrUnavailable
	request = httptest.NewRequest(http.MethodGet, "/api/operation-cycles/context-index", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"dependency_unavailable"`) {
		t.Fatalf("index unavailable status=%d body=%s", response.Code, response.Body.String())
	}
	store.err = nil

	payload := `{"schema_version":"operation_cycle_strategy_change_proposal.v1","strategy_key":"weekly.review","change":"pause"}`
	proposal := httptest.NewRequest(http.MethodPost, "/api/operation-cycles/strategy-change-proposals", strings.NewReader(payload))
	proposal.Header.Set("Authorization", "Bearer "+token)
	proposal.Header.Set("Idempotency-Key", "proposal-route-0001")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, proposal)
	if response.Code != http.StatusAccepted || store.proposal.IdempotencyKey != "proposal-route-0001" || store.proposal.ActorID != "operation-cycle-service" || store.proposal.Payload["strategy_key"] != "weekly.review" {
		t.Fatalf("proposal status=%d command=%+v body=%s", response.Code, store.proposal, response.Body.String())
	}
	malformed := httptest.NewRequest(http.MethodPost, "/api/operation-cycles/strategy-change-proposals", strings.NewReader(`{"schema_version":`))
	malformed.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, malformed)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed proposal status=%d body=%s", response.Code, response.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodGet, "/api/operation-cycles/strategies/weekly.review/context?mode=wrong", nil)
	invalid.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid context status=%d body=%s", response.Code, response.Body.String())
	}
}
