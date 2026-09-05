package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPublicProductEnabledOnlyAndSafeDTO(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	catalog := &testCatalog{product: productport.Product{
		ID: 7, ProductCode: "secret-code", Name: "公开商品", Description: "描述", PriceMinor: 990, Currency: "CNY",
		Images: []string{"https://cdn.example.test/p.png"}, CreatedBy: 99, CreatedAt: now, UpdatedAt: now, Version: 3,
		LocalLifecycle:        productport.LocalProductEnabled,
		LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"现在购买","require_mobile":true,"lead_program_id":23,"lead_channel_id":34,"lead_qr_title":"internal","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`),
	}}
	handler, err := NewPublicHandler(catalog)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/products/secret-code", nil))
	if recorder.Code != http.StatusOK || catalog.getCode != "secret-code" || strings.Contains(recorder.Body.String(), "secret-code") || strings.Contains(recorder.Body.String(), "lead_program") || !strings.Contains(recorder.Body.String(), `"require_mobile":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/p/secret-code", nil))
	if page.Code != http.StatusOK || catalog.getCode != "secret-code" || !strings.Contains(page.Body.String(), "公开商品") || !strings.Contains(page.Body.String(), `\/pay\/secret-code`) {
		t.Fatalf("status=%d body=%s", page.Code, page.Body.String())
	}
	payment := httptest.NewRecorder()
	handler.ServeHTTP(payment, httptest.NewRequest(http.MethodGet, "/pay/secret-code", nil))
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), "我确认购买后权益归我本人") || !strings.Contains(payment.Body.String(), "beneficiary_selection:'payer_self'") || strings.Contains(payment.Body.String(), "beneficiary_customer_id") {
		t.Fatalf("payment page status=%d body=%s", payment.Code, payment.Body.String())
	}
	for _, required := range []string{"自动选择最优优惠券", "checkoutStorageKey", "merchant_order_no", "正在恢复原订单", "Idempotency-Key':checkpoint.key", "clearCheckout()"} {
		if !strings.Contains(payment.Body.String(), required) {
			t.Fatalf("payment page missing stable checkout behaviour %q: %s", required, payment.Body.String())
		}
	}
	if strings.Contains(payment.Body.String(), "Idempotency-Key':crypto.randomUUID()") {
		t.Fatalf("payment page must not replace an unknown checkout key: %s", payment.Body.String())
	}
	legacyID := httptest.NewRecorder()
	handler.ServeHTTP(legacyID, httptest.NewRequest(http.MethodGet, "/p/7", nil))
	if legacyID.Code != http.StatusOK || catalog.getCode != "7" || catalog.getID != 7 {
		t.Fatalf("numeric public path must remain a historical ID alias: status=%d code=%q id=%d body=%s", legacyID.Code, catalog.getCode, catalog.getID, legacyID.Body.String())
	}
	legacyPayment := httptest.NewRecorder()
	handler.ServeHTTP(legacyPayment, httptest.NewRequest(http.MethodGet, "/pay/7", nil))
	if legacyPayment.Code != http.StatusOK || catalog.getCode != "7" || catalog.getID != 7 || !strings.Contains(legacyPayment.Body.String(), "beneficiary_selection:'payer_self'") {
		t.Fatalf("numeric payment alias must retain checkout: status=%d code=%q id=%d body=%s", legacyPayment.Code, catalog.getCode, catalog.getID, legacyPayment.Body.String())
	}

	numericCodeCatalog := &testCatalog{product: catalog.product}
	numericCodeCatalog.product.ProductCode = "7"
	numericCodeHandler, err := NewPublicHandler(numericCodeCatalog)
	if err != nil {
		t.Fatal(err)
	}
	numericCode := httptest.NewRecorder()
	numericCodeHandler.ServeHTTP(numericCode, httptest.NewRequest(http.MethodGet, "/p/7", nil))
	if numericCode.Code != http.StatusOK || numericCodeCatalog.getCode != "7" || numericCodeCatalog.getCalls != 0 {
		t.Fatalf("product code must take precedence over legacy ID: status=%d code=%q get_calls=%d", numericCode.Code, numericCodeCatalog.getCode, numericCodeCatalog.getCalls)
	}
}

func TestPublicProductDraftDisabledAndMalformedAre404(t *testing.T) {
	for _, projection := range []json.RawMessage{
		json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`),
		json.RawMessage(`{"schema_version":1,"status":"disabled","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`),
		json.RawMessage(`{"status":"unknown"}`),
	} {
		handler, err := NewPublicHandler(&testCatalog{product: productport.Product{ID: 8, ProductCode: "p-8", Name: "hidden", PriceMinor: 100, Currency: "CNY", Version: 1, LegacyAdminProjection: projection}})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/public/products/p-8", "/p/p-8", "/pay/p-8"} {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
			}
		}
	}
}

