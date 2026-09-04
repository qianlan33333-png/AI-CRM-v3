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
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/products/7", nil))
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "secret-code") || strings.Contains(recorder.Body.String(), "lead_program") || !strings.Contains(recorder.Body.String(), `"require_mobile":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/p/7", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "公开商品") || !strings.Contains(page.Body.String(), "/pay/7") {
		t.Fatalf("status=%d body=%s", page.Code, page.Body.String())
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
		for _, path := range []string{"/api/public/products/8", "/p/8", "/pay/8"} {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
			}
		}
	}
}
