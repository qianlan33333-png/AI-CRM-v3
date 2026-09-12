package paymenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type appStub struct {
	createCalls                                  int
	create                                       paymentport.CreateCommand
	handoff                                      paymentport.Handoff
	refundCalls                                  int
	refund                                       paymentport.RefundCommand
	payment                                      domain.Payment
	refundRows                                   []paymentport.RefundProjection
	refundTotal                                  int64
	listProvider                                 domain.Provider
	listMerchant                                 string
	listAllCalls                                 int
	recoveryRefund                               domain.Refund
	recoveryFound                                bool
	recoveryProvider                             domain.Provider
	recoveryMerchant, recoveryActor, recoveryKey string
}

func (stub *appStub) Create(_ context.Context, command paymentport.CreateCommand) (domain.Payment, error) {
	stub.createCalls++
	stub.create = command
	return domain.Payment{ID: 7, OrderID: 3, MerchantOrderNo: "M-7", Status: domain.StatusAwaitingPrepay, EffectID: "eer_8"}, nil
}
func (*appStub) CheckoutSessionBinding(_ context.Context, token string) (string, error) {
	binding := paymentport.CheckoutSessionBinding(token)
	if binding == "" {
		return "", paymentport.ErrSessionRequired
	}
	return binding, nil
}
func (stub *appStub) GetCheckout(context.Context, string, string) (paymentport.Handoff, error) {
	if stub.handoff.Status != "" {
		return stub.handoff, nil
	}
	return paymentport.Handoff{PaymentID: 7, MerchantOrder: "M-7", Status: domain.StatusAwaitingPayment, Payload: []byte(`{"appId":"wx-test","package":"prepay_id=safe"}`), ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type paidPurchaseActionReaderStub struct {
	action productport.PaidPurchaseAction
	order  int64
	err    error
}

func (stub *paidPurchaseActionReaderStub) ReadPaidPurchaseAction(_ context.Context, orderID int64) (productport.PaidPurchaseAction, error) {
	stub.order = orderID
	return stub.action, stub.err
}

type paidPurchaseLeadQRStub struct{ value channelport.PublicLeadQRCode }

func (stub paidPurchaseLeadQRStub) ReadPublicLeadQRCode(context.Context, int64) (channelport.PublicLeadQRCode, error) {
	return stub.value, nil
}
func (stub *appStub) RequestRefund(_ context.Context, command paymentport.RefundCommand) (domain.Refund, error) {
	stub.refundCalls++
	stub.refund = command
	return domain.Refund{ID: 8, Provider: domain.ProviderWeChatPay, RefundNo: "RF-8", Status: domain.RefundRequested}, nil
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
func (stub *appStub) FindPayment(context.Context, domain.Provider, string) (domain.Payment, error) {
	if stub.payment.ID != 0 {
		return stub.payment, nil
	}
	return domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-9", Status: domain.StatusPaid}, nil
}
func (stub *appStub) FindRefundRecoveryReceipt(_ context.Context, provider domain.Provider, merchantOrderNo, actorScope, key string) (domain.Refund, bool, error) {
	stub.recoveryProvider, stub.recoveryMerchant, stub.recoveryActor, stub.recoveryKey = provider, merchantOrderNo, actorScope, key
	return stub.recoveryRefund, stub.recoveryFound, nil
}
func (stub *appStub) GetPayment(context.Context, int64) (domain.Payment, error) {
	return stub.FindPayment(context.Background(), domain.ProviderWeChatPay, "")
}
func (stub *appStub) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	stub.listAllCalls++
	return stub.refundRows, stub.refundTotal, nil
}
func (stub *appStub) ListRefundsForPayment(_ context.Context, provider domain.Provider, merchant string, _ int32, _ int32) ([]paymentport.RefundProjection, int64, error) {
	stub.listProvider, stub.listMerchant = provider, merchant
	return stub.refundRows, stub.refundTotal, nil
}
func (*appStub) ListOrderEffects(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error) {
	return nil, nil
}

type securityStub struct{}

func (securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

type h5OAuthStub struct {
	completeError error
	enabled       bool
	starts        int
	issued        paymentsession.Issued
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
	return stub.issued, "/pay/course-7", stub.completeError
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

type recoverySecurityStub struct{ principal accessdomain.Principal }

func (stub recoverySecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, nil
}
func (stub recoverySecurityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, nil
}

func TestRefundListScopesOrderDetailToExactPayment(t *testing.T) {
	statuses := []domain.RefundStatus{
		domain.RefundCompleted,
		domain.RefundHistoryRequested,
		domain.RefundHistoryProcessing,
		domain.RefundHistoryFailed,
		domain.RefundHistoryClosed,
		domain.RefundStatus("legacy_unclassified"),
	}
	rows := make([]paymentport.RefundProjection, 0, len(statuses))
	for index, status := range statuses {
		rows = append(rows, paymentport.RefundProjection{
			Refund:  domain.Refund{ID: int64(31 + index), PaymentID: 21, Provider: domain.ProviderWeChatPay, RefundNo: "RF-" + strconv.Itoa(31+index), AmountMinor: 200, Status: status, CreatedAt: time.Date(2026, 9, 10, 4, 10, 38, 0, time.UTC)},
			OrderID: 12, MerchantOrder: "WXP2609100410381093CE4C5B0C", OrderAmount: 200000, Currency: "CNY",
		})
	}
	application := &appStub{refundRows: rows, refundTotal: int64(len(rows))}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/refunds?provider=wechat&order_no=WXP2609100410381093CE4C5B0C", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []struct {
			RefundID            string `json:"refund_id"`
			Status              string `json:"status"`
			TransactionID       string `json:"transaction_id"`
			ExternalEffectState string `json:"external_effect_state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	wantStatuses := []string{"completed", "history_requested", "history_processing", "history_failed", "history_closed", "unknown"}
	if response.Code != http.StatusOK || application.listProvider != domain.ProviderWeChatPay || application.listMerchant != "WXP2609100410381093CE4C5B0C" || len(page.Items) != len(wantStatuses) {
		t.Fatalf("status=%d provider=%q merchant=%q items=%+v", response.Code, application.listProvider, application.listMerchant, page.Items)
	}
	for index, want := range wantStatuses {
		item := page.Items[index]
		if item.Status != want || item.TransactionID != "" || item.ExternalEffectState != "" {
			t.Fatalf("item=%d got=%+v want status=%q and empty compatibility placeholders", index, item, want)
		}
	}
	for _, path := range []string{"/api/admin/refunds?provider=wechat", "/api/admin/refunds?order_no=WXP2609100410381093CE4C5B0C", "/api/admin/refunds?provider=v3pay&order_no=WXP2609100410381093CE4C5B0C"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/refunds?provider=all", nil))
	if response.Code != http.StatusOK || application.listAllCalls != 1 {
		t.Fatalf("all-provider compatibility status=%d unscopedCalls=%d", response.Code, application.listAllCalls)
	}
}
func TestRefundRecoveryReceiptIsPaymentActorAndKeyScoped(t *testing.T) {
	application := &appStub{recoveryFound: true, recoveryRefund: domain.Refund{ID: 31, PaymentID: 21, Provider: domain.ProviderWeChatPay, RefundNo: "RF-recovery-31", Status: domain.RefundCompleted, EffectID: "eer_41"}}
	security := &recoverySecurityStub{principal: accessdomain.Principal{InternalID: 17, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewHandler(application, nil, security, true)
	if err != nil {
		t.Fatal(err)
	}
	key := "refund-recovery-key-0001"
	request := httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery?provider=wechat&order_no=M-recovery-21", nil)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var found map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	actorBinding, bindingOK := found["actor_binding"].(string)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || application.recoveryProvider != domain.ProviderWeChatPay || application.recoveryMerchant != "M-recovery-21" || application.recoveryActor != "admin:17" || application.recoveryKey != key || found["found"] != true || found["receipt_id"] != float64(31) || !bindingOK || len(actorBinding) != 64 || strings.Contains(response.Body.String(), key) || strings.Contains(response.Body.String(), "admin:17") || found["external_effect_state"] != nil {
		t.Fatalf("status=%d provider=%q merchant=%q actor=%q key=%q body=%s", response.Code, application.recoveryProvider, application.recoveryMerchant, application.recoveryActor, application.recoveryKey, response.Body.String())
	}
	for _, path := range []string{
		"/api/admin/refunds/recovery?provider=wechat",
		"/api/admin/refunds/recovery?order_no=M-recovery-21",
		"/api/admin/refunds/recovery?provider=wechat&order_no=M-recovery-21&extra=1",
	} {
		response = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Idempotency-Key", key)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery?provider=wechat&order_no=M-recovery-21", nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing key status=%d", response.Code)
	}

	application.recoveryFound = false // A different actor/key/Payment must be indistinguishable from no receipt.
	security.principal.InternalID = 18
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery?provider=wechat&order_no=M-other-payment", nil)
	request.Header.Set("Idempotency-Key", "other-actor-or-key-0001")
	handler.ServeHTTP(response, request)
	var noMatch map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &noMatch); err != nil {
		t.Fatal(err)
	}
	otherBinding, otherBindingOK := noMatch["actor_binding"].(string)
	if response.Code != http.StatusOK || noMatch["found"] != false || !otherBindingOK || len(otherBinding) != 64 || otherBinding == actorBinding || strings.Contains(response.Body.String(), "admin:18") || application.recoveryActor != "admin:18" {
		t.Fatalf("nonmatch status=%d actor=%q body=%s", response.Code, application.recoveryActor, response.Body.String())
	}
}

func TestCompatRefundRequiresVerifiedWeChatTransactionID(t *testing.T) {
	transactionID := "4500000365202609101828595865"
	application := &appStub{payment: domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_order", Status: domain.StatusPaid, ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transactionID))}}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/wechat-pay/orders/v3pay_order/refunds"
	post := func(confirmation string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"provider":"wechat","order_no":"v3pay_order","refund_amount_total":200,"reason":"客户申请","transaction_id_confirmation":"`+confirmation+`","checked":true}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "refund-confirmation-test-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := post("v3pay_order"); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
		t.Fatalf("merchant fallback status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
	}
	if response := post(""); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
		t.Fatalf("missing transaction status=%d calls=%d", response.Code, application.refundCalls)
	}
	if response := post(transactionID); response.Code != http.StatusAccepted || application.refundCalls != 1 || application.refund.PaymentID != 9 {
		t.Fatalf("verified transaction status=%d calls=%d command=%+v body=%s", response.Code, application.refundCalls, application.refund, response.Body.String())
	}
}

func TestRefundConfirmationHonorsProviderBoundary(t *testing.T) {
	transactionID := "4500000365202609101828595865"
	payment := domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_order", Status: domain.StatusPaid, ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transactionID))}

	t.Run("generic WeChat Pay endpoint requires the verified transaction", func(t *testing.T) {
		application := &appStub{payment: payment}
		handler, err := NewHandler(application, nil, securityStub{}, true)
		if err != nil {
			t.Fatal(err)
		}
		post := func(confirmation string) *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/9/refunds", strings.NewReader(`{"amount_minor":200,"refund_no":"RF-generic","reason":"客户申请","transaction_id_confirmation":"`+confirmation+`"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "refund-generic-test-key")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			return response
		}
		if response := post("v3pay_order"); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("merchant fallback status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
		}
		if response := post(""); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("missing transaction status=%d calls=%d", response.Code, application.refundCalls)
		}
		if response := post(transactionID); response.Code != http.StatusAccepted || application.refundCalls != 1 || application.refund.PaymentID != payment.ID {
			t.Fatalf("verified transaction status=%d calls=%d command=%+v body=%s", response.Code, application.refundCalls, application.refund, response.Body.String())
		}
	})

	t.Run("WeChat Shop preserves its order confirmation contract", func(t *testing.T) {
		shopPayment := payment
		shopPayment.Provider = domain.ProviderWeChatShop
		shopPayment.ProviderTransactionDigest = ""
		application := &appStub{payment: shopPayment}
		handler, err := NewHandler(application, nil, securityStub{}, true, true)
		if err != nil {
			t.Fatal(err)
		}
		post := func(confirmation string) *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/refunds", strings.NewReader(`{"provider":"wechat_shop","order_no":"v3pay_order","product_id":"product-1","sku_id":"sku-1","refund_count":1,"refund_amount_total":200,"reason_code":"10000000","reason":"客户申请","transaction_id_confirmation":"`+confirmation+`","checked":true}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "refund-shop-test-key")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			return response
		}
		if response := post(transactionID); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("non-order confirmation status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
		}
		if response := post(""); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("missing order confirmation status=%d calls=%d", response.Code, application.refundCalls)
		}
		if response := post("v3pay_order"); response.Code != http.StatusAccepted || application.refundCalls != 1 || application.refund.PaymentID != shopPayment.ID {
			t.Fatalf("exact shop order status=%d calls=%d command=%+v body=%s", response.Code, application.refundCalls, application.refund, response.Body.String())
		}
	})

	t.Run("non-WeChat generic payment preserves its existing contract", func(t *testing.T) {
		application := &appStub{payment: domain.Payment{ID: 9, Provider: domain.Provider("alipay")}}
		handler, _ := NewHandler(application, nil, securityStub{}, true)
		request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/9/refunds", strings.NewReader(`{"amount_minor":200,"refund_no":"RF-generic","reason":"客户申请"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "refund-generic-test-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted || application.refundCalls != 1 {
			t.Fatalf("non-WeChat status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
		}
	})
}
func TestCheckoutAcceptsOnlyOpaqueCookieIdentity(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	token := "pays_session_token_0000000001"
	binding := paymentport.CheckoutSessionBinding(token)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "checkout-key-0000001")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || application.createCalls != 1 || application.create.ProductID != 3 || application.create.CouponClaimID != 0 || application.create.ProductType != "standard" || application.create.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || application.create.SessionToken == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("code=%d command=%+v body=%s", response.Code, application.create, response.Body.String())
	}
	for _, rawField := range []string{"customer_id", "beneficiary_customer_id", "openid", "unionid", "assurance"} {
		request = httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`","`+rawField+`":"attacker"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("field=%s code=%d", rawField, response.Code)
		}
	}
}

func TestCheckoutRejectsSessionBindingBeforeCallingApplication(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "checkout-key-0000002")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000002"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"session_mismatch"`) || application.createCalls != 0 {
		t.Fatalf("code=%d calls=%d body=%s", response.Code, application.createCalls, response.Body.String())
	}
}

