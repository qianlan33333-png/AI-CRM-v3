package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
)

type transactionMarker struct{}

type testUnitOfWork struct{ calls int }

func (unit *testUnitOfWork) Within(ctx context.Context, callback func(context.Context) error) error {
	unit.calls++
	return callback(context.WithValue(ctx, transactionMarker{}, true))
}

type testSecurity struct {
	authPrincipal accessdomain.Principal
	authErr       error
	csrfPrincipal accessdomain.Principal
	csrfErr       error
}

func (security *testSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return security.authPrincipal, security.authErr
}

func (security *testSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return security.csrfPrincipal, security.csrfErr
}

type testOneID struct {
	resolveResult identityport.ResolveResult
	resolveErr    error
	resolveInput  identitydomain.Reference
	resolveCalls  int
	confirmResult identityapp.LinkResult
	confirmErr    error
	confirmInput  identityapp.ConfirmMergeCommand
	confirmCalls  int
	reverseResult identityapp.MergeRecord
	reverseErr    error
	reverseID     int64
}

func (service *testOneID) Resolve(ctx context.Context, input identitydomain.Reference) (identityport.ResolveResult, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityport.ResolveResult{}, errors.New("resolve was not transaction bound")
	}
	service.resolveCalls++
	service.resolveInput = input
	return service.resolveResult, service.resolveErr
}

func (service *testOneID) ConfirmMerge(ctx context.Context, input identityapp.ConfirmMergeCommand) (identityapp.LinkResult, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityapp.LinkResult{}, errors.New("confirm was not transaction bound")
	}
	service.confirmCalls++
	service.confirmInput = input
	return service.confirmResult, service.confirmErr
}

func (service *testOneID) RevertConfirmedMerge(ctx context.Context, mergeID int64) (identityapp.MergeRecord, error) {
	if ctx.Value(transactionMarker{}) != true {
		return identityapp.MergeRecord{}, errors.New("reverse was not transaction bound")
	}
	service.reverseID = mergeID
	return service.reverseResult, service.reverseErr
}

type testQueries struct {
	detail           query.CustomerDetail
	detailErr        error
	customerID       customerdomain.CustomerID
	conflictPage     query.ConflictPage
	conflictOptions  query.ListOptions
	candidatePage    query.MergeCandidatePage
	candidateOptions query.ListOptions
}

func (queries *testQueries) Customer(ctx context.Context, id customerdomain.CustomerID) (query.CustomerDetail, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.CustomerDetail{}, errors.New("customer query was not transaction bound")
	}
	queries.customerID = id
	return queries.detail, queries.detailErr
}

func (queries *testQueries) Conflicts(ctx context.Context, options query.ListOptions) (query.ConflictPage, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.ConflictPage{}, errors.New("conflict query was not transaction bound")
	}
	queries.conflictOptions = options
	return queries.conflictPage, nil
}

func (queries *testQueries) MergeCandidates(ctx context.Context, options query.ListOptions) (query.MergeCandidatePage, error) {
	if ctx.Value(transactionMarker{}) != true {
		return query.MergeCandidatePage{}, errors.New("candidate query was not transaction bound")
	}
	queries.candidateOptions = options
	return queries.candidatePage, nil
}

