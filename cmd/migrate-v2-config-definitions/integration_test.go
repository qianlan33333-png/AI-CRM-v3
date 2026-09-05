package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	configtarget "github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/target"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

// OneID decision: not involved. This fixture contains only local business
// definitions and creates no customer or external-identity records.
// Persistence decision: one local PostgreSQL transaction. No worker, River
// job, Provider call, or External Effect is accepted by this import.
func TestRunnerPostgresIntegrationApplyVerifyAndReplay(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	runner := configMigrationRunner(t, pool)
	snapshot := configMigrationFixture(t, strings.Repeat("a", 40))
	digest, err := snapshot.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}

	if err = runner.Preflight(ctx, snapshot, digest, actor); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	applied, err := runner.Apply(ctx, snapshot, digest, actor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.NoOp || applied.Products != 31 || applied.Coupons != 15 || applied.GroupOps != 12 || applied.Automation != 10 {
		t.Fatalf("unexpected apply result: %+v", applied)
	}
	assertConfigMigrationCounts(t, ctx, pool, map[string]int64{
		"products": 31, "service_products": 2, "service_period_definitions": 2, "coupon_rules": 15, "coupon_targets": 15,
		"issued_coupons": 0, "group_plans": 12, "paused_group_plans": 12, "group_assets": 14,
		"group_nodes": 3, "group_runs": 0, "group_executions": 0, "automation_agents": 10,
		"paused_agents": 4, "archived_agents": 6, "enabled_agents": 0, "source_maps": 102,
		"external_effects": 0, "external_effect_jobs": 0, "product_push_configurations": 0,
		"product_push_tests": 0, "group_operation_receipts": 0, "group_audit_events": 0,
		"group_outbox": 0, "automation_receipts": 0, "automation_audit_events": 0, "automation_outbox": 0,
		"coupon_receipts": 0, "coupon_audit_events": 0, "coupon_outbox": 0,
	})
	assertImportedGroupOpsDetailsReadable(t, ctx, pool)

	verified, err := runner.Verify(ctx, snapshot, digest)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.BatchID != applied.BatchID || verified.Products != 31 || verified.Coupons != 15 || verified.GroupOps != 12 || verified.Automation != 10 {
		t.Fatalf("unexpected verify result: %+v", verified)
	}
	var status string
	if err = pool.Native().QueryRow(ctx, `SELECT status FROM config_definition_import_batches WHERE id=$1`, applied.BatchID).Scan(&status); err != nil || status != "verified" {
		t.Fatalf("batch status=%q err=%v", status, err)
	}

	replayed, err := runner.Apply(ctx, snapshot, digest, actor)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed.NoOp || replayed.BatchID != applied.BatchID {
		t.Fatalf("replay was not a no-op: %+v", replayed)
	}
	assertConfigMigrationCounts(t, ctx, pool, map[string]int64{
		"products": 31, "coupon_rules": 15, "group_plans": 12, "automation_agents": 10,
		"source_maps": 102, "external_effects": 0, "external_effect_jobs": 0, "group_runs": 0, "group_executions": 0,
	})

	drift := snapshot
	drift.Products = append([]source.Product(nil), snapshot.Products...)
	drift.Products[0].Name = "摘要漂移商品"
	if err = source.PopulateManifest(&drift, drift.Manifest.SourceSystem, drift.Manifest.SourceRevision, drift.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}
	driftDigest, err := drift.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Preflight(ctx, drift, driftDigest, actor); !errors.Is(err, configtarget.ErrDrift) {
		t.Fatalf("preflight drift error=%v", err)
	}
	if _, err = runner.Apply(ctx, drift, driftDigest, actor); !errors.Is(err, configtarget.ErrDrift) {
		t.Fatalf("apply drift error=%v", err)
	}

	collision := configMigrationFixture(t, strings.Repeat("c", 40))
	collisionDigest, err := collision.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Preflight(ctx, collision, collisionDigest, actor); !errors.Is(err, configtarget.ErrInvalid) {
		t.Fatalf("preflight collision error=%v", err)
	}
	if _, err = runner.Apply(ctx, collision, collisionDigest, actor); err == nil {
		t.Fatal("direct collision apply unexpectedly succeeded")
	}
	assertConfigMigrationCounts(t, ctx, pool, map[string]int64{"products": 31, "import_batches": 1, "source_maps": 102})

	if _, err = pool.Native().Exec(ctx, `UPDATE config_definition_import_source_maps SET source_key='mutated' WHERE id=(SELECT min(id) FROM config_definition_import_source_maps)`); err == nil {
		t.Fatal("source-map append-only trigger accepted mutation")
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE config_definition_import_batches SET source_system='mutated' WHERE id=$1`, applied.BatchID); err == nil {
		t.Fatal("batch mutation guard accepted immutable-field change")
	}
}

func TestRunnerPostgresIntegrationRollsBackWholeBatch(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	runner := configMigrationRunner(t, pool)
	runner.Automation = rejectingAutomationImporter{}
	snapshot := configMigrationFixture(t, strings.Repeat("b", 40))
	digest, err := snapshot.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = runner.Apply(ctx, snapshot, digest, actor); err == nil {
		t.Fatal("apply unexpectedly succeeded despite final-domain importer failure")
	}
	assertConfigMigrationCounts(t, ctx, pool, map[string]int64{
		"products": 0, "coupon_rules": 0, "coupon_targets": 0, "group_plans": 0,
		"group_assets": 0, "group_nodes": 0, "automation_agents": 0, "source_maps": 0,
		"import_batches": 0, "external_effects": 0, "external_effect_jobs": 0,
		"product_push_configurations": 0, "product_push_tests": 0, "group_runs": 0, "group_executions": 0,
	})
}

// OneID decision: not involved. V2 group/user text identifiers stay sealed
// history facts and never become current Access or customer identities.
// Persistence decision: one Group Ops owner transaction. This writes only the
// four read-only history projections and source ledger, never runtime/effects.
func TestGroupOpsHistoryPostgreSQLImportVerifyReplayAndDrift(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	runner := configHistoryRunner(t, pool)
	snapshot := configHistoryFixture(t, strings.Repeat("d", 40))
	digest, err := snapshot.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Preflight(ctx, snapshot, digest); err != nil {
		t.Fatalf("history preflight: %v", err)
	}
	applied, err := runner.Apply(ctx, snapshot, digest)
	if err != nil || applied.NoOp || applied.Imported != 5 || applied.Quarantined != 2 {
		t.Fatalf("history apply=%+v err=%v", applied, err)
	}
	for table, want := range map[string]int64{"group_ops_v1_history_plans": 1, "group_ops_v1_history_directory": 2, "group_ops_v1_history_groups": 1, "group_ops_v1_history_nodes": 1, "group_ops_v1_history_import_rows": 7, "group_ops_plans": 0, "group_ops_runs": 0, "group_ops_executions": 0, "external_effects": 0, "external_effect_jobs": 0} {
		var got int64
		if err = pool.Native().QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	var ownerID *int64
	var sourceOwner string
	if err = pool.Native().QueryRow(ctx, `SELECT owner_staff_id,source_owner_reference FROM group_ops_v1_history_plans WHERE plan_id=101`).Scan(&ownerID, &sourceOwner); err != nil || ownerID != nil || sourceOwner != "9" {
		t.Fatalf("text owner was coerced into current staff: id=%v source=%q err=%v", ownerID, sourceOwner, err)
	}
	assertImportedHistoryReadable(t, ctx, pool)
	verified, err := runner.Verify(ctx, snapshot, digest)
	if err != nil || verified.Imported != 5 || verified.Quarantined != 2 {
		t.Fatalf("history verify=%+v err=%v", verified, err)
	}
	replayed, err := runner.Apply(ctx, snapshot, digest)
	if err != nil || !replayed.NoOp || replayed.BatchID != applied.BatchID {
		t.Fatalf("history replay=%+v err=%v", replayed, err)
	}
	drift := snapshot
	drift.Plans = append([]source.HistoryPlan(nil), snapshot.Plans...)
	drift.Plans[0].Name = "历史计划漂移"
	if err = source.PopulateHistoryManifest(&drift, drift.Manifest.SourceSystem, drift.Manifest.SourceRevision, drift.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}
	driftDigest, err := drift.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Apply(ctx, drift, driftDigest); !errors.Is(err, configtarget.ErrHistoryDrift) {
		t.Fatalf("source drift err=%v", err)
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE group_ops_v1_history_plans SET name='drift' WHERE plan_id=101`); err == nil {
		t.Fatal("immutable historical target accepted drift")
	}
}