func TestCheckoutSessionBindingIsOpaqueAndRequiresTrustedCookie(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "payment_session_required") {
		t.Fatalf("missing cookie code=%d body=%s", response.Code, response.Body.String())
	}
	token := "pays_session_token_0000000001"
	request = httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), token) || !strings.Contains(response.Body.String(), paymentport.CheckoutSessionBinding(token)) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
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
	if err != nil || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
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

func TestCheckoutStatusExposesFrozenPurchaseActionOnlyAfterAuthorizedPaidCheckout(t *testing.T) {
	application := &appStub{handoff: paymentport.Handoff{PaymentID: 7, OrderID: 31, MerchantOrder: "M-paid-7", Status: domain.StatusPaid}}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	actions := &paidPurchaseActionReaderStub{action: productport.PaidPurchaseAction{OrderPaidEventID: 9, OrderID: 31, ProductID: 4, ProductVersion: 2, Enabled: true, Mode: productport.PaidPurchaseActionRedirect, RedirectURL: "/after-paid", TagState: "not_configured", CreatedAt: time.Now()}}
	if err = handler.SetPaidPurchaseActionReader(actions, paidPurchaseLeadQRStub{}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-paid-7", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusAccepted || actions.order != 31 || !strings.Contains(body, `"completion_action":{"mode":"redirect","redirect_url":"/after-paid","state":"available"}`) || strings.Contains(body, `"tag_state"`) || len(response.Result().Cookies()) != 0 {
		t.Fatalf("code=%d order=%d body=%s cookies=%+v", response.Code, actions.order, body, response.Result().Cookies())
	}
	// A reload is still bound to the same trusted payer session and order. It
	// can recover the immutable action but cannot mint a second checkout.
	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, request)
	if retry.Code != http.StatusAccepted || !strings.Contains(retry.Body.String(), `"completion_action":{"mode":"redirect","redirect_url":"/after-paid","state":"available"}`) || len(retry.Result().Cookies()) != 0 {
		t.Fatalf("reload code=%d body=%s cookies=%+v", retry.Code, retry.Body.String(), retry.Result().Cookies())
	}
}

