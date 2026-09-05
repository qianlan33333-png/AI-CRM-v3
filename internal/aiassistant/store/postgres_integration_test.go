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
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type integrationCustomers struct{}

func (integrationCustomers) CustomerSnapshot(_ context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	return aiassistantapp.CustomerSnapshot{CanonicalID: id, Status: customerdomain.StatusActive, DisplayName: "customer", OneIDLabel: "OneID"}, nil
}

type integrationStaff struct{}

func (integrationStaff) StaffSnapshot(_ context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	return aiassistantapp.StaffSnapshot{ID: id, DisplayName: "staff", Active: true}, nil
}

type integrationMaterials struct{}

func (integrationMaterials) ResolveMaterial(_ context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	return block, nil
}
func (integrationMaterials) RegisterMaterialReference(context.Context, aiassistantport.ContentBlock, effectport.Digest) error {
	return nil
}

type integrationIdentities struct{}

func (integrationIdentities) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}

type mutableIntegrationIdentities struct {
	result identityport.ResolveResult
}

func (r *mutableIntegrationIdentities) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return r.result, nil
}

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

func TestPostgreSQLPlanSizesAndFiftyRecipientPagination(t *testing.T) {
	native, cleanup := integrationPool(t)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 60*time.Second)
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
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 50, 51, 5000} {
		recipients := make([]aiassistantport.RecipientCandidate, size)
		for index := range recipients {
			recipients[index] = aiassistantport.RecipientCandidate{CustomerID: customerdomain.CustomerID(index + 1), StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}}}
		}
		created, createErr := service.CreatePlan(context.Background(), aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: 7}, IdempotencyKey: "size-plan-" + strconv.Itoa(size), Name: "size plan", SourceKind: "test", SourceDigest: effectport.Hash("source", strconv.Itoa(size)), Recipients: recipients, OccurredAt: time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)})
		if createErr != nil {
			t.Fatalf("size=%d create: %v", size, createErr)
		}
		if size == 51 {
			// Exercise the real owner reader across its 50-row boundary with
			// mixed terminal facts. Automation consumes only this stable Port.
			if _, err = native.Exec(context.Background(), `UPDATE ai_assistant_plan_recipients
				SET execution_state=CASE WHEN id=(SELECT max(id) FROM ai_assistant_plan_recipients WHERE plan_id=$1) THEN 'outcome_unknown' ELSE 'retryable_failed' END
				WHERE plan_id=$1`, created.Plan.ID); err != nil {
				t.Fatalf("size=%d set mixed execution states: %v", size, err)
			}
		}
		seen, unknown, retryable, cursor := 0, 0, 0, ""
		for {
			page, pageErr := service.ListRecipients(context.Background(), aiassistantport.RecipientPageQuery{PlanID: created.Plan.ID, Limit: 50, Cursor: cursor})
			if pageErr != nil {
				t.Fatalf("size=%d page: %v", size, pageErr)
			}
			if len(page.Items) == 0 || len(page.Items) > 50 {
				t.Fatalf("size=%d invalid page length=%d", size, len(page.Items))
			}
			seen += len(page.Items)
			for _, recipient := range page.Items {
				switch recipient.ExecutionState {
				case aiassistantport.ExecutionOutcomeUnknown:
					unknown++
				case aiassistantport.ExecutionRetryableFailed:
					retryable++
				}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if seen != size || created.Plan.TargetCount != size || created.Plan.PendingCount != size {
			t.Fatalf("size=%d seen=%d plan=%+v", size, seen, created.Plan)
		}
		if size == 51 && (unknown != 1 || retryable != 50) {
			t.Fatalf("mixed second-page states unknown=%d retryable=%d", unknown, retryable)
		}
	}
}

