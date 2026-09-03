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

type testCustomerStore struct{}

func (testCustomerStore) List(context.Context, customerapp.Query) (customerapp.PageData, error) {
	return customerapp.PageData{Items: []customerapp.Item{}}, nil
}
func (testCustomerStore) Detail(context.Context, customerdomain.CustomerID) (customerapp.Detail, error) {
	return customerapp.Detail{}, customerapp.ErrNotFound
}

type testIdentities struct{ reveals int }

func (*testIdentities) VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}

func (*testIdentities) CustomerForPhone(context.Context, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
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
			store := testCustomerStore{}
			handler, err := NewHandler(Config{UnitOfWork: testUOW{}, Auth: security, CSRF: security, Directory: customerapp.Directory{Store: store, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: store, Identities: identities, Audit: audit})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/admin/customers/1/phone-reveal", strings.NewReader(`{"reason":"support verification"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.wantStatus || identities.reveals != test.wantReveal || len(audit.events) != test.wantAudit {
				t.Fatalf("status=%d reveals=%d audits=%d body=%s", response.Code, identities.reveals, len(audit.events), response.Body.String())
			}
			if test.wantStatus == 200 && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestPhoneRevealRejectsReasonBoundsBeforeRead(t *testing.T) {
	identities := &testIdentities{}
	audit := &testAudit{}
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := testCustomerStore{}
	handler, _ := NewHandler(Config{UnitOfWork: testUOW{}, Auth: security, CSRF: security, Directory: customerapp.Directory{Store: store, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: store, Identities: identities, Audit: audit})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/customers/1/phone-reveal", strings.NewReader(`{"reason":"   "}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != 400 || identities.reveals != 0 || len(audit.events) != 0 {
		t.Fatalf("status=%d reveals=%d audits=%d", response.Code, identities.reveals, len(audit.events))
	}
}