func TestPublicProductRejectsMalformedCodePaths(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	catalog := &testCatalog{product: productport.Product{ID: 7, ProductCode: "course-7", Name: "公开商品", PriceMinor: 990, Currency: "CNY", CreatedBy: 99, CreatedAt: now, UpdatedAt: now, Version: 1, LocalLifecycle: productport.LocalProductEnabled, LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)}}
	handler, err := NewPublicHandler(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/p/course-7/extra", "/pay/course-7/extra", "/p/%2F", "/api/public/products/course-7?x=1"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

type servicePeriodPublicStub struct {
	product productport.CheckoutProduct
	code    string
}

func (stub *servicePeriodPublicStub) ReadPublicServicePeriodByCode(_ context.Context, code string) (productport.CheckoutProduct, error) {
	stub.code = code
	if code != stub.product.Code {
		return productport.CheckoutProduct{}, errors.New("not found")
	}
	return stub.product, nil
}

type servicePeriodTestUOW struct{}

func (servicePeriodTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type servicePeriodSessionStub struct{}

func (servicePeriodSessionStub) LookupWithin(_ context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if token != "service-period-trusted" {
		return paymentport.SessionActor{}, errors.New("bad session")
	}
	return paymentport.SessionActor{PayerCustomerID: 11, BeneficiaryCustomerID: 11}, nil
}

type servicePeriodEntitlementStub struct{ page orderport.EntitlementPage }

func (stub servicePeriodEntitlementStub) ListCustomerEntitlements(_ context.Context, customerID int64, _ int32) (orderport.EntitlementPage, error) {
	if customerID != 11 {
		return orderport.EntitlementPage{}, errors.New("wrong customer")
	}
	return stub.page, nil
}
func (servicePeriodEntitlementStub) ListServicePeriodMembers(context.Context, orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	return orderport.ServicePeriodMemberPage{}, errors.New("unused")
}
func (stub servicePeriodEntitlementStub) GetCustomerServicePeriodEntitlement(_ context.Context, customerID, productID int64) (orderport.Entitlement, bool, error) {
	if customerID != 11 {
		return orderport.Entitlement{}, false, errors.New("wrong customer")
	}
	for _, item := range stub.page.Items {
		if item.ServiceProductID == productID {
			return item, true, nil
		}
	}
	return orderport.Entitlement{}, false, nil
}
func (servicePeriodEntitlementStub) UpdateEntitlementRemark(context.Context, orderport.RemarkCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, errors.New("unused")
}

type servicePeriodLeadQRStub struct{ value channelport.PublicLeadQRCode }

func (stub servicePeriodLeadQRStub) ReadPublicLeadQRCode(context.Context, int64) (channelport.PublicLeadQRCode, error) {
	return stub.value, nil
}

func TestServicePeriodPublicHostRetainsFrozenDonorStateDOM(t *testing.T) {
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(frozenServicePeriodPublicRenderer))); got != frozenServicePeriodPublicRendererSHA256 {
		t.Fatalf("service-period donor hash=%s", got)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(frozenPublicProductService))); got != frozenPublicProductServiceSHA256 {
		t.Fatalf("public-product service donor hash=%s", got)
	}
	var page bytes.Buffer
	if err := renderServicePeriodPublicPage(&page, servicePeriodPublicState{Available: true, Product: publicProduct{ID: 71, Name: "31 天服务期", PriceMinor: 12800, PaymentPath: "/s/term-31/pay", ServicePeriodDurationDays: 31}, Status: "active", CTA: "立即续费", EndAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), RemainingDays: 15}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`service-period-page`, `servicePeriodStateCard`, `servicePeriodPayButton`, `data-route-owner="ai_crm_next"`, `service-period-wecom-action`, `detail-media`, `width:48%`, `fetch(window.location.pathname.replace(`} {
		if !strings.Contains(page.String(), marker) {
			t.Fatalf("adapted donor state DOM missing %q", marker)
		}
	}
}

