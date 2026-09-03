package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"testing"
	"time"
)

type txKey struct{}
type uowStub struct{}

func (uowStub) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(context.WithValue(ctx, txKey{}, true))
}

type orderStub struct{ snapshot orderdomain.Snapshot }

func (o orderStub) ReservePaymentWithin(ctx context.Context, _ int64) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	return o.snapshot, nil
}

type checkoutOrderStub struct {
	command orderport.PaymentOrderCommand
}

func (*checkoutOrderStub) ReservePaymentWithin(context.Context, int64) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, errors.New("unexpected existing order")
}
func (stub *checkoutOrderStub) CreatePaymentOrderWithin(ctx context.Context, command orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	stub.command = command
	order := nativeOrder()
	order.MerchantOrderNo = command.MerchantOrderNo
	order.Amount = orderdomain.Money{AmountMinor: command.UnitAmountMinor, Currency: command.Currency}
	return order, nil
}
func (*checkoutOrderStub) SettlePaymentWithin(context.Context, orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (o orderStub) CreatePaymentOrderWithin(ctx context.Context, _ orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	return o.snapshot, nil
}
func (o orderStub) SettlePaymentWithin(ctx context.Context, command orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true || command.OrderID < 1 {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	return o.snapshot, nil
}

type recordingOrderStub struct {
	orderStub
	settlement *orderport.PaymentSettlementCommand
}

func (stub *recordingOrderStub) SettlePaymentWithin(ctx context.Context, command orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	if ctx.Value(txKey{}) != true || command.OrderID < 1 {
		return orderdomain.Snapshot{}, errors.New("not in tx")
	}
	stub.settlement = &command
	return stub.snapshot, nil
}

type sessionStub struct{ actor paymentport.SessionActor }

func (stub sessionStub) ConsumeWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}
func (stub sessionStub) LookupWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}

type oneShotSessionStub struct {
	actor    paymentport.SessionActor
	consumed bool
}

func (stub *oneShotSessionStub) ConsumeWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" || stub.consumed {
		return paymentport.SessionActor{}, errors.New("session already consumed")
	}
	stub.consumed = true
	return stub.actor, nil
}
func (stub *oneShotSessionStub) LookupWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}

type effectStub struct {
	fail   bool
	within bool
}

func (e *effectStub) Get(_ context.Context, id string) (effectport.Projection, error) {
	return effectport.Projection{ID: id, Owner: effectport.OwnerPayment, Kind: effectport.KindWeChatPayRefund, State: effectport.StateQueued, AttemptCount: 1, UpdatedAt: time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)}, nil
}

