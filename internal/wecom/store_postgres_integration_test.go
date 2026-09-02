package wecom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

func TestPostgreSQLWeComStoresIntegration(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("oauth state nonce and transaction boundary", func(t *testing.T) {
		store := NewPostgreSQLOAuthStateStore()
		now := time.Now().UTC()
		stateDigest := sha256.Sum256([]byte("state"))
		nonceDigest := sha256.Sum256([]byte("nonce"))
		state := OAuthState{Purpose: OAuthSidebar, Redirect: "/sidebar", ExpiresAt: now.Add(time.Minute)}
		if err := store.Create(ctx, state, stateDigest, nonceDigest); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("transaction boundary error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Create(txContext, state, stateDigest, nonceDigest)
		}); err != nil {
			t.Fatal(err)
		}
		wrongNonce := sha256.Sum256([]byte("wrong nonce"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, wrongNonce, now)
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("wrong nonce error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			consumed, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, nonceDigest, now)
			if consumeErr == nil && consumed.Redirect != "/sidebar" {
				return errors.New("unexpected consumed redirect")
			}
			return consumeErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, nonceDigest, now)
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("state replay error=%v", err)
		}
		expiredState := sha256.Sum256([]byte("expired state"))
		expiredNonce := sha256.Sum256([]byte("expired nonce"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Create(txContext, OAuthState{Purpose: OAuthAdmin, Redirect: "/admin", ExpiresAt: now.Add(time.Second)}, expiredState, expiredNonce)
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthAdmin, expiredState, expiredNonce, now.Add(2*time.Second))
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("expired state error=%v", err)
		}
	})

	t.Run("follow relationships are employee scoped and same-second deletion wins", func(t *testing.T) {
		var customerID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customers (status) VALUES ('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		store := NewPostgreSQLFollowRelationshipStore()
		base := time.Unix(1_788_336_000, 0).UTC()
		first := CallbackFollowRelationship{CallbackID: "callback-100", CorpID: "wx-corp", EmployeeID: "employee-one", CustomerID: customerdomain.CustomerID(customerID), Active: true, OccurredAt: base}
		if _, err := store.ApplyCallbackEvent(ctx, first); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("relationship transaction boundary error=%v", err)
		}
		var firstApplication FollowRelationshipApplication
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var applyErr error
			firstApplication, applyErr = store.ApplyCallbackEvent(txContext, first)
			return applyErr
		}); err != nil || !firstApplication.Applied || !firstApplication.Active {
			t.Fatalf("first application=%+v err=%v", firstApplication, err)
		}
		second := first
		second.CallbackID = "callback-101"
		second.EmployeeID = "employee-two"
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, second)
			if applyErr == nil && (!application.Applied || !application.Active) {
				return errors.New("second employee was not activated")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}

		deleteOne := first
		deleteOne.CallbackID = "callback-200"
		deleteOne.Active = false
		deleteOne.OccurredAt = base.Add(20 * time.Second)
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, deleteOne)
			if applyErr == nil && (!application.Applied || application.Active) {
				return errors.New("employee-one was not deactivated")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
		lateAdd := first
		lateAdd.CallbackID = "callback-150"
		lateAdd.OccurredAt = base.Add(10 * time.Second)
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, lateAdd)
			if applyErr == nil && (application.Applied || application.Active) {
				return errors.New("older add reactivated employee-one")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			active, activeErr := store.IsActive(txContext, "wx-corp", "employee-two", customerdomain.CustomerID(customerID))
			if activeErr == nil && !active {
				return errors.New("employee-one deletion changed employee-two")
			}
			return activeErr
		}); err != nil {
			t.Fatal(err)
		}

		sameSecondDelete := second
		sameSecondDelete.CallbackID = "callback-delete-same-second"
		sameSecondDelete.Active = false
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, sameSecondDelete)
			if applyErr == nil && (!application.Applied || application.Active) {
				return errors.New("same-second deletion did not win")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
		sameSecondAdd := second
		sameSecondAdd.CallbackID = "zzzz-callback-add-hash-order-must-not-win"
		if err := unit.Within(ctx, func(txContext context.Context) error {
			application, applyErr := store.ApplyCallbackEvent(txContext, sameSecondAdd)
			if applyErr == nil && (application.Applied || application.Active) {
				return errors.New("same-second callback hash reactivated relationship")
			}
			return applyErr
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown delete cursor rejects older add before OneID exists", func(t *testing.T) {
		store := NewPostgreSQLFollowRelationshipStore()
		identityDigest := sha256.Sum256([]byte("protected external identity"))
		deleted := CallbackExternalContactEvent{CallbackID: "delete-newer", CorpID: "wx-corp", EmployeeID: "employee-one", ExternalIdentityDigest: identityDigest, Active: false, OccurredAt: time.Unix(400, 0).UTC()}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			admission, admitErr := store.AdmitExternalContactEvent(txContext, deleted)
			if admitErr == nil && (!admission.Admitted || !admission.Advanced || admission.Active) {
				return errors.New("unknown delete cursor was not retained")
			}
			return admitErr
		}); err != nil {
			t.Fatal(err)
		}
		olderAdd := deleted
		olderAdd.CallbackID = "add-older"
		olderAdd.Active = true
		olderAdd.OccurredAt = time.Unix(300, 0).UTC()
		if err := unit.Within(ctx, func(txContext context.Context) error {
			admission, admitErr := store.AdmitExternalContactEvent(txContext, olderAdd)
			if admitErr == nil && (admission.Admitted || admission.Active) {
				return errors.New("older activation passed delete cursor")
			}
			return admitErr
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("first identity cursor insert is concurrent idempotent", func(t *testing.T) {
		store := NewPostgreSQLFollowRelationshipStore()
		identityDigest := sha256.Sum256([]byte("concurrent protected identity"))
		const workers = 16
		errorsFound := make(chan error, workers)
		var group sync.WaitGroup
		for index := 0; index < workers; index++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				event := CallbackExternalContactEvent{
					CallbackID: "concurrent-callback-" + strconv.Itoa(worker), CorpID: "wx-corp",
					EmployeeID: "employee-concurrent", ExternalIdentityDigest: identityDigest,
					Active: true, OccurredAt: time.Unix(500, 0).UTC(),
				}
				errorsFound <- unit.Within(ctx, func(txContext context.Context) error {
					admission, admitErr := store.AdmitExternalContactEvent(txContext, event)
					if admitErr == nil && !admission.Admitted {
						return errors.New("equivalent same-second event was not admitted")
					}
					return admitErr
				})
			}(index)
		}
		group.Wait()
		close(errorsFound)
		for err := range errorsFound {
			if err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_external_contact_event_cursors WHERE corp_id='wx-corp' AND employee_id='employee-concurrent' AND external_identity_digest=$1`, identityDigest[:]).Scan(&count); err != nil || count != 1 {
			t.Fatalf("cursor rows=%d err=%v", count, err)
		}
	})

	t.Run("callback receipts are immutable concurrent-idempotent and retry intent is exact", func(t *testing.T) {
		store := NewPostgreSQLCallbackReceiptStore()
		processedInboxID := insertWeComInbox(t, ctx, pool, "receipt-processed", "processing", 1)
		processed := AppendCallbackProcessingReceipt{
			InboxID: processedInboxID, AttemptNumber: 1,
			CommandDigest: sha256.Sum256([]byte("processed-command")),
			EventType:     "change_external_contact", ChangeType: "add_external_contact",
			ResultingInboxStatus: "processed",
			ResultCodes:          []CallbackResultCode{CallbackRelationshipActivated, CallbackCustomerCreated},
		}
		if _, _, err := store.AppendProcessing(ctx, processed); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("processing receipt transaction boundary error=%v", err)
		}
		var processedReceipt CallbackReceipt
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var created bool
			var appendErr error
			processedReceipt, created, appendErr = store.AppendProcessing(txContext, processed)
			if appendErr == nil && !created {
				return errors.New("first processing receipt was a replay")
			}
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}
		if processedReceipt.EventType != "change_external_contact" {
			t.Fatalf("event type=%q", processedReceipt.EventType)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, appendErr := store.AppendProcessing(txContext, processed)
			if appendErr == nil && created {
				return errors.New("processing replay inserted a second fact")
			}
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}
		drift := processed
		drift.ResultCodes = []CallbackResultCode{CallbackCustomerResolved}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, appendErr := store.AppendProcessing(txContext, drift)
			return appendErr
		}); !errors.Is(err, ErrCallbackReceiptConflict) {
			t.Fatalf("processing drift error=%v", err)
		}

		failedInboxID := insertWeComInbox(t, ctx, pool, "receipt-failed", "failed", 2)
		failed := AppendCallbackProcessingReceipt{
			InboxID: failedInboxID, AttemptNumber: 2,
			CommandDigest: sha256.Sum256([]byte("failed-command")),
			EventType:     "change_external_contact", ChangeType: "edit_external_contact",
			ResultingInboxStatus: "failed", ResultCodes: []CallbackResultCode{CallbackFailedTerminal},
			ErrorCode: "identity_conflict_terminal",
		}
		var failedReceipt CallbackReceipt
		if err := unit.Within(ctx, func(txContext context.Context) error {
			var appendErr error
			failedReceipt, _, appendErr = store.AppendProcessing(txContext, failed)
			return appendErr
		}); err != nil {
			t.Fatal(err)
		}
		actorID := insertAdminUser(t, ctx, pool)
		retry := BeginCallbackRetry{
			TargetReceiptID: failedReceipt.ID, ExpectedAttempt: 2,
			ExpectedInboxStatus: "failed", ActorAdminUserID: actorID,
			Reason:             "configuration corrected",
			OperationKeyDigest: sha256.Sum256([]byte("retry-key")),
			CommandDigest:      sha256.Sum256([]byte("retry-command")),
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, beginErr := store.BeginRetry(txContext, retry)
			if beginErr == nil && !created {
				return errors.New("first retry intent was a replay")
			}
			// Production composition must call platform webhook Retry CAS here in
			// this same UOW. This Store test exercises only the WeCom-owned fact.
			return beginErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, created, beginErr := store.BeginRetry(txContext, retry)
			if beginErr == nil && created {
				return errors.New("retry replay inserted a second operation")
			}
			return beginErr
		}); err != nil {
			t.Fatal(err)
		}
		retryDrift := retry
		retryDrift.Reason = "different reason"
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, _, beginErr := store.BeginRetry(txContext, retryDrift)
			return beginErr
		}); !errors.Is(err, ErrCallbackReceiptConflict) {
			t.Fatalf("retry drift error=%v", err)
		}

		concurrentInboxID := insertWeComInbox(t, ctx, pool, "receipt-concurrent", "processing", 1)
		concurrent := processed
		concurrent.InboxID = concurrentInboxID
		concurrent.CommandDigest = sha256.Sum256([]byte("concurrent-command"))
		type concurrentResult struct {
			created bool
			err     error
		}
		results := make(chan concurrentResult, 2)
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				var created bool
				err := unit.Within(ctx, func(txContext context.Context) error {
					var appendErr error
					_, created, appendErr = store.AppendProcessing(txContext, concurrent)
					return appendErr
				})
				results <- concurrentResult{created: created, err: err}
			}()
		}
		wait.Wait()
		close(results)
		createdCount := 0
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.created {
				createdCount++
			}
		}
		if createdCount != 1 {
			t.Fatalf("concurrent created count=%d", createdCount)
		}
		if _, err := pool.Native().Exec(ctx, `UPDATE wecom_callback_receipts SET error_code='tampered' WHERE id=$1`, processedReceipt.ID); err == nil {
			t.Fatal("append-only callback receipt accepted UPDATE")
		}
		if _, err := pool.Native().Exec(ctx, `DELETE FROM wecom_callback_receipts WHERE id=$1`, processedReceipt.ID); err == nil {
			t.Fatal("append-only callback receipt accepted DELETE")
		}
		if _, err := pool.Native().Exec(ctx, `TRUNCATE wecom_callback_receipts`); err == nil {
			t.Fatal("append-only callback receipt accepted TRUNCATE")
		}
	})

	t.Run("processor commits relationship receipt audit and Inbox together", func(t *testing.T) {
		var customerID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customers (status) VALUES ('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
		if err != nil {
			t.Fatal(err)
		}
		auditor, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
		if err != nil {
			t.Fatal(err)
		}
		key, _ := idempotency.Parse("wecom:external-contact:processor-integration-0001")
		payload := json.RawMessage(`{"corp_id":"wx-corp","to_user_name":"wx-corp","msg_type":"event","event":"change_external_contact","change_type":"add_external_contact","external_userid":"processor-external","userid":"processor-employee","state_present":false,"create_time":1788336000,"msg_id_present":false,"welcome_code_present":false,"source_present":false,"fail_reason_present":false}`)
		if err = unit.Within(ctx, func(txContext context.Context) error {
			_, ingestErr := inbox.Ingest(txContext, webhook.Ingest{Provider: callbackProvider, IdempotencyKey: key, Payload: payload})
			return ingestErr
		}); err != nil {
			t.Fatal(err)
		}
		relationships := NewPostgreSQLFollowRelationshipStore()
		processor := InboxProcessor{
			Enabled: true, CorpID: "wx-corp", Inbox: inbox, UOW: unit,
			Lifecycle: ExternalContactLifecycle{
				Identity:      canonicalLifecycleIdentity{customerID: customerdomain.CustomerID(customerID)},
				Relationships: relationships, States: &lifecycleStates{}, Entrants: &lifecycleReceipts{},
			},
			Receipts: NewPostgreSQLCallbackReceiptStore(), Audit: auditor,
		}
		if count, processErr := processor.ProcessOnce(ctx, "processor-integration", 1); processErr != nil || count != 1 {
			t.Fatalf("processed=%d err=%v", count, processErr)
		}
		var status string
		var receiptCount, auditCount int
		if err = pool.Native().QueryRow(ctx, `SELECT status FROM webhook_inbox WHERE idempotency_key=$1`, key).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_callback_receipts receipt JOIN webhook_inbox inbox ON inbox.id=receipt.inbox_id WHERE inbox.idempotency_key=$1 AND receipt.receipt_kind='processing'`, key).Scan(&receiptCount); err != nil {
			t.Fatal(err)
		}
		if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='wecom.callback_processed'`).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if status != string(webhook.StatusProcessed) || receiptCount != 1 || auditCount != 1 {
			t.Fatalf("status=%s receipts=%d audits=%d", status, receiptCount, auditCount)
		}
		if err = unit.Within(ctx, func(txContext context.Context) error {
			active, activeErr := relationships.IsActive(txContext, "wx-corp", "processor-employee", customerdomain.CustomerID(customerID))
			if activeErr == nil && !active {
				return errors.New("follow relationship is not active")
			}
			return activeErr
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func insertWeComInbox(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, suffix, status string, attempts int) int64 {
	t.Helper()
	digest := sha256.Sum256([]byte("payload-" + suffix))
	var id int64
	err := pool.Native().QueryRow(ctx, `
		INSERT INTO webhook_inbox (
			provider, idempotency_key, payload_hash, payload, status,
			attempt_count, max_attempts, next_attempt_at
		) VALUES ('wecom.external_contact', $1, $2, '{}'::jsonb, $3, $4, 8, clock_timestamp())
		RETURNING id`, "wecom:test:"+suffix, digest[:], status, attempts).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAdminUser(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.Native().QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash, display_name)
		VALUES ('callback_operator', '$argon2id$fixture', 'Callback Operator')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func wecomIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("open PostgreSQL integration database")
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal("ping PostgreSQL integration database")
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_wecom_test_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("open isolated PostgreSQL integration schema")
	}
	for _, path := range wecomMigrationPaths(t) {
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal("apply WeCom integration migration")
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func wecomMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate WeCom integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0001_platform.sql"),
		filepath.Join(root, "migrations", "0002_identity.sql"),
		filepath.Join(root, "migrations", "0003_access.sql"),
		filepath.Join(root, "migrations", "0004_wecom.sql"),
		filepath.Join(root, "migrations", "0005_external_effects.sql"),
		filepath.Join(root, "migrations", "0006_wecom_callback_channel_acquisition.sql"),
	}
}
