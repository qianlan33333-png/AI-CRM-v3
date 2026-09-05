package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type checkoutRecoveryProvisioner struct {
	identityID int64
	customerID customerdomain.CustomerID
}

func (p checkoutRecoveryProvisioner) ProvisionVerifiedIdentity(_ context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	if !command.Fact.Valid() || len(command.IdempotencyKey) < 16 {
		return identityport.ProvisionResult{}, errors.New("unverified payment session")
	}
	return identityport.ProvisionResult{IdentityID: p.identityID, CustomerID: p.customerID}, nil
}

type checkoutRecoveryProductReader struct{ product productport.CheckoutProduct }

func (reader checkoutRecoveryProductReader) ReadCheckoutProductWithin(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil || kind != reader.product.ProductType || id != reader.product.ID {
		return productport.CheckoutProduct{}, errors.New("checkout product unavailable")
	}
	return reader.product, nil
}

// TestPostgreSQLPublicCheckoutResponseLossRejectsRenewedSessionReplay uses the
// actual Payment application, Payment/Order stores, Payment sessions, durable
// External Effects acceptance and Payment HTTP handler. It proves that a
// committed response-lost checkout remains one merchant order when the same
// customer completes OAuth again: the old checkpoint binding is rejected
// before Create, and even a forged current binding cannot bypass the existing
// Payment receipt's full session-bound payload.
func TestPostgreSQLPublicCheckoutResponseLossRejectsRenewedSessionReplay(t *testing.T) {
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
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 71, customerID: customerdomain.CustomerID(customerID)}, paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	productReader := checkoutRecoveryProductReader{product: productport.CheckoutProduct{ID: 7, ProductType: productport.ProductOptionStandard, Code: "course-7", Name: "恢复测试商品", PriceMinor: 990, Currency: "CNY", Version: 3}}
	paymentService := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orderapp.NewService(uow, orders), sessions, effectStore, effectStore)
	if err = paymentService.SetCheckoutProductReader(productReader); err != nil {
		t.Fatal(err)
	}
	paymentHandler, err := paymenthttp.NewHandler(paymentService, nil, checkoutJourneySecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	loss := &checkoutJourneyResponseLoss{next: paymentHandler}

	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-h5", Value: "opaque-openid", Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := sessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact, IdempotencyKey: "checkout-recovery-oauth-first-0001"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := sessions.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact, IdempotencyKey: "checkout-recovery-oauth-second-001"})
	if err != nil {
		t.Fatal(err)
	}
	key := "checkout-recovery-response-loss-0001"
	firstBinding := paymentport.CheckoutSessionBinding(first.Token)
	firstRequest := checkoutRecoveryRequest(t, first.Token, key, firstBinding)
	firstResponse := httptest.NewRecorder()
	loss.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusServiceUnavailable || !strings.Contains(firstResponse.Body.String(), "response_lost") {
		t.Fatalf("lost create status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// The original session and marker replay exactly one existing merchant
	// order after the dropped response.
	replayed := httptest.NewRecorder()
	loss.ServeHTTP(replayed, checkoutRecoveryRequest(t, first.Token, key, firstBinding))
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("exact replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
	var replayBody struct {
		MerchantOrderNo string `json:"merchant_order_no"`
	}
	if err = json.Unmarshal(replayed.Body.Bytes(), &replayBody); err != nil || replayBody.MerchantOrderNo == "" {
		t.Fatalf("replayed response=%s err=%v", replayed.Body.String(), err)
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// A different trusted session for the same authoritative customer sees an
	// opaque different marker. Sending the stored old one stops in the HTTP
	// boundary before payment Create can mutate anything.
	secondBindingResponse := httptest.NewRecorder()
	secondBindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	secondBindingRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: second.Token})
	paymentHandler.ServeHTTP(secondBindingResponse, secondBindingRequest)
	if secondBindingResponse.Code != http.StatusOK || strings.Contains(secondBindingResponse.Body.String(), second.Token) {
		t.Fatalf("renewed session binding status=%d body=%s", secondBindingResponse.Code, secondBindingResponse.Body.String())
	}
	var bindingBody struct {
		Binding string `json:"checkout_session_binding"`
	}
	if err = json.Unmarshal(secondBindingResponse.Body.Bytes(), &bindingBody); err != nil || bindingBody.Binding == "" || bindingBody.Binding == firstBinding {
		t.Fatalf("renewed binding=%q err=%v", bindingBody.Binding, err)
	}
	oldMarker := httptest.NewRecorder()
	paymentHandler.ServeHTTP(oldMarker, checkoutRecoveryRequest(t, second.Token, key, firstBinding))
	if oldMarker.Code != http.StatusConflict || !strings.Contains(oldMarker.Body.String(), "session_mismatch") {
		t.Fatalf("old session marker status=%d body=%s", oldMarker.Code, oldMarker.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)

	// Replacing the local marker with the new opaque value still cannot create a
	// second payment: Payment's persisted receipt includes the original trusted
	// session-bound command payload and returns a factual conflict.
	forgedMarker := httptest.NewRecorder()
	paymentHandler.ServeHTTP(forgedMarker, checkoutRecoveryRequest(t, second.Token, key, bindingBody.Binding))
	if forgedMarker.Code != http.StatusConflict || !strings.Contains(forgedMarker.Body.String(), `"conflict"`) {
		t.Fatalf("forged current marker status=%d body=%s", forgedMarker.Code, forgedMarker.Body.String())
	}
	assertCheckoutRecoveryCounts(t, ctx, pool, 1)
}

func checkoutRecoveryRequest(t *testing.T, token, key, binding string) *http.Request {
	t.Helper()
	body := `{"product_id":7,"product_kind":"standard","beneficiary_selection":"payer_self","coupon_claim_id":0,"checkout_session_binding":"` + binding + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	return request
}

func assertCheckoutRecoveryCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, expected int) {
	t.Helper()
	var orders, payments, receipts, effects int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM payments),(SELECT count(*) FROM payment_operation_receipts WHERE operation='create'),(SELECT count(*) FROM external_effects WHERE owner='payment')`).Scan(&orders, &payments, &receipts, &effects); err != nil || orders != expected || payments != expected || receipts != expected || effects != expected {
		t.Fatalf("orders=%d payments=%d receipts=%d effects=%d expected=%d err=%v", orders, payments, receipts, effects, expected, err)
	}
}