func (e *effectStub) AcceptAndQueueWithin(ctx context.Context, c effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	e.within = ctx.Value(txKey{}) == true
	if e.fail {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("accept failed")
	}
	if !c.Valid() {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("bad")
	}
	return effectport.Projection{ID: "eer_8", State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_1"}, nil
}

type storeStub struct {
	payment                 domain.Payment
	refund                  domain.Refund
	shopMaterial            paymentport.ShopRefundMaterial
	bound                   bool
	reserved                int64
	refundReconciliationID  int64
	paymentReconciliationID int64
	callbackOutcome         string
}

func (s *storeStub) CreatePayment(_ context.Context, p domain.Payment, _, _ [32]byte, _ string) (domain.Payment, bool, error) {
	if s.payment.ID > 0 {
		return s.payment, false, nil
	}
	p.ID = 7
	s.payment = p
	return p, true, nil
}
func (s *storeStub) ReplayPayment(context.Context, [32]byte, [32]byte, string) (domain.Payment, bool, error) {
	return s.payment, s.payment.ID > 0, nil
}
func (s *storeStub) BindPaymentEffect(_ context.Context, p domain.Payment, _ effectport.PaymentV1Intent, _ map[string]any) (domain.Payment, error) {
	s.payment = p
	s.bound = true
	return p, nil
}
func (s *storeStub) GetPayment(context.Context, int64, bool) (domain.Payment, error) {
	if s.payment.ID < 1 {
		return domain.Payment{}, paymentport.ErrNotFound
	}
	return s.payment, nil
}
func (s *storeStub) GetHandoff(context.Context, int64) (paymentport.Handoff, error) {
	return paymentport.Handoff{PaymentID: s.payment.ID, Payload: []byte(`{"appId":"app"}`), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (s *storeStub) ReservedRefundMinor(context.Context, int64) (int64, error) {
	return s.reserved, nil
}
func (s *storeStub) CreateRefund(_ context.Context, r domain.Refund, _, _ [32]byte, _ string) (domain.Refund, bool, error) {
	r.ID = 9
	s.refund = r
	return r, true, nil
}
func (s *storeStub) ReplayRefund(context.Context, [32]byte, [32]byte, string) (domain.Refund, bool, error) {
	return s.refund, s.refund.ID > 0, nil
}
func (s *storeStub) BindRefundEffect(_ context.Context, r domain.Refund, _ effectport.PaymentV1Intent, _ map[string]any) (domain.Refund, error) {
	s.refund = r
	return r, nil
}
func (s *storeStub) GetRefund(context.Context, int64, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) GetRefundByProviderReference(context.Context, domain.Provider, string, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) GetShopRefundMaterial(context.Context, int64) (paymentport.ShopRefundMaterial, error) {
	return s.shopMaterial, nil
}
func (s *storeStub) RecordReconciliation(_ context.Context, id int64, _ effectport.Digest, _ string, _ time.Time) (bool, error) {
	s.refundReconciliationID = id
	return true, nil
}
func (s *storeStub) RecordPaymentReconciliation(_ context.Context, id int64, _ effectport.Digest, _ string, _ time.Time) (bool, error) {
	s.paymentReconciliationID = id
	return true, nil
}
func (s *storeStub) UpdatePaymentSettlement(_ context.Context, p domain.Payment, _, _ string) (domain.Payment, error) {
	s.payment = p
	return p, nil
}
func (s *storeStub) UpdateRefundSettlement(_ context.Context, r domain.Refund, _, _ string) (domain.Refund, error) {
	s.refund = r
	return r, nil
}
func (s *storeStub) GetPaymentByMerchant(context.Context, string, bool) (domain.Payment, error) {
	return s.payment, nil
}
func (s *storeStub) GetPaymentByMerchantProvider(context.Context, domain.Provider, string, bool) (domain.Payment, error) {
	return s.payment, nil
}
func (s *storeStub) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}
func (s *storeStub) ListEffectBindings(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error) {
	return []paymentport.EffectProjection{{EffectID: "eer_8", Kind: effectport.KindWeChatPayRefund}}, nil
}
func (s *storeStub) GetRefundByNumber(context.Context, string, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) ClaimCallback(_ context.Context, _ string, _ [32]byte, _ [32]byte, _, outcome string, _ int64) (bool, error) {
	s.callbackOutcome = outcome
	return false, nil
}
func (s *storeStub) ImportTerminalPayment(_ context.Context, payment domain.Payment, _ [32]byte, _ string) (domain.Payment, error) {
	payment.ID = 10
	return payment, nil
}
func (s *storeStub) ImportTerminalRefund(_ context.Context, refund domain.Refund, _ [32]byte, _ string) (domain.Refund, error) {
	refund.ID = 11
	return refund, nil
}
func nativeOrder() orderdomain.Snapshot {
	payer, beneficiary := int64(11), int64(22)
	return orderdomain.Snapshot{ID: 3, Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-3", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 1000, Currency: "CNY"}, Status: orderdomain.StatusPendingPayment, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true}
}
func TestCreateAcceptsPaymentEffectInSameUOWAndReplays(t *testing.T) {
	store := &storeStub{}
	effects := &effectStub{}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22}}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessions, effects)
	service.now = func() time.Time { return time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC) }
	command := paymentport.CreateCommand{OrderID: 3, SessionToken: "pays_session_token_0000000001", ActorScope: "customer:11", IdempotencyKey: "payment-create-key-0001"}
	first, err := service.Create(context.Background(), command)
	if err != nil || first.EffectID != "eer_8" || !store.bound || !effects.within {
		t.Fatalf("payment=%+v store=%+v effects=%+v err=%v", first, store, effects, err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != first.ID || replay.EffectID != first.EffectID || !sessions.consumed {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}
func TestCreateFailsClosedWhenAtomicEffectAcceptanceFails(t *testing.T) {
	store := &storeStub{}
	effects := &effectStub{fail: true}
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessionStub{paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22}}, effects)
	_, err := service.Create(context.Background(), paymentport.CreateCommand{OrderID: 3, SessionToken: "pays_session_token_0000000002", ActorScope: "customer:11", IdempotencyKey: "payment-create-key-0002"})
	if !errors.Is(err, paymentport.ErrUnavailable) || store.bound {
		t.Fatalf("bound=%v err=%v", store.bound, err)
	}
}