type commerceOrderReaderStub struct {
	value orderport.CommercePushDeliveryReference
	err   error
}

func (s commerceOrderReaderStub) CommercePushDeliveryReference(_ context.Context, provider orderdomain.Provider, reference string) (orderport.CommercePushDeliveryReference, error) {
	if provider != orderdomain.ProviderWeChatPay || reference != "legacy-order-1" {
		return orderport.CommercePushDeliveryReference{}, orderport.ErrNotFound
	}
	return s.value, s.err
}

type commerceDeliveryReaderStub struct {
	query outboundport.CommercePushDeliveryQuery
	rows  []outboundport.CommercePushDelivery
	err   error
}

func (s *commerceDeliveryReaderStub) ListCommercePushDeliveries(_ context.Context, query outboundport.CommercePushDeliveryQuery) ([]outboundport.CommercePushDelivery, error) {
	s.query = query
	return s.rows, s.err
}

func TestOrderExternalPushDeliveriesUsesOrderAndOutboundPorts(t *testing.T) {
	application := &appStub{}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	attempted, executed := true, true
	reader := &commerceDeliveryReaderStub{rows: []outboundport.CommercePushDelivery{{ID: "current:19", Source: "current", EffectID: "eer_19", State: "outcome_unknown", AttemptCount: 2, ProviderCallAttempted: &attempted, RealExternalCallExecuted: &executed, UpdatedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}}}
	if err = handler.SetCommercePushDeliveryReaders(commerceOrderReaderStub{value: orderport.CommercePushDeliveryReference{OrderID: 7, PaidEventID: 11, HistoricalMappingState: "current"}}, reader); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/legacy-order-1/external-push-deliveries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.query.PaidEventID != 11 || reader.query.HistoricalSourceKey != "" || !strings.Contains(response.Body.String(), `"outcome_unknown"`) || !strings.Contains(response.Body.String(), `"real_external_call_executed":true`) || strings.Contains(response.Body.String(), "payment") {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, reader.query, response.Body.String())
	}
}

