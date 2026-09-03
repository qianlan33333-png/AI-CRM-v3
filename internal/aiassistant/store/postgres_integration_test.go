package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLPlanReceiptAuditOutboxAtomicJourney(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	aggregate, err := aiassistantdomain.NewPlan("retention review", "automation", effectport.Hash("source", "1"), 2, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	recipients := []aiassistantport.RecipientCandidate{
		{CustomerID: customerdomain.CustomerID(11), StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello 11"}}},
		{CustomerID: customerdomain.CustomerID(12), StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello 12"}}},
	}
	keyDigest, payloadDigest := sha256.Sum256([]byte("key")), sha256.Sum256([]byte("payload"))
	var plan aiassistantport.Plan
	err = uow.Within(context.Background(), func(tx context.Context) error {
		receipt, created, reserveErr := repository.Reserve(tx, Reservation{Operation: "create", ActorScope: "service:7", KeyDigest: keyDigest, PayloadDigest: payloadDigest, CreatedAt: now})
		if reserveErr != nil || !created {
			return reserveErr
		}
		var createErr error
		plan, _, createErr = repository.CreatePlan(tx, aggregate, recipients, 7, now)
		if createErr != nil {
			return createErr
		}
		if appendErr := repository.AppendEvent(tx, aiassistantport.Event{Type: aiassistantport.EventPlanCreated, AggregateID: plan.ID, ActorID: 7, IdempotencyKey: "event-plan-1", Payload: []byte(`{"plan_id":1}`), OccurredAt: now}); appendErr != nil {
			return appendErr
		}
		_, completeErr := repository.Complete(tx, receipt.ID, []byte(`{"plan_id":1}`), now)
		return completeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	var plans, recipientCount, contents, receipts, audits, outbox, effects int
	err = native.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM ai_assistant_plans),
		(SELECT count(*) FROM ai_assistant_plan_recipients),
		(SELECT count(*) FROM ai_assistant_content_versions),
		(SELECT count(*) FROM ai_assistant_operation_receipts),
		(SELECT count(*) FROM ai_assistant_audit_events),
		(SELECT count(*) FROM ai_assistant_outbox),
		(SELECT count(*) FROM ai_assistant_effect_bindings)`).Scan(&plans, &recipientCount, &contents, &receipts, &audits, &outbox, &effects)
	if err != nil {
		t.Fatal(err)
	}
	if plans != 1 || recipientCount != 2 || contents != 2 || receipts != 1 || audits != 1 || outbox != 1 || effects != 0 {
		t.Fatalf("plans=%d recipients=%d contents=%d receipts=%d audits=%d outbox=%d effects=%d", plans, recipientCount, contents, receipts, audits, outbox, effects)
	}
	rollback := errors.New("inject rollback")
	err = uow.Within(context.Background(), func(tx context.Context) error {
		other, newErr := aiassistantdomain.NewPlan("rolled back", "automation", effectport.Hash("source", "2"), 1, 7, now)
		if newErr != nil {
			return newErr
		}
		if _, _, createErr := repository.CreatePlan(tx, other, recipients[:1], 7, now); createErr != nil {
			return createErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_plans`).Scan(&plans); err != nil || plans != 1 {
		t.Fatalf("rolled-back plan visible count=%d err=%v", plans, err)
	}
	if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_content_versions SET version=version`); err == nil {
		t.Fatal("content history accepted mutation")
	}
	if plan.State != aiassistantport.PlanPendingReview {
		t.Fatalf("state=%s", plan.State)
	}
}

func integrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping AI Assistant PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_aiassistant_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0028_ai_assistant_review.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
