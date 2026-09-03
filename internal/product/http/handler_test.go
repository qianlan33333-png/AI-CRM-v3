package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type testSecurity struct {
	principal accessdomain.Principal
	authErr   error
	csrfErr   error
	authCalls int
	csrfCalls int
}

func (security *testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.authCalls++
	return security.principal, security.authErr
}

func (security *testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.csrfCalls++
	return security.principal, security.csrfErr
}

type testCatalog struct {
	page       productport.Page
	product    productport.Product
	listCalls  int
	getCalls   int
	createCall *productport.CreateCommand
	updateCall *productport.UpdateCommand
}

func (catalog *testCatalog) List(context.Context, string, int32) (productport.Page, error) {
	catalog.listCalls++
	return catalog.page, nil
}

func (catalog *testCatalog) Get(context.Context, productport.ID) (productport.Product, error) {
	catalog.getCalls++
	return catalog.product, nil
}

func (catalog *testCatalog) Create(_ context.Context, command productport.CreateCommand) (productport.Product, error) {
	catalog.createCall = &command
	return catalog.product, nil
}

func (catalog *testCatalog) Update(_ context.Context, command productport.UpdateCommand) (productport.Product, error) {
	catalog.updateCall = &command
	return catalog.product, nil
}

type testLifecycle struct {
	local      productport.LocalProduct
	share      productport.LocalProductShare
	deleteCall *productport.DeleteLocalProductCommand
}

func (lifecycle *testLifecycle) SetLocalProductEnabled(context.Context, productport.SetLocalProductEnabledCommand) (productport.LocalProduct, error) {
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) CopyLocalProduct(context.Context, productport.CopyLocalProductCommand) (productport.LocalProduct, error) {
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) DeleteLocalProduct(_ context.Context, command productport.DeleteLocalProductCommand) (productport.DeleteLocalProductResult, error) {
	lifecycle.deleteCall = &command
	return productport.DeleteLocalProductResult{ProductID: command.ID, Deleted: true}, nil
}

func (lifecycle *testLifecycle) ShareLocalProduct(context.Context, productport.ID) (productport.LocalProductShare, error) {
	return lifecycle.share, nil
}

type testServicePeriod struct {
	page    productport.ServicePeriodPage
	product productport.ServicePeriodProduct
}

func (service *testServicePeriod) ListServicePeriodProducts(context.Context, int32, int32) (productport.ServicePeriodPage, error) {
	return service.page, nil
}

func (service *testServicePeriod) GetServicePeriodProduct(context.Context, productport.ID) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) CreateServicePeriodProduct(context.Context, productport.CreateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) UpdateServicePeriodProduct(context.Context, productport.UpdateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) SetServicePeriodProductEnabled(context.Context, productport.SetServicePeriodProductEnabledCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) CopyServicePeriodProduct(context.Context, productport.CopyServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) ArchiveServicePeriodProduct(context.Context, productport.ArchiveServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

type testExternalPush struct {
	configuration productport.ExternalPushConfiguration
	test          productport.ExternalPushTest
}

func (external *testExternalPush) GetExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return external.configuration, nil
}

func (external *testExternalPush) SaveExternalPushConfiguration(context.Context, productport.SaveExternalPushConfigurationCommand) (productport.ExternalPushConfiguration, error) {
	return external.configuration, nil
}

func (external *testExternalPush) QueueExternalPushTest(context.Context, productport.QueueExternalPushTestCommand) (productport.ExternalPushTest, error) {
	return external.test, nil
}

