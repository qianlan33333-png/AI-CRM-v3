package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type directUOW struct{}

func (directUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type memoryStore struct {
	nextID   int64
	orders   map[int64]domain.Snapshot
	receipts map[string]Receipt
	imports  map[string]ImportReceipt
	failSave bool
	exports  map[string]ExportReceipt
	contacts map[int64][]byte
	checkout map[int64]orderport.CheckoutSnapshot
}

func newMemoryStore() *memoryStore {
	return &memoryStore{nextID: 1, orders: map[int64]domain.Snapshot{}, receipts: map[string]Receipt{}, imports: map[string]ImportReceipt{}, exports: map[string]ExportReceipt{}, contacts: map[int64][]byte{}, checkout: map[int64]orderport.CheckoutSnapshot{}}
}

func (s *memoryStore) Reserve(_ context.Context, reservation Reservation) (Receipt, bool, error) {
	key := reservation.Operation + ":" + reservation.ActorScope + ":" + string(reservation.KeyDigest[:])
	if receipt, ok := s.receipts[key]; ok {
		return receipt, false, nil
	}
	receipt := Receipt{ID: int64(len(s.receipts) + 1), Operation: reservation.Operation, ActorScope: reservation.ActorScope, KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	s.receipts[key] = receipt
	return receipt, true, nil
}

func (s *memoryStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	if s.failSave {
		return Receipt{}, errors.New("save failed")
	}
	for key, receipt := range s.receipts {
		if receipt.ID == id {
			receipt.State, receipt.ResultSnapshot = "completed", append(json.RawMessage(nil), snapshot...)
			s.receipts[key] = receipt
			return receipt, nil
		}
	}
	return Receipt{}, errors.New("missing receipt")
}

func (s *memoryStore) Insert(_ context.Context, order domain.Order, _ int64, _ time.Time) (domain.Order, error) {
	snapshot := order.Snapshot()
	snapshot.ID = s.nextID
	s.nextID++
	persisted, err := domain.Restore(snapshot)
	if err == nil {
		s.orders[snapshot.ID] = snapshot
	}
	return persisted, err
}
func (s *memoryStore) InsertScoped(ctx context.Context, order domain.Order, _ string, now time.Time) (domain.Order, error) {
	return s.Insert(ctx, order, 1, now)
}
func (s *memoryStore) InsertContactSnapshot(_ context.Context, orderID int64, ciphertext []byte, _ int16, _ time.Time) error {
	s.contacts[orderID] = append([]byte(nil), ciphertext...)
	return nil
}
func (s *memoryStore) InsertCheckoutSnapshot(_ context.Context, snapshot orderport.CheckoutSnapshot) error {
	s.checkout[snapshot.OrderID] = snapshot
	return nil
}
func (s *memoryStore) ReadCheckoutSnapshot(_ context.Context, orderID int64) (orderport.CheckoutSnapshot, error) {
	snapshot, ok := s.checkout[orderID]
	if !ok {
		return orderport.CheckoutSnapshot{}, orderport.ErrNotFound
	}
	return snapshot, nil
}

type contactCipherStub struct{}

func (contactCipherStub) Encrypt(value string) ([]byte, error) {
	return append(make([]byte, 28), value...), nil
}
func (contactCipherStub) KeyVersion() int16 { return 1 }

type checkoutCouponStub struct {
	reserved couponport.ReservationSnapshot
	reserve  []couponport.ReserveCommand
	consume  []couponport.ConsumeCommand
	release  []couponport.ReleaseCommand
}

func (stub *checkoutCouponStub) ReserveWithin(_ context.Context, command couponport.ReserveCommand) (couponport.ReservationSnapshot, error) {
	stub.reserve = append(stub.reserve, command)
	if stub.reserved.ProductID != command.ProductID || stub.reserved.ProductType != command.ProductType || stub.reserved.ProductCode != command.ProductCode || stub.reserved.GrossAmountMinor != command.GrossAmountMinor || stub.reserved.Currency != command.Currency {
		return couponport.ReservationSnapshot{}, errors.New("unexpected coupon reserve")
	}
	return stub.reserved, nil
}
func (stub *checkoutCouponStub) ConsumeWithin(_ context.Context, command couponport.ConsumeCommand) (couponport.ReservationSnapshot, error) {
	stub.consume = append(stub.consume, command)
	return stub.reserved, nil
}
func (stub *checkoutCouponStub) ReleaseWithin(_ context.Context, command couponport.ReleaseCommand) (couponport.ReservationSnapshot, error) {
	stub.release = append(stub.release, command)
	return stub.reserved, nil
}

type fulfillmentStub struct {
	grants  []orderport.ServicePeriodGrantCommand
	refunds []orderport.ServicePeriodRefundCommand
}

func (stub *fulfillmentStub) GrantPaidServicePeriodWithin(_ context.Context, command orderport.ServicePeriodGrantCommand) (orderport.Entitlement, error) {
	stub.grants = append(stub.grants, command)
	return orderport.Entitlement{ID: 1}, nil
}
func (stub *fulfillmentStub) ApplyServicePeriodRefundWithin(_ context.Context, command orderport.ServicePeriodRefundCommand) (orderport.Entitlement, error) {
	stub.refunds = append(stub.refunds, command)
	return orderport.Entitlement{ID: 1}, nil
}

func (s *memoryStore) Get(_ context.Context, id int64, _ bool) (domain.Order, error) {
	snapshot, ok := s.orders[id]
	if !ok {
		return domain.Order{}, orderport.ErrNotFound
	}
	return domain.Restore(snapshot)
}

func (s *memoryStore) List(_ context.Context, before *Cursor, limit int32, _ ListFilter) ([]domain.Order, error) {
	rows := make([]domain.Order, 0)
	for id := s.nextID - 1; id >= 1 && len(rows) < int(limit); id-- {
		snapshot := s.orders[id]
		if before != nil && (snapshot.CreatedAt.After(before.CreatedAt) || snapshot.CreatedAt.Equal(before.CreatedAt) && snapshot.ID >= before.ID) {
			continue
		}
		order, _ := domain.Restore(snapshot)
		rows = append(rows, order)
	}
	return rows, nil
}

func (s *memoryStore) Count(context.Context, ListFilter) (int64, error) {
	return int64(len(s.orders)), nil
}

func (s *memoryStore) FindByReference(_ context.Context, reference string) ([]domain.Order, error) {
	rows := []domain.Order{}
	for _, snapshot := range s.orders {
		if snapshot.MerchantOrderNo == reference || snapshot.ProviderTransactionNo == reference || snapshot.SourceKey == reference {
			order, _ := domain.Restore(snapshot)
			rows = append(rows, order)
		}
	}
	return rows, nil
}

func (s *memoryStore) Export(_ context.Context, _ ListFilter, limit int32) ([]domain.Order, error) {
	return s.List(context.Background(), nil, limit, ListFilter{})
}

func (s *memoryStore) RecordExport(_ context.Context, receipt ExportReceipt) (ExportReceipt, bool, error) {
	key := string(receipt.KeyDigest[:])
	if existing, ok := s.exports[key]; ok {
		return existing, false, nil
	}
	receipt.ID = int64(len(s.exports) + 1)
	s.exports[key] = receipt
	return receipt, true, nil
}

func (s *memoryStore) UpdateSettlement(_ context.Context, order domain.Order, _ domain.StatusEvent, _ string) (domain.Order, error) {
	s.orders[order.ID] = order.Snapshot()
	return order, nil
}

func (s *memoryStore) Import(_ context.Context, runID string, digest [32]byte, order domain.Order) (domain.Order, bool, error) {
	key := runID + ":" + order.SourceSystem + ":" + order.SourceKey
	if receipt, ok := s.imports[key]; ok {
		if receipt.SourceDigest != digest {
			return domain.Order{}, false, orderport.ErrConflict
		}
		persisted, err := s.Get(context.Background(), receipt.OrderID, false)
		return persisted, false, err
	}
	persisted, err := s.Insert(context.Background(), order, 0, order.CreatedAt)
	if err != nil {
		return domain.Order{}, false, err
	}
	s.imports[key] = ImportReceipt{RunID: runID, SourceDigest: digest, OrderID: persisted.ID}
	return persisted, true, nil
}

func orderInput(key string) domain.NewOrderInput {
	payer, beneficiary := int64(11), int64(22)
	return domain.NewOrderInput{Provider: domain.ProviderWeChatPay, SourceSystem: "aicrm-v3", SourceKey: key, MerchantOrderNo: "M-" + key, PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: domain.Money{AmountMinor: 1000, Currency: "CNY"}, Items: []domain.ItemSnapshot{{LineNo: 1, ProductCode: "p", ProductName: "课程", UnitAmountMinor: 1000, Quantity: 1, LineAmountMinor: 1000}}, RecordOrigin: domain.RecordOriginNative, CreatedAt: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)}
}

func TestCreateReplayAndPayloadDrift(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	command := orderport.CreateCommand{Input: orderInput("1"), Actor: 7, IdempotencyKey: "order-create-key-0001"}
	first, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Create(context.Background(), command)
	if err != nil || replay.ID != first.ID || len(store.orders) != 1 {
		t.Fatalf("replay=%#v err=%v orders=%d", replay, err, len(store.orders))
	}
	command.Input.MerchantOrderNo = "M-drift"
	if _, err = service.Create(context.Background(), command); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("payload drift err=%v", err)
	}
}

