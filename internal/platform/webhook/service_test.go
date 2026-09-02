package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

type fakeStore struct {
	delivery Delivery
	created  bool
}

func (store *fakeStore) PutIfAbsent(_ context.Context, delivery Delivery) (Delivery, bool, error) {
	if store.delivery.ID == 0 {
		store.delivery = delivery
	}
	return store.delivery, store.created, nil
}

func (*fakeStore) Claim(context.Context, Claim) ([]Delivery, error) {
	return nil, nil
}

func (store *fakeStore) Complete(_ context.Context, _ Completion) (Delivery, error) {
	return store.delivery, nil
}

func TestIngestDetectsReplayAndPayloadDrift(t *testing.T) {
	key, _ := idempotency.Parse("wecom:event:0001")
	hash, _ := idempotency.CanonicalPayloadHash(json.RawMessage(`{"event":"change"}`))
	store := &fakeStore{delivery: Delivery{
		ID:             42,
		Provider:       "wecom",
		IdempotencyKey: key,
		PayloadHash:    hash,
	}}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Ingest(context.Background(), Ingest{
		Provider:       "wecom",
		IdempotencyKey: key,
		Payload:        json.RawMessage(`{ "event": "change" }`),
	})
	if err != nil || !result.Replay {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	_, err = service.Ingest(context.Background(), Ingest{
		Provider:       "wecom",
		IdempotencyKey: key,
		Payload:        json.RawMessage(`{"event":"different"}`),
	})
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("expected ErrPayloadMismatch, got %v", err)
	}
}

func TestClaimValidation(t *testing.T) {
	service, _ := NewService(&fakeStore{})
	_, err := service.Claim(context.Background(), Claim{Owner: "worker", Limit: 0, LeaseDuration: time.Minute})
	if !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expected ErrInvalidClaim, got %v", err)
	}
}
