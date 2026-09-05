package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type commerceFundsSecurity struct{}

func (commerceFundsSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (commerceFundsSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return commerceFundsSecurity{}.Authenticate(context.Background(), nil)
}

type commerceFundsSessionVerifier struct{ fact identitydomain.VerifiedFact }

func (v commerceFundsSessionVerifier) VerifyCode(_ context.Context, code string) (identitydomain.VerifiedFact, error) {
	if code != "provider-verified-session-code" {
		return identitydomain.VerifiedFact{}, errors.New("unverified session code")
	}
	return v.fact, nil
}

type commerceFundsFailingEntitlement struct{ err error }

func (f commerceFundsFailingEntitlement) GrantPaidServicePeriodWithin(context.Context, orderport.ServicePeriodGrantCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, f.err
}
func (f commerceFundsFailingEntitlement) ApplyServicePeriodRefundWithin(context.Context, orderport.ServicePeriodRefundCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, f.err
}

// TestPostgreSQLCommerceFundsHTTPJourney validates the actual composition-root
// journey: provider-verified public session, self selection, coupon reserve,
// signed payment settlement, service-period grant, partial refund and a later
// refund. A forced fulfillment failure proves Payment, Order, Coupon and the
// entitlement facts roll back in one UoW; concurrent callback/refund requests
// then prove the successful lifecycle cannot duplicate those facts.
func TestPostgreSQLCommerceFundsHTTPJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	coupons, err := couponstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	couponCheckout, err := couponapp.NewCheckoutService(uow, coupons)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	if err = orderService.SetCheckoutCouponCoordinator(couponCheckout); err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := effects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}

	var customerID int64
	if err = pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 901, customerID: customerdomain.CustomerID(customerID)}, paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	product := productport.CheckoutProduct{ID: 17, ProductType: productport.ProductOptionServicePeriod, Code: "period-17", Name: "三十天服务期", PriceMinor: 1200, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 30}
	paymentService := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orderService, sessions, effectStore, effectStore)
	if err = paymentService.SetCheckoutProductReader(checkoutRecoveryProductReader{product: product}); err != nil {
		t.Fatal(err)
	}
	if err = paymentService.SetPaymentChannelAppIDs("app", ""); err != nil {
		t.Fatal(err)
	}

	apiKey := []byte("0123456789abcdef0123456789abcdef")
	platformKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := paymentprovider.NewCallbackVerifier(map[string]*rsa.PublicKey{"local-platform": &platformKey.PublicKey}, apiKey, "app", "mch")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(paymentService, verifier, commerceFundsSecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:local-app", Value: "verified-local-openid", Source: "local-provider"})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetTrustedSessionIssuer(commerceFundsSessionVerifier{fact: fact}, sessions); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	days := int32(7)
	rules := couponapp.NewService(uow, coupons, commerceCheckoutProductFacts{17: {ID: 17, ProductType: productport.ProductOptionServicePeriod, Currency: "CNY", PriceMinor: product.PriceMinor}}, coupons)
	rule, err := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "资金联合券", DiscountAmountTotal: 200, TotalIssueLimit: 1, PerUserIssueLimit: 1, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"service_period:17"}}, Actor: 1, IdempotencyKey: "commerce-funds-rule-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rules.Publish(ctx, rule.ID, 1, "commerce-funds-rule-publish-0001"); err != nil {
		t.Fatal(err)
	}
	claim, err := couponCheckout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: customerID, ActorScope: "commerce-funds-payer", IdempotencyKey: "commerce-funds-claim-0001", ClaimedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	issue := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/sessions", bytes.NewReader(commerceFundsJSON(t, map[string]string{"code": "provider-verified-session-code"})))
	issue.Header.Set("Content-Type", "application/json")
	issued := httptest.NewRecorder()
	handler.ServeHTTP(issued, issue)
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	sessionCookie := commerceFundsCookie(t, issued.Result().Cookies(), paymentport.TrustedSessionCookieName)
	if !sessionCookie.HttpOnly || sessionCookie.Value == "" {
		t.Fatalf("unsafe session cookie=%+v", sessionCookie)
	}
	bindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	bindingRequest.AddCookie(sessionCookie)
	bindingResponse := httptest.NewRecorder()
	handler.ServeHTTP(bindingResponse, bindingRequest)
	binding := commerceFundsObject(t, bindingResponse, http.StatusOK)["checkout_session_binding"].(string)
	if binding == "" || binding == sessionCookie.Value {
		t.Fatalf("binding is not opaque=%q", binding)
	}

	checkoutPayload := map[string]any{"product_id": 17, "product_kind": "service_period", "coupon_claim_id": claim.ClaimID, "beneficiary_selection": "payer_self", "checkout_session_binding": binding}
	checkoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", bytes.NewReader(commerceFundsJSON(t, checkoutPayload)))
	checkoutRequest.Header.Set("Content-Type", "application/json")
	checkoutRequest.Header.Set("Idempotency-Key", "commerce-funds-checkout-0001")
	checkoutRequest.AddCookie(sessionCookie)
	checkoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(checkoutResponse, checkoutRequest)
	checkout := commerceFundsObject(t, checkoutResponse, http.StatusAccepted)
	orderID := commerceFundsInt(t, checkout, "order_id")
	paymentID := commerceFundsInt(t, checkout, "payment_id")
	merchant := commerceFundsString(t, checkout, "merchant_order_no")
	commerceFundsAssertReserved(t, ctx, pool, orderID, paymentID, claim.ClaimID)

	unknownBody, unknownHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-unknown", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": "v3pay_unknown_funds", "transaction_id": "tx-unknown", "trade_state": "SUCCESS", "success_time": now.Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", unknownBody, unknownHeaders))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("out-of-order status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	paymentBody, paymentHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-payment", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": merchant, "transaction_id": "tx-commerce-funds", "trade_state": "SUCCESS", "success_time": now.Add(time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	badHeaders := paymentHeaders.Clone()
	badHeaders.Set("Wechatpay-Signature", "bad")
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, badHeaders))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status=%d body=%s", bad.Code, bad.Body.String())
	}
	commerceFundsAssertRollback(t, ctx, pool, orderID, paymentID, merchant, 0)

	if err = orderService.SetServicePeriodEntitlementCoordinator(commerceFundsFailingEntitlement{err: errors.New("forced entitlement failure")}); err != nil {
		t.Fatal(err)
	}
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("forced settlement status=%d body=%s", failed.Code, failed.Body.String())
	}
	commerceFundsAssertRollback(t, ctx, pool, orderID, paymentID, merchant, 0)

	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(orders)
	if err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetServicePeriodEntitlementCoordinator(fulfillment); err != nil {
		t.Fatal(err)
	}
	callbacks := make(chan int, 2)
	var callbackWait sync.WaitGroup
	for range 2 {
		callbackWait.Add(1)
		go func() {
			defer callbackWait.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
			callbacks <- response.Code
		}()
	}
	callbackWait.Wait()
	close(callbacks)
	for code := range callbacks {
		if code != http.StatusOK {
			t.Fatalf("concurrent payment callback status=%d", code)
		}
	}
	commerceFundsAssertPaid(t, ctx, pool, orderID, paymentID, merchant)

	firstRefund := commerceFundsRequestRefund(t, handler, paymentID, 300, "commerce-funds-first-refund", "commerce-funds-first-refund-key")
	firstRefundBody, firstRefundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-refund-1", "REFUND.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_refund_no": firstRefund, "refund_id": "provider-refund-1", "refund_status": "SUCCESS", "success_time": now.Add(2 * time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"refund": 300, "total": 1000, "currency": "CNY"}})
	firstRefundResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstRefundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", firstRefundBody, firstRefundHeaders))
	if firstRefundResponse.Code != http.StatusOK {
		t.Fatalf("partial refund status=%d body=%s", firstRefundResponse.Code, firstRefundResponse.Body.String())
	}
	replayedRefund := httptest.NewRecorder()
	handler.ServeHTTP(replayedRefund, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", firstRefundBody, firstRefundHeaders))
	if replayedRefund.Code != http.StatusOK {
		t.Fatalf("duplicate partial refund status=%d body=%s", replayedRefund.Code, replayedRefund.Body.String())
	}
	firstEnd, firstUpdated := commerceFundsRefundedEntitlement(t, ctx, pool, orderID, 300)

	type refundAttempt struct {
		code     int
		refundNo string
	}
	attempts := make(chan refundAttempt, 2)
	var refundWait sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		refundWait.Add(1)
		go func() {
			defer refundWait.Done()
			result := commerceFundsRefundRequest(handler, paymentID, 700, "commerce-funds-final-refund-"+strconv.Itoa(index), "commerce-funds-final-refund-key-"+strconv.Itoa(index))
			attempts <- refundAttempt{code: result.code, refundNo: result.refundNo}
		}()
	}
	refundWait.Wait()
	close(attempts)
	var finalRefund string
	var accepted, conflicted int
	for attempt := range attempts {
		if attempt.code == http.StatusAccepted {
			accepted++
			finalRefund = attempt.refundNo
		} else if attempt.code == http.StatusConflict {
			conflicted++
		} else {
			t.Fatalf("concurrent refund status=%d", attempt.code)
		}
	}
	if accepted != 1 || conflicted != 1 || finalRefund == "" {
		t.Fatalf("concurrent refunds accepted=%d conflicted=%d refund=%q", accepted, conflicted, finalRefund)
	}
	finalRefundBody, finalRefundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-refund-2", "REFUND.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_refund_no": finalRefund, "refund_id": "provider-refund-2", "refund_status": "SUCCESS", "success_time": now.Add(3 * time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"refund": 700, "total": 1000, "currency": "CNY"}})
	finalRefundResponse := httptest.NewRecorder()
	handler.ServeHTTP(finalRefundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", finalRefundBody, finalRefundHeaders))
	if finalRefundResponse.Code != http.StatusOK {
		t.Fatalf("final refund status=%d body=%s", finalRefundResponse.Code, finalRefundResponse.Body.String())
	}
	duplicateFinal := httptest.NewRecorder()
	handler.ServeHTTP(duplicateFinal, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", finalRefundBody, finalRefundHeaders))
	if duplicateFinal.Code != http.StatusOK {
		t.Fatalf("duplicate final refund status=%d body=%s", duplicateFinal.Code, duplicateFinal.Body.String())
	}
	commerceFundsAssertFinal(t, ctx, pool, orderID, paymentID, merchant, firstEnd, firstUpdated)
}

func commerceFundsJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func commerceFundsObject(t *testing.T, response *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), want)
	}
	var object map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	return object
}
func commerceFundsInt(t *testing.T, value map[string]any, key string) int64 {
	t.Helper()
	number, ok := value[key].(float64)
	if !ok || number < 1 || number != float64(int64(number)) {
		t.Fatalf("invalid %s=%#v", key, value[key])
	}
	return int64(number)
}
func commerceFundsString(t *testing.T, value map[string]any, key string) string {
	t.Helper()
	text, ok := value[key].(string)
	if !ok || text == "" {
		t.Fatalf("invalid %s=%#v", key, value[key])
	}
	return text
}
func commerceFundsCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}