func TestCreatePaymentOrderWithinFreezesProductVersionAndReplays(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	command := orderport.PaymentOrderCommand{
		Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_1234567890abcdef",
		PayerCustomerID: 11, BeneficiaryCustomerID: 22,
		ProductID: 5, ProductCode: "course-5", ProductName: "Course 5", ProductVersion: 3,
		ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY", ActorScope: "payment-session:1234567890abcdef", IdempotencyKey: "checkout-product-key-0005",
	}
	first, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil || first.ID < 1 || len(first.Items) != 1 || first.Items[0].ProductVersion == nil || *first.Items[0].ProductVersion != 3 || first.BeneficiaryCustomerID == nil || *first.BeneficiaryCustomerID != 22 {
		t.Fatalf("order=%+v err=%v", first, err)
	}
	replay, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil || replay.ID != first.ID || len(store.orders) != 1 {
		t.Fatalf("replay=%+v orders=%d err=%v", replay, len(store.orders), err)
	}
}

func TestCreatePaymentOrderWithinEncryptsRequiredContactSnapshot(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	if err := service.SetContactCipher(contactCipherStub{}); err != nil {
		t.Fatal(err)
	}
	command := orderport.PaymentOrderCommand{
		Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_mobile_123456",
		PayerCustomerID: 11, BeneficiaryCustomerID: 11, ProductID: 5, ProductCode: "course-5", ProductName: "Course 5", ProductVersion: 3,
		ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY", MobileE164: "+8613812345678", ActorScope: "payment-session:mobile-123456", IdempotencyKey: "checkout-mobile-key-0005",
	}
	order, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil || len(store.contacts[order.ID]) < 28 || strings.Contains(string(order.Items[0].ProductName), command.MobileE164) {
		t.Fatalf("order=%+v contact=%x err=%v", order, store.contacts[order.ID], err)
	}
	command.MobileE164 = "+8612812345678"
	if _, err = service.CreatePaymentOrderWithin(context.Background(), command); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("invalid mobile error=%v", err)
	}
}