func TestResolveRejectsClientTrustFieldsAndNeverProvisions(t *testing.T) {
	for _, field := range []string{
		`"assurance":"verified"`, `"verified":true`, `"source":"provider"`,
	} {
		t.Run(field, func(t *testing.T) {
			service := &testOneID{}
			handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
			body := `{"kind":"unionid","scope":"wechat-open-platform:one","value":"secret",` + field + `}`
			response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve", body, true)
			if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if service.resolveCalls != 0 {
				t.Fatalf("resolve calls=%d", service.resolveCalls)
			}
		})
	}

	service := &testOneID{resolveResult: identityport.ResolveResult{Status: identityport.ResolveNotFound}}
	unit := &testUnitOfWork{}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, unit)
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"unionid","scope":"wechat-open-platform:one","value":"secret"}`, true)
	if response.Code != nethttp.StatusOK || response.Body.String() != "{\"status\":\"not_found\"}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if service.resolveCalls != 1 || service.resolveInput.Assurance != identitydomain.AssuranceDeclared ||
		service.resolveInput.Source != "oneid-admin-resolve" || unit.calls != 1 {
		t.Fatalf("resolve input=%#v calls=%d uow=%d", service.resolveInput, service.resolveCalls, unit.calls)
	}
}

func TestResolveDoesNotCreateCustomerOrIdentity(t *testing.T) {
	store := identityapp.NewMemoryStore()
	service := &identityapp.OneIDService{Store: store}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"ext","scope":"ext:admin-search","value":"not-present"}`, true)
	if response.Code != nethttp.StatusOK || !strings.Contains(response.Body.String(), `"status":"not_found"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if store.CustomerCount() != 0 || store.ActiveIdentityCount() != 0 {
		t.Fatalf("resolve created customers=%d identities=%d", store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestReadAuthenticationAndWriteAuthorization(t *testing.T) {
	tests := []struct {
		name           string
		auth           accessdomain.Principal
		authErr        error
		csrf           accessdomain.Principal
		csrfErr        error
		method, target string
		body           string
		wantStatus     int
		wantCode       string
	}{
		{name: "missing session", authErr: accessdomain.ErrAuthentication, method: nethttp.MethodGet,
			target: "/api/admin/oneid/conflicts", wantStatus: nethttp.StatusUnauthorized, wantCode: "authentication_required"},
		{name: "customer principal", auth: accessdomain.Principal{Kind: accessdomain.KindCustomer, InternalID: 9}, method: nethttp.MethodGet,
			target: "/api/admin/oneid/conflicts", wantStatus: nethttp.StatusForbidden, wantCode: "permission_denied"},
		{name: "missing csrf", csrfErr: accessdomain.ErrCSRFRequired, method: nethttp.MethodPost,
			target: "/api/admin/oneid/merge-candidates/1/confirm", body: `{"survivor_customer_id":1}`,
			wantStatus: nethttp.StatusForbidden, wantCode: "csrf_required"},
		{name: "reverse missing csrf", csrfErr: accessdomain.ErrCSRFRequired, method: nethttp.MethodPost,
			target:     "/api/admin/oneid/merges/1/reverse",
			wantStatus: nethttp.StatusForbidden, wantCode: "csrf_required"},
		{name: "non super admin", csrf: activeAdmin(), method: nethttp.MethodPost,
			target: "/api/admin/oneid/merge-candidates/1/confirm", body: `{"survivor_customer_id":1}`,
			wantStatus: nethttp.StatusForbidden, wantCode: "permission_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			security := &testSecurity{authPrincipal: test.auth, authErr: test.authErr, csrfPrincipal: test.csrf, csrfErr: test.csrfErr}
			handler := configuredHandler(t, &testUnitOfWork{}, security, &testOneID{}, &testQueries{})
			response := perform(handler, test.method, test.target, test.body, test.body != "")
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCode) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestConfirmOperatorComesOnlyFromPrincipal(t *testing.T) {
	service := &testOneID{confirmResult: identityapp.LinkResult{
		Status: identityapp.LinkMerged, CustomerID: 20, Merge: &identityapp.MergeRecord{ID: 71},
	}}
	handler := newTestHandler(t, activeAdmin(), accessdomain.Principal{
		Kind: accessdomain.KindAdmin, InternalID: 42, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin},
	}, service, &testQueries{}, nil)
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/merge-candidates/8/confirm",
		`{"survivor_customer_id":20,"operator":"forged"}`, true)
	if response.Code != nethttp.StatusBadRequest || service.confirmCalls != 0 {
		t.Fatalf("forged operator status=%d calls=%d body=%q", response.Code, service.confirmCalls, response.Body.String())
	}
	response = perform(handler, nethttp.MethodPost, "/api/admin/oneid/merge-candidates/8/confirm",
		`{"survivor_customer_id":20}`, true)
	if response.Code != nethttp.StatusOK || service.confirmCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%q", response.Code, service.confirmCalls, response.Body.String())
	}
	if service.confirmInput.CandidateID != 8 || service.confirmInput.SurvivorCustomerID != 20 || service.confirmInput.Operator != "admin:42" {
		t.Fatalf("command=%#v", service.confirmInput)
	}
}

func TestCustomerAndMutationResponsesCannotRepresentPIIOrEvidence(t *testing.T) {
	now := time.Now().UTC()
	queries := &testQueries{detail: query.CustomerDetail{
		CustomerID: 1, Status: customerdomain.StatusMerged, CanonicalCustomerID: 2, CanonicalStatus: customerdomain.StatusActive,
		Identities:   []query.IdentitySummary{{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:tenant"}},
		MergeLineage: []query.MergeLineageSummary{{ID: 9, FromCustomerID: 1, ToCustomerID: 2, ReversibleStatus: "not_reversed", MergedAt: now}},
	}}
	service := &testOneID{reverseResult: identityapp.MergeRecord{
		ID: 9, FromCustomerID: 1, ToCustomerID: 2, Operator: "secret-operator",
		Evidence: identitydomain.LinkEvidence{Digest: "raw-secret-digest", EventID: "external-user-secret"},
	}}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, queries, nil)
	responses := []*httptest.ResponseRecorder{
		perform(handler, nethttp.MethodGet, "/api/admin/oneid/customers/1", "", false),
		perform(handler, nethttp.MethodPost, "/api/admin/oneid/merges/9/reverse", "", false),
	}
	for _, response := range responses {
		if response.Code != nethttp.StatusOK {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, forbidden := range []string{"normalized_value", "external-user-secret", "+13800138000", "raw-secret-digest", "secret-operator", "evidence", "operator"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("response exposed %q: %s", forbidden, body)
			}
		}
	}
}

func TestListPaginationDefaultsBoundsAndStatusWhitelist(t *testing.T) {
	queries := &testQueries{conflictPage: query.ConflictPage{Items: []query.Conflict{}}, candidatePage: query.MergeCandidatePage{Items: []query.MergeCandidate{}}}
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), &testOneID{}, queries, nil)
	response := perform(handler, nethttp.MethodGet, "/api/admin/oneid/conflicts", "", false)
	if response.Code != nethttp.StatusOK || queries.conflictOptions != (query.ListOptions{Status: "open", Limit: 50}) {
		t.Fatalf("status=%d options=%#v body=%q", response.Code, queries.conflictOptions, response.Body.String())
	}
	response = perform(handler, nethttp.MethodGet, "/api/admin/oneid/merge-candidates?status=confirmed&limit=100&offset=2", "", false)
	if response.Code != nethttp.StatusOK || queries.candidateOptions != (query.ListOptions{Status: "confirmed", Limit: 100, Offset: 2}) {
		t.Fatalf("status=%d options=%#v body=%q", response.Code, queries.candidateOptions, response.Body.String())
	}
	for _, target := range []string{
		"/api/admin/oneid/conflicts?limit=101", "/api/admin/oneid/conflicts?status=deleted",
		"/api/admin/oneid/conflicts?limit=1&limit=2", "/api/admin/oneid/conflicts?cursor=secret",
	} {
		response = perform(handler, nethttp.MethodGet, target, "", false)
		if response.Code != nethttp.StatusBadRequest {
			t.Fatalf("target=%q status=%d body=%q", target, response.Code, response.Body.String())
		}
	}
}

func TestBodyLimitAndInternalErrorsAreSafe(t *testing.T) {
	handler := newTestHandler(t, activeAdmin(), activeSuperAdmin(), &testOneID{}, &testQueries{}, nil)
	large := `{"kind":"ext","scope":"ext:test","value":"` + strings.Repeat("x", int(maxRequestBodyBytes)) + `"}`
	response := perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve", large, true)
	if response.Code != nethttp.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "invalid_request") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	service := &testOneID{resolveErr: errors.New("SELECT normalized_value FROM customer_identities: password=secret")}
	handler = newTestHandler(t, activeAdmin(), activeSuperAdmin(), service, &testQueries{}, nil)
	response = perform(handler, nethttp.MethodPost, "/api/admin/oneid/resolve",
		`{"kind":"ext","scope":"ext:test","value":"opaque"}`, true)
	if response.Code != nethttp.StatusInternalServerError || response.Body.String() != "{\"error\":\"internal_error\",\"ok\":false}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func newTestHandler(t *testing.T, read, write accessdomain.Principal, service OneIDService, queries *testQueries, unit *testUnitOfWork) nethttp.Handler {
	t.Helper()
	if unit == nil {
		unit = &testUnitOfWork{}
	}
	security := &testSecurity{authPrincipal: read, csrfPrincipal: write}
	return configuredHandler(t, unit, security, service, queries)
}

func configuredHandler(t *testing.T, unit *testUnitOfWork, security *testSecurity, service OneIDService, queries *testQueries) nethttp.Handler {
	t.Helper()
	handler, err := NewHandler(Config{UnitOfWork: unit, Authenticator: security, CSRF: security, OneID: service, Queries: queries})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func perform(handler nethttp.Handler, method, target, body string, jsonBody bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if jsonBody {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func activeAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}

func activeSuperAdmin() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
}
