package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLComposedWeChatPayRefundReconcileRouteFailsClosedWhenProviderDisabled
// reaches the real adminAPIs mux through the outer Composition root. Payment
// remains disabled, so the assertion proves the route is mounted without
// creating a Provider request.
func TestPostgreSQLComposedWeChatPayRefundReconcileRouteFailsClosedWhenProviderDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://payment-refund-route.test",
		ReleaseSHA:   "payment-refund-route-integration",
		WorkerOwner:  "payment-refund-route-integration",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "payment-refund-route-integration-webhook-secret"},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
		},
		Bootstrap: platformconfig.Bootstrap{
			Enabled: true, Username: "payment-route-owner", Password: "payment-route-owner-password", DisplayName: "Payment Route Owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{
		Enabled: true, Username: "payment-route-owner", Password: "payment-route-owner-password", DisplayName: "Payment Route Owner",
	}); err != nil {
		t.Fatal(err)
	}

	session, csrf := adminAccessLogin(t, application.handler, "payment-route-owner", "payment-route-owner-password")
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/admin/wechat-pay/refunds/987/reconcile", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "payment-refund-route-987")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"payment_provider_disabled"`) {
		t.Fatalf("composed refund reconcile status=%d disabled=%t body=%s", response.Code, strings.Contains(response.Body.String(), `"payment_provider_disabled"`), response.Body.String())
	}
}
