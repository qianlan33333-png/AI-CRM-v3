package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

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
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("fresh migration: %v", err)
	}
	if err = applyMigrations(ctx, pool, filesystem); err != nil {
		t.Fatalf("upgrade replay: %v", err)
	}
	var applied int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM platform_schema_migrations`).Scan(&applied); err != nil || applied != 9 {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	for _, table := range []string{"media_blobs", "media_references", "media_attachment_upload_parts", "tag_groups", "tag_catalog_tags", "tag_provider_observations"} {
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