func TestRefundReservesOutstandingAmountsAndRejectsOverRefund(t *testing.T) {
	store := &storeStub{payment: domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Minute)}, reserved: 800}
	effects := &effectStub{}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, effects, effects)
	_, err := service.RequestRefund(context.Background(), paymentport.RefundCommand{PaymentID: 7, AmountMinor: 201, RefundNo: "R-7", Reason: "customer request", ActorScope: "admin:1", IdempotencyKey: "refund-key-000000001"})
	if !errors.Is(err, paymentport.ErrConflict) || store.refund.ID != 0 {
		t.Fatalf("refund=%+v err=%v", store.refund, err)
	}
}

func TestRefundReplayReturnsExistingBeforeReservedAmountCheck(t *testing.T) {
	now := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		payment:  domain.Payment{ID: 7, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		refund:   domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatPay, RefundNo: "R-7", Reason: "customer request", AmountMinor: 1000, Status: domain.RefundEffectAccepted, EffectID: "eer_9", Version: 2, CreatedAt: now, UpdatedAt: now},
		reserved: 1000,
	}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	replay, err := service.RequestRefund(context.Background(), paymentport.RefundCommand{PaymentID: 7, AmountMinor: 1000, RefundNo: "R-7", Reason: "customer request", ActorScope: "admin:1", IdempotencyKey: "refund-key-000000001"})
	if err != nil || replay.ID != 9 {
		t.Fatalf("refund=%+v err=%v", replay, err)
	}
}

func TestCheckoutFromProductCreatesOrderAndPaymentInSameUOW(t *testing.T) {
	store := &storeStub{}
	orders := &checkoutOrderStub{}
	products := &checkoutProductStub{product: productport.CheckoutProduct{ID: 5, ProductType: productport.ProductOptionStandard, Code: "course-5", Name: "Course 5", PriceMinor: 8800, Currency: "CNY", Version: 3}}
	sessions := &oneShotSessionStub{actor: paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22}}
	service := NewService(uowStub{}, store, orders, sessions, &effectStub{})
	if err := service.SetCheckoutProductReader(products); err != nil {
		t.Fatal(err)
	}
	command := paymentport.CreateCommand{ProductID: 5, ProductType: "standard", SessionToken: "pays_session_token_0000000005", ActorScope: "public-checkout", IdempotencyKey: "checkout-product-key-0005"}
	first, err := service.Create(context.Background(), command)
	if err != nil || first.ID != 7 || first.AmountMinor != 8800 || products.calls != 1 || orders.command.ProductID != 5 || orders.command.ProductVersion != 3 || orders.command.BeneficiaryCustomerID != 22 || orders.command.MerchantOrderNo == "" || !sessions.consumed {
		t.Fatalf("payment=%+v product_calls=%d order=%+v consumed=%v err=%v", first, products.calls, orders.command, sessions.consumed, err)
	}
	store.payment.MerchantOrderNo = orders.command.MerchantOrderNo
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != first.ID || products.calls != 1 {
		t.Fatalf("replay=%+v product_calls=%d err=%v", replay, products.calls, err)
	}
}