func TestPaymentCheckoutNoCouponReplayKeepsOriginalFrozenPrice(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	coupons := &checkoutCouponStub{reserved: couponport.ReservationSnapshot{CouponApplied: false, ProductID: 5, ProductType: "standard_product", ProductCode: "course-5", GrossAmountMinor: 8800, PayableAmountMinor: 8800, Currency: "CNY"}}
	if err := service.SetCheckoutCouponCoordinator(coupons); err != nil {
		t.Fatal(err)
	}
	command := orderport.PaymentOrderCommand{Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_no_coupon_replay", PayerCustomerID: 11, BeneficiaryCustomerID: 22, ProductID: 5, ProductCode: "course-5", ProductName: "Course 5", ProductVersion: 3, ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY", ActorScope: "payment-session:no-coupon", IdempotencyKey: "checkout-no-coupon-key-0001"}
	first, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil || first.Amount.AmountMinor != 8800 || store.checkout[first.ID].CouponApplied || len(coupons.reserve) != 1 {
		t.Fatalf("first=%+v snapshot=%+v reserves=%+v err=%v", first, store.checkout[first.ID], coupons.reserve, err)
	}
	// A matching coupon becoming available later must not be reconsidered on an
	// Order idempotency replay; only the completed Order receipt is authoritative.
	coupons.reserved = couponport.ReservationSnapshot{CouponApplied: true, ReservationRef: "cr_99", ClaimID: 99, CouponID: 7, ProductID: 5, ProductType: "standard_product", ProductCode: "course-5", RuleVersion: 2, GrossAmountMinor: 8800, DiscountAmountMinor: 1000, PayableAmountMinor: 7800, Currency: "CNY"}
	replay, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil || replay.ID != first.ID || replay.Amount.AmountMinor != 8800 || store.checkout[first.ID].CouponApplied || len(coupons.reserve) != 1 {
		t.Fatalf("replay=%+v snapshot=%+v reserves=%+v err=%v", replay, store.checkout[first.ID], coupons.reserve, err)
	}
}