func commerceFundsRequestRefund(t *testing.T, handler http.Handler, paymentID, amount int64, refundNo, key string) string {
	t.Helper()
	result := commerceFundsRefundRequest(handler, paymentID, amount, refundNo, key)
	if result.code != http.StatusAccepted || result.refundNo != refundNo {
		t.Fatalf("refund status=%d refund=%q body=%s", result.code, result.refundNo, result.body)
	}
	return result.refundNo
}
func commerceFundsRefundRequest(handler http.Handler, paymentID, amount int64, refundNo, key string) struct {
	code     int
	refundNo string
	body     string
} {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/"+strconv.FormatInt(paymentID, 10)+"/refunds", bytes.NewReader(commerceFundsJSONNoTest(map[string]any{"amount_minor": amount, "refund_no": refundNo, "reason": "用户申请退款"})))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var object map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &object)
	got, _ := object["out_refund_no"].(string)
	return struct {
		code     int
		refundNo string
		body     string
	}{code: response.Code, refundNo: got, body: response.Body.String()}
}
func commerceFundsJSONNoTest(value any) []byte {
	body, _ := json.Marshal(value)
	return body
}

func commerceFundsAssertReserved(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID, claimID int64) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus string
	var payable int64
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE claim_id=$3),(SELECT status FROM coupon_customer_claims WHERE id=$3),(SELECT payable_amount_minor FROM order_checkout_snapshots WHERE order_id=$1)", orderID, paymentID, claimID).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &payable)
	if err != nil || orderStatus != "pending_payment" || paymentStatus != "awaiting_prepay" || redemptionStatus != "reserved" || claimStatus != "reserved" || payable != 1000 {
		t.Fatalf("reserved order=%q payment=%q redemption=%q claim=%q payable=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, payable, err)
	}
}
func commerceFundsAssertRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, callbacks int) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus string
	var callbackCount, entitlementCount, consumeCount int
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM coupon_customer_claims WHERE id=(SELECT claim_id FROM coupon_order_redemptions WHERE order_reference=$3)),(SELECT count(*) FROM payment_callback_receipts),(SELECT count(*) FROM order_service_entitlements),(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$3 AND receipt.operation='consume')", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &callbackCount, &entitlementCount, &consumeCount)
	if err != nil || orderStatus != "pending_payment" || paymentStatus != "awaiting_prepay" || redemptionStatus != "reserved" || claimStatus != "reserved" || callbackCount != callbacks || entitlementCount != 0 || consumeCount != 0 {
		t.Fatalf("rollback order=%q payment=%q redemption=%q claim=%q callbacks=%d entitlements=%d consumes=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, callbackCount, entitlementCount, consumeCount, err)
	}
}
func commerceFundsAssertPaid(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus, entitlementStatus string
	var callbackCount, grantCount, consumeCount int
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM coupon_customer_claims WHERE id=(SELECT claim_id FROM coupon_order_redemptions WHERE order_reference=$3)),(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM payment_callback_receipts),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$1),(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$3 AND receipt.operation='consume')", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &entitlementStatus, &callbackCount, &grantCount, &consumeCount)
	if err != nil || orderStatus != "paid" || paymentStatus != "paid" || redemptionStatus != "consumed" || claimStatus != "consumed" || entitlementStatus != "active" || callbackCount != 1 || grantCount != 1 || consumeCount != 1 {
		t.Fatalf("paid order=%q payment=%q redemption=%q claim=%q entitlement=%q callbacks=%d grants=%d consumes=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, entitlementStatus, callbackCount, grantCount, consumeCount, err)
	}
}
func commerceFundsRefundedEntitlement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, amount int64) (time.Time, time.Time) {
	t.Helper()
	var status string
	var endAt, updatedAt time.Time
	var firstAmount int64
	err := pool.QueryRow(ctx, "SELECT entitlement.status,entitlement.end_at,entitlement.updated_at,receipt.refund_amount_minor FROM order_service_entitlements entitlement JOIN order_entitlement_fulfillment_receipts receipt ON receipt.operation='refund' AND receipt.source_order_id=$1 WHERE entitlement.last_order_id=$1", orderID).Scan(&status, &endAt, &updatedAt, &firstAmount)
	if err != nil || status != "refunded" || firstAmount != amount {
		t.Fatalf("partial refund entitlement status=%q amount=%d end=%s updated=%s err=%v", status, firstAmount, endAt, updatedAt, err)
	}
	return endAt, updatedAt
}
func commerceFundsAssertFinal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, firstEnd, firstUpdated time.Time) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, entitlementStatus string
	var completedRefunds, entitlementReceipts, callbackReceipts int
	var endAt, updatedAt time.Time
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM payment_refunds WHERE payment_id=$2 AND status='completed'),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='refund' AND source_order_id=$1),(SELECT count(*) FROM payment_callback_receipts),(SELECT end_at FROM order_service_entitlements WHERE last_order_id=$1),(SELECT updated_at FROM order_service_entitlements WHERE last_order_id=$1)", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &entitlementStatus, &completedRefunds, &entitlementReceipts, &callbackReceipts, &endAt, &updatedAt)
	if err != nil || orderStatus != "refunded" || paymentStatus != "paid" || redemptionStatus != "consumed" || entitlementStatus != "refunded" || completedRefunds != 2 || entitlementReceipts != 1 || callbackReceipts != 3 || !endAt.Equal(firstEnd) || !updatedAt.Equal(firstUpdated) {
		t.Fatalf("final order=%q payment=%q redemption=%q entitlement=%q refunds=%d receipts=%d callbacks=%d end=%s updated=%s err=%v", orderStatus, paymentStatus, redemptionStatus, entitlementStatus, completedRefunds, entitlementReceipts, callbackReceipts, endAt, updatedAt, err)
	}
}

func commerceFundsSignedCallback(t *testing.T, platformKey *rsa.PrivateKey, apiKey []byte, eventID, eventType string, payload map[string]any) ([]byte, http.Header) {
	t.Helper()
	plain := commerceFundsJSON(t, payload)
	block, err := aes.NewCipher(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("nonce:" + eventID))
	resourceNonce := hex.EncodeToString(digest[:6])
	associated := "transaction"
	ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(resourceNonce), plain, []byte(associated)))
	body := commerceFundsJSON(t, map[string]any{"id": eventID, "event_type": eventType, "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ciphertext, "nonce": resourceNonce, "associated_data": associated}})
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	headerNonce := "local-" + hex.EncodeToString(digest[6:12])
	signature := commerceFundsSign(t, platformKey, timestamp+"\n"+headerNonce+"\n"+string(body)+"\n")
	return body, http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {headerNonce}, "Wechatpay-Serial": {"local-platform"}, "Wechatpay-Signature": {signature}}
}
func commerceFundsSign(t *testing.T, key *rsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(cryptorand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}
func commerceFundsCallbackRequest(path string, body []byte, headers http.Header) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for key, values := range headers {
		request.Header[key] = append([]string(nil), values...)
	}
	return request
}
