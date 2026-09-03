package store

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
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type productFacts map[int64]productport.ProductOption

func (p productFacts) ReadProductTarget(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	x, ok := p[int64(id)]
	if !ok {
		return productport.ProductOption{}, errors.New("missing")
	}
	if x.ProductType != kind {
		return productport.ProductOption{}, errors.New("wrong product type")
	}
	return x, nil
}

func TestPostgreSQLCouponRulesAtomicReceiptAuditAndOutbox(t *testing.T) {
	native, cleanup := couponIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, table := range []string{"coupon_rules", "coupon_rule_targets", "coupon_operation_receipts", "coupon_audit_events", "coupon_outbox"} {
		var owned bool
		if err := native.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s owner=%t err=%v", table, owned, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := couponapp.NewService(uow, repository, productFacts{9: {ID: 9, ProductType: productport.ProductOptionStandard, Currency: "CNY", PriceMinor: 1000}}, repository)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	days := int32(7)
	input := couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "新客券", DiscountAmountTotal: 100, TotalIssueLimit: 10, PerUserIssueLimit: 1, ClaimStartsAt: now, ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:9"}}, Actor: 7, IdempotencyKey: "coupon-pg-create-key-0001"}
	created, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Create(ctx, input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	if _, err = service.Publish(ctx, created.ID, 7, "coupon-pg-publish-key-01"); err != nil {
		t.Fatal(err)
	}
	var repositoryItems []couponport.Coupon
	if err = uow.Within(ctx, func(txCtx context.Context) error {
		repositoryItems, err = repository.List(txCtx, 50, 0, "", "")
		return err
	}); err != nil {
		t.Fatalf("repository list persisted coupons: %v", err)
	}
	if len(repositoryItems) != 1 || repositoryItems[0].ID != created.ID {
		t.Fatalf("repository list=%#v", repositoryItems)
	}
	page, err := service.List(ctx, 50, 0, "", "")
	if err != nil {
		t.Fatalf("list persisted coupons: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID || len(page.Items[0].TargetRefs) != 1 || page.Items[0].TargetRefs[0] != "standard_product:9" {
		t.Fatalf("persisted list=%#v", page)
	}
	var rules, targets, receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM coupon_rules),(SELECT count(*) FROM coupon_rule_targets),(SELECT count(*) FROM coupon_operation_receipts),(SELECT count(*) FROM coupon_audit_events),(SELECT count(*) FROM coupon_outbox)`).Scan(&rules, &targets, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if rules != 1 || targets != 1 || receipts != 2 || audits != 2 || outbox != 2 {
		t.Fatalf("rows rules=%d targets=%d receipts=%d audits=%d outbox=%d", rules, targets, receipts, audits, outbox)
	}
	if _, err = native.Exec(ctx, `UPDATE coupon_audit_events SET event_type=event_type`); err == nil {
		t.Fatal("audit update unexpectedly succeeded")
	}
	// Outbox delivery bookkeeping deliberately remains mutable by its future
	// owner; only the audit ledger is database-append-only.
}

func couponIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping coupon PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_coupon_test_" + hex.EncodeToString(random[:])
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
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0011_coupon_rules.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply coupon migration: %v", err)
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
