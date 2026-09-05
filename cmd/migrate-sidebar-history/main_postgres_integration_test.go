package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLSidebarHistoryAllianceApplyReplayReconcile drives the actual
// migration command over PostgreSQL. The fixture preserves three donor states:
// no admin_alliance fact, an explicit donor clear, and a non-empty fact. The
// command must not invent a value for the first state, replay must not insert
// a second entitlement, and reconcile must reject a target-side alliance
// alteration even when the immutable source digest remains unchanged.
func TestPostgreSQLSidebarHistoryAllianceApplyReplayReconcile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	manifestPath, digest := sidebarHistoryAllianceManifest(t)
	seedSidebarHistoryIdentitiesAndDefinition(t, ctx, pool)
	applyArgs := []string{"--mode=apply", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest, "--confirm-apply"}

	// The Order-owned entitlement write, its historical receipt and source-map
	// follow-up must not leave a target row when its UoW fails. The existing
	// command performs no direct Order-table write itself.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION sidebar_history_fail_entitlement_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'sidebar history entitlement failpoint'; END;
		$$;
		CREATE TRIGGER sidebar_history_fail_entitlement_insert
		BEFORE INSERT ON order_service_entitlements
		FOR EACH ROW EXECUTE FUNCTION sidebar_history_fail_entitlement_insert();`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err == nil {
		t.Fatal("entitlement failpoint allowed sidebar history apply")
	}
	var failedEntitlements, failedMaps int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM order_service_entitlements),(SELECT count(*) FROM sidebar_history_migration_source_map)`).Scan(&failedEntitlements, &failedMaps); err != nil {
		t.Fatal(err)
	}
	if failedEntitlements != 0 || failedMaps != 0 {
		t.Fatalf("failed apply leaked Order target rows entitlements=%d maps=%d", failedEntitlements, failedMaps)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER sidebar_history_fail_entitlement_insert ON order_service_entitlements; DROP FUNCTION sidebar_history_fail_entitlement_insert()`); err != nil {
		t.Fatal(err)
	}

	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("real sidebar history apply: %v", err)
	}
	sidebarHistoryAssertAllianceTargets(t, ctx, pool)

	// Re-running the exact protected file exercises the production replay path.
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("real sidebar history replay: %v", err)
	}
	sidebarHistoryAssertAllianceTargets(t, ctx, pool)

	var sourceDigest []byte
	if err := pool.QueryRow(ctx, `
		SELECT source_digest FROM sidebar_history_migration_source_map
		WHERE source_kind='service_period_entitlement' AND source_key='9'`).Scan(&sourceDigest); err != nil {
		t.Fatal(err)
	}
	if len(sourceDigest) != 32 {
		t.Fatalf("source digest length=%d", len(sourceDigest))
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET alliance='目标漂移' WHERE source_system=$1 AND source_key='9'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	reconcileArgs := []string{"--mode=reconcile", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest}
	if err := run(ctx, reconcileArgs); err == nil {
		t.Fatal("reconcile accepted alliance target drift")
	}
	var persistedDigest []byte
	var runStatus string
	if err := pool.QueryRow(ctx, `
		SELECT source_digest,(SELECT status FROM sidebar_history_migration_batches WHERE run_key='sidebar-alliance-pg-001')
		FROM sidebar_history_migration_source_map
		WHERE source_kind='service_period_entitlement' AND source_key='9'`).Scan(&persistedDigest, &runStatus); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sourceDigest, persistedDigest) || runStatus != "applied" {
		t.Fatalf("failed reconcile changed immutable source or status digest=%x status=%q", persistedDigest, runStatus)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET alliance='联盟甲' WHERE source_system=$1 AND source_key='9'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("real sidebar history reconcile: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE run_key='sidebar-alliance-pg-001'`).Scan(&runStatus); err != nil || runStatus != "reconciled" {
		t.Fatalf("reconcile status=%q err=%v", runStatus, err)
	}
}

