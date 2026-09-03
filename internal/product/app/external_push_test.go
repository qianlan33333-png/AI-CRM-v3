package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type commerceExternalPushTestUoW struct{ calls int }

func (uow *commerceExternalPushTestUoW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	return callback(ctx)
}

type commerceExternalPushTestStore struct {
	products    map[productport.ID]productport.ExternalPushProductKind
	configs     map[productport.ID]productport.ExternalPushConfiguration
	receipts    map[string]Receipt
	tests       []productport.ExternalPushTest
	testDigests map[string]bool
	saves       int
}

func (store *commerceExternalPushTestStore) ReadCommerceExternalPushConfiguration(_ context.Context, id productport.ID, kind productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	if store.products[id] != kind {
		return productport.ExternalPushConfiguration{}, ErrNotFound
	}
	if value, ok := store.configs[id]; ok {
		return value, nil
	}
	return productport.ExternalPushConfiguration{ProductID: id, ProductKind: kind, UpdatedAt: time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)}, nil
}

func (store *commerceExternalPushTestStore) LockCommerceExternalPushConfiguration(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return store.ReadCommerceExternalPushConfiguration(ctx, id, kind)
}

func (store *commerceExternalPushTestStore) SaveCommerceExternalPushConfiguration(_ context.Context, value productport.ExternalPushConfiguration, now time.Time) (productport.ExternalPushConfiguration, error) {
	if store.products[value.ProductID] != value.ProductKind {
		return productport.ExternalPushConfiguration{}, ErrNotFound
	}
	store.saves++
	value.UpdatedAt = now.UTC()
	store.configs[value.ProductID] = value
	return value, nil
}

func (store *commerceExternalPushTestStore) ReserveCommerceExternalPush(_ context.Context, reservation Reservation) (Receipt, bool, error) {
	key := commerceExternalPushTestReceiptKey(reservation)
	if receipt, ok := store.receipts[key]; ok {
		return receipt, false, nil
	}
	receipt := Receipt{ID: int64(len(store.receipts) + 1), Operation: reservation.Operation, ActorScope: reservation.ActorScope, KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}
	store.receipts[key] = receipt
	return receipt, true, nil
}

func (store *commerceExternalPushTestStore) CompleteCommerceExternalPush(_ context.Context, receiptID int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, receipt := range store.receipts {
		if receipt.ID == receiptID {
			receipt.State, receipt.ResultSnapshot = "completed", append(json.RawMessage(nil), snapshot...)
			store.receipts[key] = receipt
			return receipt, nil
		}
	}
	return Receipt{}, ErrNotFound
}

func (store *commerceExternalPushTestStore) CommerceExternalPushTestExists(_ context.Context, productID productport.ID, kind productport.ExternalPushProductKind, digest [32]byte) (bool, error) {
	return store.testDigests[commerceExternalPushTestDigestKey(productID, kind, digest)], nil
}

func (store *commerceExternalPushTestStore) CreateCommerceExternalPushTest(_ context.Context, value productport.ExternalPushTest, digest [32]byte, _ int64) (productport.ExternalPushTest, error) {
	store.tests = append(store.tests, value)
	if store.testDigests == nil {
		store.testDigests = map[string]bool{}
	}
	store.testDigests[commerceExternalPushTestDigestKey(value.ProductID, value.ProductKind, digest)] = true
	return value, nil
}

func commerceExternalPushTestDigestKey(productID productport.ID, kind productport.ExternalPushProductKind, digest [32]byte) string {
	return fmt.Sprintf("%d\x00%s\x00%x", productID, kind, digest)
}

func commerceExternalPushTestReceiptKey(reservation Reservation) string {
	return reservation.Operation + "\x00" + reservation.ActorScope + "\x00" + string(reservation.KeyDigest[:])
}

type commerceExternalPushTestEffects struct {
	result productport.ExternalPushTest
	calls  int
	inputs []ProductExternalPushEffectCommand
}

func (effects *commerceExternalPushTestEffects) AcceptProductExternalPushTest(_ context.Context, input ProductExternalPushEffectCommand) (productport.ExternalPushTest, error) {
	effects.calls++
	effects.inputs = append(effects.inputs, input)
	return effects.result, nil
}

func newCommerceExternalPushTestService(store *commerceExternalPushTestStore, effects *commerceExternalPushTestEffects) (*CommerceExternalPushService, *commerceExternalPushTestUoW) {
	uow := &commerceExternalPushTestUoW{}
	service := NewCommerceExternalPushService(uow, store, effects)
	service.now = func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }
	return service, uow
}

