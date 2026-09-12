package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	aiassistant "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant"
	hxcdashboard "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

func TestCurrentReleaseReadinessRequiresAppliedMigrationsPostgreSQL(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := readinessIntegrationPool(t, ctx)
	defer cleanup()
	config := readinessEnabledConfig()
	createReadinessMigrationLedger(t, ctx, pool)
	if _, err := pool.Exec(ctx, `CREATE TABLE order_service_entitlements (alliance text)`); err != nil {
		t.Fatal(err)
	}
	required := requiredCurrentReleaseMigrations(config)
	for _, version := range required {
		insertReadinessMigration(t, ctx, pool, version)
	}
	handler := currentReleaseReadinessHandler(t, pool, config)
	for _, missing := range []string{"0124", "0149"} {
		if !containsMigration(required, missing) {
			t.Fatalf("runtime-required migration list omitted %s", missing)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM platform_schema_migrations WHERE version=$1`, missing); err != nil {
			t.Fatal(err)
		}
		assertReadinessStatus(t, handler, http.StatusServiceUnavailable)
		if err := checkCurrentReleaseSchema(ctx, pool, config); err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing %s readiness error=%v", missing, err)
		}

		insertReadinessMigration(t, ctx, pool, missing)
		assertReadinessStatus(t, handler, http.StatusOK)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE order_service_entitlements DROP COLUMN alliance`); err != nil {
		t.Fatal(err)
	}
	assertReadinessStatus(t, handler, http.StatusServiceUnavailable)
	if err := checkCurrentReleaseSchema(ctx, pool, config); err == nil || !strings.Contains(err.Error(), "order_service_entitlements.alliance") {
		t.Fatalf("missing global column readiness error=%v", err)
	}
}

func TestCurrentReleaseReadinessAllowsDisabledOptionalProjectionsPostgreSQL(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := readinessIntegrationPool(t, ctx)
	defer cleanup()
	config := platformconfig.Runtime{}
	createReadinessMigrationLedger(t, ctx, pool)
	if _, err := pool.Exec(ctx, `CREATE TABLE order_service_entitlements (alliance text)`); err != nil {
		t.Fatal(err)
	}
	for _, version := range requiredCurrentReleaseMigrations(config) {
		insertReadinessMigration(t, ctx, pool, version)
	}
	if err := checkCurrentReleaseSchema(ctx, pool, config); err != nil {
		t.Fatalf("disabled optional projections blocked readiness: %v", err)
	}
	for _, version := range []string{"0136", "0137", "0138", "0139"} {
		if containsMigration(requiredCurrentReleaseMigrations(config), version) {
			t.Fatalf("disabled projection unexpectedly requires migration %s", version)
		}
	}
	assertReadinessStatus(t, currentReleaseReadinessHandler(t, pool, config), http.StatusOK)
}

func TestCurrentReleaseReadinessChecksCurrentModuleStructuresPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, cleanup := readinessIntegrationPool(t, ctx)
	defer cleanup()
	applyAllReadinessMigrations(t, ctx, pool)
	config := readinessEnabledConfig()
	if err := checkCurrentReleaseSchema(ctx, pool, config); err != nil {
		t.Fatalf("current migration ledger readiness: %v", err)
	}
	if err := aiassistant.NewModuleRegistration().Readiness(ctx, pool); err != nil {
		t.Fatalf("AI Assistant lifecycle readiness: %v", err)
	}
	if err := wecom.CheckGroupMembershipReadiness(ctx, pool); err != nil {
		t.Fatalf("WeCom group membership readiness: %v", err)
	}
	if err := hxcdashboard.NewModuleRegistration().Readiness(ctx, pool); err != nil {
		t.Fatalf("HXC registration coverage readiness: %v", err)
	}
	handler := currentReleaseAndCurrentModuleReadinessHandler(t, pool, config)
	assertReadinessStatus(t, handler, http.StatusOK)

	if _, err := pool.Exec(ctx, `ALTER TABLE wecom_group_provider_facts DROP COLUMN identity_hashes`); err != nil {
		t.Fatal(err)
	}
	assertReadinessStatus(t, handler, http.StatusServiceUnavailable)
	if err := wecom.CheckGroupMembershipReadiness(ctx, pool); err == nil || !strings.Contains(err.Error(), "WeCom group membership schema") {
		t.Fatalf("missing WeCom field readiness error=%v", err)
	}
	if err := hxcdashboard.NewModuleRegistration().Readiness(ctx, pool); err != nil {
		t.Fatalf("HXC readiness changed after WeCom field removal: %v", err)
	}

	if _, err := pool.Exec(ctx, `DROP TABLE hxc_registration_coverage`); err != nil {
		t.Fatal(err)
	}
	if err := hxcdashboard.NewModuleRegistration().Readiness(ctx, pool); err == nil || !strings.Contains(err.Error(), "HXC dashboard schema") {
		t.Fatalf("missing HXC coverage readiness error=%v", err)
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE ai_assistant_excel_imports DROP COLUMN source_origin`); err != nil {
		t.Fatal(err)
	}
	if err := aiassistant.NewModuleRegistration().Readiness(ctx, pool); err == nil || !strings.Contains(err.Error(), "AI Assistant schema") {
		t.Fatalf("missing AI Excel field readiness error=%v", err)
	}
}

func readinessEnabledConfig() platformconfig.Runtime {
	config := platformconfig.Runtime{}
	config.WeCom.ChannelProviderReadEnabled = true
	config.HXCDashboard.Enabled = true
	return config
}

func currentReleaseReadinessHandler(t *testing.T, pool *pgxpool.Pool, config platformconfig.Runtime) http.Handler {
	t.Helper()
	handler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{
		ReleaseSHA: "readiness-test",
		Readiness:  platformruntime.ReadinessFunc(func(ctx context.Context) error { return checkCurrentReleaseSchema(ctx, pool, config) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func currentReleaseAndCurrentModuleReadinessHandler(t *testing.T, pool *pgxpool.Pool, config platformconfig.Runtime) http.Handler {
	t.Helper()
	handler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{
		ReleaseSHA: "readiness-test",
		Readiness: platformruntime.ReadinessFunc(func(ctx context.Context) error {
			if err := checkCurrentReleaseSchema(ctx, pool, config); err != nil {
				return err
			}
			if err := aiassistant.NewModuleRegistration().Readiness(ctx, pool); err != nil {
				return err
			}
			if config.WeCom.ChannelProviderReadEnabled {
				if err := wecom.CheckGroupMembershipReadiness(ctx, pool); err != nil {
					return err
				}
			}
			if config.HXCDashboard.Enabled {
				if err := hxcdashboard.NewModuleRegistration().Readiness(ctx, pool); err != nil {
					return err
				}
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertReadinessStatus(t *testing.T, handler http.Handler, want int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != want {
		t.Fatalf("readyz status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), want)
	}
}

func readinessIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is required for PostgreSQL readiness integration tests")
	}
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if version < 160000 || version >= 170000 {
		admin.Close()
		t.Fatalf("PostgreSQL 16 is required, server_version_num=%d", version)
	}
	var raw [6]byte
	if _, err = rand.Read(raw[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "readiness_it_" + hex.EncodeToString(raw[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}
}

func createReadinessMigrationLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE platform_schema_migrations (
		version text PRIMARY KEY,
		name text NOT NULL UNIQUE,
		checksum bytea NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
	)`); err != nil {
		t.Fatal(err)
	}
}

func insertReadinessMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO platform_schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, version, version+"_test.sql", make([]byte, sha256.Size)); err != nil {
		t.Fatal(err)
	}
}

func applyAllReadinessMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	createReadinessMigrationLedger(t, ctx, pool)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate readiness integration test")
	}
	migrationDirectory := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	entries, err := os.ReadDir(migrationDirectory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(migrationDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		version, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Fatalf("invalid migration name %q", name)
		}
		digest := sha256.Sum256(contents)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(contents)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO platform_schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, version, name, digest[:])
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply %s: %v", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit %s: %v", name, err)
		}
	}
}

func containsMigration(versions []string, want string) bool {
	for _, version := range versions {
		if version == want {
			return true
		}
	}
	return false
}
