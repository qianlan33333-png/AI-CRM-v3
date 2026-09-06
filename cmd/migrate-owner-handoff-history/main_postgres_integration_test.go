package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLOwnerHandoffHistoryExtractApplyReplayVerify exercises the
// operator command against real migrations. The protected capture is the only
// place legacy identifiers exist; applying it can only write the Customer
// history ledger and never creates effects or changes local ownership.
func TestPostgreSQLOwnerHandoffHistoryExtractApplyReplayVerify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, pool, cleanup := ownerHistoryDatabase(t, ctx)
	defer cleanup()
	t.Setenv("AICRM_DATABASE_URL", databaseURL)
	t.Setenv("AICRM_SURVEY_DATA_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")

	var customerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:history','external-historical','verified','history-fixture',1,clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('history-source','$argon2id$fixture','History source','source-user'),('history-target','$argon2id$fixture','History target','target-user')`); err != nil {
		t.Fatal(err)
	}

	captured := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	source := ownerHistorySource(t, captured, "observed")
	snapshot := filepath.Join(t.TempDir(), "owner-handoff-history.snapshot")
	if err := run(ctx, []string{"--mode=inspect-stream", "--source-stream=" + source, "--snapshot=" + snapshot}); err != nil {
		t.Fatalf("extract protected snapshot: %v", err)
	}
	protected, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(protected), "external-historical") || strings.Contains(string(protected), "source-user") {
		t.Fatal("protected snapshot leaked legacy identifier")
	}
	_, digest, err := load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	digestHex := hex.EncodeToString(digest[:])
	applyArgs := []string{"--mode=apply", "--snapshot=" + snapshot, "--manifest-sha256=" + digestHex, "--confirm-apply"}
	if err = run(ctx, applyArgs); err != nil {
		t.Fatalf("real apply: %v", err)
	}
	ownerHistoryAssertLedger(t, ctx, pool, customerID, 3, "applied")
	if err = run(ctx, applyArgs); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	ownerHistoryAssertLedger(t, ctx, pool, customerID, 3, "applied")
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshot, "--manifest-sha256=" + digestHex}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	ownerHistoryAssertLedger(t, ctx, pool, customerID, 3, "reconciled")

	// A later capture carrying the same old source ID but a changed legacy
	// result is evidence drift. It cannot silently overwrite the immutable
	// row, even when its run key differs.
	driftSource := ownerHistorySource(t, captured.Add(time.Second), "changed")
	driftSnapshot := filepath.Join(t.TempDir(), "owner-handoff-history-drift.snapshot")
	if err = run(ctx, []string{"--mode=inspect-stream", "--source-stream=" + driftSource, "--snapshot=" + driftSnapshot}); err != nil {
		t.Fatal(err)
	}
	_, driftDigest, err := load(driftSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=apply", "--snapshot=" + driftSnapshot, "--manifest-sha256=" + hex.EncodeToString(driftDigest[:]), "--confirm-apply"}); err == nil {
		t.Fatal("changed old source row was accepted")
	}
	ownerHistoryAssertLedger(t, ctx, pool, customerID, 3, "reconciled")

	// Verification is a ledger readback, not a count-only success marker.
	if _, err = pool.Exec(ctx, `UPDATE customer_owner_handoff_history_imports SET source_state='tampered' WHERE source_batch_id='legacy-result-001' AND source_line_id='1'`); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, []string{"--mode=verify", "--snapshot=" + snapshot, "--manifest-sha256=" + digestHex}); err == nil {
		t.Fatal("verify accepted ledger source drift")
	}
}

func ownerHistorySource(t *testing.T, captured time.Time, firstState string) string {
	t.Helper()
	rows := []sourceRow{
		{SourceBatchID: "legacy-result-001", SourceLineID: "1", Mode: "wecom_then_crm", SourceState: firstState, OccurredAt: captured, CorpScope: "wecom-corp:history", ExternalUserID: "external-historical", SourceOwnerUserID: "source-user", TargetOwnerUserID: "target-user", WeComStatus: "success", CRMStatus: "updated"},
		{SourceBatchID: "legacy-result-001", SourceLineID: "2", Mode: "local_only", SourceState: "legacy_missing_identity", OccurredAt: captured, CorpScope: "wecom-corp:history", ExternalUserID: "external-unresolved", SourceOwnerUserID: "source-user", TargetOwnerUserID: "target-user", WeComStatus: "not_requested", CRMStatus: "updated"},
		// Older rows with missing identifiers are retained as an explicit invalid
		// historical fact. They must not be guessed from the current directory.
		{SourceBatchID: "legacy-result-001", SourceLineID: "3", Mode: "local_only", SourceState: "invalid_source", OccurredAt: captured, CorpScope: "wecom-corp:history", ExternalUserID: "", SourceOwnerUserID: "", TargetOwnerUserID: "", WeComStatus: "", CRMStatus: ""},
	}
	file := filepath.Join(t.TempDir(), "legacy-owner-results.stream")
	var builder strings.Builder
	builder.WriteString(historyMarker + captured.Format(time.RFC3339Nano) + "\n")
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		builder.WriteString(historyRowMarker + hex.EncodeToString(raw) + "\n")
	}
	if err := os.WriteFile(file, []byte(builder.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func ownerHistoryAssertLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID int64, wantRows int64, wantRun string) {
	t.Helper()
	var rows, effects, owners, batches int64
	var run string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_owner_handoff_history_imports`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM customer_owner_handoff_history_runs`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM external_effects),(SELECT count(*) FROM customer_local_owners),(SELECT count(*) FROM customer_owner_handoff_batches)`).Scan(&effects, &owners, &batches); err != nil {
		t.Fatal(err)
	}
	var observedCustomer *int64
	var observed, pending, invalid int64
	if err := pool.QueryRow(ctx, `SELECT max(customer_id) FILTER (WHERE imported_state='observed'),count(*) FILTER (WHERE imported_state='observed'),count(*) FILTER (WHERE imported_state='pending_mapping'),count(*) FILTER (WHERE imported_state='invalid') FROM customer_owner_handoff_history_imports`).Scan(&observedCustomer, &observed, &pending, &invalid); err != nil {
		t.Fatal(err)
	}
	if rows != wantRows || run != wantRun || observed != 1 || pending != 1 || invalid != 1 || observedCustomer == nil || *observedCustomer != customerID || effects != 0 || owners != 0 || batches != 0 {
		t.Fatalf("history ledger rows=%d run=%q observed=%d pending=%d invalid=%d customer=%v effects=%d owners=%d batches=%d", rows, run, observed, pending, invalid, observedCustomer, effects, owners, batches)
	}
}

func ownerHistoryDatabase(t *testing.T, ctx context.Context) (string, *pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff history PostgreSQL integration test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_owner_handoff_history_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0092_customer_owner_handoff.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(raw)); execErr != nil {
			pool.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return databaseURL + "&search_path=" + schema, pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
