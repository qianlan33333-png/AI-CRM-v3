package paymenthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
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
	return domain.Payment{ID: 7, OrderID: 3, MerchantOrderNo: "M-7", Status: domain.StatusAwaitingPrepay, EffectID: "eer_8"}, nil
}
func (*appStub) GetCheckout(context.Context, string, string) (paymentport.Handoff, error) {
	return paymentport.Handoff{PaymentID: 7, MerchantOrder: "M-7", Status: domain.StatusAwaitingPayment, Payload: []byte(`{"appId":"wx-test","package":"prepay_id=safe"}`), ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (*appStub) RequestRefund(context.Context, paymentport.RefundCommand) (domain.Refund, error) {
	return domain.Refund{}, nil
}
func (*appStub) ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error {
	return nil
}
func (*appStub) ApplyVerifiedShopCallback(context.Context, paymentport.ShopRefundCallback) error {
	return nil
}
func (*appStub) ReconcileShopRefund(context.Context, int64) (domain.Refund, error) {
	return domain.Refund{}, nil
}
func (*appStub) ReconcileWeChatPayPayment(context.Context, int64) (domain.Payment, error) {
	return domain.Payment{}, nil
}
func (*appStub) ReconcileWeChatPayRefund(context.Context, int64) (domain.Refund, error) {
	return domain.Refund{}, nil
}
func (*appStub) FindPayment(context.Context, domain.Provider, string) (domain.Payment, error) {
	return domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-9", Status: domain.StatusPaid}, nil
}
func (*appStub) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}
func (*appStub) ListOrderEffects(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error) {
	return nil, nil
}

type securityStub struct{}

func (securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}

type h5OAuthStub struct {
	enabled bool
	starts  int
	issued  paymentsession.Issued
}

func (stub *h5OAuthStub) Enabled() bool { return stub.enabled }
func (stub *h5OAuthStub) Start(_ context.Context, returnPath string) (string, error) {
	stub.starts++
	if returnPath != "/pay/course-7" {
		return "", errors.New("invalid")
	}
	return "https://open.weixin.qq.com/oauth", nil
}
func (stub *h5OAuthStub) Complete(context.Context, string, string) (paymentsession.Issued, string, error) {
	return stub.issued, "/pay/course-7", nil
}

type sessionVerifierStub struct{ fact identitydomain.VerifiedFact }

func (stub sessionVerifierStub) VerifyCode(context.Context, string) (identitydomain.VerifiedFact, error) {
	return stub.fact, nil
}

type sessionIssuerStub struct {
	command paymentsession.IssueCommand
}

func (stub *sessionIssuerStub) IssueTrusted(_ context.Context, command paymentsession.IssueCommand) (paymentsession.Issued, error) {
	stub.command = command
	return paymentsession.Issued{Token: "pays_session_token_0000000001", ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func TestTrustedSessionEndpointVerifiesCodeAndSetsOpaqueCookie(t *testing.T) {
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:wx-app", Value: "openid-1", Source: "wechat_miniprogram"})
	if err != nil {
		t.Fatal(err)
	}
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	issuer := &sessionIssuerStub{}
	if err = handler.SetTrustedSessionIssuer(sessionVerifierStub{fact: fact}, issuer); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/sessions", strings.NewReader(`{"code":"one-time-code"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "session-issue-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	cookies := response.Result().Cookies()
	if response.Code != http.StatusCreated || len(cookies) != 1 || cookies[0].Name != SessionCookieName || !cookies[0].HttpOnly || !issuer.command.Fact.Valid() || strings.Contains(response.Body.String(), "openid") {
		t.Fatalf("status=%d cookies=%+v command=%+v body=%s", response.Code, cookies, issuer.command, response.Body.String())
	}
}
func (securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

func TestCheckoutAcceptsOnlyOpaqueCookieIdentity(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "checkout-key-0000001")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || application.createCalls != 1 || application.create.ProductID != 3 || application.create.ProductType != "standard" || application.create.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || application.create.SessionToken == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("code=%d command=%+v body=%s", response.Code, application.create, response.Body.String())
	}
	for _, rawField := range []string{"customer_id", "beneficiary_customer_id", "openid", "unionid", "assurance"} {
		request = httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","`+rawField+`":"attacker"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("field=%s code=%d", rawField, response.Code)
		}
	}
}

func TestH5OAuthStartRequiresWeChatAndDisabledMakesZeroCalls(t *testing.T) {
	handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
	disabled := &h5OAuthStub{}
	if err := handler.SetH5OAuth(disabled); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || disabled.starts != 0 {
		t.Fatalf("code=%d starts=%d", response.Code, disabled.starts)
	}
	enabled := &h5OAuthStub{enabled: true}
	_ = handler.SetH5OAuth(enabled)
	request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=https%3A%2F%2Fevil.test%2Fpay%2Fcourse-7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || enabled.starts != 1 {
		t.Fatalf("code=%d starts=%d", response.Code, enabled.starts)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://open.weixin.qq.com/oauth" {
		t.Fatalf("code=%d location=%q", response.Code, response.Header().Get("Location"))
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

func TestCheckoutHandoffPollingKeepsIdentityOpaqueAndSessionUntilTerminalStatus(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-7", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("unexpected terminal cookie clear=%+v", cookies)
	}
}
