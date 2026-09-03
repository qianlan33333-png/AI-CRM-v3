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
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
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

type testCustomerStore struct {
	lastQuery customerapp.Query
	detail    customerapp.Detail
	detailErr error
}

func (store *testCustomerStore) List(_ context.Context, query customerapp.Query) (customerapp.PageData, error) {
	store.lastQuery = query
	return customerapp.PageData{Items: []customerapp.Item{}}, nil
}
func (store *testCustomerStore) Detail(context.Context, customerdomain.CustomerID) (customerapp.Detail, error) {
	return store.detail, store.detailErr
}

type testIdentities struct {
	reveals      int
	phoneQueries []string
	summaries    []identityport.DirectoryIdentitySummary
	phones       []identityport.MaskedPhone
}

func (*testIdentities) VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}

func (identities *testIdentities) CustomerForPhone(_ context.Context, phone string) (customerdomain.CustomerID, bool, error) {
	identities.phoneQueries = append(identities.phoneQueries, phone)
	return 42, true, nil
}
func (identities *testIdentities) DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	return identities.summaries, identities.phones, nil
}
func (identities *testIdentities) RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error) {
	identities.reveals++
	return "13812345678", true, nil
}

type testAudit struct{ events []platformaudit.Event }

func (audit *testAudit) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	audit.events = append(audit.events, event)
	return event, nil
}

type testCanonical struct{}

func (testCanonical) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}

type testOwners struct{}

func (testOwners) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testOwners) CustomerOwners(context.Context, customerdomain.CustomerID) (customerport.OwnerPage, error) {
	return customerport.OwnerPage{Items: []customerport.OwnerItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testTags struct{}

func (testTags) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testTags) CustomerTags(context.Context, customerdomain.CustomerID) (customerport.TagPage, error) {
	return customerport.TagPage{Items: []customerport.TagItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testSurveys struct{}

func (testSurveys) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testSurveys) CustomerSurveys(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.SurveyPage, error) {
	return customerport.SurveyPage{Items: []customerport.SurveyItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testTimeline struct{}

func (testTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (testTimeline) CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error) {
	return customerport.TimelinePage{Items: []customerport.TimelineItem{}, Status: customerport.SectionStatus{State: customerport.SectionReady}}, nil
}

type testChat struct{}

func (testChat) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionNotReady}
}
func (testChat) CustomerChatActivity(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.ChatActivityPage, error) {
	return customerport.ChatActivityPage{}, customerport.ErrCapabilityNotReady
}

func testConfig(security testSecurity, store *testCustomerStore, identities *testIdentities, audit *testAudit) Config {
	key := []byte("0123456789abcdef0123456789abcdef")
	return Config{UnitOfWork: testUOW{}, Auth: security, CSRF: security, Directory: customerapp.Directory{Store: store, SigningKey: key},
		Store: store, Identities: identities, Audit: audit, Canonical: testCanonical{}, Owners: testOwners{}, Tags: testTags{},
		Surveys: testSurveys{}, Timeline: testTimeline{}, Chat: testChat{}, ProfileSigningKey: key}
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
			handler, err := NewHandler(testConfig(security, store, identities, audit))
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

func TestPhoneSearchAcceptsOnlyStrictLocalCNFormat(t *testing.T) {
	identities := &testIdentities{}
	audit := &testAudit{}
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{}
	handler, _ := NewHandler(testConfig(security, store, identities, audit))

	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?phone=13812345678", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(identities.phoneQueries) != 1 || identities.phoneQueries[0] != "13812345678" || store.lastQuery.Filters.PhoneCustomerID != 42 {
		t.Fatalf("queries=%v filter=%+v", identities.phoneQueries, store.lastQuery.Filters)
	}

	for _, value := range []string{"123", "%2B8613812345678", "138%201234%205678", "12812345678"} {
		response = httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?phone="+value, nil))
		if response.Code != http.StatusBadRequest || len(identities.phoneQueries) != 1 {
			t.Fatalf("invalid=%s status=%d queries=%v", value, response.Code, identities.phoneQueries)
		}
	}

	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers?activation_status=active", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("removed activation filter status=%d", response.Code)
	}
}

func TestCustomerDetailReturnsOnlySafeIdentityAndPhoneSummaries(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	store := &testCustomerStore{detail: customerapp.Detail{Item: customerapp.Item{CustomerID: 42, CustomerStatus: customerdomain.StatusActive,
		DisplayName: "Alice", AvatarURL: "https://provider/avatar", OneIDLabel: "CID-42", PhoneMasked: "138****5678",
		PhoneAssurance: string(identitydomain.AssuranceDeclared), ActivationState: "active"}, CorpName: "Example", Source: "wecom_directory_sync"}}
	identities := &testIdentities{summaries: []identityport.DirectoryIdentitySummary{{Kind: identitydomain.KindWeComExternalUserID,
		Scope: "wecom-corp:raw-corp", Assurance: identitydomain.AssuranceVerified, Status: "active", Source: "secret-source"}},
		phones: []identityport.MaskedPhone{{Masked: "138****5678", Assurance: identitydomain.AssuranceDeclared}}}
	handler, err := NewHandler(testConfig(security, store, identities, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/42", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"raw-corp", "secret-source", "declared", "verified", "+86", "provider/avatar", "activation_status", "phone_assurance"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("detail leaked %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{"企微身份已验证", "138****5678", `"chat_activity":{"status":"not_ready"}`} {
		if !strings.Contains(body, required) {
			t.Fatalf("detail missing %q: %s", required, body)
		}
	}
}

func TestChatSectionIsExplicitlyNotReady(t *testing.T) {
	security := testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	handler, err := NewHandler(testConfig(security, &testCustomerStore{}, &testIdentities{}, &testAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/customers/42/chat-activity?limit=20", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "capability_not_ready") || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
}
