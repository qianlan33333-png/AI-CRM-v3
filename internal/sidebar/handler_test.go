package sidebar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type testContext struct{}

func (testContext) VerifySidebarContext(context.Context, string) (Principal, customerdomain.CustomerID, error) {
	return Principal{CorpID: "corp", EmployeeID: "staff"}, 42, nil
}

type testProfile struct{}

func (testProfile) ReadSidebarProfile(context.Context, customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{CustomerID: 42, DisplayName: "Alice", Status: "active", Version: 1}, nil
}
func (testProfile) UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{CustomerID: 42, DisplayName: "Alice", Version: 2}, nil
}
func (testProfile) BindSidebarPhone(context.Context, customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	return customerport.SidebarPhoneResult{Status: "attached", PhoneMasked: "138****5678"}, nil
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

type testProducts struct{}

func (testProducts) ListProductOptions(context.Context, productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	return productport.ProductOptionPage{Items: []productport.ProductOption{}}, nil
}
func (testProducts) ReadProductTarget(context.Context, productport.ProductOptionType, productport.ID) (productport.ProductOption, error) {
	return productport.ProductOption{}, nil
}

type testOrders struct{}

func (testOrders) Get(context.Context, int64) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (testOrders) GetByReference(context.Context, string) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (testOrders) List(context.Context, orderport.ListQuery) (orderport.Page, error) {
	return orderport.Page{Items: []orderdomain.Snapshot{}}, nil
}

type testEntitlements struct{}

func (testEntitlements) ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error) {
	return orderport.EntitlementPage{Items: []orderport.Entitlement{}}, nil
}
func (testEntitlements) ListServicePeriodMembers(context.Context, orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	return orderport.ServicePeriodMemberPage{Items: []orderport.Entitlement{}}, nil
}
func (testEntitlements) GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error) {
	return orderport.Entitlement{}, false, nil
}
func (testEntitlements) UpdateEntitlementRemark(context.Context, orderport.RemarkCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, nil
}

type testCoupons struct{}

func (testCoupons) ListCustomerCoupons(context.Context, int64, int32) (couponport.CustomerCouponPage, error) {
	return couponport.CustomerCouponPage{Items: []couponport.CustomerCoupon{}}, nil
}

type testMaterials struct{}

func (testMaterials) ListImages(context.Context, mediaport.ImageListQuery) (mediaport.ImageListPage, error) {
	return mediaport.ImageListPage{Items: []mediaport.ImageListItem{}}, nil
}
func (testMaterials) Facets(context.Context) (mediaport.ImageFacets, error) {
	return mediaport.ImageFacets{}, nil
}
func (testMaterials) LocalImageExists(context.Context, int64) (bool, error) { return true, nil }
func (testMaterials) ReadSidebarImageForSend(context.Context, int64, time.Time) (mediaport.SidebarImageSendMaterial, error) {
	return mediaport.SidebarImageSendMaterial{ImageID: 1, MediaID: "media-1", ReadyUntil: time.Now().Add(time.Hour)}, nil
}

type unreadyMaterials struct{ testMaterials }

func (unreadyMaterials) ReadSidebarImageForSend(context.Context, int64, time.Time) (mediaport.SidebarImageSendMaterial, error) {
	return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
}

type testRadar struct{}

func (testRadar) List(context.Context, radarport.ListQuery) (radarport.LinkPage, error) {
	return radarport.LinkPage{Items: []radarport.LinkSummary{}}, nil
}
func (testRadar) Get(context.Context, radarport.RadarID) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}
func (testRadar) Create(context.Context, radarport.CreateCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}
func (testRadar) Update(context.Context, radarport.UpdateCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}
func (testRadar) SetStatus(context.Context, radarport.SetStatusCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{}, nil
}

type testSends struct{}

func (testSends) AcceptSidebarSend(context.Context, outboundport.SidebarSendCommand) (outboundport.SidebarSendAcceptance, error) {
	return outboundport.SidebarSendAcceptance{}, nil
}
func (testSends) CompleteSidebarSend(context.Context, outboundport.SidebarSendOutcomeCommand) (outboundport.SidebarSendAcceptance, error) {
	return outboundport.SidebarSendAcceptance{}, nil
}

func testRoutes(t *testing.T) http.Handler {
	t.Helper()
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return handler.Routes()
}

func TestEverySidebarCapabilityRequiresContextAndNeverAcceptsCustomerIdentity(t *testing.T) {
	for _, path := range []string{"/api/sidebar/v2/workbench", "/api/sidebar/v2/profile", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/timeline", "/api/sidebar/v2/products", "/api/sidebar/v2/orders", "/api/sidebar/v2/periodic-orders", "/api/sidebar/v2/coupons", "/api/sidebar/v2/materials", "/api/sidebar/v2/radar-links"} {
		response := httptest.NewRecorder()
		testRoutes(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"?external_userid=forbidden", nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestReadyReadsExcludeRemovedCapabilitiesAndRawExternalIdentity(t *testing.T) {
	for _, path := range []string{"/api/sidebar/v2/workbench", "/api/sidebar/v2/profile", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/timeline", "/api/sidebar/v2/products", "/api/sidebar/v2/orders", "/api/sidebar/v2/periodic-orders", "/api/sidebar/v2/coupons", "/api/sidebar/v2/materials", "/api/sidebar/v2/radar-links"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer signed")
		response := httptest.NewRecorder()
		testRoutes(t).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		for _, forbidden := range []string{"external_userid", "relationship", "message_summary", "automation_status", `"tags"`, `"owners"`} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("path=%s leaked %q: %s", path, forbidden, response.Body.String())
			}
		}
		var payload any
		if json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatalf("path=%s invalid JSON", path)
		}
	}
}

func TestMaterialSendFailsClosedWithoutProviderReadyMediaID(t *testing.T) {
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: unreadyMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/sidebar/v2/send-intents", strings.NewReader(`{"resource_kind":"material","resource_id":"7"}`))
	request.Header.Set("Authorization", "Bearer signed")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "material-send-test-0001")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"capability_not_ready"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
