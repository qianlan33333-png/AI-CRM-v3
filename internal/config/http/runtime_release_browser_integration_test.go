package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"net/http/httptest"
)

// OneID decision: not involved. This JSDOM regression journey mutates only Config-owned numeric
// runtime policy. Persistence decision: the actual HTTP handler reaches the
// real Config store, whose release, pointer, audit, receipt, and outbox share
// one PostgreSQL UoW; no Provider is constructed.
func TestRuntimeReleaseHostJSDOMJourneyUsesActualPostgreSQLHTTP(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1)
	if err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: principal}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate runtime release browser journey")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "webshell", "runtime_config_releases_pg.test.mjs")
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "AICRM_RUNTIME_RELEASE_TEST_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime release browser journey: %v output=%s", err, output)
	}
	var releases, published, superseded, usage int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state='published'),count(*) FILTER (WHERE state='superseded') FROM config_runtime_releases`).Scan(&releases, &published, &superseded); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage`).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if releases != 2 || published != 1 || superseded != 1 || usage != 0 {
		t.Fatalf("browser release facts releases/published/superseded/usage=%d/%d/%d/%d output=%s", releases, published, superseded, usage, output)
	}
}

func runtimeReleaseBrowserPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping runtime release browser PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "runtime_release_browser_" + hex.EncodeToString(random[:])
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
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close(ctx)
		t.Fatal("locate runtime release browser migration")
	}
	for _, name := range []string{"0013_automation_agents.sql", "0015_config_adminops.sql", "0043_automation_runtime.sql", "0094_runtime_config_releases.sql"} {
		payload, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(payload)); execErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}