func TestPaymentCheckoutFreezesCouponPriceAndSettlesWithinOrderTransaction(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	coupons := &checkoutCouponStub{reserved: couponport.ReservationSnapshot{CouponApplied: true, ReservationRef: "redemption-1", ClaimID: 21, CouponID: 22, ProductID: 5, ProductType: "standard_product", ProductCode: "course-5", RuleVersion: 3, GrossAmountMinor: 8800, DiscountAmountMinor: 1800, PayableAmountMinor: 7000, Currency: "CNY"}}
	if err := service.SetCheckoutCouponCoordinator(coupons); err != nil {
		t.Fatal(err)
	}
	command := orderport.PaymentOrderCommand{Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_coupon_checkout", PayerCustomerID: 11, BeneficiaryCustomerID: 22, ProductID: 5, CouponClaimID: 21, ProductCode: "course-5", ProductName: "Course 5", ProductVersion: 3, ProductType: "standard_product", UnitAmountMinor: 8800, Currency: "CNY", ActorScope: "payment-session:coupon", IdempotencyKey: "checkout-coupon-key-0001"}
	created, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil || created.Amount.AmountMinor != 7000 || created.Items[0].UnitAmountMinor != 7000 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	frozen := store.checkout[created.ID]
	if !frozen.CouponApplied || frozen.GrossAmountMinor != 8800 || frozen.DiscountAmountMinor != 1800 || frozen.PayableAmountMinor != 7000 || frozen.CouponReservationRef != "redemption-1" {
		t.Fatalf("frozen=%+v", frozen)
	}
	// Settlement facts cannot predate their immutable order snapshot. Derive
	// the callback time from the created snapshot so this test stays valid as
	// the test clock advances without weakening the payment assertion.
	paidAt := created.CreatedAt.Add(time.Minute)
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: created.ID, OccurredAt: paidAt, ReceiptKey: "payment-callback-1"}); err != nil {
		t.Fatal(err)
	}
	if len(coupons.consume) != 1 || coupons.consume[0].SettledAmountMinor != 7000 || coupons.consume[0].SettledCurrency != "CNY" || coupons.consume[0].ReservationRef != "redemption-1" || len(coupons.release) != 0 {
		t.Fatalf("consume=%+v release=%+v", coupons.consume, coupons.release)
	}
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: created.ID, RefundedDelta: 1, OccurredAt: paidAt.Add(time.Minute), ReceiptKey: "refund-callback-1"}); err != nil {
		t.Fatal(err)
	}
	if len(coupons.release) != 0 {
		t.Fatalf("a confirmed refund must retain consumed coupon, release=%+v", coupons.release)
	}
}