func TestGroupOpsHistoryPostgreSQLImportRollsBackWholeBatch(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	runner := configHistoryRunner(t, pool)
	runner.GroupOps = rejectingHistoryImporter{inner: runner.GroupOps}
	snapshot := configHistoryFixture(t, strings.Repeat("e", 40))
	digest, err := snapshot.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Apply(ctx, snapshot, digest); err == nil {
		t.Fatal("history import unexpectedly accepted a target-invalid source row")
	}
	for _, table := range []string{"group_ops_v1_history_plans", "group_ops_v1_history_directory", "group_ops_v1_history_groups", "group_ops_v1_history_nodes", "group_ops_v1_history_import_batches", "group_ops_v1_history_import_rows"} {
		var n int64
		if err = pool.Native().QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rollback left %s=%d err=%v", table, n, err)
		}
	}
}

type rejectingHistoryImporter struct {
	inner groupopsport.HistoricalImporter
}

func (r rejectingHistoryImporter) ApplyHistoricalImport(ctx context.Context, batch groupopsport.HistoricalImportBatch, records []groupopsport.HistoricalImportRecord) (groupopsport.HistoricalImportResult, error) {
	if len(records) == 0 {
		return groupopsport.HistoricalImportResult{}, errors.New("test: no history records")
	}
	if _, err := r.inner.ApplyHistoricalImport(ctx, batch, records[:1]); err != nil {
		return groupopsport.HistoricalImportResult{}, err
	}
	return groupopsport.HistoricalImportResult{}, errors.New("test: fail after Group Ops history target write")
}
func (r rejectingHistoryImporter) VerifyHistoricalImport(ctx context.Context, batch groupopsport.HistoricalImportBatch, records []groupopsport.HistoricalImportRecord) (groupopsport.HistoricalImportResult, error) {
	return r.inner.VerifyHistoricalImport(ctx, batch, records)
}

