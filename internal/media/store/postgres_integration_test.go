package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLReceiptAuditOutboxAndPayloadDrift(t *testing.T) {
	url := os.Getenv("AICRM_DATABASE_URL")
	if url == "" {
		t.Skip("AICRM_DATABASE_URL not configured")
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
	schema := "media_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0006_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	key := "integration-key-0001"
	first, err := repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "素材", "appid": "wx123", "pagepath": "pages/a", "title": "卡片", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "素材", "appid": "wx123", "pagepath": "pages/a", "title": "卡片", "enabled": true})
	if err != nil || fmt.Sprint(replay["id"]) != fmt.Sprint(first["id"]) {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if _, err = repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "漂移", "appid": "wx123", "pagepath": "pages/a", "title": "卡片"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload drift=%v", err)
	}
	var receipts, audits, outbox int
	for _, q := range []string{"SELECT count(*) FROM media_operation_receipts", "SELECT count(*) FROM media_audit_events", "SELECT count(*) FROM media_outbox"} {
		var n int
		if err = native.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if receipts == 0 {
			receipts = n
		} else if audits == 0 {
			audits = n
		} else {
			outbox = n
		}
	}
	if receipts != 1 || audits != 1 || outbox != 1 {
		t.Fatalf("atomic facts receipts=%d audit=%d outbox=%d", receipts, audits, outbox)
	}
}
