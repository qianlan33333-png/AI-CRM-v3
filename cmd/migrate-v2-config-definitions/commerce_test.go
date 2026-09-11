package main

import (
	"context"
	"strings"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	configtarget "github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/target"
)

func TestCommerceApplyFailsBeforeDatabaseAccess(t *testing.T) {
	for _, mode := range []string{"apply", "verify", "history-extract", "history-apply"} {
		if err := run(context.Background(), []string{"--commerce-only", "--mode=" + mode}); err == nil || !strings.Contains(err.Error(), "supports extract, inspect and dry-run only") {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
}

func TestCommercePreflightReusesMappingsAndReportsDrift(t *testing.T) {
	pool, cleanup := configMigrationIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	actor := configMigrationActor(t, ctx, pool)
	legacy := configMigrationFixture(t, strings.Repeat("b", 40))
	d, err := legacy.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = configMigrationRunner(t, pool).Apply(ctx, legacy, d, actor); err != nil {
		t.Fatal(err)
	}
	s := legacy
	s.Manifest.Scope = "commerce-only"
	s.GroupPlans = nil
	s.GroupNodes = nil
	s.GroupAssets = nil
	s.Agents = nil
	for i := range s.Coupons {
		slug := "public-coupon"
		issued := int64(i)
		s.Coupons[i].PublicSlug = &slug
		s.Coupons[i].IssuedCount = &issued
	}
	refresh := func() {
		t.Helper()
		if err := source.PopulateManifest(&s, s.Manifest.SourceSystem, s.Manifest.SourceRevision, s.Manifest.SnapshotAt); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	report, err := configtarget.InspectCommerceTarget(ctx, pool.Native(), s, actor)
	if err != nil {
		t.Fatal(err)
	}
	if report.ApplyReady || report.Counts["mapped_source_equal"] != 63 {
		t.Fatalf("unsafe preflight %#v", report)
	}
	var before int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM config_definition_import_source_maps`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	s.Products[0].Name = "source changed"
	fresh := s.Products[1]
	fresh.ID = 100000
	fresh.ProductCode = "new-cutover-product"
	s.Products = append(s.Products, fresh)
	if _, err = pool.Native().Exec(ctx, `UPDATE products SET version=version+1 WHERE id=(SELECT target_id FROM config_definition_import_source_maps WHERE source_kind='wechat_pay_products' AND source_key=$1)`, "2"); err != nil {
		t.Fatal(err)
	}
	refresh()
	report, err = configtarget.InspectCommerceTarget(ctx, pool.Native(), s, actor)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["conflict_source_drift"] < 1 || report.Counts["new_source"] != 1 || report.Counts["conflict_target_edited"] < 1 {
		t.Fatalf("missing conflicts %#v", report)
	}
	var after int
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM config_definition_import_source_maps`).Scan(&after); err != nil || after != before {
		t.Fatalf("preflight mutated mappings %d %d %v", before, after, err)
	}
}