func TestPublicServicePeriodRendersTrustedEntitlementWithoutIdentityFallback(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, DetailMedia: []productport.PublicDetailMedia{{ImageID: 88}}, LeadChannelID: 44, LeadQRTitle: "加企微", LeadQRSubtitle: "领取资料", ServicePeriodDurationDays: 31}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	activeEnd := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	if err = handler.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodEntitlementStub{page: orderport.EntitlementPage{Items: []orderport.Entitlement{{CustomerID: 11, ServiceProductID: 71, Status: "active", EndAt: activeEnd}}}}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicLeadQRCodeReader(servicePeriodLeadQRStub{value: channelport.PublicLeadQRCode{URL: "https://work.weixin.qq.com/q/term"}}); err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC) }
	request := httptest.NewRequest(http.MethodGet, "/s/term-31", nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "service-period-trusted"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `is-active`) || !strings.Contains(response.Body.String(), "服务中") || !strings.Contains(response.Body.String(), "剩余 15 天") || !strings.Contains(response.Body.String(), "立即续费") || !strings.Contains(response.Body.String(), "https://work.weixin.qq.com/q/term") || !strings.Contains(response.Body.String(), `class="slice-img"`) || !strings.Contains(response.Body.String(), "/images/88/variants/original") {
		t.Fatalf("active page status=%d body=%s", response.Code, response.Body.String())
	}
	untrusted := httptest.NewRecorder()
	handler.ServeHTTP(untrusted, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	if untrusted.Code != http.StatusOK || !strings.Contains(untrusted.Body.String(), `is-none`) {
		t.Fatalf("untrusted page status=%d body=%s", untrusted.Code, untrusted.Body.String())
	}
}

func TestPublicServicePeriodUsesExactCodeAndSeparateCheckoutRoute(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, DetailMedia: []productport.PublicDetailMedia{{ImageID: 88}}, ServicePeriodDurationDays: 31}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	if page.Code != http.StatusOK || reader.code != "term-31" || !strings.Contains(page.Body.String(), `class="service-period-page is-none"`) || !strings.Contains(page.Body.String(), "有效期</span><strong>31 天") || !strings.Contains(page.Body.String(), `"checkout_url":"/s/term-31/pay"`) || !strings.Contains(page.Body.String(), `fetch(window.location.pathname`) || !strings.Contains(page.Body.String(), `createLeadQrModalController`) {
		t.Fatalf("page status=%d code=%q body=%s", page.Code, reader.code, page.Body.String())
	}
	payment := httptest.NewRecorder()
	handler.ServeHTTP(payment, httptest.NewRequest(http.MethodGet, "/s/term-31/pay", nil))
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), "product_kind:'service_period'") || !strings.Contains(payment.Body.String(), "beneficiary_selection:'payer_self'") {
		t.Fatalf("payment status=%d body=%s", payment.Code, payment.Body.String())
	}
	for _, path := range []string{"/s/71", "/s/term-31/extra", "/s/term-31/pay/extra", "/s/%2F", "/s/term-31?x=1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
}

func TestPublicServicePeriodStateEndpointUsesTheFrozenStateContract(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}}
	h, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodEntitlementStub{page: orderport.EntitlementPage{Items: []orderport.Entitlement{{ServiceProductID: 71, Status: "active", EndAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)}}}}); err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC) }
	request := httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/term-31", nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "service-period-trusted"})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":true`) || !strings.Contains(response.Body.String(), `"status":"active"`) || !strings.Contains(response.Body.String(), `"remaining_days":15`) || !strings.Contains(response.Body.String(), `"checkout_url":"/s/term-31/pay"`) {
		t.Fatalf("state endpoint status=%d body=%s", response.Code, response.Body.String())
	}
}

type servicePeriodMediaStub struct{}

func (servicePeriodMediaStub) LocalImageExists(_ context.Context, id int64) (bool, error) {
	return id == 88, nil
}
func (servicePeriodMediaStub) GetImageVariant(_ context.Context, id int64, key string) (mediaport.ImageVariant, error) {
	if id != 88 || key != "original" {
		return mediaport.ImageVariant{}, errors.New("not found")
	}
	return mediaport.ImageVariant{Content: []byte("image-88"), MediaType: "image/png", ETag: `"image-88"`}, nil
}
func TestPublicServicePeriodMediaIsRestrictedToProductSlices(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "期", PriceMinor: 100, Currency: "CNY", Version: 1, ServicePeriodDurationDays: 31, DetailMedia: []productport.PublicDetailMedia{{ImageID: 88}}}}
	h, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.SetPublicMediaReader(servicePeriodMediaStub{}); err != nil {
		t.Fatal(err)
	}
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/term-31/images/88/variants/original", nil))
	if ok.Code != http.StatusOK || ok.Body.String() != "image-88" || ok.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("ok=%d body=%q headers=%v", ok.Code, ok.Body.String(), ok.Header())
	}
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/term-31/images/89/variants/original", nil))
	if denied.Code != http.StatusNotFound {
		t.Fatalf("denied=%d", denied.Code)
	}
}
