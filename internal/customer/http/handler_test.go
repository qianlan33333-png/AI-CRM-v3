package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, run func(context.Context) error) error { return run(ctx) }

type testSecurity struct{ principal accessdomain.Principal }

func (security testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, nil
}
func (security testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, nil
}

type testCustomerStore struct{ lastQuery customerapp.Query }

func (store *testCustomerStore) List(_ context.Context, query customerapp.Query) (customerapp.PageData, error) {
	store.lastQuery = query
	return customerapp.PageData{Items: []customerapp.Item{}}, nil
}
func (*testCustomerStore) Detail(context.Context, customerdomain.CustomerID) (customerapp.Detail, error) {
	return customerapp.Detail{}, customerapp.ErrNotFound
}

type testIdentities struct {
	reveals      int
	phoneQueries []string
}

func (*testIdentities) VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}

func (identities *testIdentities) CustomerForPhone(_ context.Context, phone string) (customerdomain.CustomerID, bool, error) {
	identities.phoneQueries = append(identities.phoneQueries, phone)
	return 42, true, nil
}
func (*testIdentities) DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	return nil, nil, nil
}
func (identities *testIdentities) RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error) {
	identities.reveals++
	return "+8613812345678", true, nil
}

type testAudit struct{ events []platformaudit.Event }

func (audit *testAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	audit.events = append(audit.events, event)
	return event, nil
}

func TestPhoneRevealEnforcesRoleAndNoStoreAudit(t *testing.T) {
	for _, test := range []struct {
		name       string
		role       accessdomain.Role
		wantStatus int
		wantReveal int
		wantAudit  int
	}{{"viewer", accessdomain.RoleViewer, 403, 0, 0}, {"admin", accessdomain.RoleAdmin, 200, 1, 1}, {"super", accessdomain.RoleSuperAdmin, 200, 1, 1}} {
		t.Run(test.name, func(t *testing.T) {
			identities := &testIdentities{}
			audit := &testAudit{}
			security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{test.role}}}
			store := &testCustomerStore{}
			handler, err := NewHandler(Config{UnitOfWork: testUOW{}, Auth: security, CSRF: security, Directory: customerapp.Directory{Store: store, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: store, Identities: identities, Audit: audit})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/admin/customers/1/phone-reveal", nil)
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.wantStatus || identities.reveals != test.wantReveal || len(audit.events) != test.wantAudit {
				t.Fatalf("status=%d reveals=%d audits=%d body=%s", response.Code, identities.reveals, len(audit.events), response.Body.String())
			}
			if test.wantStatus == 200 && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
			if test.wantStatus == 200 && (strings.Contains(response.Body.String(), "+86") || !strings.Contains(response.Body.String(), "13812345678")) {
				t.Fatalf("phone response=%s", response.Body.String())
			}
			if test.wantAudit == 1 && !strings.Contains(string(audit.events[0].Payload), `"purpose":"customer_detail_query"`) {
				t.Fatalf("audit payload=%s", audit.events[0].Payload)
			}
		})
	}
}

func TestPhoneSearchAcceptsLocalCNFormatAndRejectsInvalidInput(t *testing.T) {
	identities := &testIdentities{}
	audit := &testAudit{}
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{}
	handler, _ := NewHandler(Config{UnitOfWork: testUOW{}, Auth: security, CSRF: security, Directory: customerapp.Directory{Store: store, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: store, Identities: identities, Audit: audit})

	for _, value := range []string{"13812345678", "%2B8613812345678"} {
		response := httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?phone="+value, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("phone=%s status=%d body=%s", value, response.Code, response.Body.String())
		}
	}
	if len(identities.phoneQueries) != 2 || identities.phoneQueries[0] != "+8613812345678" || identities.phoneQueries[1] != "+8613812345678" || store.lastQuery.Filters.PhoneCustomerID != 42 {
		t.Fatalf("queries=%v filter=%+v", identities.phoneQueries, store.lastQuery.Filters)
	}

	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?phone=123", nil))
	if response.Code != http.StatusBadRequest || len(identities.phoneQueries) != 2 {
		t.Fatalf("invalid status=%d queries=%v", response.Code, identities.phoneQueries)
	}

	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?activation_status=active", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("removed activation filter status=%d", response.Code)
	}
}