// TestPostgreSQLSidebarHistoryReconcileVerifiesEveryImportedTargetFact is the
// command-level proof that preserved source digests alone cannot make a batch
// reconciled. It uses the real Order and Coupon importers with mixed terminal
// facts, then changes each target category while leaving the protected source
// rows and receipts untouched.
func TestPostgreSQLSidebarHistoryReconcileVerifiesEveryImportedTargetFact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	manifestPath, digest := sidebarHistoryFullFactsManifest(t)
	sidebarHistorySeedFullFacts(t, ctx, pool)
	applyArgs := []string{"--mode=apply", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest, "--confirm-apply"}
	reconcileArgs := []string{"--mode=reconcile", "--snapshot=" + manifestPath, "--manifest-sha256=" + digest}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("apply full sidebar history: %v", err)
	}
	var entitlements, claims, orders, grants int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM coupon_customer_claims WHERE source_system=$1),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts)`, productionSourceSystem).Scan(&entitlements, &claims, &orders, &grants); err != nil {
		t.Fatal(err)
	}
	if entitlements != 3 || claims != 3 || orders != 0 || grants != 0 {
		t.Fatalf("initial history targets entitlements=%d claims=%d orders=%d grants=%d", entitlements, claims, orders, grants)
	}

	// Entitlement status, period, and product mapping are target facts, not
	// merely an import digest. Each mutation must leave the batch applied.
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET status='expired' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET status='active' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET end_at=end_at + interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET end_at=end_at - interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET start_at=start_at + interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET start_at=start_at - interval '1 day' WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET remark='目标漂移' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET remark='待续' WHERE source_system=$1 AND source_key='101'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET service_product_id=501 WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET service_product_id=502 WHERE source_system=$1 AND source_key='102'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}

	// Coupon mapping, lifecycle status, and redemption time all come from the
	// protected row and must match the Coupon-owned target projection.
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET coupon_id=(SELECT id FROM coupon_rules ORDER BY id DESC LIMIT 1) WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET coupon_id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND domain='coupon' AND source_kind='commerce_coupons' AND source_key='61') WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET status='expired' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET status='claimed' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claim_no_masked='历史券号漂移' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claim_no_masked='' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claimed_at=claimed_at + interval '1 second' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET claimed_at=claimed_at - interval '1 second' WHERE source_system=$1 AND source_key='201'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET valid_until=valid_until + interval '1 second' WHERE source_system=$1 AND source_key='203'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET valid_until=valid_until - interval '1 second' WHERE source_system=$1 AND source_key='203'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET redeemed_at=redeemed_at + interval '1 second' WHERE source_system=$1 AND source_key='202'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE coupon_customer_claims SET redeemed_at=redeemed_at - interval '1 second' WHERE source_system=$1 AND source_key='202'`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}

	// A missing target cannot be repaired by retaining its map. Restore the
	// exact target row in this fixture, then prove the protected batch can pass.
	var deleted sidebarHistoryCouponTarget
	if err := pool.QueryRow(ctx, `SELECT id,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at,source_digest,created_at,updated_at FROM coupon_customer_claims WHERE source_system=$1 AND source_key='203'`, productionSourceSystem).Scan(&deleted.id, &deleted.customerID, &deleted.couponID, &deleted.status, &deleted.claimNoMasked, &deleted.claimedAt, &deleted.validFrom, &deleted.validUntil, &deleted.redeemedAt, &deleted.sourceDigest, &deleted.createdAt, &deleted.updatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM coupon_customer_claims WHERE id=$1`, deleted.id); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `INSERT INTO coupon_customer_claims(id,source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at,source_digest,created_at,updated_at)
		OVERRIDING SYSTEM VALUE VALUES($1,$2,'203',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, deleted.id, productionSourceSystem, deleted.customerID, deleted.couponID, deleted.status, deleted.claimNoMasked, deleted.claimedAt, deleted.validFrom, deleted.validUntil, deleted.redeemedAt, deleted.sourceDigest, deleted.createdAt, deleted.updatedAt); err != nil {
		t.Fatal(err)
	}

	// The source map and its target could be changed together while retaining
	// the protected row digest. Reconciliation must bind the scoped, verified
	// UnionID through OneID in the same Serializable transaction rather than
	// trusting that two altered customer_id columns agree.
	var originalCustomerID, wrongCustomerID int64
	if err := pool.QueryRow(ctx, `SELECT customer_id FROM order_service_entitlements WHERE source_system=$1 AND source_key='101'`, productionSourceSystem).Scan(&originalCustomerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&wrongCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET customer_id=$1 WHERE source_system=$2 AND source_key='101'`, wrongCustomerID, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_source_map SET customer_id=$1 WHERE source_kind='service_period_entitlement' AND source_key='101'`, wrongCustomerID); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE order_service_entitlements SET customer_id=$1 WHERE source_system=$2 AND source_key='101'`, originalCustomerID, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_source_map SET customer_id=$1 WHERE source_kind='service_period_entitlement' AND source_key='101'`, originalCustomerID); err != nil {
		t.Fatal(err)
	}

	// Quarantine has no target, but its protected reason is still an observable
	// source result. A change must fail and a restored receipt can reconcile.
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_quarantine SET reason='identity_conflict' WHERE source_kind='service_period_entitlement' AND source_key='104'`); err != nil {
		t.Fatal(err)
	}
	sidebarHistoryReconcileRejects(t, ctx, pool, reconcileArgs, "sidebar-full-facts-pg-001")
	if _, err := pool.Exec(ctx, `UPDATE sidebar_history_migration_quarantine SET reason='identity_not_found' WHERE source_kind='service_period_entitlement' AND source_key='104'`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile restored full facts: %v", err)
	}

	// Replay does not turn historical facts into new entitlement grants, orders,
	// coupon redemptions, or additional target rows.
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("replay full sidebar history: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM coupon_customer_claims WHERE source_system=$1),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts)`, productionSourceSystem).Scan(&entitlements, &claims, &orders, &grants); err != nil {
		t.Fatal(err)
	}
	if entitlements != 3 || claims != 3 || orders != 0 || grants != 0 {
		t.Fatalf("replay created effects entitlements=%d claims=%d orders=%d grants=%d", entitlements, claims, orders, grants)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile replayed full facts: %v", err)
	}

	// Both remaining receipts have an identity_not_found outcome. Once their
	// protected subject becomes verifiably resolvable, the existing Order and
	// Coupon import paths add maps and atomically remove the old quarantines.
	// A subsequent replay must retain exactly one outcome per source row.
	var recoveredCustomerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&recoveredCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'unionid','wechat-open-platform:primary','sidebar-full-unresolved','verified','provider-history:sidebar-full-recovery',1,clock_timestamp())`, recoveredCustomerID); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("apply resolved prior quarantines: %v", err)
	}
	var maps, quarantines, imported, replayed int64
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine),
		imported_count,replayed_count
		FROM sidebar_history_migration_batches WHERE run_key='sidebar-full-facts-pg-001'`).Scan(&maps, &quarantines, &imported, &replayed); err != nil {
		t.Fatal(err)
	}
	if maps != 8 || quarantines != 0 || imported != 2 || replayed != 6 {
		t.Fatalf("resolved quarantine receipts maps=%d quarantines=%d imported=%d replayed=%d", maps, quarantines, imported, replayed)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile resolved prior quarantines: %v", err)
	}
	// A later loss of identity proof cannot convert an already mapped receipt
	// back into a second quarantine outcome. Apply stops instead, retaining the
	// map for reconciliation to report as evidence drift.
	if _, err := pool.Exec(ctx, `UPDATE customer_identities SET status='retired' WHERE normalized_value='sidebar-full-unresolved'`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err == nil {
		t.Fatal("apply converted an existing map into a second quarantine")
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine)`).Scan(&maps, &quarantines); err != nil {
		t.Fatal(err)
	}
	if maps != 8 || quarantines != 0 {
		t.Fatalf("failed replay changed source outcomes maps=%d quarantines=%d", maps, quarantines)
	}
	if _, err := pool.Exec(ctx, `UPDATE customer_identities SET status='active' WHERE normalized_value='sidebar-full-unresolved'`); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, applyArgs); err != nil {
		t.Fatalf("replay resolved prior quarantines: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM coupon_customer_claims WHERE source_system=$1),
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine),
		(SELECT count(*) FROM orders),
		(SELECT count(*) FROM order_entitlement_fulfillment_receipts)`, productionSourceSystem).Scan(&entitlements, &claims, &maps, &quarantines, &orders, &grants); err != nil {
		t.Fatal(err)
	}
	if entitlements != 4 || claims != 4 || maps != 8 || quarantines != 0 || orders != 0 || grants != 0 {
		t.Fatalf("resolved quarantine replay conservation entitlements=%d claims=%d maps=%d quarantines=%d orders=%d grants=%d", entitlements, claims, maps, quarantines, orders, grants)
	}
	if err := run(ctx, reconcileArgs); err != nil {
		t.Fatalf("reconcile replayed resolved quarantines: %v", err)
	}
}

