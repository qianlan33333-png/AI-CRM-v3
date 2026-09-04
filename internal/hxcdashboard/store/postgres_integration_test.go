package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPublishRetainsReceiptLineageAfterEightProjections(t *testing.T) {
	native, uow, cleanup := hxcIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	store := NewPostgreSQL(native)

	for index := 1; index <= 9; index++ {
		var runID int64
		requestDigest := make([]byte, 32)
		requestDigest[31] = byte(index)
		if err := native.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(
			run_key,request_digest,trigger,identity_mode,status,source_count,processed_count,identity_replay_verified_count
		) VALUES($1,$2,'initial','apply','publishing',1,1,1) RETURNING id`, "retention-run-"+time.Unix(int64(index), 0).UTC().Format(time.RFC3339), requestDigest).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		projection := hxcRetentionProjection(index)
		if err := uow.Within(ctx, func(txCtx context.Context) error {
			_, publishErr := store.Publish(txCtx, runID, projection)
			return publishErr
		}); err != nil {
			t.Fatalf("publish projection %d: %v", index, err)
		}
	}

	var versions, receipts, retainedRows, oldestVersionRows int64
	if err := native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hxc_dashboard_versions),
		(SELECT count(*) FROM hxc_dashboard_refresh_runs WHERE status='succeeded' AND projection_id IS NOT NULL),
		(SELECT count(*) FROM hxc_dashboard_rows),
		(SELECT count(*) FROM hxc_dashboard_rows WHERE projection_id=(SELECT projection_id FROM hxc_dashboard_refresh_runs ORDER BY id LIMIT 1))
	`).Scan(&versions, &receipts, &retainedRows, &oldestVersionRows); err != nil {
		t.Fatal(err)
	}
	if versions != 9 || receipts != 9 || retainedRows != 8 || oldestVersionRows != 0 {
		t.Fatalf("versions=%d receipts=%d rows=%d oldest_rows=%d", versions, receipts, retainedRows, oldestVersionRows)
	}
}

func hxcRetentionProjection(index int) domain.Projection {
	sourceDigest, projectionDigest, subjectDigest := [32]byte{}, [32]byte{}, [32]byte{}
	sourceDigest[31], projectionDigest[31], subjectDigest[31] = byte(index), byte(index), byte(index)
	asOf := time.Date(2026, 9, 5, 0, 0, index, 0, time.UTC)
	return domain.Projection{
		AsOf: asOf, SourceDigest: sourceDigest, ProjectionDigest: projectionDigest,
		Counts: domain.Counts{Total: 1, RegisteredNoActiveMembership: 1, Unmatched: 1, PendingObservation: 1},
		Rows: []domain.ProjectionRow{{
			SubjectDigest: subjectDigest, UserRef: "HXC-000000000001", Stage: domain.RegisteredNoActiveMembership,
			SourceRow: domain.SourceRow{
				MembershipAttribution: "none", CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`), SourceUpdatedAt: asOf,
			},
			IdentityState: domain.Unmatched, MatchedBy: "none", IdentityReasonCode: "no_match",
		}},
	}
}

func hxcIntegrationPool(t *testing.T) (*pgxpool.Pool, *platformpostgres.UnitOfWork, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_hxc_test_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `CREATE TABLE customers(id BIGINT PRIMARY KEY)`); err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	for _, migration := range []string{"0028_hxc_dashboard.sql", "0064_hxc_dashboard_identity_v2.sql"} {
		contents, readErr := os.ReadFile(hxcMigrationPath(t, migration))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = native.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
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
	return native, uow, func() {
		wrapped.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func hxcMigrationPath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate HXC store integration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
}