type rejectingAutomationImporter struct{}

func (rejectingAutomationImporter) ImportDefinition(context.Context, automationport.DefinitionImport) (automationport.Agent, error) {
	return automationport.Agent{}, errors.New("test: reject automation import after prior domains wrote")
}

func configMigrationRunner(t *testing.T, pool *platformpostgres.Pool) configtarget.Runner {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	products, err := productstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	coupons, err := couponstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	groupOps, err := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	automation, err := automationstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	return configtarget.Runner{UOW: uow, Products: products, Coupons: coupons, GroupOps: groupOps, Automation: automation}
}

func configHistoryRunner(t *testing.T, pool *platformpostgres.Pool) configtarget.HistoryRunner {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	groupOps, err := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	return configtarget.HistoryRunner{UOW: uow, GroupOps: groupOps}
}

func configHistoryFixture(t *testing.T, revision string) source.HistorySnapshot {
	t.Helper()
	now := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	owner, empty := "9", ""
	snapshot := source.HistorySnapshot{
		Plans:              []source.HistoryPlan{{ID: 101, PlanCode: "", Name: "历史计划", PlanType: "standard", Status: "disabled", OwnerReference: &owner, CreatedByReference: &empty, UpdatedByReference: &owner, CreatedAt: now, UpdatedAt: now}, {ID: 102, PlanCode: "invalid", Name: " leading", PlanType: "standard", Status: "disabled", CreatedAt: now, UpdatedAt: now}},
		DirectoryChats:     []source.HistoryDirectoryChat{{ChatReference: "chat-history-1", DisplayName: "历史群", OwnerReference: &owner, MemberCount: 3, Status: "active", RecordedAt: now}},
		DirectorySnapshots: []source.HistoryDirectorySnapshot{{ChatReference: "chat-history-1", DisplayName: "历史群", OwnerReference: &empty, OwnerName: "", InternalMemberCount: 1, ExternalMemberCount: 2, Status: "active", RecordedAt: now}},
		Groups:             []source.HistoryGroup{{ID: 201, PlanID: 101, ChatReference: "chat-history-1", DisplayName: "历史群", OwnerReference: &owner, InternalMemberCount: 1, ExternalMemberCount: 2, Status: "active", CreatedAt: now}, {ID: 202, PlanID: 999, ChatReference: "orphan", DisplayName: "孤立群", InternalMemberCount: 1, ExternalMemberCount: 0, Status: "active", CreatedAt: now}},
		Nodes:              []source.HistoryNode{{ID: 301, PlanID: 101, DayIndex: 1, TriggerTime: "09:00", SortOrder: 1, Status: "active", ContentPackage: json.RawMessage(`{"text":"历史内容"}`), Attachments: json.RawMessage(`[{"kind":"image","id":"m1"}]`), CreatedAt: now, UpdatedAt: now}},
	}
	if err := source.PopulateHistoryManifest(&snapshot, source.ProductionSourceSystem, revision, now); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertImportedHistoryReadable(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewHistoryService(uow, repository)
	plans, err := service.ListHistoricalPlans(ctx, 20, 0)
	if err != nil || plans.Total != 1 || plans.Items[0].PlanID != 101 || plans.Items[0].CreatedBy != nil || plans.Items[0].SourceOwnerReference != nil {
		t.Fatalf("history plan page=%#v err=%v", plans, err)
	}
	groups, err := service.ListHistoricalGroups(ctx, 101, 20, 0)
	if err != nil || groups.Total != 1 || groups.Items[0].SourceOwnerReference != nil {
		t.Fatalf("history group page=%#v err=%v", groups, err)
	}
	nodes, err := service.ListHistoricalNodes(ctx, 101, 20, 0)
	if err != nil || nodes.Total != 1 || !strings.Contains(string(nodes.Items[0].ContentPackage), "source_attachments") {
		t.Fatalf("history node page=%#v err=%v", nodes, err)
	}
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(historyHTTPApplication{}, historyHTTPRuntime{}, service, historyHTTPSecurity{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, groupopshttp.HistoryPath+"/plans?limit=20&offset=0", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "source_owner_reference") || !strings.Contains(response.Body.String(), `"plan_id":"101"`) {
		t.Fatalf("history HTTP response status=%d body=%s", response.Code, response.Body.String())
	}
}

type historyHTTPApplication struct{ groupopshttp.Application }
type historyHTTPRuntime struct {
	groupopshttp.RuntimeApplication
}
type historyHTTPSecurity struct{}

func (historyHTTPSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (historyHTTPSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return historyHTTPSecurity{}.Authenticate(context.Background(), nil)
}

func configMigrationActor(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.Native().QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active) VALUES('config-import-test','$argon2id$test','Config Import Test',TRUE) RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertImportedGroupOpsDetailsReadable(t *testing.T, ctx context.Context, pool *platformpostgres.Pool) {
	t.Helper()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewService(uow, repository, configMigrationTestStaff{}, repository)
	rows, err := pool.Native().Query(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='automation_group_ops_plans' ORDER BY target_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var planID int64
		if err = rows.Scan(&planID); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Detail(ctx, planID); err != nil {
			t.Fatalf("imported group plan %d is not readable through the production service: %v", planID, err)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

type configMigrationTestStaff struct{}

func (configMigrationTestStaff) IsActiveStaff(context.Context, int64) (bool, error) {
	return true, nil
}

func assertConfigMigrationCounts(t *testing.T, ctx context.Context, pool *platformpostgres.Pool, expected map[string]int64) {
	t.Helper()
	queries := map[string]string{
		"products":                    `SELECT count(*) FROM products`,
		"service_products":            `SELECT count(*) FROM products WHERE legacy_admin_projection->>'status' LIKE 'service_period_%'`,
		"service_period_definitions":  `SELECT count(*) FROM product_imported_service_period_definitions WHERE duration_days IN (90,365)`,
		"coupon_rules":                `SELECT count(*) FROM coupon_rules`,
		"coupon_targets":              `SELECT count(*) FROM coupon_rule_targets`,
		"issued_coupons":              `SELECT count(*) FROM coupon_rules WHERE issued_count <> 0`,
		"group_plans":                 `SELECT count(*) FROM group_ops_plans`,
		"paused_group_plans":          `SELECT count(*) FROM group_ops_plans WHERE status='paused'`,
		"group_assets":                `SELECT count(*) FROM group_ops_plan_group_assets`,
		"group_nodes":                 `SELECT count(*) FROM group_ops_plan_nodes`,
		"group_runs":                  `SELECT count(*) FROM group_ops_runs`,
		"group_executions":            `SELECT count(*) FROM group_ops_executions`,
		"automation_agents":           `SELECT count(*) FROM automation_agents`,
		"paused_agents":               `SELECT count(*) FROM automation_agents WHERE status='paused' AND archived_at IS NULL`,
		"archived_agents":             `SELECT count(*) FROM automation_agents WHERE status='archived' AND archived_at IS NOT NULL`,
		"enabled_agents":              `SELECT count(*) FROM automation_agents WHERE execution_enabled`,
		"source_maps":                 `SELECT count(*) FROM config_definition_import_source_maps`,
		"import_batches":              `SELECT count(*) FROM config_definition_import_batches`,
		"external_effects":            `SELECT count(*) FROM external_effects`,
		"external_effect_jobs":        `SELECT count(*) FROM external_effect_jobs`,
		"product_push_configurations": `SELECT count(*) FROM product_external_push_configurations`,
		"product_push_tests":          `SELECT count(*) FROM product_external_push_tests`,
		"group_operation_receipts":    `SELECT count(*) FROM group_ops_operation_receipts`,
		"group_audit_events":          `SELECT count(*) FROM group_ops_audit_events`,
		"group_outbox":                `SELECT count(*) FROM group_ops_outbox`,
		"automation_receipts":         `SELECT count(*) FROM automation_operation_receipts`,
		"automation_audit_events":     `SELECT count(*) FROM automation_audit_events`,
		"automation_outbox":           `SELECT count(*) FROM automation_outbox`,
		"coupon_receipts":             `SELECT count(*) FROM coupon_operation_receipts`,
		"coupon_audit_events":         `SELECT count(*) FROM coupon_audit_events`,
		"coupon_outbox":               `SELECT count(*) FROM coupon_outbox`,
	}
	for name, want := range expected {
		query, ok := queries[name]
		if !ok {
			t.Fatalf("missing count query %q", name)
		}
		var got int64
		if err := pool.Native().QueryRow(ctx, query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s=%d want=%d", name, got, want)
		}
	}
}

func configMigrationFixture(t *testing.T, revision string) source.Snapshot {
	t.Helper()
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	snapshot := source.Snapshot{}
	for id := int64(1); id <= 31; id++ {
		active := id%7 != 0
		status := "disabled"
		if active {
			status = "active"
		}
		snapshot.Products = append(snapshot.Products, source.Product{ID: id, ProductCode: fmt.Sprintf("product-%02d", id), Name: fmt.Sprintf("商品%02d", id), Description: "", PriceMinor: id * 100, Currency: "CNY", Status: status, Enabled: active, BuyButtonText: "立即购买", RequireMobile: true, LeadQRTitle: "", LeadQRSubtitle: "", CreatedAt: now, UpdatedAt: now})
	}
	snapshot.ServicePeriods = []source.ServicePeriod{
		{ID: 1, TradeProductID: 30, DurationDays: 90, CreatedAt: now, UpdatedAt: now},
		{ID: 2, TradeProductID: 31, DurationDays: 365, CreatedAt: now, UpdatedAt: now},
	}
	for id := int64(1); id <= 15; id++ {
		coupon := source.Coupon{ID: id, Name: fmt.Sprintf("优惠券%02d", id), DiscountAmountTotal: id * 10, Currency: "CNY", Status: "published", TotalIssueLimit: 100, PerUserIssueLimit: 1, ClaimStartsAt: now, ClaimEndsAt: now.Add(24 * time.Hour), Instructions: "", CreatedAt: now, UpdatedAt: now}
		if id == 1 {
			useStart, useEnd := now, now.Add(48*time.Hour)
			coupon.ValidityMode, coupon.UseStartsAt, coupon.UseEndsAt = "fixed_range", &useStart, &useEnd
		} else {
			days := int32(30)
			coupon.ValidityMode, coupon.RelativeValidityDays = "relative_days", &days
		}
		snapshot.Coupons = append(snapshot.Coupons, coupon)
		snapshot.CouponBindings = append(snapshot.CouponBindings, source.CouponBinding{ID: id, CouponID: id, TradeProductID: id, CreatedAt: now})
	}
	for id := int64(1); id <= 12; id++ {
		status, kind := "disabled", "standard"
		if id <= 2 {
			status = "active"
		}
		if id > 5 {
			kind = "webhook"
		}
		snapshot.GroupPlans = append(snapshot.GroupPlans, source.GroupPlan{ID: id, PlanCode: fmt.Sprintf("group-plan-%02d", id), Name: fmt.Sprintf("群计划%02d", id), PlanType: kind, Status: status, CreatedAt: now, UpdatedAt: now})
	}
	for id := int64(1); id <= 3; id++ {
		snapshot.GroupNodes = append(snapshot.GroupNodes, source.GroupNode{ID: id, PlanID: id, DayIndex: 1, TriggerTimeLabel: "09:00", ActionTitle: "", TextContent: fmt.Sprintf("群消息%02d", id), SortOrder: int32(id), CreatedAt: now, UpdatedAt: now})
	}
	for id := int64(1); id <= 14; id++ {
		planID := id
		if planID > 12 {
			planID -= 12
		}
		chatID := fmt.Sprintf("chat-%02d", id)
		if id == 1 {
			chatID = "chat-z"
		} else if id == 13 {
			chatID = "chat-A"
		}
		snapshot.GroupAssets = append(snapshot.GroupAssets, source.GroupAsset{ID: id, PlanID: planID, ChatID: chatID, CreatedAt: now})
	}
	for id := int64(1); id <= 10; id++ {
		status, kind := "archived", "fixed_script"
		var archivedAt *time.Time
		content := fmt.Sprintf("固定话术%02d", id)
		if id <= 4 {
			status, kind, content = "active", "agent", ""
		} else {
			archived := now
			archivedAt = &archived
		}
		snapshot.Agents = append(snapshot.Agents, source.Agent{ID: id, AgentCode: fmt.Sprintf("agent-%02d", id), AgentName: fmt.Sprintf("智能体%02d", id), Status: status, AutomationType: kind, DraftRolePrompt: "角色提示", DraftTaskPrompt: "任务提示", PublishedRolePrompt: "已发布角色", PublishedTaskPrompt: "已发布任务", DraftVersion: 2, PublishedVersion: 1, FixedContentText: content, NeedHumanReview: false, CreatedAt: now, UpdatedAt: now, ArchivedAt: archivedAt})
	}
	if err := source.PopulateManifest(&snapshot, source.ProductionSourceSystem, revision, now); err != nil {
		t.Fatal(err)
	}
	if err := source.ValidateExpectedBaseline(snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func configMigrationIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping configuration migration PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig.Copy())
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
	schema := "aicrm_config_import_test_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := adminConfig.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("open isolated PostgreSQL integration schema")
	}
	for _, path := range configMigrationPaths(t) {
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
			t.Fatalf("apply configuration migration %s: %v", filepath.Base(path), execErr)
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

func configMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate configuration migration integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	files := []string{"0001_platform.sql", "0003_access.sql", "0005_external_effects.sql", "0010_product.sql", "0011_coupon_rules.sql", "0012_group_ops.sql", "0013_automation_agents.sql", "0017_group_ops_history.sql", "0030_config_definition_import.sql", "0078_group_ops_provider_tasks.sql", "0081_group_ops_webhook_unconfigured_reference.sql", "0082_group_ops_history_import.sql"}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, filepath.Join(root, "migrations", file))
	}
	return paths
}