func TestPostgreSQLCreatePlanWithinRequiresCallerTransactionAndRollsBackWithIt(t *testing.T) {
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
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, integrationIdentities{})
	if err != nil {
		t.Fatal(err)
	}
	command := aiassistantport.CreatePlanCommand{Actor: aiassistantport.Actor{Kind: aiassistantport.ActorAdmin, ID: 7}, IdempotencyKey: "within-plan-rollback-0001", Name: "within plan", SourceKind: "automation.manual_audience_run.v1", SourceDigest: effectport.Hash("source", "within"), Recipients: []aiassistantport.RecipientCandidate{{CustomerID: 1, StaffID: 21, Content: []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}}}}, OccurredAt: time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)}
	if _, err = service.CreatePlanWithin(context.Background(), command); !errors.Is(err, aiassistantapp.ErrUnavailable) {
		t.Fatalf("unbound CreatePlanWithin err=%v", err)
	}
	rollback := errors.New("rollback caller transaction")
	err = uow.Within(context.Background(), func(tx context.Context) error {
		created, createErr := service.CreatePlanWithin(tx, command)
		if createErr != nil || created.Plan.ID < 1 {
			t.Fatalf("within create=%+v err=%v", created, createErr)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback err=%v", err)
	}
	var count int
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM ai_assistant_plans`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back plans=%d err=%v", count, err)
	}
	var first aiassistantport.CreatePlanResult
	if err = uow.Within(context.Background(), func(tx context.Context) error {
		var createErr error
		first, createErr = service.CreatePlanWithin(tx, command)
		return createErr
	}); err != nil || first.Plan.ID < 1 || first.Replayed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	var replay aiassistantport.CreatePlanResult
	if err = uow.Within(context.Background(), func(tx context.Context) error {
		var createErr error
		replay, createErr = service.CreatePlanWithin(tx, command)
		return createErr
	}); err != nil || !replay.Replayed || replay.Plan.ID != first.Plan.ID {
		t.Fatalf("replay=%+v first=%+v err=%v", replay, first, err)
	}
}

func TestPostgreSQLIntegrationNonceAllowsOnlyExactReplay(t *testing.T) {
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
	at := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	payload := sha256.Sum256([]byte("same payload"))
	for iteration := 0; iteration < 2; iteration++ {
		if err = uow.Within(context.Background(), func(tx context.Context) error {
			return repository.ReserveIntegrationNonce(tx, "integration-key", "1234567890abcdef", "idem-key-1", payload, at, at.Add(5*time.Minute))
		}); err != nil {
			t.Fatalf("exact replay %d: %v", iteration, err)
		}
	}
	drift := sha256.Sum256([]byte("changed payload"))
	err = uow.Within(context.Background(), func(tx context.Context) error {
		return repository.ReserveIntegrationNonce(tx, "integration-key", "1234567890abcdef", "idem-key-1", drift, at, at.Add(5*time.Minute))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("drift err=%v", err)
	}
}

func TestPostgreSQLIntegrationReplayDoesNotResolveIdentityAgain(t *testing.T) {
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
	identities := &mutableIntegrationIdentities{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(91)}}
	service, err := aiassistantapp.NewService(uow, repository, integrationCustomers{}, integrationStaff{}, integrationMaterials{}, identities)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	command := aiassistantapp.IdentityPlanCommand{
		Actor:          aiassistantport.Actor{Kind: aiassistantport.ActorService, ID: 7},
		IdempotencyKey: "integration-replay-1",
		Name:           "identity plan",
		SourceKind:     "automation",
		SourceDigest:   effectport.Hash("identity-plan"),
		Targets: []aiassistantapp.IdentityTarget{{
			Reference: identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:corp-1", Value: "external-1", Assurance: identitydomain.AssuranceVerified, Source: "test"},
			StaffID:   21,
			Content:   []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "hello"}},
		}},
		OccurredAt: at, IntegrationKey: "integration-key", Nonce: "1234567890abcdef", ExpiresAt: at.Add(5 * time.Minute),
	}
	first, err := service.CreatePlanFromIdentities(context.Background(), command)
	if err != nil || first.Replayed || first.Found != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	identities.result = identityport.ResolveResult{Status: identityport.ResolveConflict}
	replayed, err := service.CreatePlanFromIdentities(context.Background(), command)
	if err != nil || !replayed.Replayed || replayed.Plan.ID != first.Plan.ID || replayed.Found != 1 || replayed.Conflicted != 0 {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	changed := command
	changed.Name = "changed identity plan"
	if _, err = service.CreatePlanFromIdentities(context.Background(), changed); !errors.Is(err, aiassistantapp.ErrConflict) {
		t.Fatalf("changed payload err=%v", err)
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
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0036_ai_assistant_review.sql"))
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
