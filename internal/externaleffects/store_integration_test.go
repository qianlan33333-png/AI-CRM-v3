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

type integrationAdapter struct {
	calls  int
	result AdapterResult
	err    error
}

func (a *integrationAdapter) Execute(context.Context, Envelope, Attempt) (AdapterResult, error) {
	a.calls++
	return a.result, a.err
}

func TestPostgreSQLAttemptedLeaseRecoveryDoesNotRepeatProviderCall(t *testing.T) {
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

	active, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: digestForTest("active-lease"), Envelope: envelopeForTest()})
	if err != nil {
		t.Fatal(err)
	}
	activeID, err := parseEffectID(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeJob := markAttemptedAfterCrash(t, pool, activeID, "5 minutes")
	activeAdapter := &integrationAdapter{}
	err = repository.RunAttempt(ctx, activeID, 1, activeJob, activeAdapter)
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) || snooze.Duration <= 0 {
		t.Fatalf("active lease error=%v snooze=%+v", err, snooze)
	}
	if activeAdapter.calls != 0 {
		t.Fatalf("active lease repeated provider call=%d", activeAdapter.calls)
	}

	expiredEnvelope := envelopeForTest()
	expiredEnvelope.PayloadDigest = digestForTest("expired-lease-payload")
	expired, _, err := repository.AcceptAndQueue(ctx, AcceptCommand{ReceiptKey: digestForTest("expired-lease"), Envelope: expiredEnvelope})
	if err != nil {
		t.Fatal(err)
	}
	expiredID, err := parseEffectID(expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	expiredJob := markAttemptedAfterCrash(t, pool, expiredID, "-1 minute")
	expiredAdapter := &integrationAdapter{}
	if err = repository.RunAttempt(ctx, expiredID, 1, expiredJob, expiredAdapter); err != nil {
		t.Fatalf("expired lease recovery=%v", err)
	}
	if expiredAdapter.calls != 0 {
		t.Fatalf("expired lease repeated provider call=%d", expiredAdapter.calls)
	}
	current, err := repository.Get(ctx, expired.ID)
	if err != nil || current.State != StateUnknown {
		t.Fatalf("expired lease state=%+v err=%v", current, err)
	}
	var attemptState, receipt string
	var callAttempted bool
	if err = pool.QueryRow(ctx, `SELECT state,receipt_digest,call_attempted FROM external_effect_attempts WHERE effect_id=$1 AND number=1 AND generation=1 AND fence=1`, expiredID).Scan(&attemptState, &receipt, &callAttempted); err != nil {
		t.Fatal(err)
	}
	wantRecovery := Hash("attempt-lease-expired", effectID(expiredID), "1", "1", "1")
	if State(attemptState) != StateUnknown || Digest(receipt) != wantRecovery || callAttempted {
		t.Fatalf("recovery fact state=%q receipt=%q call_attempted=%t", attemptState, receipt, callAttempted)
	}
	if _, _, err = repository.Retry(ctx, ControlCommand{EffectID: expired.ID, ReceiptKey: digestForTest("expired-retry")}); !errors.Is(err, ErrTransition) {
		t.Fatalf("unknown recovery retried: %v", err)
	}
}

func TestPostgreSQLProviderAttemptErrorBecomesUnknown(t *testing.T) {
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
	envelope := envelopeForTest()
	envelope.PayloadDigest = digestForTest("provider-error-payload")
	projection, _, err := repository.AcceptAndQueue(context.Background(), AcceptCommand{ReceiptKey: digestForTest("provider-error"), Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	id, err := parseEffectID(projection.ID)
	if err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err = pool.QueryRow(context.Background(), `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, id).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	adapter := &integrationAdapter{result: AdapterResult{CallAttempted: true}, err: errors.New("provider connection dropped after request")}
	if err = repository.RunAttempt(context.Background(), id, 1, jobID, adapter); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 {
		t.Fatalf("provider calls=%d", adapter.calls)
	}
	current, err := repository.Get(context.Background(), projection.ID)
	if err != nil || current.State != StateUnknown {
		t.Fatalf("provider attempt result=%+v err=%v", current, err)
	}
	var callAttempted bool
	if err = pool.QueryRow(context.Background(), `SELECT call_attempted FROM external_effect_attempts WHERE effect_id=$1 AND number=1 AND generation=1`, id).Scan(&callAttempted); err != nil || !callAttempted {
		t.Fatalf("provider call fact attempted=%t err=%v", callAttempted, err)
	}
	if _, _, err = repository.Retry(context.Background(), ControlCommand{EffectID: projection.ID, ReceiptKey: digestForTest("provider-error-retry")}); !errors.Is(err, ErrTransition) {
		t.Fatalf("unknown provider error retried: %v", err)
	}
}

func TestPostgreSQLQueuedEffectRunsWhenRuntimeStartsLater(t *testing.T) {
	pool, cleanup := effectIntegrationPool(t)
	defer cleanup()
	workers := river.NewWorkers()
	worker := NewWorker(nil, nil)
	if err := river.AddWorkerSafely[EffectJobArgs](workers, worker); err != nil {
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
	projection, _, err := repository.AcceptAndQueue(context.Background(), AcceptCommand{ReceiptKey: digestForTest("runtime-later"), Envelope: envelopeForTest()})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.BindRepository(repository); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(runContext) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		current, getErr := repository.Get(context.Background(), projection.ID)
		if getErr == nil && current.State == StateFinalFailed {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("queued effect was not processed after runtime start: state=%+v err=%v", current, getErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatalf("runtime stop=%v", err)
	}
}

func markAttemptedAfterCrash(t *testing.T, pool *pgxpool.Pool, effectID int64, lease string) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE external_effects SET state='attempted',attempt_count=1,lease_fence=1,lease_expires_at=clock_timestamp()+$2::interval,updated_at=clock_timestamp() WHERE id=$1 AND state='queued'`, effectID, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state) VALUES($1,1,1,1,'attempted')`, effectID); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, effectID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
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
