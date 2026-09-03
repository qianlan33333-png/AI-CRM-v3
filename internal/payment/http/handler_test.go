package paymenthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
)

type appStub struct {
	createCalls int
	create      paymentport.CreateCommand
}

func (stub *appStub) Create(_ context.Context, command paymentport.CreateCommand) (domain.Payment, error) {
	stub.createCalls++
	stub.create = command
	return domain.Payment{ID: 7, Status: domain.StatusAwaitingPrepay, EffectID: "eer_8"}, nil
}
func (*appStub) RequestRefund(context.Context, paymentport.RefundCommand) (domain.Refund, error) {
	return domain.Refund{}, nil
}
func (*appStub) ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error {
	return nil
}
func (*appStub) FindPayment(context.Context, domain.Provider, string) (domain.Payment, error) {
	return domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-9", Status: domain.StatusPaid}, nil
}
func (*appStub) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}

type securityStub struct{}

func (securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}
func (securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

func TestCheckoutAcceptsOnlyOpaqueCookieIdentity(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"order_id":3}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "checkout-key-0000001")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || application.createCalls != 1 || application.create.OrderID != 3 || application.create.SessionToken == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("code=%d command=%+v body=%s", response.Code, application.create, response.Body.String())
	}
	for _, rawField := range []string{"customer_id", "openid", "unionid", "assurance"} {
		request = httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"order_id":3,"`+rawField+`":"attacker"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("field=%s code=%d", rawField, response.Code)
		}
	}
}

func TestTrustedCookieSecurityAttributes(t *testing.T) {
	response := httptest.NewRecorder()
	err := WriteTrustedSessionCookie(response, paymentsession.Issued{Token: "pays_session_token_0000000001", ExpiresAt: time.Now().Add(time.Minute)})
	cookies := response.Result().Cookies()
	if err != nil || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/api/v1/wechat-pay/" {
		t.Fatalf("cookies=%+v err=%v", cookies, err)
	}
}