func TestListOrderEffectsJoinsOnlyPaymentOwnedProjection(t *testing.T) {
	store := &storeStub{}
	effects := &effectStub{}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, effects, effects)
	items, err := service.ListOrderEffects(context.Background(), domain.ProviderWeChatPay, "M-7")
	if err != nil || len(items) != 1 || items[0].State != effectport.StateQueued || items[0].AttemptCount != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

type payReconcilerStub struct {
	payment paymentport.WeChatPayPaymentQuery
	refund  paymentport.WeChatPayRefundQuery
}

func (s payReconcilerStub) QueryPayment(context.Context, string) (paymentport.WeChatPayPaymentQuery, error) {
	return s.payment, nil
}
func (s payReconcilerStub) QueryRefund(context.Context, string) (paymentport.WeChatPayRefundQuery, error) {
	return s.refund, nil
}

type shopReconcilerStub struct{ query paymentport.ShopRefundQuery }

func (shopReconcilerStub) ValidateRefundMaterial(context.Context, paymentport.ShopRefundMaterial) error {
	return nil
}

type reconciliationEnqueuerStub struct{ refundID int64 }

func (s *reconciliationEnqueuerStub) EnqueueWithin(ctx context.Context, target paymentport.ReconciliationTarget) error {
	if ctx.Value(txKey{}) != true {
		return errors.New("not in tx")
	}
	s.refundID = target.RefundID
	return nil
}

type checkoutProductStub struct {
	product productport.CheckoutProduct
	calls   int
}

func (stub *checkoutProductStub) ReadCheckoutProductWithin(ctx context.Context, _ productport.ProductOptionType, _ productport.ID) (productport.CheckoutProduct, error) {
	if ctx.Value(txKey{}) != true {
		return productport.CheckoutProduct{}, errors.New("not in tx")
	}
	stub.calls++
	return stub.product, nil
}
func (s shopReconcilerStub) QueryRefund(context.Context, string) (paymentport.ShopRefundQuery, error) {
	return s.query, nil
}