type sidebarHistoryCouponTarget struct {
	id, customerID, couponID          int64
	status, claimNoMasked             string
	claimedAt                         time.Time
	validFrom, validUntil, redeemedAt *time.Time
	sourceDigest                      []byte
	createdAt, updatedAt              time.Time
}

func sidebarHistoryReconcileRejects(t *testing.T, ctx context.Context, pool *pgxpool.Pool, args []string, runKey string) {
	t.Helper()
	if err := run(ctx, args); err == nil {
		t.Fatal("reconcile accepted target or quarantine drift")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE run_key=$1`, runKey).Scan(&status); err != nil || status != "applied" {
		t.Fatalf("failed reconcile status=%q err=%v", status, err)
	}
}

func sidebarHistoryFullFactsManifest(t *testing.T) (string, string) {
	t.Helper()
	at := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	alliance := "联盟甲"
	from, until := at.AddDate(0, -1, 0), at.AddDate(0, 1, 0)
	redeemedAt := at.Add(-48 * time.Hour)
	m := manifest{
		SchemaVersion: currentSchemaVersion, RunKey: "sidebar-full-facts-pg-001", SourceSystem: productionSourceSystem,
		UnionIDScope: "wechat-open-platform:primary", CapturedAt: at,
		Entitlements: []sourceEntitlement{
			{SourceID: 101, UnionID: "sidebar-full-union-1", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "待续", Alliance: &alliance, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 102, UnionID: "sidebar-full-union-2", ServiceProductID: 12, ProductName: "季度服务", Status: "expired", StartAt: at.AddDate(0, -4, 0), EndAt: at.AddDate(0, -1, 0), Remark: "过期", CreatedAt: at.AddDate(0, -4, 0), UpdatedAt: at},
			{SourceID: 103, UnionID: "sidebar-full-union-3", ServiceProductID: 11, ProductName: "年度服务", Status: "refunded", StartAt: at.AddDate(0, -3, 0), EndAt: at.AddDate(0, -2, 0), Remark: "退款", CreatedAt: at.AddDate(0, -3, 0), UpdatedAt: at},
			{SourceID: 104, UnionID: "sidebar-full-unresolved", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "隔离", CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
		},
		Coupons: []sourceCoupon{
			{SourceID: 201, UnionID: "sidebar-full-union-1", CouponID: 61, Status: "available", ClaimedAt: at.AddDate(0, -1, 0), ValidFrom: &from, ValidUntil: &until, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 202, UnionID: "sidebar-full-union-2", CouponID: 62, Status: "consumed", ClaimedAt: at.AddDate(0, -2, 0), ValidFrom: &from, ValidUntil: &until, RedeemedAt: &redeemedAt, CreatedAt: at.AddDate(0, -2, 0), UpdatedAt: at},
			{SourceID: 203, UnionID: "sidebar-full-union-3", CouponID: 61, Status: "expired", ClaimedAt: at.AddDate(0, -3, 0), ValidFrom: &from, ValidUntil: &until, CreatedAt: at.AddDate(0, -3, 0), UpdatedAt: at},
			{SourceID: 204, UnionID: "sidebar-full-unresolved", CouponID: 61, Status: "expired", ClaimedAt: at.AddDate(0, -3, 0), ValidFrom: &from, ValidUntil: &until, CreatedAt: at.AddDate(0, -3, 0), UpdatedAt: at},
		},
	}
	return writeSidebarHistoryManifest(t, m)
}

func sidebarHistorySeedFullFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, unionID := range []string{"sidebar-full-union-1", "sidebar-full-union-2", "sidebar-full-union-3"} {
		var customerID int64
		if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'unionid','wechat-open-platform:primary',$2,'verified','provider-history:sidebar-full',1,clock_timestamp())`, customerID, unionID); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	insertCoupon := func(name string) int64 {
		var id int64
		err := pool.QueryRow(ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,use_starts_at,use_ends_at,instructions,created_by,updated_by,version,created_at,updated_at)
			VALUES($1,100,'CNY','published',100,10,0,$2,$3,'fixed_range',$2,$3,'',1,1,1,$2,$2) RETURNING id`, name, at.AddDate(0, -2, 0), at.AddDate(0, 2, 0)).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	couponOne, couponTwo := insertCoupon("历史券甲"), insertCoupon("历史券乙")
	if _, err := pool.Exec(ctx, `INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_id) VALUES
		($1,'product','service_period_products','11',501),
		($1,'product','service_period_products','12',502),
		($1,'coupon','commerce_coupons','61',$2),
		($1,'coupon','commerce_coupons','62',$3)`, productionSourceSystem, couponOne, couponTwo); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLSidebarHistoryLegacyNoAllianceDigestReplayAndQuarantine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := sidebarHistoryCommandDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	unresolvedAlliance := "不应写入"
	m := manifest{
		SchemaVersion: 1, RunKey: "sidebar-alliance-legacy-pg-001", SourceSystem: productionSourceSystem,
		UnionIDScope: "wechat-open-platform:primary", CapturedAt: at,
		Entitlements: []sourceEntitlement{
			// This is the protected layout from before alliance existed. It must
			// serialize with no alliance key and therefore retain its old receipt.
			{SourceID: 17, UnionID: "sidebar-legacy-union", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "旧快照", CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			// An unresolved new-source row proves Reconcile validates quarantine
			// facts rather than requiring every entitlement to have a target.
			{SourceID: 18, UnionID: "sidebar-legacy-unresolved", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &unresolvedAlliance, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
		},
		Coupons: []sourceCoupon{},
	}
	path, digest := writeSidebarHistoryManifest(t, m)
	legacyRaw, err := json.Marshal(m.Entitlements[0])
	if err != nil || bytes.Contains(legacyRaw, []byte(`"alliance"`)) {
		t.Fatalf("legacy digest row=%s err=%v", legacyRaw, err)
	}
	legacyDigest := sha256.Sum256(legacyRaw)

	var customerID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'unionid','wechat-open-platform:primary','sidebar-legacy-union','verified','provider-history:sidebar-alliance',1,clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_id)
		VALUES($1,'product','service_period_products','11',501)`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
	var targetID int64
	if err = pool.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,alliance,source_digest,created_at,updated_at)
		VALUES($1,'17',$2,501,'年度服务','active',$3,$4,'旧快照',NULL,$5,$3,$6) RETURNING id`, productionSourceSystem, customerID, at.AddDate(0, -1, 0), at.AddDate(0, 11, 0), legacyDigest[:], at).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifestRaw)
	var batchID int64
	if err = pool.QueryRow(ctx, `INSERT INTO sidebar_history_migration_batches(run_key,manifest_digest,source_system,input_count,status)
		VALUES($1,$2,$3,2,'applying') RETURNING id`, m.RunKey, manifestDigest[:], m.SourceSystem).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO sidebar_history_migration_source_map(batch_id,source_kind,source_key,source_digest,customer_id,target_table,target_id,disposition)
		VALUES($1,'service_period_entitlement','17',$2,$3,'order_service_entitlements',$4,'imported')`, batchID, legacyDigest[:], customerID, targetID); err != nil {
		t.Fatal(err)
	}

	applyArgs := []string{"--mode=apply", "--snapshot=" + path, "--manifest-sha256=" + digest, "--confirm-apply"}
	if err = run(ctx, applyArgs); err != nil {
		t.Fatalf("legacy protected replay: %v", err)
	}
	var targets, mappings, quarantines int64
	var targetDigest []byte
	var alliance *string
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
		(SELECT count(*) FROM sidebar_history_migration_source_map),
		(SELECT count(*) FROM sidebar_history_migration_quarantine),
		(SELECT source_digest FROM order_service_entitlements WHERE id=$2),
		(SELECT alliance FROM order_service_entitlements WHERE id=$2)`, productionSourceSystem, targetID).Scan(&targets, &mappings, &quarantines, &targetDigest, &alliance); err != nil {
		t.Fatal(err)
	}
	if targets != 1 || mappings != 1 || quarantines != 1 || !bytes.Equal(targetDigest, legacyDigest[:]) || alliance != nil {
		t.Fatalf("legacy replay targets=%d maps=%d quarantines=%d digest=%x alliance=%v", targets, mappings, quarantines, targetDigest, alliance)
	}

	if err = run(ctx, []string{"--mode=reconcile", "--snapshot=" + path, "--manifest-sha256=" + digest}); err != nil {
		t.Fatalf("legacy replay reconcile: %v", err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM sidebar_history_migration_batches WHERE id=$1`, batchID).Scan(&status); err != nil || status != "reconciled" {
		t.Fatalf("legacy replay status=%q err=%v", status, err)
	}
}