func newHandlerForTest(t *testing.T) (*Handler, *testSecurity, *testCatalog, *testLifecycle) {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	projection := json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)
	product := productport.Product{ID: 7, ProductCode: "p-7", Name: "商品七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Version: 2, LegacyAdminProjection: projection}
	security := &testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	catalog := &testCatalog{product: product, page: productport.Page{Items: []productport.Product{product}}}
	lifecycle := &testLifecycle{local: productport.LocalProduct{ID: 7, ProductCode: "p-7", Name: "商品七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Lifecycle: productport.LocalProductDraft, Enabled: false, Version: 2}, share: productport.LocalProductShare{ProductID: 7, ProductCode: "p-7", Lifecycle: productport.LocalProductDraft, Available: false, Reason: productappUnavailableReason}}
	service := &testServicePeriod{page: productport.ServicePeriodPage{OK: true, Items: []productport.ServicePeriodProduct{}, Limit: 50, Offset: 0}, product: productport.ServicePeriodProduct{ServiceProductID: 7, ProductCode: "sp-7", Name: "周期七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, AdminProjection: json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`), Lifecycle: productport.ServicePeriodEnabled, Enabled: true, Version: 2, CreatedAt: now, UpdatedAt: now}}
	external := &testExternalPush{configuration: productport.ExternalPushConfiguration{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, Enabled: false, UpdatedAt: now}, test: productport.ExternalPushTest{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_1", State: "accepted", CreatedAt: now}}
	handler, err := NewHandler(catalog, lifecycle, service, external, security)
	if err != nil {
		t.Fatal(err)
	}
	return handler, security, catalog, lifecycle
}

// Keep the test fixture independent from the application package's internal
// constant while asserting the public blocked sharing contract.
const productappUnavailableReason = "no_authoritative_public_purchase_route"

func TestHandlerUsesOneToOneLimitAndProductListDTO(t *testing.T) {
	handler, security, catalog, _ := newHandlerForTest(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=100", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || catalog.listCalls != 1 || security.authCalls != 1 {
		t.Fatalf("status=%d listCalls=%d authCalls=%d body=%s", recorder.Code, catalog.listCalls, security.authCalls, recorder.Body.String())
	}
	var response struct {
		Items      []productport.Product `json:"items"`
		NextCursor string                `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 || response.NextCursor != "" {
		t.Fatalf("response=%s err=%v", recorder.Body.String(), err)
	}
	tooLarge := httptest.NewRecorder()
	handler.ServeHTTP(tooLarge, httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=101", nil))
	if tooLarge.Code != http.StatusBadRequest || catalog.listCalls != 1 {
		t.Fatalf("limit gate status=%d calls=%d body=%s", tooLarge.Code, catalog.listCalls, tooLarge.Body.String())
	}
}

func TestHandlerDeleteCompatibilityPathReachesLifecycle(t *testing.T) {
	handler, security, _, lifecycle := newHandlerForTest(t)
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/wechat-pay/products/7", strings.NewReader(`{"expected_version":2}`))
	request.Header.Set("Idempotency-Key", "product-delete-00000001")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || lifecycle.deleteCall == nil || lifecycle.deleteCall.ID != 7 || lifecycle.deleteCall.ExpectedVersion != 2 || security.csrfCalls != 1 {
		t.Fatalf("status=%d delete=%+v csrfCalls=%d body=%s", recorder.Code, lifecycle.deleteCall, security.csrfCalls, recorder.Body.String())
	}
}

func TestHandlerWriteRequiresAdminCSRFAndMintsCompatibilityKey(t *testing.T) {
	handler, security, catalog, _ := newHandlerForTest(t)
	security.csrfErr = errors.New("missing csrf")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"p-8","name":"商品八","description":"","price_minor":0,"currency":"CNY","stock_quantity":0,"images":[]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || catalog.createCall != nil || security.csrfCalls != 1 {
		t.Fatalf("status=%d create=%+v csrfCalls=%d", recorder.Code, catalog.createCall, security.csrfCalls)
	}
	security.csrfErr = nil
	request = httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"p-8","name":"商品八","description":"","price_minor":0,"currency":"CNY","stock_quantity":0,"images":[]}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || catalog.createCall == nil || len(catalog.createCall.IdempotencyKey) < 16 {
		t.Fatalf("status=%d create=%+v body=%s", recorder.Code, catalog.createCall, recorder.Body.String())
	}
}

func TestHandlerReturnsTruthfulCompatibilityReads(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	paths := []string{
		"/api/v1/products/7/local-entitlements",
		"/api/admin/service-period-products/7/members?state=all&source=paid_order&limit=100&cursor=opaque",
		"/api/admin/service-period-products/7/member-grid/access",
		"/api/admin/service-period-products/7/member-grid/schema",
		"/api/admin/service-period-products/7/member-views",
		"/api/admin/service-period-products/7/member-grid/share-settings",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/query", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("excluded grid query status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerRejectsMalformedIDWithoutApplicationCall(t *testing.T) {
	handler, _, catalog, _ := newHandlerForTest(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/products/07", nil))
	if recorder.Code != http.StatusNotFound || catalog.getCalls != 0 {
		t.Fatalf("status=%d getCalls=%d", recorder.Code, catalog.getCalls)
	}
}

func TestCompatibilityIdempotencyKeyFailsClosedWhenRandomReadFails(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	key, err := compatibilityIdempotencyKey(func([]byte) (int, error) {
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) || key != "" {
		t.Fatalf("key=%q err=%v, want empty key and entropy error", key, err)
	}
}
