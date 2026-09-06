package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func TestCustomerTagHistoryCLIExtractDryRunApplyReplayVerifyAndDrift(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, cleanup := tagHistoryPool(t, ctx, url)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", urlWithSchema(t, url, pool))
	seedTagHistory(t, ctx, pool)
	stream := filepath.Join(t.TempDir(), "source.stream")
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	rows := []sourceJob{
		{ID: 71, EffectType: "wecom.contact.tag.mark", Operation: "tag_mark", TargetID: "external-one", ActorID: "staff-one", Payload: json.RawMessage(`{"tag_ids":["provider-tag-one"]}`), Status: "provider_result_received", CreatedAt: at},
		{ID: 72, EffectType: "wecom.contact.tag.unmark", Operation: "tag_unmark", TargetID: "external-missing", ActorID: "staff-one", Payload: json.RawMessage(`{"tag_ids":["provider-tag-one"]}`), Status: "provider_accepted", CreatedAt: at.Add(time.Second)},
	}
	if err = writeStream(stream, at, rows); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "tag-history.json")
	if err = run(ctx, []string{"--mode=extract", "--source-stream=" + stream, "--snapshot=" + snapshot, "--wecom-corp-id=corp-test"}); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(snapshot); statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot protection info=%v err=%v", info, statErr)
	}
	_, digest, err := load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=inspect", "--snapshot=" + snapshot}); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=dry-run", "--snapshot=" + snapshot}); err != nil {
		t.Fatal(err)
	}
	apply := []string{"--mode=apply", "--snapshot=" + snapshot, "--manifest-sha256=" + digest, "--confirm-apply"}
	if err = run(ctx, apply); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, apply); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshot}); err != nil {
		t.Fatal(err)
	}
	var receipts, effects, commands, jobs int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM customer_tag_history_receipts),(SELECT count(*) FROM external_effects),(SELECT count(*) FROM customer_tag_commands),(SELECT count(*) FROM river_job)`).Scan(&receipts, &effects, &commands, &jobs); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 || effects != 0 || commands != 0 || jobs != 0 {
		t.Fatalf("receipts=%d effects=%d commands=%d river=%d", receipts, effects, commands, jobs)
	}

	// A newer protected capture may overlap the first one. Its exact source
	// fact replays globally, and verification must read that global receipt.
	m, _, err := load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	m.CapturedAt = m.CapturedAt.Add(time.Minute)
	overlap := filepath.Join(t.TempDir(), "overlap.json")
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(overlap, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	_, overlapDigest, err := load(overlap)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=apply", "--snapshot=" + overlap, "--manifest-sha256=" + overlapDigest, "--confirm-apply"}); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + overlap}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM customer_tag_history_receipts`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("overlap receipts=%d err=%v", receipts, err)
	}
	bad := filepath.Join(t.TempDir(), "drift.stream")
	if err = os.WriteFile(bad, []byte(timeMarker+at.Format(time.RFC3339Nano)+"\n"+streamMarker+hex.EncodeToString([]byte(`{"id":73,"effect_type":"wecom.contact.tag.mark","operation":"tag_mark","target_id":"x","actor_id":"staff-one","payload_json":{"tag_ids":["provider-tag-one"]},"status":"queued","created_at":"2026-09-06T12:00:00Z","unexpected":true}`))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = extract(bad, "corp-test"); err == nil {
		t.Fatal("source field drift was accepted")
	}
}

func writeStream(path string, at time.Time, rows []sourceJob) error {
	data := []byte(timeMarker + at.UTC().Format(time.RFC3339Nano) + "\n")
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return err
		}
		data = append(data, []byte(streamMarker+hex.EncodeToString(raw)+"\n")...)
	}
	return os.WriteFile(path, data, 0600)
}
func tagHistoryPool(t *testing.T, ctx context.Context, url string) (*pgxpool.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_tag_history_" + hex.EncodeToString(raw)
	id := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+id); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0019_tag_catalog_sync_projection.sql", "0093_customer_tag_commands.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+id+" CASCADE")
		admin.Close()
	}
}
func seedTagHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO admin_users(id,username,password_hash,display_name,wecom_userid,is_active,session_version) OVERRIDING SYSTEM VALUE VALUES(7,'staff-one','$argon2id$test','Staff','staff-one',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(11,'active'); INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES(11,'wecom_external_userid','wecom-corp:corp-test','external-one','verified','test',1,clock_timestamp()); INSERT INTO tag_groups(id,group_name,sort_order) OVERRIDING SYSTEM VALUE VALUES(13,'history',1); INSERT INTO tag_catalog_tags(id,group_id,tag_name,sort_order) OVERRIDING SYSTEM VALUE VALUES(17,13,'tag',1); INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('provider-tag-one',17)`)
	if err != nil {
		t.Fatal(err)
	}
}
func urlWithSchema(t *testing.T, url string, pool *pgxpool.Pool) string {
	t.Helper()
	var schema string
	if err := pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return url + "&search_path=" + schema
}