func sidebarHistoryAssertAllianceTargets(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT source_key,alliance FROM order_service_entitlements
		WHERE source_system=$1 ORDER BY source_key`, productionSourceSystem)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type target struct {
		key      string
		alliance *string
	}
	got := []target{}
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.key, &item.alliance); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].key != "7" || got[0].alliance != nil || got[1].key != "8" || got[1].alliance == nil || *got[1].alliance != "" || got[2].key != "9" || got[2].alliance == nil || *got[2].alliance != "联盟甲" {
		t.Fatalf("alliance facts not preserved: %#v", got)
	}
	var mappings, entitlementCount, quarantines int64
	var quarantineReason string
	if err = pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM sidebar_history_migration_source_map WHERE source_kind='service_period_entitlement'),
			(SELECT count(*) FROM order_service_entitlements WHERE source_system=$1),
			(SELECT count(*) FROM sidebar_history_migration_quarantine WHERE source_kind='service_period_entitlement'),
			(SELECT reason FROM sidebar_history_migration_quarantine WHERE source_kind='service_period_entitlement' AND source_key='10')`, productionSourceSystem).Scan(&mappings, &entitlementCount, &quarantines, &quarantineReason); err != nil {
		t.Fatal(err)
	}
	if mappings != 3 || entitlementCount != 3 || quarantines != 1 || quarantineReason != "identity_not_found" {
		t.Fatalf("replay conservation maps=%d entitlements=%d quarantines=%d reason=%q", mappings, entitlementCount, quarantines, quarantineReason)
	}
}

