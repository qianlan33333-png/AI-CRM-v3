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
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type routeSecurity struct {
	principal accessdomain.Principal
	authErr   error
	csrfErr   error
}

func (security routeSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.authErr
}
func (security routeSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.csrfErr
}

type routeGate struct {
	value domain.ExecutionGate
	err   error
}

func (gate routeGate) Get(context.Context) (domain.ExecutionGate, error) { return gate.value, gate.err }

type routeSyncStore struct {
	handlerStore
	command tagport.SyncCommand
}

func (store *routeSyncStore) ReserveSync(_ context.Context, command tagport.SyncCommand) (tagport.SyncReceipt, error) {
	store.command = command
	return tagport.SyncReceipt{ID: 17, Command: command, State: tagport.SyncReserved}, nil
}
func (store *routeSyncStore) AcceptSync(_ context.Context, receiptID, eventID int64, effect tagport.SyncEffectReceipt) (tagport.SyncReceipt, error) {
	return tagport.SyncReceipt{ID: receiptID, Command: store.command, State: tagport.SyncAccepted, EventID: eventID, Effect: effect}, nil
}

func newRouteContractHandler(t *testing.T, security routeSecurity, gate routeGate, store *routeSyncStore) *Handler {
	t.Helper()
	handler, err := NewHandler(
		tagapp.NewService(handlerUOW{}, store, nil, nil, nil),
		tagapp.NewSyncService(handlerUOW{}, store, handlerSyncEvents{}, handlerSyncEnqueuer{}),
		gate,
		security,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestTagLiveGateHTTPContract(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	gate := domain.ExecutionGate{LocalCommandAcceptanceAvailable: true, LocalQueueAvailable: true, ObservedAt: time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)}
	store := &routeSyncStore{}
	handler := newRouteContractHandler(t, routeSecurity{principal: admin}, routeGate{value: gate}, store)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"local_command_acceptance_available":true`) {
		t.Fatalf("success status=%d body=%s", response.Code, response.Body.String())
	}

	for name, security := range map[string]routeSecurity{
		"unauthenticated":            {authErr: errors.New("session absent")},
		"viewer without internal ID": {principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newRouteContractHandler(t, security, routeGate{value: gate}, store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
			if response.Code != map[string]int{"unauthenticated": http.StatusUnauthorized, "viewer without internal ID": http.StatusForbidden}[name] {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	response = httptest.NewRecorder()
	newRouteContractHandler(t, routeSecurity{principal: admin}, routeGate{err: errors.New("status reader unavailable")}, store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/live/gate", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"error":"unavailable"`) {
		t.Fatalf("unavailable status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTagSyncDueHTTPContract(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	store := &routeSyncStore{}
	handler := newRouteContractHandler(t, routeSecurity{principal: admin}, routeGate{}, store)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wecom/tags/sync-due", strings.NewReader(`{"trace_id":"release-route-gap"}`))
	request.Header.Set("Idempotency-Key", "tag-sync-due-route-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || store.command.Kind != tagport.SyncDue || store.command.Actor != 9 || store.command.IdempotencyKey != "tag-sync-due-route-0001" || store.command.TraceID != "release-route-gap" || !strings.Contains(response.Body.String(), `"state":"queued"`) {
		t.Fatalf("status=%d command=%+v body=%s", response.Code, store.command, response.Body.String())
	}

	for name, request := range map[string]*http.Request{
		"csrf":     httptest.NewRequest(http.MethodPost, "/api/admin/wecom/tags/sync-due", strings.NewReader(`{}`)),
		"bad body": httptest.NewRequest(http.MethodPost, "/api/admin/wecom/tags/sync-due", strings.NewReader(`{"extra":true}`)),
	} {
		t.Run(name, func(t *testing.T) {
			security := routeSecurity{principal: admin}
			if name == "csrf" {
				security.csrfErr = errors.New("csrf")
			}
			response := httptest.NewRecorder()
			newRouteContractHandler(t, security, routeGate{}, &routeSyncStore{}).ServeHTTP(response, request)
			want := http.StatusBadRequest
			if name == "csrf" {
				want = http.StatusForbidden
			}
			if response.Code != want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
