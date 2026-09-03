package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLAudienceReceiptReplayAndPayloadDrift(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	result := json.RawMessage(`{"package_id":1}`)
	reservation := reservationFor("stable-key", json.RawMessage(`{"code":"new-customers"}`), now)
	err = uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := repo.Reserve(tx, reservation)
		if reserveErr != nil || !owned {
			return reserveErr
		}
		_, reserveErr = repo.Complete(tx, receipt.ID, result, now)
		return reserveErr
	})
	if err != nil {
		t.Fatal(err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		replayed, owned, replayErr := repo.Reserve(tx, reservation)
		if replayErr == nil && (owned || replayed.State != "completed" || string(replayed.ResultSnapshot) != string(result)) {
			t.Fatalf("replay=%+v owned=%v", replayed, owned)
		}
		return replayErr
	})
	if err != nil {
		t.Fatal(err)
	}
	drift := reservation
	drift.PayloadDigest = sha256.Sum256([]byte(`{"code":"different"}`))
	err = uow.Within(ctx, func(tx context.Context) error {
		_, _, driftErr := repo.Reserve(tx, drift)
		return driftErr
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("payload drift=%v", err)
	}
}
