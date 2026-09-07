package sidebar

import (
	"context"
	"encoding/json"
	"errors"
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

type fixedProducts struct{ product productport.ProductOption }

func (products fixedProducts) ListProductOptions(context.Context, productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	return productport.ProductOptionPage{Items: []productport.ProductOption{products.product}, Total: 1}, nil
}
func (products fixedProducts) ReadProductTarget(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	if kind != products.product.ProductType || id != products.product.ID {
		return productport.ProductOption{}, errors.New("product unavailable")
	}
	return products.product, nil
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
func (testEntitlements) UpdateEntitlementAlliance(context.Context, orderport.AllianceCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, nil
}

type testCoupons struct{}

func (testCoupons) ListSidebarClaimable(context.Context, int64, couponport.SidebarClaimableQuery) (couponport.SidebarClaimablePage, error) {
	return couponport.SidebarClaimablePage{Items: []couponport.SidebarClaimableItem{}}, nil
}

type recordingCoupons struct {
	page       couponport.SidebarClaimablePage
	customerID int64
	query      couponport.SidebarClaimableQuery
}

func (catalog *recordingCoupons) ListSidebarClaimable(_ context.Context, customerID int64, query couponport.SidebarClaimableQuery) (couponport.SidebarClaimablePage, error) {
	catalog.customerID, catalog.query = customerID, query
	return catalog.page, nil
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

type testImageVariants struct {
	variant mediaport.ImageVariant
	err     error
	calls   int
}

func (reader *testImageVariants) GetEnabledImageVariant(context.Context, int64, string) (mediaport.ImageVariant, error) {
	reader.calls++
	return reader.variant, reader.err
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
	return testRoutesWithCoupons(t, testCoupons{})
}

func testRoutesWithCoupons(t *testing.T, coupons couponport.SidebarClaimableCatalog) http.Handler {
	t.Helper()
	products := testProducts{}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: coupons, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com"})
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

func TestMaterialVariantUsesEnabledViewerProjection(t *testing.T) {
	products := testProducts{}
	variants := &testImageVariants{variant: mediaport.ImageVariant{Content: []byte("png"), MediaType: "image/png", ETag: `"fixture"`}}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, ImageVariants: variants, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/materials/7/variants/thumb_320", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || variants.calls != 1 {
		t.Fatalf("enabled variant status=%d content_type=%q calls=%d", response.Code, response.Header().Get("Content-Type"), variants.calls)
	}
	variants.err = errors.New("disabled")
	request = httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/materials/8/variants/thumb_320", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"resource_not_available"`) {
		t.Fatalf("unavailable variant status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCouponsExposeCouponRuleDirectoryWithoutCreatingShares(t *testing.T) {
	catalog := &recordingCoupons{page: couponport.SidebarClaimablePage{
		Items: []couponport.SidebarClaimableItem{
			{CouponID: 7, Name: "目录券", DiscountMinor: 990, Currency: "CNY", Targets: []couponport.SidebarClaimableTarget{{Title: "标准商品", ProductType: productport.ProductOptionStandard}}, ClaimEndsAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), PublicSlug: "coupon-7", AvailabilityStatus: "scheduled"},
			{CouponID: 8, Name: "无链接目录券", DiscountMinor: 100, Currency: "CNY", ClaimEndsAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), AvailabilityStatus: "sold_out", UserLimitReached: true},
		}, Total: 2, Limit: 2, Offset: 3,
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/coupons?limit=2&offset=3", nil)
	request.Header.Set("Authorization", "Bearer signed")
	response := httptest.NewRecorder()
	testRoutesWithCoupons(t, catalog).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if catalog.customerID != 42 || catalog.query != (couponport.SidebarClaimableQuery{Limit: 2, Offset: 3}) {
		t.Fatalf("catalog scope customer=%d query=%+v", catalog.customerID, catalog.query)
	}
	var payload struct {
		Items []struct {
			CouponID           int64  `json:"coupon_id"`
			URL                string `json:"url"`
			AvailabilityStatus string `json:"availability_status"`
			UserLimitReached   bool   `json:"user_limit_reached"`
			Targets            []struct {
				Title string `json:"title"`
			} `json:"targets"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 || payload.Items[0].CouponID != 7 || payload.Items[0].URL != "https://crm.example.com/c/coupon-7" || payload.Items[0].AvailabilityStatus != "scheduled" || len(payload.Items[0].Targets) != 1 || payload.Items[0].Targets[0].Title != "标准商品" {
		t.Fatalf("unexpected coupon directory=%s", response.Body.String())
	}
	if payload.Items[1].URL != "" || payload.Items[1].AvailabilityStatus != "sold_out" || !payload.Items[1].UserLimitReached {
		t.Fatalf("missing-slug or availability mapping changed=%s", response.Body.String())
	}
	for _, forbidden := range []string{"claim_id", "claimed_at", "public_slug", "eligible"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("coupon directory leaked or inferred %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestProductSendIntentUsesStandardNewsCardPayload(t *testing.T) {
	products := fixedProducts{product: productport.ProductOption{ID: 9, Code: "course-9", ProductType: productport.ProductOptionStandard, Name: "标准课程", PriceMinor: 19900, Currency: "CNY"}}
	handler, err := NewHandler(Config{Contexts: testContext{}, Profiles: testProfile{}, Surveys: testSurveys{}, Timeline: testTimeline{}, Products: products, ProductByID: products, Orders: testOrders{}, Entitlements: testEntitlements{}, Coupons: testCoupons{}, Materials: testMaterials{}, MaterialSend: testMaterials{}, Radar: testRadar{}, Sends: testSends{}, PublicOrigin: "https://crm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := handler.sendPayload(context.Background(), "product", "9", productport.ProductOptionStandard)
	if err != nil {
		t.Fatal(err)
	}
	var news struct {
		MessageType string `json:"msgtype"`
		News        struct {
			Link   string `json:"link"`
			Title  string `json:"title"`
			Desc   string `json:"desc"`
			ImgURL string `json:"imgUrl"`
		} `json:"news"`
	}
	if err := json.Unmarshal(payload, &news); err != nil {
		t.Fatal(err)
	}
	if news.MessageType != "news" || news.News.Link != "https://crm.example.com/p/course-9" || news.News.Title != "标准课程" || news.News.Desc != "" || news.News.ImgURL != "https://crm.example.com/static/sidebar_workbench/product-card-cover.png" {
		t.Fatalf("standard product card payload=%s", payload)
	}
}
