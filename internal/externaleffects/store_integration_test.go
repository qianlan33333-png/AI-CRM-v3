package externaleffects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func TestPostgreSQLEffectReplayUnknownAndStaleWorker(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely[EffectJobArgs](workers, NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	command := AcceptCommand{ReceiptKey: digestForTest("accept-key"), Envelope: envelopeForTest()}
	first, firstReceipt, err := repository.AcceptAndQueue(ctx, command)
	if err != nil || first.State != StateQueued {
		t.Fatalf("accept+queue=%+v %v", first, err)
	}
	second, secondReceipt, err := repository.AcceptAndQueue(ctx, command)
	if err != nil || second.ID != first.ID || secondReceipt != firstReceipt {
		t.Fatalf("exact replay projection=%+v receipt=%+v want=%+v err=%v", second, secondReceipt, firstReceipt, err)
	}
	concurrent := make(chan Receipt, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			_, receipt, acceptErr := repository.AcceptAndQueue(ctx, command)
			if acceptErr != nil {
				failures <- acceptErr
				return
			}
			concurrent <- receipt
		}()
	}
	for range 2 {
		select {
		case acceptErr := <-failures:
			t.Fatalf("concurrent replay=%v", acceptErr)
		case receipt := <-concurrent:
			if receipt != firstReceipt {
				t.Fatalf("concurrent receipt=%+v want=%+v", receipt, firstReceipt)
			}
		}
	}
	drift := command
	drift.Envelope.PayloadDigest = digestForTest("other")
	if _, _, err = repository.AcceptAndQueue(ctx, drift); !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("payload drift=%v", err)
	}
	var oldJob int64
	if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=1 AND generation=1`).Scan(&oldJob); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE external_effects SET state='retryable_failed' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.Retry(ctx, ControlCommand{EffectID: first.ID, ReceiptKey: digestForTest("retry")}); err != nil {
		t.Fatal(err)
	}
	if err = repository.RunAttempt(ctx, 1, 1, oldJob, nil); err != nil {
		t.Fatal(err)
	}
	current, err := repository.Get(ctx, first.ID)
	if err != nil || current.State != StateQueued || current.Generation != 2 {
		t.Fatalf("stale worker changed current=%+v err=%v", current, err)
	}
	var currentJob int64
	if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=1 AND generation=2`).Scan(&currentJob); err != nil {
		t.Fatal(err)
	}
	if err = repository.RunAttempt(ctx, 1, 2, currentJob, nil); err != nil {
		t.Fatal(err)
	}
	current, err = repository.Get(ctx, first.ID)
	if err != nil || current.State != StateFinalFailed {
		t.Fatalf("disabled provider must final-fail without external call: %+v %v", current, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE external_effects SET state='outcome_unknown',attempt_count=1,lease_fence=7,lease_expires_at=clock_timestamp() WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE external_effect_attempts SET state='outcome_unknown',fence=7 WHERE effect_id=1 AND number=1 AND generation=2`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.Retry(ctx, ControlCommand{EffectID: first.ID, ReceiptKey: digestForTest("forbidden")}); !errors.Is(err, ErrTransition) {
		t.Fatalf("unknown retry=%v", err)
	}
}

func effectIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, configErr := platformconfig.DatabaseURL()
	if configErr != nil {
		t.Skip("DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	bytes := make([]byte, 8)
	if _, err = rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_effects_test_" + hex.EncodeToString(bytes)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0005_external_effects.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
