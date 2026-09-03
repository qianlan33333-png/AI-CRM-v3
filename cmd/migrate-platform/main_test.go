package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestLoadMigrationsSortsAndChecksVersions(t *testing.T) {
	filesystem := fstest.MapFS{
		"0002_second.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
		"README.md":       &fstest.MapFile{Data: []byte("ignored")},
		"0001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	items, err := loadMigrations(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].version != "0001" || items[1].version != "0002" {
		t.Fatalf("unexpected order: %+v", items)
	}

	filesystem["0001_duplicate.sql"] = &fstest.MapFile{Data: []byte("SELECT 3")}
	if _, err = loadMigrations(filesystem); err == nil {
		t.Fatal("expected duplicate migration version error")
	}
}

func TestApplyMigrationsFreshAndUpgradePostgreSQL(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	items, err := loadMigrations(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("fresh migration: %v", err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade replay: %v", err)
	}
	foundConfigMigration := false
	foundSegmentMigration := false
	foundSegmentSnapshotMigration := false
	foundSegmentWebhookMigration := false
	for _, item := range items {
		if item.name == "0015_config_adminops.sql" {
			foundConfigMigration = true
		}
		if item.name == "0028_segment_audience_configuration.sql" {
			foundSegmentMigration = true
		}
		if item.name == "0029_segment_audience_snapshots.sql" {
			foundSegmentSnapshotMigration = true
		}
		if item.name == "0030_segment_audience_webhooks.sql" {
			foundSegmentWebhookMigration = true
		}
	}
	if !foundConfigMigration {
		t.Fatal("expected 0015_config_adminops.sql in the platform migration set")
	}
	if !foundSegmentMigration || !foundSegmentSnapshotMigration || !foundSegmentWebhookMigration {
		t.Fatal("expected Segment configuration, snapshot and webhook migrations in the platform migration set")
	}
	var applied int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM platform_schema_migrations`).Scan(&applied); err != nil || applied != len(items) {
		t.Fatalf("applied=%d expected=%d err=%v", applied, len(items), err)
	}
	for _, table := range []string{"media_blobs", "media_references", "media_attachment_upload_parts", "media_content_packages", "media_content_package_versions", "media_content_package_version_refs", "media_content_delivery_receipts", "media_content_delivery_bindings", "media_group_ops_preparation_receipts", "media_group_ops_preparation_items", "tag_groups", "tag_catalog_tags", "tag_provider_observations", "products", "product_operation_receipts", "product_external_push_configurations", "product_external_push_tests", "coupon_rules", "coupon_rule_targets", "coupon_operation_receipts", "coupon_audit_events", "coupon_outbox", "automation_agents", "automation_operation_receipts", "automation_audit_events", "automation_outbox", "segment_audience_groups", "segment_audience_packages", "segment_audience_configuration_versions", "segment_audience_operation_receipts", "segment_audience_audit_events", "segment_audience_outbox", "segment_audience_refresh_runs", "segment_audience_snapshots", "segment_audience_snapshot_members", "segment_audience_refresh_batches", "segment_audience_webhook_receipts", "segment_audience_automation_binding_versions", "segment_audience_sender_sets", "segment_audience_sender_set_members", "group_ops_plans", "group_ops_plan_members", "group_ops_plan_group_assets", "group_ops_plan_nodes", "group_ops_plan_webhook_descriptors", "group_ops_operation_receipts", "group_ops_audit_events", "group_ops_outbox", "group_ops_runs", "group_ops_executions", "group_ops_directory_groups", "group_ops_directory_refresh_receipts", "group_ops_protocol_replays", "operation_cycle_strategies", "operation_cycle_runs", "operation_cycle_report_receipts", "operation_cycle_runners", "operation_cycle_action_requests", "operation_cycle_action_request_events", "operation_cycle_strategy_proposals", "operation_cycle_strategy_versions", "operation_cycle_run_versions", "operation_cycle_run_ordinals", "operation_cycle_admin_receipts", "config_settings", "config_audits", "config_outbox", "adminops_release_projections", "adminops_diagnostic_snapshots", "admin_access_login_compat_receipts"} {
		var present bool
		if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&present); err != nil || !present {
			t.Fatalf("owned table %s present=%v err=%v", table, present, err)
		}
	}
}

func TestLoadMigrationsRejectsEmptySet(t *testing.T) {
	_, err := loadMigrations(fstest.MapFS{"README.md": &fstest.MapFile{Data: []byte("none")}})
	if err == nil {
		t.Fatal("expected missing migration error")
	}
	if _, ok := err.(*fs.PathError); ok {
		t.Fatalf("expected safe domain error, got %T", err)
	}
}

func TestOperationCycleHistoryMigrationUpgradesExistingProjection(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_upgrade_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		t.Fatal(err)
	}
	prior := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0023_operation_cycle_admin_history.sql" {
			continue
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		prior[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	if err = applyMigrations(ctx, pool, prior); err != nil {
		t.Fatalf("apply previous release: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO operation_cycle_strategies(strategy_key,title,status,version,definition,snapshot,updated_at)
		VALUES ('upgrade.review','升级复盘','active',3,'{"external_effects":"none"}','{"schema_version":"operation_cycle_snapshot.v1"}',clock_timestamp());
		INSERT INTO operation_cycle_runs(run_key,strategy_key,snapshot_revision,snapshot,received_at)
		VALUES ('upgrade.review.001','upgrade.review',8,'{"schema_version":"operation_cycle_snapshot.v1","revision":8}',clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade current release: %v", err)
	}
	var strategyVersion, runRevision, runOrdinal int32
	if err = pool.QueryRow(ctx, `SELECT version FROM operation_cycle_strategy_versions WHERE strategy_key='upgrade.review'`).Scan(&strategyVersion); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT snapshot_revision FROM operation_cycle_run_versions WHERE run_key='upgrade.review.001'`).Scan(&runRevision); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT ordinal FROM operation_cycle_run_ordinals WHERE run_key='upgrade.review.001'`).Scan(&runOrdinal); err != nil {
		t.Fatal(err)
	}
	if strategyVersion != 3 || runRevision != 8 || runOrdinal < 1 {
		t.Fatalf("backfilled strategy/run version/ordinal=%d/%d/%d, want 3/8/positive", strategyVersion, runRevision, runOrdinal)
	}
	if _, err = pool.Exec(ctx, `UPDATE operation_cycle_run_ordinals SET run_key='changed' WHERE ordinal=$1`, runOrdinal); err == nil {
		t.Fatal("backfilled run ordinal accepted mutation")
	}
}

// TestAdminAccessCompatibilityMigrationUpgradesPreviousRelease proves that
// 0027 executes against a database whose migration ledger already contains the
// complete previous release (0001 through 0026). Re-applying a full filesystem
// after 0027 is present would only test checksum skipping, not this upgrade.
func TestAdminAccessCompatibilityMigrationUpgradesPreviousRelease(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "migrate_access_upgrade_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, file, _, _ := runtime.Caller(0)
	filesystem := os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := fs.ReadDir(filesystem, ".")
	if err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0027_admin_access_login_compat.sql" {
			continue
		}
		contents, readErr := fs.ReadFile(filesystem, entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		previous[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	if err = applyMigrations(ctx, pool, previous); err != nil {
		t.Fatalf("apply previous release through 0026: %v", err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade previous release through 0027: %v", err)
	}
	var name string
	if err = pool.QueryRow(ctx, `SELECT name FROM platform_schema_migrations WHERE version='0027'`).Scan(&name); err != nil || name != "0027_admin_access_login_compat.sql" {
		t.Fatalf("0027 ledger name=%q err=%v", name, err)
	}
	var definition string
	if err = pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='admin_access_audit'::regclass AND conname='ck_admin_access_audit_action'`).Scan(&definition); err != nil || !strings.Contains(definition, "set_login_enabled") {
		t.Fatalf("upgraded action constraint=%q err=%v", definition, err)
	}
	var receiptTable bool
	if err = pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.admin_access_login_compat_receipts') IS NOT NULL`).Scan(&receiptTable); err != nil || !receiptTable {
		t.Fatalf("upgraded receipt table=%v err=%v", receiptTable, err)
	}
}
