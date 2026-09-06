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
	statuses    map[string]productport.ExternalPushTestStatus
	saves       int
}

func (store *commerceExternalPushTestStore) ReadCommerceExternalPushConfiguration(_ context.Context, id productport.ID, kind productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	if store.products[id] != kind {
		return productport.ExternalPushConfiguration{}, ErrNotFound
	}
	if value, ok := store.configs[id]; ok {
		return value, nil
	}
	return productport.ExternalPushConfiguration{ProductID: id, ProductKind: kind, Revision: 0, UpdatedAt: time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)}, nil
}

func (store *commerceExternalPushTestStore) LockCommerceExternalPushConfiguration(ctx context.Context, id productport.ID, kind productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return store.ReadCommerceExternalPushConfiguration(ctx, id, kind)
}

func (store *commerceExternalPushTestStore) ReadCommerceExternalPushConfigurationForOrder(ctx context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	kind, ok := store.products[id]
	if !ok {
		return productport.ExternalPushConfiguration{}, ErrNotFound
	}
	return store.ReadCommerceExternalPushConfiguration(ctx, id, kind)
}

func (store *commerceExternalPushTestStore) SaveCommerceExternalPushConfiguration(_ context.Context, value productport.ExternalPushConfiguration, now time.Time) (productport.ExternalPushConfiguration, error) {
	if store.products[value.ProductID] != value.ProductKind {
		return productport.ExternalPushConfiguration{}, ErrNotFound
	}
	store.saves++
	if previous, ok := store.configs[value.ProductID]; ok {
		value.Revision = previous.Revision + 1
	} else {
		value.Revision = 1
	}
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

func (store *commerceExternalPushTestStore) ListCommerceExternalPushTests(_ context.Context, productID productport.ID, kind productport.ExternalPushProductKind, limit int32) ([]productport.ExternalPushTest, error) {
	if store.products[productID] != kind || limit < 1 {
		return nil, ErrNotFound
	}
	out := make([]productport.ExternalPushTest, 0, len(store.tests))
	for _, value := range store.tests {
		if value.ProductID == productID && value.ProductKind == kind {
			out = append(out, value)
		}
	}
	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (store *commerceExternalPushTestStore) CreateCommerceExternalPushTest(_ context.Context, value productport.ExternalPushTest, digest [32]byte, _ int64) (productport.ExternalPushTest, error) {
	store.tests = append(store.tests, value)
	if store.testDigests == nil {
		store.testDigests = map[string]bool{}
	}
	store.testDigests[commerceExternalPushTestDigestKey(value.ProductID, value.ProductKind, digest)] = true
	return value, nil
}

func (store *commerceExternalPushTestStore) ReadExternalPushTestStatus(_ context.Context, productID productport.ID, effectID string) (productport.ExternalPushTestStatus, error) {
	if store.products[productID] == "" {
		return productport.ExternalPushTestStatus{}, ErrNotFound
	}
	value, found := store.statuses[effectID]
	if !found {
		return productport.ExternalPushTestStatus{}, ErrNotFound
	}
	return value, nil
}

func commerceExternalPushTestDigestKey(productID productport.ID, kind productport.ExternalPushProductKind, digest [32]byte) string {
	return fmt.Sprintf("%d\x00%s\x00%x", productID, kind, digest)
}

func commerceExternalPushTestReceiptKey(reservation Reservation) string {
	return reservation.Operation + "\x00" + reservation.ActorScope + "\x00" + string(reservation.KeyDigest[:])
}

type commerceExternalPushTestStatuses struct {
	values map[string]productport.ExternalPushTestStatus
}

func (statuses commerceExternalPushTestStatuses) ReadExternalPushTestStatus(_ context.Context, _ productport.ID, effectID string) (productport.ExternalPushTestStatus, error) {
	if value, found := statuses.values[effectID]; found {
		return value, nil
	}
	return productport.ExternalPushTestStatus{}, ErrNotFound
}

type commerceExternalPushTestEffects struct {
	result productport.ExternalPushTest
	calls  int
	inputs []productport.ExternalPushTestIntent
}

func (effects *commerceExternalPushTestEffects) AcceptExternalPushTestWithin(_ context.Context, input productport.ExternalPushTestIntent) (productport.ExternalPushTest, error) {
	effects.calls++
	effects.inputs = append(effects.inputs, input)
	return effects.result, nil
}

type commerceExternalPushTestEvents struct {
	events []productport.Event
}

func (events *commerceExternalPushTestEvents) Append(_ context.Context, event productport.Event) (productport.EventID, error) {
	events.events = append(events.events, event)
	return productport.EventID(len(events.events)), nil
}

func newCommerceExternalPushTestService(store *commerceExternalPushTestStore, effects *commerceExternalPushTestEffects) (*CommerceExternalPushService, *commerceExternalPushTestUoW) {
	uow := &commerceExternalPushTestUoW{}
	service, err := NewCommerceExternalPushService(uow, store, effects, store, &commerceExternalPushTestEvents{})
	if err != nil {
		panic(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }
	return service, uow
}

func TestCommerceExternalPushRequiresEventAppender(t *testing.T) {
	_, err := NewCommerceExternalPushService(&commerceExternalPushTestUoW{}, &commerceExternalPushTestStore{}, &commerceExternalPushTestEffects{}, commerceExternalPushTestStatuses{}, nil)
	if err == nil {
		t.Fatal("constructor must reject a missing event appender")
	}
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

func TestCommerceExternalPushBusinessParametersFreezeJSONTypesAndLegacyBindingPreservesThem(t *testing.T) {
	day, frequency := int64(45), int64(2)
	updated := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	store := &commerceExternalPushTestStore{
		products: map[productport.ID]productport.ExternalPushProductKind{81: productport.ExternalPushWeChatPay},
		configs: map[productport.ID]productport.ExternalPushConfiguration{81: {
			ProductID: 81, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-81",
			PushType: "member_open", Day: &day, Frequency: &frequency, Remark: "旧备注", CustomParams: map[string]any{"old": true}, Revision: 3, UpdatedAt: updated,
		}},
		receipts: map[string]Receipt{},
	}
	service, _ := newCommerceExternalPushTestService(store, &commerceExternalPushTestEffects{})
	command := productport.SaveExternalPushConfigurationCommand{
		ProductID: 81, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-81",
		BusinessParametersSet: true, PushType: "member_renew", Day: &day, Frequency: &frequency, Remark: "保留业务备注",
		CustomParams:     map[string]any{"count": float64(2), "flag": false, "nil": nil, "nested": []any{" 空白 ", map[string]any{"k": true}}},
		ExpectedRevision: 3, Actor: 7, IdempotencyKey: "commerce-push-business-0001",
	}
	first, err := service.SaveExternalPushConfiguration(context.Background(), command)
	if err != nil || first.Revision != 4 || !sameCommerceExternalPushBusiness(first, productport.ExternalPushConfiguration{PushType: command.PushType, Day: command.Day, Frequency: command.Frequency, Remark: command.Remark, CustomParams: command.CustomParams}) {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	legacy := productport.SaveExternalPushConfigurationCommand{ProductID: 81, ProductKind: productport.ExternalPushWeChatPay, Enabled: false, ConfigurationReference: "", Actor: 7, IdempotencyKey: "commerce-push-business-0002", ExpectedRevision: first.Revision}
	second, err := service.SaveExternalPushConfiguration(context.Background(), legacy)
	if err != nil || second.Enabled || second.Revision != 5 || !sameCommerceExternalPushBusiness(second, first) {
		t.Fatalf("legacy=%#v err=%v", second, err)
	}
	stale := command
	stale.IdempotencyKey, stale.ExpectedRevision = "commerce-push-business-0003", first.Revision
	if _, err = service.SaveExternalPushConfiguration(context.Background(), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS err=%v", err)
	}
}

func TestCommerceExternalPushFirstBusinessSaveRejectsStaleUnpersistedRevision(t *testing.T) {
	store := &commerceExternalPushTestStore{products: map[productport.ID]productport.ExternalPushProductKind{82: productport.ExternalPushWeChatPay}, configs: map[productport.ID]productport.ExternalPushConfiguration{}, receipts: map[string]Receipt{}}
	service, _ := newCommerceExternalPushTestService(store, &commerceExternalPushTestEffects{})
	command := productport.SaveExternalPushConfigurationCommand{ProductID: 82, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-82", BusinessParametersSet: true, PushType: "member_open", CustomParams: map[string]any{"n": json.Number("9007199254740993")}, ExpectedRevision: 0, Actor: 7, IdempotencyKey: "commerce-push-first-0001"}
	first, err := service.SaveExternalPushConfiguration(context.Background(), command)
	if err != nil || first.Revision != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	command.IdempotencyKey = "commerce-push-first-0002"
	if _, err = service.SaveExternalPushConfiguration(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale first-save err=%v", err)
	}
}

func TestCommerceExternalPushTestCreatesOnlyAcceptedLocalEERFactAndReplays(t *testing.T) {
	updated := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	store := &commerceExternalPushTestStore{
		products: map[productport.ID]productport.ExternalPushProductKind{52: productport.ExternalPushServicePeriod},
		configs:  map[productport.ID]productport.ExternalPushConfiguration{52: {ProductID: 52, ProductKind: productport.ExternalPushServicePeriod, Enabled: true, ConfigurationReference: "service-period-notify-52", Revision: 1, UpdatedAt: updated}},
		receipts: map[string]Receipt{},
	}
	effects := &commerceExternalPushTestEffects{result: productport.ExternalPushTest{ProductID: 52, ProductKind: productport.ExternalPushServicePeriod, EffectID: "eer_1", State: "accepted", CreatedAt: updated}}
	service, _ := newCommerceExternalPushTestService(store, effects)
	command := productport.QueueExternalPushTestCommand{ProductID: 52, ProductKind: productport.ExternalPushServicePeriod, Actor: 9, IdempotencyKey: "commerce-push-test-0001"}
	first, err := service.QueueExternalPushTest(context.Background(), command)
	if err != nil || first.EffectID != "eer_1" || first.State != "accepted" || first.ProviderAccepted || first.DeliveryProven || first.RealExternalCallExecuted || first.AutoRetryAllowed || len(store.tests) != 1 || effects.calls != 1 {
		t.Fatalf("first=%#v tests=%#v effects=%d err=%v", first, store.tests, effects.calls, err)
	}
	if effects.inputs[0].ProductID != 52 || effects.inputs[0].ProductKind != productport.ExternalPushServicePeriod || effects.inputs[0].ConfigurationReference != "service-period-notify-52" || effects.inputs[0].ConfigurationRevision != 1 || effects.inputs[0].ReceiptKeyDigest == ([32]byte{}) {
		t.Fatalf("effect input=%#v", effects.inputs[0])
	}
	replayed, err := service.QueueExternalPushTest(context.Background(), command)
	if err != nil || !reflect.DeepEqual(replayed, first) || len(store.tests) != 1 || effects.calls != 1 {
		t.Fatalf("replayed=%#v tests=%d effects=%d err=%v", replayed, len(store.tests), effects.calls, err)
	}
	command.IdempotencyKey = "commerce-push-test-different-key"
	second, err := service.QueueExternalPushTest(context.Background(), command)
	if err != nil || second.EffectID != "eer_1" || len(store.tests) != 2 || effects.calls != 2 {
		t.Fatalf("explicit new test=%#v err=%v tests=%d effects=%d", second, err, len(store.tests), effects.calls)
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
	store.configs[61] = productport.ExternalPushConfiguration{ProductID: 61, ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "commerce-push-config-61", Revision: 1, UpdatedAt: time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)}
	command.IdempotencyKey = "commerce-push-test-0003"
	effects.result = productport.ExternalPushTest{ProductID: 61, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_74", State: "accepted", ProviderAccepted: true, CreatedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	if _, err := service.QueueExternalPushTest(context.Background(), command); !errors.Is(err, ErrUnavailable) || effects.calls != 1 || len(store.tests) != 0 {
		t.Fatalf("delivery claim error=%v effects=%d tests=%d", err, effects.calls, len(store.tests))
	}
}

func TestCommerceExternalPushTimelineUsesOutboundStatusWithoutClaimingDelivery(t *testing.T) {
	updated := time.Date(2026, 9, 6, 4, 5, 6, 0, time.UTC)
	received := true
	store := &commerceExternalPushTestStore{
		products: map[productport.ID]productport.ExternalPushProductKind{71: productport.ExternalPushWeChatPay},
		tests:    []productport.ExternalPushTest{{ProductID: 71, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_71", State: "accepted", CreatedAt: updated}},
		statuses: map[string]productport.ExternalPushTestStatus{"eer_71": {EffectID: "eer_71", State: "provider_accepted", AttemptCount: 1, ProviderCallAttempted: true, RealExternalCallExecuted: true, ProviderResultReceived: &received, UpdatedAt: updated.Add(time.Minute)}},
	}
	service, _ := newCommerceExternalPushTestService(store, &commerceExternalPushTestEffects{})
	items, err := service.ListExternalPushTests(context.Background(), 71, productport.ExternalPushWeChatPay)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	item := items[0]
	if item.State != "provider_accepted" || item.AttemptCount != 1 || !item.ProviderAccepted || !item.RealExternalCallExecuted || item.DeliveryProven || item.AutoRetryAllowed || !item.UpdatedAt.Equal(updated.Add(time.Minute)) {
		t.Fatalf("unsafe status projection=%#v", item)
	}
	store.statuses["eer_71"] = productport.ExternalPushTestStatus{EffectID: "eer_71", State: "outcome_unknown", AttemptCount: 1, ProviderCallAttempted: true, RealExternalCallExecuted: true, UpdatedAt: updated.Add(2 * time.Minute)}
	items, err = service.ListExternalPushTests(context.Background(), 71, productport.ExternalPushWeChatPay)
	if err != nil || len(items) != 1 || items[0].ProviderAccepted || items[0].DeliveryProven || items[0].AutoRetryAllowed || items[0].State != "outcome_unknown" {
		t.Fatalf("unknown status must require reconciliation items=%#v err=%v", items, err)
	}
}

var _ CommerceExternalPushStore = (*commerceExternalPushTestStore)(nil)
var _ ProductExternalPushEffectAccepter = (*commerceExternalPushTestEffects)(nil)
var _ productport.ExternalPushTestStatusReader = (*commerceExternalPushTestStore)(nil)
var _ productport.ExternalPushTestStatusReader = commerceExternalPushTestStatuses{}