func TestPaymentCheckoutFinalFailureReleasesCouponAndServicePeriodRefundsOnce(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	coupons := &checkoutCouponStub{reserved: couponport.ReservationSnapshot{CouponApplied: true, ReservationRef: "redemption-period", ClaimID: 31, CouponID: 32, ProductID: 6, ProductType: "service_period", ProductCode: "period-6", RuleVersion: 2, GrossAmountMinor: 12800, DiscountAmountMinor: 2800, PayableAmountMinor: 10000, Currency: "CNY"}}
	fulfillment := &fulfillmentStub{}
	if err := service.SetCheckoutCouponCoordinator(coupons); err != nil {
		t.Fatal(err)
	}
	if err := service.SetServicePeriodEntitlementCoordinator(fulfillment); err != nil {
		t.Fatal(err)
	}
	command := orderport.PaymentOrderCommand{Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_period_checkout", PayerCustomerID: 11, BeneficiaryCustomerID: 22, ProductID: 6, CouponClaimID: 31, ProductCode: "period-6", ProductName: "Period 6", ProductVersion: 3, ProductType: "service_period", ServicePeriodDurationDays: 31, UnitAmountMinor: 12800, Currency: "CNY", ActorScope: "payment-session:period", IdempotencyKey: "checkout-period-key-0001"}
	failed, err := service.CreatePaymentOrderWithin(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	closedAt := failed.CreatedAt.Add(time.Minute)
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: failed.ID, Failed: true, OccurredAt: closedAt, ReceiptKey: "effect-final-key-0001"}); err != nil {
		t.Fatal(err)
	}
	if len(coupons.release) != 1 || coupons.release[0].CloseReason != "payment_final_failed" || len(fulfillment.grants) != 0 || len(fulfillment.refunds) != 0 {
		t.Fatalf("release=%+v grants=%+v refunds=%+v", coupons.release, fulfillment.grants, fulfillment.refunds)
	}

	paid, err := service.CreatePaymentOrderWithin(context.Background(), orderport.PaymentOrderCommand{Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_period_paid", PayerCustomerID: 11, BeneficiaryCustomerID: 22, ProductID: 6, CouponClaimID: 31, ProductCode: "period-6", ProductName: "Period 6", ProductVersion: 3, ProductType: "service_period", ServicePeriodDurationDays: 31, UnitAmountMinor: 12800, Currency: "CNY", ActorScope: "payment-session:period-paid", IdempotencyKey: "checkout-period-key-0002"})
	if err != nil {
		t.Fatal(err)
	}
	paidAt := paid.CreatedAt.Add(time.Minute)
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: paid.ID, OccurredAt: paidAt, ReceiptKey: "payment-paid-key-0001"}); err != nil {
		t.Fatal(err)
	}
	if len(fulfillment.grants) != 1 || fulfillment.grants[0].DurationDays != 31 || fulfillment.grants[0].BeneficiaryCustomerID != 22 {
		t.Fatalf("grants=%+v", fulfillment.grants)
	}
	if _, err = service.SettlePaymentWithin(context.Background(), orderport.PaymentSettlementCommand{OrderID: paid.ID, RefundedDelta: 10000, OccurredAt: paidAt.Add(time.Minute), ReceiptKey: "refund-paid-key-0001"}); err != nil {
		t.Fatal(err)
	}
	if len(fulfillment.refunds) != 1 || fulfillment.refunds[0].SourceOrderID != paid.ID || fulfillment.refunds[0].RefundAmountMinor != 10000 {
		t.Fatalf("refunds=%+v", fulfillment.refunds)
	}
}

func TestListUsesStableCreatedAtIDCursor(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	for _, key := range []string{"1", "2", "3"} {
		if _, err := service.Create(context.Background(), orderport.CreateCommand{Input: orderInput(key), Actor: 7, IdempotencyKey: "order-create-key-000" + key}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.List(context.Background(), orderport.ListQuery{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].ID != 3 || first.Items[1].ID != 2 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := service.List(context.Background(), orderport.ListQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != 1 || second.NextCursor != "" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	if _, err = service.List(context.Background(), orderport.ListQuery{Limit: 2, Cursor: first.NextCursor + "tampered"}); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("tampered cursor err=%v", err)
	}
}

func TestHistoricalImportRejectsEffectEligibleAndReplaysSource(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	input := orderInput("history-1")
	input.RecordOrigin = domain.RecordOriginHistory
	input.SourceSystem = "aicrm-production"
	input.PayerCustomerID, input.BeneficiaryCustomerID = nil, nil
	history, err := domain.NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := history.Snapshot()
	command := orderport.HistoricalImportCommand{RunID: "run-1", SourceDigest: [32]byte{1}, Order: snapshot}
	first, err := service.ImportHistorical(context.Background(), command)
	if err != nil || first.EffectEligible {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	replay, err := service.ImportHistorical(context.Background(), command)
	if err != nil || replay.ID != first.ID || len(store.orders) != 1 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	command.SourceDigest = [32]byte{2}
	if _, err = service.ImportHistorical(context.Background(), command); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("source drift err=%v", err)
	}
}

func TestExportCSVIsReceiptBackedReplayAndEscapesFormulas(t *testing.T) {
	store := newMemoryStore()
	service := NewService(directUOW{}, store)
	input := orderInput("export-1")
	input.Items[0].ProductName = "=HYPERLINK(\"bad\")"
	if _, err := service.Create(context.Background(), orderport.CreateCommand{Input: input, Actor: 7, IdempotencyKey: "order-create-export-0001"}); err != nil {
		t.Fatal(err)
	}
	first, err := service.ExportCSV(context.Background(), orderport.ListQuery{}, 7, "order-export-key-0001")
	if err != nil || first.ReceiptID < 1 || !strings.Contains(string(first.Content), `"'=HYPERLINK(""bad"")"`) {
		t.Fatalf("result=%+v content=%s err=%v", first, first.Content, err)
	}
	replay, err := service.ExportCSV(context.Background(), orderport.ListQuery{}, 7, "order-export-key-0001")
	if err != nil || replay.ReceiptID != first.ReceiptID || len(store.exports) != 1 {
		t.Fatalf("replay=%+v err=%v receipts=%d", replay, err, len(store.exports))
	}
}