func TestOrderExternalPushDeliveriesLeavesUnpaidNativeOrderEmpty(t *testing.T) {
	application := &appStub{}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	reader := &commerceDeliveryReaderStub{rows: []outboundport.CommercePushDelivery{{ID: "must-not-read"}}}
	if err = handler.SetCommercePushDeliveryReaders(commerceOrderReaderStub{value: orderport.CommercePushDeliveryReference{OrderID: 99, HistoricalMappingState: "current"}}, reader); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/legacy-order-1/external-push-deliveries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.query.PaidEventID != 0 || reader.query.HistoricalSourceKey != "" || !strings.Contains(response.Body.String(), `"history_mapping_state":"current"`) || !strings.Contains(response.Body.String(), `"total":0`) || strings.Contains(response.Body.String(), "must-not-read") {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, reader.query, response.Body.String())
	}
}

func TestOrderExternalPushDeliveriesLeavesUnmappedHistoryPending(t *testing.T) {
	application := &appStub{}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	reader := &commerceDeliveryReaderStub{rows: []outboundport.CommercePushDelivery{{ID: "must-not-read"}}}
	if err = handler.SetCommercePushDeliveryReaders(commerceOrderReaderStub{value: orderport.CommercePushDeliveryReference{OrderID: 99, HistoricalMappingState: "pending"}}, reader); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/legacy-order-1/external-push-deliveries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.query.PaidEventID != 0 || reader.query.HistoricalSourceKey != "" || !strings.Contains(response.Body.String(), `"history_mapping_state":"pending"`) || strings.Contains(response.Body.String(), "must-not-read") {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, reader.query, response.Body.String())
	}
}

func TestCheckoutStatusReportsUnknownWithoutHandoffOrClearingSession(t *testing.T) {
	application := &appStub{handoff: paymentport.Handoff{PaymentID: 7, MerchantOrder: "M-7", Status: domain.StatusAwaitingPrepay, PrepayState: "outcome_unknown"}}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-7", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusAccepted || !strings.Contains(body, `"prepay_state":"outcome_unknown"`) || !strings.Contains(body, `"ready":false`) || strings.Contains(body, `"handoff"`) || len(response.Result().Cookies()) != 0 {
		t.Fatalf("unexpected checkout: code=%d body=%s", response.Code, body)
	}
}

func TestOAuthIdentityConflictExplainsReviewWithoutRedirect(t *testing.T) {
	handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
	_ = handler.SetH5OAuth(&h5OAuthStub{enabled: true, completeError: paymenth5oauth.ErrIdentityConflict})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=opaque&code=opaque", nil))
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "历史账号资料需要核对") || response.Header().Get("Location") != "" || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected OAuth response: %d", response.Code)
	}
}
