package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