func TestCommerceExternalPushSavesReadsAndReplaysLocally(t *testing.T) {
	store := &commerceExternalPushTestStore{products: map[productport.ID]productport.ExternalPushProductKind{41: productport.ExternalPushWeChatPay}, configs: map[productport.ID]productport.ExternalPushConfiguration{}, receipts: map[string]Receipt{}}
	service, uow := newCommerceExternalPushTestService(store, &commerceExternalPushTestEffects{})
	command := productport.SaveExternalPushConfigurationCommand{ProductID: 41, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "commerce-push-config-41", Actor: 7, IdempotencyKey: "commerce-push-save-0001"}
	first, err := service.SaveExternalPushConfiguration(context.Background(), command)
	if err != nil || !first.Enabled || first.ConfigurationReference != command.ConfigurationReference || first.UpdatedAt.IsZero() || store.saves != 1 {
		t.Fatalf("first=%#v saves=%d err=%v", first, store.saves, err)
	}
	replayed, err := service.SaveExternalPushConfiguration(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, first) || store.saves != 1 {
		t.Fatalf("replayed=%#v saves=%d err=%v", replayed, store.saves, err)
	}
	read, err := service.GetExternalPushConfiguration(context.Background(), 41, productport.ExternalPushWeChatPay)
	if err != nil || !reflect.DeepEqual(read, first) || uow.calls != 3 {
		t.Fatalf("read=%#v uow=%d err=%v", read, uow.calls, err)
	}
	conflict := command
	conflict.Enabled, conflict.ConfigurationReference = false, ""
	if _, err = service.SaveExternalPushConfiguration(context.Background(), conflict); !errors.Is(err, ErrConflict) || store.saves != 1 {
		t.Fatalf("conflict error=%v saves=%d", err, store.saves)
	}
}

func TestCommerceExternalPushTestCreatesOnlyAcceptedLocalEERFactAndReplays(t *testing.T) {
	updated := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	store := &commerceExternalPushTestStore{
		products: map[productport.ID]productport.ExternalPushProductKind{52: productport.ExternalPushServicePeriod},
		configs:  map[productport.ID]productport.ExternalPushConfiguration{52: {ProductID: 52, ProductKind: productport.ExternalPushServicePeriod, Enabled: true, ConfigurationReference: "service-period-notify-52", UpdatedAt: updated}},
		receipts: map[string]Receipt{},
	}
	effects := &commerceExternalPushTestEffects{result: productport.ExternalPushTest{ProductID: 52, ProductKind: productport.ExternalPushServicePeriod, EffectID: "eer_1", State: "accepted", CreatedAt: updated}}
	service, _ := newCommerceExternalPushTestService(store, effects)
	command := productport.QueueExternalPushTestCommand{ProductID: 52, ProductKind: productport.ExternalPushServicePeriod, Actor: 9, IdempotencyKey: "commerce-push-test-0001"}
	first, err := service.QueueExternalPushTest(context.Background(), command)
	if err != nil || first.EffectID != "eer_1" || first.State != "accepted" || first.ProviderAccepted || first.DeliveryProven || first.RealExternalCallExecuted || first.AutoRetryAllowed || len(store.tests) != 1 || effects.calls != 1 {
		t.Fatalf("first=%#v tests=%#v effects=%d err=%v", first, store.tests, effects.calls, err)
	}
	if effects.inputs[0].ProductID != 52 || effects.inputs[0].ProductKind != productport.ExternalPushServicePeriod || effects.inputs[0].ConfigurationDigest == ([32]byte{}) || effects.inputs[0].ReceiptKeyDigest == ([32]byte{}) {
		t.Fatalf("effect input=%#v", effects.inputs[0])
	}
	replayed, err := service.QueueExternalPushTest(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, first) || len(store.tests) != 1 || effects.calls != 1 {
		t.Fatalf("replayed=%#v tests=%d effects=%d err=%v", replayed, len(store.tests), effects.calls, err)
	}
	command.IdempotencyKey = "commerce-push-test-different-key"
	if _, err = service.QueueExternalPushTest(context.Background(), command); !errors.Is(err, ErrConflict) || len(store.tests) != 1 || effects.calls != 1 {
		t.Fatalf("different key error=%v tests=%d effects=%d", err, len(store.tests), effects.calls)
	}
}

func TestCommerceExternalPushTestFailsClosedWithoutConfigurationOrWithDeliveryClaim(t *testing.T) {
	store := &commerceExternalPushTestStore{products: map[productport.ID]productport.ExternalPushProductKind{61: productport.ExternalPushWeChatPay}, configs: map[productport.ID]productport.ExternalPushConfiguration{}, receipts: map[string]Receipt{}}
	effects := &commerceExternalPushTestEffects{}
	service, _ := newCommerceExternalPushTestService(store, effects)
	command := productport.QueueExternalPushTestCommand{ProductID: 61, ProductKind: productport.ExternalPushWeChatPay, Actor: 7, IdempotencyKey: "commerce-push-test-0002"}
	if _, err := service.QueueExternalPushTest(context.Background(), command); !errors.Is(err, ErrExternalPushNotConfigured) || effects.calls != 0 || len(store.tests) != 0 {
		t.Fatalf("unconfigured error=%v effects=%d tests=%d", err, effects.calls, len(store.tests))
	}
	store.configs[61] = productport.ExternalPushConfiguration{ProductID: 61, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "commerce-push-config-61", UpdatedAt: time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)}
	command.IdempotencyKey = "commerce-push-test-0003"
	effects.result = productport.ExternalPushTest{ProductID: 61, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_74", State: "accepted", ProviderAccepted: true, CreatedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	if _, err := service.QueueExternalPushTest(context.Background(), command); !errors.Is(err, ErrUnavailable) || effects.calls != 1 || len(store.tests) != 0 {
		t.Fatalf("delivery claim error=%v effects=%d tests=%d", err, effects.calls, len(store.tests))
	}
}

var _ CommerceExternalPushStore = (*commerceExternalPushTestStore)(nil)
var _ ProductExternalPushEffectAccepter = (*commerceExternalPushTestEffects)(nil)
