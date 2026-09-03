package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
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

type sessionStub struct{ actor paymentport.SessionActor }

func (stub sessionStub) ConsumeWithin(ctx context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if ctx.Value(txKey{}) != true || token == "" {
		return paymentport.SessionActor{}, errors.New("not in tx")
	}
	return stub.actor, nil
}

type effectStub struct {
	fail   bool
	within bool
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
	payment domain.Payment
	refund  domain.Refund
	bound   bool
}

func (s *storeStub) CreatePayment(_ context.Context, p domain.Payment, _, _ [32]byte, _ string) (domain.Payment, bool, error) {
	if s.payment.ID > 0 {
		return s.payment, false, nil
	}
	p.ID = 7
	s.payment = p
	return p, true, nil
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
func (s *storeStub) CreateRefund(_ context.Context, r domain.Refund, _, _ [32]byte, _ string) (domain.Refund, bool, error) {
	r.ID = 9
	s.refund = r
	return r, true, nil
}
func (s *storeStub) BindRefundEffect(_ context.Context, r domain.Refund, _ effectport.PaymentV1Intent, _ map[string]any) (domain.Refund, error) {
	s.refund = r
	return r, nil
}
func (s *storeStub) GetRefund(context.Context, int64, bool) (domain.Refund, error) {
	return s.refund, nil
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
func (s *storeStub) GetRefundByNumber(context.Context, string, bool) (domain.Refund, error) {
	return s.refund, nil
}
func (s *storeStub) ClaimCallback(context.Context, string, [32]byte, [32]byte, string, int64) (bool, error) {
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
	service := NewService(uowStub{}, store, orderStub{nativeOrder()}, sessionStub{paymentport.SessionActor{PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22}}, effects)
	service.now = func() time.Time { return time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC) }
	command := paymentport.CreateCommand{OrderID: 3, SessionToken: "pays_session_token_0000000001", ActorScope: "customer:11", IdempotencyKey: "payment-create-key-0001"}
	first, err := service.Create(context.Background(), command)
	if err != nil || first.EffectID != "eer_8" || !store.bound || !effects.within {
		t.Fatalf("payment=%+v store=%+v effects=%+v err=%v", first, store, effects, err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != first.ID || replay.EffectID != first.EffectID {
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