func sidebarHistoryAllianceManifest(t *testing.T) (string, string) {
	t.Helper()
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	empty := ""
	value := "联盟甲"
	m := manifest{
		SchemaVersion: 1,
		RunKey:        "sidebar-alliance-pg-001",
		SourceSystem:  productionSourceSystem,
		UnionIDScope:  "wechat-open-platform:primary",
		CapturedAt:    at,
		Entitlements: []sourceEntitlement{
			{SourceID: 7, UnionID: "sidebar-union-7", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: nil, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 8, UnionID: "sidebar-union-8", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &empty, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			{SourceID: 9, UnionID: "sidebar-union-9", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &value, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
			// This row is deliberately unresolved. Reconcile must validate its
			// immutable quarantine receipt rather than demanding a target map.
			{SourceID: 10, UnionID: "sidebar-unresolved", ServiceProductID: 11, ProductName: "年度服务", Status: "active", StartAt: at.AddDate(0, -1, 0), EndAt: at.AddDate(0, 11, 0), Remark: "", Alliance: &value, alliancePresent: true, CreatedAt: at.AddDate(0, -1, 0), UpdatedAt: at},
		},
		Coupons: []sourceCoupon{},
	}
	return writeSidebarHistoryManifest(t, m)
}

func writeSidebarHistoryManifest(t *testing.T, m manifest) (string, string) {
	t.Helper()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join(t.TempDir(), "frozen-sidebar-alliance.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the exact protected-file digest construction obvious at the test
	// boundary rather than accepting a hard-coded confirmation hash.
	digest := sha256.Sum256(raw)
	return path, hex.EncodeToString(digest[:])
}

func seedSidebarHistoryIdentitiesAndDefinition(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, unionID := range []string{"sidebar-union-7", "sidebar-union-8", "sidebar-union-9"} {
		var customerID int64
		if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
			VALUES($1,'unionid','wechat-open-platform:primary',$2,'verified','provider-history:sidebar-alliance',1,clock_timestamp())`, customerID, unionID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO config_definition_import_source_maps(source_system,domain,source_kind,source_key,target_id)
		VALUES($1,'product','service_period_products','11',501)`, productionSourceSystem); err != nil {
		t.Fatal(err)
	}
}

func sidebarHistoryCommandDatabase(t *testing.T, ctx context.Context) (string, *pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping sidebar-history command PostgreSQL journey")
	}
	adminConfig, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "sidebar_history_command_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err = sidebarHistoryCommandMigrate(ctx, pool); err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func sidebarHistoryCommandMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{
		"0002_identity.sql",
		"0020_order.sql",
		"0024_order_product_version.sql",
		"0049_order_history_attribution.sql",
		"0011_coupon_rules.sql",
		"0055_order_service_entitlements.sql",
		"0056_coupon_customer_claims.sql",
		"0070_service_period_entitlement_fulfillment.sql",
		"0076_order_checkout_snapshots.sql",
		"0088_order_service_entitlement_alliance.sql",
		"0058_sidebar_history_migration.sql",
	} {
		raw, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			return errors.New(name + ": " + err.Error())
		}
	}
	// The production definition-import migration owns this immutable mapping.
	// The command only reads these five columns; a narrow local projection keeps
	// this test focused on the command while avoiding GroupOps setup unrelated to
	// an entitlement import.
	_, err := pool.Exec(ctx, `
		CREATE TABLE config_definition_import_source_maps (
			source_system TEXT NOT NULL,
			domain TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_key TEXT NOT NULL,
			target_id BIGINT NOT NULL CHECK (target_id > 0),
			UNIQUE(source_system,domain,source_kind,source_key)
		)`)
	return err
}
