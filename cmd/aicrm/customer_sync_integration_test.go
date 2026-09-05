package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type integrationDirectoryProvider struct{}

type integrationSyncEnqueuer struct{ runID int64 }

func (enqueuer *integrationSyncEnqueuer) EnqueueCustomerSync(_ context.Context, runID int64) error {
	enqueuer.runID = runID
	return nil
}

func (integrationDirectoryProvider) DirectoryReady() bool { return true }
func (integrationDirectoryProvider) ListContactStaff(context.Context) ([]string, error) {
	return []string{"staff-integration"}, nil
}
func (integrationDirectoryProvider) BatchExternalContacts(context.Context, string, string, int) (wecomport.ExternalContactPage, error) {
	return wecomport.ExternalContactPage{Contacts: []wecomport.ExternalContact{{ExternalUserID: "external-integration", Name: "Integration Customer", Gender: 1, Type: 1, CorpName: "Integration Corp",
		FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-integration", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "provider-tag", Name: "重点客户", Type: 1}}}}}}}, nil
}

func TestCustomerSyncJourneyPostgreSQL(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping customer sync integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_customer_sync_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql", "0086_wecom_profile_primary_owner.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	customerStore := customerstore.NewPostgreSQL()
	enqueuer := &integrationSyncEnqueuer{}
	service := wecom.CustomerSyncService{Enabled: true, CorpID: "integration-corp", Provider: integrationDirectoryProvider{}, Identity: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, Projection: customerStore, Timeline: customerStore, Store: wecom.NewPostgreSQLCustomerSyncStore(), Outbox: platformoutbox.NewPostgreSQL(), Enqueuer: enqueuer, Audit: audit, UOW: uow, Now: func() time.Time { return time.Now().UTC() }}
	run, _, err := service.CreateScheduled(ctx, "initial", "initial:integration-customer-sync")
	if err != nil {
		t.Fatal(err)
	}
	if enqueuer.runID != run.ID {
		t.Fatalf("queued run=%d, want %d", enqueuer.runID, run.ID)
	}
	worker := wecom.NewCustomerSyncWorker()
	if err = worker.BindService(service); err != nil {
		t.Fatal(err)
	}
	if err = worker.Work(ctx, &river.Job[wecom.CustomerSyncJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12}, Args: wecom.CustomerSyncJobArgs{RunID: run.ID}}); err != nil {
		t.Fatal(err)
	}
	run, err = service.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != wecom.SyncSucceeded || run.Discovered != 1 || run.Activated != 1 || run.Projected != 1 {
		t.Fatalf("run=%+v", run)
	}
	var identities, projections, receipts, pending, owners, tags, timeline int
	if err = pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_identities WHERE kind='wecom_external_userid' AND assurance='verified'),
		(SELECT count(*) FROM customer_directory_projection),(SELECT count(*) FROM wecom_customer_sync_items WHERE run_id=$1),
		(SELECT count(*) FROM outbox_events WHERE processed_at IS NULL),(SELECT count(*) FROM wecom_customer_owner_observations WHERE last_seen_run_id=$1),
		(SELECT count(*) FROM wecom_customer_tag_observations WHERE last_seen_run_id=$1),(SELECT count(*) FROM customer_timeline_projection WHERE source_domain='wecom')`, run.ID).Scan(&identities, &projections, &receipts, &pending, &owners, &tags, &timeline); err != nil {
		t.Fatal(err)
	}
	if identities != 1 || projections != 1 || receipts != 1 || pending != 0 || owners != 1 || tags != 1 || timeline != 1 {
		t.Fatalf("identities=%d projections=%d receipts=%d pending=%d owners=%d tags=%d timeline=%d", identities, projections, receipts, pending, owners, tags, timeline)
	}
	var primaryOwner string
	var primaryRunID int64
	if err = pool.Native().QueryRow(ctx, `SELECT primary_owner_userid,primary_owner_run_id FROM wecom_external_contact_profiles`).Scan(&primaryOwner, &primaryRunID); err != nil {
		t.Fatal(err)
	}
	if primaryOwner != "staff-integration" || primaryRunID != run.ID {
		t.Fatalf("primary owner=%q run=%d, want staff-integration/%d", primaryOwner, primaryRunID, run.ID)
	}

	var recoveryRunID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,staff_index,provider_cursor,started_at)
		VALUES('manual:integration-recovery','manual','fetching_profiles','wecom-corp:integration-corp','["staff-integration"]',0,'cursor-committed',clock_timestamp()) RETURNING id`).Scan(&recoveryRunID); err != nil {
		t.Fatal(err)
	}
	syncStore := wecom.NewPostgreSQLCustomerSyncStore()
	var recovery wecom.CustomerSyncRun
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		recovery, loadErr = syncStore.Get(txContext, recoveryRunID)
		if loadErr != nil {
			return loadErr
		}
		return syncStore.Fail(txContext, recovery.ID, recovery.Version, wecom.SyncFailedRetryable, "provider_unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		recovery, loadErr = syncStore.Get(txContext, recoveryRunID)
		if loadErr != nil {
			return loadErr
		}
		return syncStore.Transition(txContext, recovery.ID, recovery.Version, recovery.Status, recovery.ResumeStatus)
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		recovery, loadErr = syncStore.Get(txContext, recoveryRunID)
		return loadErr
	}); err != nil {
		t.Fatal(err)
	}
	if recovery.Status != wecom.SyncFetchingProfiles || recovery.ProviderCursor != "cursor-committed" || recovery.StaffIndex != 0 {
		t.Fatalf("recovery did not preserve cursor: %+v", recovery)
	}
}
