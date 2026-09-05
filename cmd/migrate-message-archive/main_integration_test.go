package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	archivemigration "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/migration"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestMessageArchiveMigrationDryRunApplyReplayResolveAndReconcilePostgreSQL(t *testing.T) {
	pool, cleanup := archiveMigrationIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native := pool.Native()
	manifest := archiveMigrationManifest(t)
	if err := seedArchiveMigrationIdentity(ctx, native, "wm_known"); err != nil {
		t.Fatal(err)
	}
	resolver := newHistoricalResolver(manifest)

	dry, err := dryRun(ctx, native, manifest, resolver)
	if err != nil || dry.Inserted != 1 || dry.Unresolved != 3 || len(dry.Rows) != 4 {
		t.Fatalf("dry-run=%+v err=%v", dry, err)
	}
	assertArchiveMigrationCounts(t, ctx, native, 0, 0, 0)

	applied, err := apply(ctx, native, manifest, resolver)
	if err != nil || applied.Inserted != 1 || applied.Unresolved != 3 {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	var knownCustomer, knownStaff int64
	if err = native.QueryRow(ctx, `SELECT COALESCE(max(customer_id_at_ingest),0),COALESCE(max(staff_user_id),0) FROM message_archive_participants participant JOIN message_archive_messages message ON message.id=participant.message_id WHERE message.msgid='m-known'`).Scan(&knownCustomer, &knownStaff); err != nil || knownCustomer < 1 || knownStaff < 1 {
		t.Fatalf("resolved archive participant customer=%d staff=%d err=%v", knownCustomer, knownStaff, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("initial reconcile matched=%t err=%v", matched, reconcileErr)
	}
	// A fully matching snapshot replays without creating a second message. The
	// stronger existing-message comparison below must preserve this normal case.
	replayed, err := apply(ctx, native, manifest, resolver)
	if err != nil || replayed.Duplicates != 4 || replayed.Inserted != 0 || replayed.Unresolved != 0 || replayed.Quarantined != 0 {
		t.Fatalf("duplicate replay=%+v err=%v", replayed, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("duplicate reconcile matched=%t err=%v", matched, reconcileErr)
	}

	if err = seedArchiveMigrationIdentity(ctx, native, "wm_later"); err != nil {
		t.Fatal(err)
	}
	// The first bounded pass is deliberately occupied by unresolved rows that
	// remain not_found. The operator receives a cursor and can continue past
	// them to the later verified identity; there is no background worker.
	firstPass, err := reResolve(ctx, native, manifest, resolver, 2, 0)
	if err != nil || firstPass.Inserted != 0 || firstPass.Unresolved != 2 || firstPass.NextParticipantID < 1 {
		t.Fatalf("first re-resolve=%+v err=%v", firstPass, err)
	}
	reResolved, err := reResolve(ctx, native, manifest, resolver, 2, firstPass.NextParticipantID)
	if err != nil || reResolved.Inserted != 1 || reResolved.Unresolved != 0 || reResolved.NextParticipantID <= firstPass.NextParticipantID {
		t.Fatalf("continued re-resolve=%+v err=%v", reResolved, err)
	}
	var attempts int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM message_archive_resolution_attempts`).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatalf("resolution attempts=%d err=%v", attempts, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, manifest); reconcileErr != nil || !matched {
		t.Fatalf("reconcile after resolution matched=%t err=%v", matched, reconcileErr)
	}
	conflictManifest := archiveMigrationSequenceConflictManifest(t)
	conflicted, err := apply(ctx, native, conflictManifest, resolver)
	if err != nil || conflicted.Quarantined != 1 || conflicted.Duplicates != 0 || conflicted.Inserted != 0 {
		t.Fatalf("same msgid changed seq=%+v err=%v", conflicted, err)
	}
	if matched, reconcileErr := reconcile(ctx, native, conflictManifest); reconcileErr != nil || !matched {
		t.Fatalf("sequence conflict reconcile matched=%t err=%v", matched, reconcileErr)
	}

	if _, err = native.Exec(ctx, `UPDATE message_archive_messages SET content_text='drift' WHERE msgid='m-known'`); err != nil {
		t.Fatal(err)
	}
	if _, reconcileErr := reconcile(ctx, native, manifest); !errors.Is(reconcileErr, errReconcileDrift) {
		t.Fatalf("content drift reconcile=%v", reconcileErr)
	}
}

func archiveMigrationManifest(t *testing.T) archivemigration.Manifest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": archivemigration.SchemaVersion,
		"source_name":    "archive-migration-integration",
		"corp_scope":     "wecom-corp:wx-archive-integration",
		"records": []map[string]any{
			{"source_row_key": "row-known", "seq": 1, "msgid": "m-known", "payload": map[string]any{"msgid": "m-known", "from": "staff-one", "tolist": []string{"wm_known"}, "msgtype": "text", "msgtime": 1788336000, "text": map[string]string{"content": "known"}}},
			{"source_row_key": "row-never-one", "seq": 2, "msgid": "m-never-one", "payload": map[string]any{"msgid": "m-never-one", "from": "staff-one", "tolist": []string{"wm_never_one"}, "msgtype": "text", "msgtime": 1788336060, "text": map[string]string{"content": "never one"}}},
			{"source_row_key": "row-never-two", "seq": 3, "msgid": "m-never-two", "payload": map[string]any{"msgid": "m-never-two", "from": "staff-one", "tolist": []string{"wm_never_two"}, "msgtype": "text", "msgtime": 1788336120, "text": map[string]string{"content": "never two"}}},
			{"source_row_key": "row-later", "seq": 4, "msgid": "m-later", "payload": map[string]any{"msgid": "m-later", "from": "staff-one", "tolist": []string{"wm_later"}, "msgtype": "text", "msgtime": 1788336180, "text": map[string]string{"content": "later"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := archivemigration.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func archiveMigrationSequenceConflictManifest(t *testing.T) archivemigration.Manifest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": archivemigration.SchemaVersion,
		"source_name":    "archive-migration-sequence-conflict",
		"corp_scope":     "wecom-corp:wx-archive-integration",
		"records": []map[string]any{
			{"source_row_key": "row-sequence-conflict", "seq": 99, "msgid": "m-known", "payload": map[string]any{"msgid": "m-known", "from": "staff-one", "tolist": []string{"wm_known"}, "msgtype": "text", "msgtime": 1788336000, "text": map[string]string{"content": "known"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := archivemigration.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func seedArchiveMigrationIdentity(ctx context.Context, native *pgxpool.Pool, externalUserID string) error {
	var customerID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		return err
	}
	_, err := native.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:wx-archive-integration',$2,'verified','integration',1,'active',clock_timestamp())`, customerID, externalUserID)
	return err
}

func assertArchiveMigrationCounts(t *testing.T, ctx context.Context, native *pgxpool.Pool, messages, receipts, runs int) {
	t.Helper()
	var actualMessages, actualReceipts, actualRuns int
	if err := native.QueryRow(ctx, `SELECT count(*) FROM message_archive_messages`).Scan(&actualMessages); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT count(*) FROM message_archive_migration_receipts`).Scan(&actualReceipts); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `SELECT count(*) FROM message_archive_migration_runs`).Scan(&actualRuns); err != nil {
		t.Fatal(err)
	}
	if actualMessages != messages || actualReceipts != receipts || actualRuns != runs {
		t.Fatalf("counts messages=%d receipts=%d runs=%d", actualMessages, actualReceipts, actualRuns)
	}
}

func archiveMigrationIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
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
	schema := "aicrm_message_archive_test_" + hex.EncodeToString(random)
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
	for _, path := range archiveMigrationPaths(t) {
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
			t.Fatalf("apply archive migration %s: %v", filepath.Base(path), execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('archive-staff','$argon2id$integration','Archive Staff','staff-one')`); err != nil {
		pool.Close()
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

func archiveMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate archive migration integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0001_platform.sql"),
		filepath.Join(root, "migrations", "0002_identity.sql"),
		filepath.Join(root, "migrations", "0003_access.sql"),
		filepath.Join(root, "migrations", "0071_message_archive_core.sql"),
		filepath.Join(root, "migrations", "0072_message_archive_migration_receipts.sql"),
	}
}