func TestWeChatPayPaymentReconciliationUsesPaymentForeignKey(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt}}
	service := NewService(uowStub{}, store, orderStub{snapshot: nativeOrder()}, sessionStub{}, &effectStub{})
	if err := service.SetWeChatPayReconciler(payReconcilerStub{payment: paymentport.WeChatPayPaymentQuery{MerchantOrderNo: "M-7", Currency: "CNY", Status: "SUCCESS", AmountMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("pay-query", "M-7"), TransactionDigest: effectport.Hash("pay-transaction", "M-7")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileWeChatPayPayment(context.Background(), 7)
	if err != nil || result.Status != domain.StatusPaid || store.paymentReconciliationID != 7 || store.refundReconciliationID != 0 {
		t.Fatalf("payment=%+v payment_reconciliation=%d refund_reconciliation=%d err=%v", result, store.paymentReconciliationID, store.refundReconciliationID, err)
	}
}

func TestWeChatPayTerminalFailureClosesPaymentAndOrderAtomically(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt}}
	orders := &recordingOrderStub{orderStub: orderStub{snapshot: nativeOrder()}}
	service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
	if err := service.SetWeChatPayReconciler(payReconcilerStub{payment: paymentport.WeChatPayPaymentQuery{MerchantOrderNo: "M-7", Currency: "CNY", Status: "CLOSED", AmountMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("pay-query", "closed-M-7")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileWeChatPayPayment(context.Background(), 7)
	if err != nil || result.Status != domain.StatusFailed || orders.settlement == nil || !orders.settlement.Failed || orders.settlement.OrderID != 3 {
		t.Fatalf("payment=%+v settlement=%+v err=%v", result, orders.settlement, err)
	}
}

func TestWeChatPayClosedRefundReleasesReservationWithoutChangingOrder(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt},
		refund:  domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatPay, RefundNo: "R-9", AmountMinor: 500, Status: domain.RefundEffectAccepted, EffectID: "eer_9", Version: 2, CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt},
	}
	orders := &recordingOrderStub{orderStub: orderStub{snapshot: nativeOrder()}}
	service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
	if err := service.SetWeChatPayReconciler(payReconcilerStub{refund: paymentport.WeChatPayRefundQuery{RefundNo: "R-9", Currency: "CNY", Status: "CLOSED", AmountMinor: 500, TotalMinor: 1000, OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("refund-query", "closed-R-9")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileWeChatPayRefund(context.Background(), 9)
	if err != nil || result.Status != domain.RefundFinalFailed || orders.settlement != nil {
		t.Fatalf("refund=%+v settlement=%+v err=%v", result, orders.settlement, err)
	}
}

func TestShopRefundReconciliationUsesRefundForeignKey(t *testing.T) {
	updatedAt := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		payment:      domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatShop, MerchantOrderNo: "SHOP-7", AmountMinor: 1000, Currency: "CNY", Status: domain.StatusPaid, Version: 2, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt},
		refund:       domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatShop, RefundNo: "R-9", AmountMinor: 500, Status: domain.RefundEffectAccepted, EffectID: "eer_9", ProviderRefundReference: "AS-9", Version: 2, CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt},
		shopMaterial: paymentport.ShopRefundMaterial{RefundID: 9, PaymentID: 7, AmountMinor: 500, RefundNo: "R-9", ProviderOrderID: "SHOP-7", ProductID: "P-1", SKUID: "SKU-1", RefundCount: 1, ReasonCode: "10000000", Currency: "CNY"},
	}
	service := NewService(uowStub{}, store, orderStub{snapshot: nativeOrder()}, sessionStub{}, &effectStub{})
	if err := service.SetShopReconciler(shopReconcilerStub{query: paymentport.ShopRefundQuery{AfterSaleID: "AS-9", ProviderOrderID: "SHOP-7", ProductID: "P-1", SKUID: "SKU-1", Count: 1, AmountMinor: 500, Currency: "CNY", Status: "MERCHANT_REFUND_SUCCESS", OccurredAt: updatedAt.Add(time.Minute), EvidenceDigest: effectport.Hash("shop-query", "AS-9"), ProviderRefundDigest: effectport.Hash("shop-refund", "AS-9")}}); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileShopRefund(context.Background(), 9)
	if err != nil || result.Status != domain.RefundCompleted || store.refundReconciliationID != 9 || store.paymentReconciliationID != 0 {
		t.Fatalf("refund=%+v payment_reconciliation=%d refund_reconciliation=%d err=%v", result, store.paymentReconciliationID, store.refundReconciliationID, err)
	}
}

func TestShopCallbackPersistsQueryRequiredReceiptAndDurableJob(t *testing.T) {
	now := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	store := &storeStub{
		refund:       domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderWeChatShop, RefundNo: "R-9", Status: domain.RefundEffectAccepted, ProviderRefundReference: "AS-9", Version: 2, CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
		shopMaterial: paymentport.ShopRefundMaterial{RefundID: 9, ProviderOrderID: "SHOP-7"},
	}
	jobs := &reconciliationEnqueuerStub{}
	service := NewService(uowStub{}, store, orderStub{}, sessionStub{}, &effectStub{})
	if err := service.SetShopReconciler(shopReconcilerStub{}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetReconciliationEnqueuer(jobs); err != nil {
		t.Fatal(err)
	}
	err := service.ApplyVerifiedShopCallback(context.Background(), paymentport.ShopRefundCallback{AfterSaleID: "AS-9", ProviderOrderID: "SHOP-7", Status: "MERCHANT_REFUND_SUCCESS", EventDigest: [32]byte{1}, PayloadDigest: [32]byte{2}, OccurredAt: now})
	if err != nil || jobs.refundID != 9 || store.callbackOutcome != "query_required" {
		t.Fatalf("job_refund=%d outcome=%q err=%v", jobs.refundID, store.callbackOutcome, err)
	}
}
