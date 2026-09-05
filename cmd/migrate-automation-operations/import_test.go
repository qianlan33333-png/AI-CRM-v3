package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type resolverStub struct {
	results map[string]identityport.ResolveResult
	err     error
}

func (stub resolverStub) Resolve(_ context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	if stub.err != nil {
		return identityport.ResolveResult{}, stub.err
	}
	result, ok := stub.results[string(reference.Kind)+"|"+reference.Scope+"|"+reference.Value]
	if !ok {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return result, nil
}

func TestResolveOneIDNeverUsesSourceCustomerID(t *testing.T) {
	resolver := resolverStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:wx-open|union-1": {Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(91)},
	}}
	id, disposition, err := resolveOneID(context.Background(), resolver, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "union-1", Assurance: "verified", Source: "provider-history:v2"}})
	if err != nil || id != 91 || disposition != "mapped" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
	id, disposition, err = resolveOneID(context.Background(), resolver, nil)
	if err != nil || id != 0 || disposition != "unresolved" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
}

func TestResolveOneIDConflictsAcrossCanonicalRoots(t *testing.T) {
	resolver := resolverStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:wx-open|union-1":  {Status: identityport.ResolveFound, CustomerID: 91},
		"wecom_external_userid|wecom-corp:corp-1|ext-1": {Status: identityport.ResolveFound, CustomerID: 92},
	}}
	rows := []identityRow{
		{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "union-1", Assurance: "verified", Source: "provider-history:v2"},
		{Kind: "wecom_external_userid", Scope: "wecom-corp:corp-1", Value: "ext-1", Assurance: "verified", Source: "provider-history:v2"},
	}
	if id, disposition, err := resolveOneID(context.Background(), resolver, rows); err != nil || id != 0 || disposition != "conflict" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
}

func TestResolveOneIDRejectsUnverifiedInvalidAndPropagatesStoreFailure(t *testing.T) {
	if _, disposition, err := resolveOneID(context.Background(), resolverStub{}, []identityRow{{Kind: "unionid", Scope: "", Value: "x", Assurance: "verified", Source: "provider-history:v2"}}); err != nil || disposition != "invalid" {
		t.Fatalf("disposition=%s err=%v", disposition, err)
	}
	if _, disposition, err := resolveOneID(context.Background(), resolverStub{}, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "x", Assurance: "declared", Source: "provider-history:v2"}}); err != nil || disposition != "unresolved" {
		t.Fatalf("disposition=%s err=%v", disposition, err)
	}
	want := errors.New("store unavailable")
	if _, _, err := resolveOneID(context.Background(), resolverStub{err: want}, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "x", Assurance: "verified", Source: "provider-history:v2"}}); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestImportDryRunApplyReplayAndReconcilePostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "automationops_migration_" + hex.EncodeToString(random)
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyPlatformSQL(t, ctx, pool)
	applyRiverSchema(t, ctx, pool)
	var actorID, customerID, otherCustomerID int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,created_at,updated_at) VALUES('migration-admin','$argon2id$fixture','Migration Admin','staff-provider-1',true,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO customers(status,created_at,updated_at) VALUES('active',clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO customers(status,created_at,updated_at) VALUES('active',clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&otherCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at,created_at,updated_at) VALUES($1,'unionid','wechat-open-platform:fixture','union-fixture','verified','fixture',1,'active',clock_timestamp(),clock_timestamp(),clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	snapshot := migrationFixture(t)
	keyFile := filepath.Join(t.TempDir(), "snapshot.key")
	snapshotFile := filepath.Join(t.TempDir(), "snapshot.enc")
	if err = generateKey(keyFile); err != nil {
		t.Fatal(err)
	}
	if err = writeEncryptedSnapshot(snapshotFile, keyFile, snapshot); err != nil {
		t.Fatal(err)
	}
	cliURL := databaseURLWithSearchPath(t, url, schema)
	t.Setenv("AICRM_AUTOMATION_RECONCILE_TEST_URL", cliURL)
	commandArgs := []string{"--actor-id", strconv.FormatInt(actorID, 10), "--snapshot-file", snapshotFile, "--key-file", keyFile, "--target-url-env", "AICRM_AUTOMATION_RECONCILE_TEST_URL", "--timeout", "30s"}
	report := executeImportCommand(t, "dry-run", commandArgs...)
	if report.Tables["audience_members"].Mapped != 1 || report.Tables["audience_members"].Unresolved != 1 {
		t.Fatalf("dry-run report=%+v", report)
	}
	var batches int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_batches`).Scan(&batches); err != nil || batches != 0 {
		t.Fatalf("dry-run batches=%d err=%v", batches, err)
	}
	report = executeImportCommand(t, "apply", append(commandArgs, "--confirm-import")...)
	if report.ProviderEffectsCreated != 0 || report.RiverJobsCreated != 0 {
		t.Fatalf("side effects=%+v", report)
	}
	// Re-applying the same frozen snapshot is the operator's recovery path.
	// It must only load the prior source receipts: no history row may be
	// rewritten and it must not create River work or a Provider effect.
	replay := executeImportCommand(t, "replay-check", commandArgs...)
	if replay.Tables["audience_members"].Mapped != 1 || replay.Tables["audience_members"].Unresolved != 1 {
		t.Fatalf("replay=%+v", replay)
	}
	if replay.ProviderEffectsCreated != 0 || replay.RiverJobsCreated != 0 {
		t.Fatalf("replay side effects=%+v", replay)
	}
	var lifecycle string
	var member int64
	if err = pool.QueryRow(ctx, `SELECT p.lifecycle,m.customer_id FROM segment_audience_packages p JOIN segment_audience_snapshot_members m ON m.snapshot_id=p.published_snapshot_id WHERE p.code='v2-audience-10'`).Scan(&lifecycle, &member); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "paused" || member != customerID {
		t.Fatalf("lifecycle=%s member=%d want=%d", lifecycle, member, customerID)
	}
	var historyRows, readOnlyRows, replayableRows, effectDigests, effects, riverJobs int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE read_only),count(*) FILTER (WHERE replayable),count(*) FILTER (WHERE source_effect_digest IS NOT NULL) FROM automation_operations_legacy_history`).Scan(&historyRows, &readOnlyRows, &replayableRows, &effectDigests); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&riverJobs); err != nil {
		t.Fatal(err)
	}
	if historyRows != 4 || readOnlyRows != 4 || replayableRows != 0 || effectDigests != 1 || effects != 0 || riverJobs != 0 {
		t.Fatalf("legacy history rows=%d readonly=%d replayable=%d effects=%d external=%d river=%d", historyRows, readOnlyRows, replayableRows, effectDigests, effects, riverJobs)
	}
	var quarantines int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_quarantine`).Scan(&quarantines); err != nil || quarantines != 2 {
		t.Fatalf("quarantines=%d err=%v", quarantines, err)
	}

	// The reconciliation command must bind the encrypted snapshot to the exact
	// stored manifest. Matching donor commit and watermark alone are not enough.
	if _, err = Reconcile(ctx, pool, report.BatchKey, alteredFrozenSnapshot(t, snapshot)); err == nil {
		t.Fatal("expected mismatched frozen snapshot rejection")
	}
	assertBatchStatus(t, ctx, pool, report.BatchKey, "imported")

	// The database guards these facts append-only. Disable those test-only
	// guards to prove Reconcile detects a privileged/manual drift before it can
	// stamp the batch reconciled, then restore each fact for the final CLI path.
	disableTestAppendOnlyGuards(t, ctx, pool)
	defer restoreTestAppendOnlyGuards(t, ctx, pool)

	originalHistory := loadHistoryFixture(t, ctx, pool, report.BatchKey, "automation_enrollments")
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET source_state='tampered' WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET source_state=$2 WHERE id=$1`, originalHistory.ID, originalHistory.SourceState); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET occurred_at=occurred_at + interval '1 second' WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET occurred_at=$2 WHERE id=$1`, originalHistory.ID, originalHistory.OccurredAt); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `ALTER TABLE automation_operations_legacy_history DROP CONSTRAINT automation_operations_legacy_history_read_only_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET read_only=false WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_legacy_history SET read_only=true WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE automation_operations_legacy_history ADD CONSTRAINT automation_operations_legacy_history_read_only_check CHECK(read_only)`); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `DELETE FROM automation_operations_legacy_history WHERE id=$1`, originalHistory.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	restoreHistoryFixture(t, ctx, pool, originalHistory)

	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshot_members SET customer_id=$2 WHERE customer_id=$1`, customerID, otherCustomerID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshot_members SET customer_id=$2 WHERE customer_id=$1`, otherCustomerID, customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshots SET reference_time=reference_time + interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE segment_audience_snapshots SET reference_time=$1`, snapshot.Manifest.SnapshotAt); err != nil {
		t.Fatal(err)
	}

	originalQuarantine := loadQuarantineFixture(t, ctx, pool, report.BatchKey, "audience_members")
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_migration_quarantine SET reason_code='tampered' WHERE id=$1`, originalQuarantine.ID); err != nil {
		t.Fatal(err)
	}
	assertReconcileRejected(t, ctx, pool, report.BatchKey, snapshot)
	if _, err = pool.Exec(ctx, `UPDATE automation_operations_migration_quarantine SET reason_code=$2 WHERE id=$1`, originalQuarantine.ID, originalQuarantine.ReasonCode); err != nil {
		t.Fatal(err)
	}

	var cliOutput bytes.Buffer
	if err = execute([]string{"reconcile", "--batch-key", report.BatchKey, "--snapshot-file", snapshotFile, "--key-file", keyFile, "--target-url-env", "AICRM_AUTOMATION_RECONCILE_TEST_URL", "--timeout", "30s"}, &cliOutput); err != nil {
		t.Fatalf("actual reconcile command: %v", err)
	}
	assertBatchStatus(t, ctx, pool, report.BatchKey, "reconciled")
	shadow, err := Shadow(ctx, pool, report.BatchKey, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !shadow.ReadyForProbe || !shadow.ReadyForReconcile || !shadow.SnapshotManifestMatches || len(shadow.Packages) != 1 || !shadow.Packages[0].MemberDigestMatches || shadow.Packages[0].IsolatedMemberRows != 1 || len(shadow.Quarantines) != 2 || !hasReadyQuarantine(shadow.Quarantines, "automation_agents") || !hasReadyQuarantine(shadow.Quarantines, "audience_members") || len(shadow.History) != 4 {
		t.Fatalf("shadow=%+v", shadow)
	}
	for _, item := range shadow.History {
		if !item.Ready || !item.StateMatches || !item.OccurredAtMatches || !item.ReadOnly || item.Replayable {
			t.Fatalf("history shadow=%+v", item)
		}
	}
}

func applyPlatformSQL(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func applyRiverSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
}

func executeImportCommand(t *testing.T, command string, args ...string) Report {
	t.Helper()
	var output bytes.Buffer
	if err := execute(append([]string{command}, args...), &output); err != nil {
		t.Fatalf("actual %s command: %v", command, err)
	}
	var report Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode %s report: %v", command, err)
	}
	return report
}

func assertReconcileRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey string, snapshot segmentmigration.Snapshot) {
	t.Helper()
	if _, err := Reconcile(ctx, pool, batchKey, snapshot); err == nil {
		t.Fatal("expected reconciliation rejection")
	}
	assertBatchStatus(t, ctx, pool, batchKey, "imported")
}

func assertBatchStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey, want string) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM automation_operations_migration_batches WHERE batch_key=$1`, batchKey).Scan(&status); err != nil || status != want {
		t.Fatalf("batch status=%q want=%q err=%v", status, want, err)
	}
}

func alteredFrozenSnapshot(t *testing.T, source segmentmigration.Snapshot) segmentmigration.Snapshot {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var altered segmentmigration.Snapshot
	if err = json.Unmarshal(raw, &altered); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err = json.Unmarshal(altered.Tables["automation_actions"], &rows); err != nil || len(rows) == 0 {
		t.Fatalf("decode altered frozen snapshot: %v", err)
	}
	rows[0]["status"] = "tampered"
	updated, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	altered.Tables["automation_actions"] = updated
	digest := sha256.Sum256(updated)
	altered.Manifest.Digests["automation_actions"] = hex.EncodeToString(digest[:])
	if err = segmentmigration.ValidateSnapshot(altered); err != nil {
		t.Fatalf("altered snapshot must remain structurally valid: %v", err)
	}
	return altered
}

func disableTestAppendOnlyGuards(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE automation_operations_legacy_history DISABLE TRIGGER automation_operations_legacy_history_append_only`,
		`ALTER TABLE automation_operations_migration_quarantine DISABLE TRIGGER automation_operations_migration_quarantine_append_only`,
		`ALTER TABLE segment_audience_snapshot_members DISABLE TRIGGER segment_audience_snapshot_members_append_only`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

func restoreTestAppendOnlyGuards(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE automation_operations_legacy_history ENABLE TRIGGER automation_operations_legacy_history_append_only`,
		`ALTER TABLE automation_operations_migration_quarantine ENABLE TRIGGER automation_operations_migration_quarantine_append_only`,
		`ALTER TABLE segment_audience_snapshot_members ENABLE TRIGGER segment_audience_snapshot_members_append_only`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Error(err)
		}
	}
}

type historyFixture struct {
	ID                 int64
	BatchID            int64
	SourceSystem       string
	SourceTable        string
	SourcePK           string
	SourceState        string
	SourceEffectDigest []byte
	RecordDigest       []byte
	SafeSummary        string
	OccurredAt         time.Time
	ImportedAt         time.Time
	ReadOnly           bool
	Replayable         bool
}

func loadHistoryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey, sourceTable string) historyFixture {
	t.Helper()
	var value historyFixture
	err := pool.QueryRow(ctx, `SELECT h.id,h.batch_id,h.source_system,h.source_table,h.source_pk,h.source_state,h.source_effect_digest,h.record_digest,h.safe_summary::text,h.occurred_at,h.imported_at,h.read_only,h.replayable FROM automation_operations_legacy_history h JOIN automation_operations_migration_batches b ON b.id=h.batch_id WHERE b.batch_key=$1 AND h.source_table=$2`, batchKey, sourceTable).Scan(&value.ID, &value.BatchID, &value.SourceSystem, &value.SourceTable, &value.SourcePK, &value.SourceState, &value.SourceEffectDigest, &value.RecordDigest, &value.SafeSummary, &value.OccurredAt, &value.ImportedAt, &value.ReadOnly, &value.Replayable)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func restoreHistoryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, value historyFixture) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO automation_operations_legacy_history(id,batch_id,source_system,source_table,source_pk,source_state,source_effect_digest,record_digest,safe_summary,occurred_at,imported_at,read_only,replayable) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13)`, value.ID, value.BatchID, value.SourceSystem, value.SourceTable, value.SourcePK, value.SourceState, value.SourceEffectDigest, value.RecordDigest, value.SafeSummary, value.OccurredAt, value.ImportedAt, value.ReadOnly, value.Replayable); err != nil {
		t.Fatal(err)
	}
}

type quarantineFixture struct {
	ID           int64
	BatchID      int64
	SourceSystem string
	SourceTable  string
	SourcePK     string
	ReasonCode   string
	SafeSummary  string
	RecordDigest []byte
	CreatedAt    time.Time
}

func loadQuarantineFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, batchKey, sourceTable string) quarantineFixture {
	t.Helper()
	var value quarantineFixture
	err := pool.QueryRow(ctx, `SELECT q.id,q.batch_id,q.source_system,q.source_table,q.source_pk,q.reason_code,q.safe_summary::text,q.record_digest,q.created_at FROM automation_operations_migration_quarantine q JOIN automation_operations_migration_batches b ON b.id=q.batch_id WHERE b.batch_key=$1 AND q.source_table=$2`, batchKey, sourceTable).Scan(&value.ID, &value.BatchID, &value.SourceSystem, &value.SourceTable, &value.SourcePK, &value.ReasonCode, &value.SafeSummary, &value.RecordDigest, &value.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func hasReadyQuarantine(items []ShadowQuarantine, sourceTable string) bool {
	for _, item := range items {
		if item.SourceTable == sourceTable && item.Ready {
			return true
		}
	}
	return false
}

func databaseURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := neturl.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	options := strings.TrimSpace(query.Get("options"))
	if options != "" {
		options += " "
	}
	query.Set("options", options+"-c search_path="+schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func migrationFixture(t *testing.T) segmentmigration.Snapshot {
	t.Helper()
	at := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	rows := map[string]any{
		"audience_groups":                 []any{map[string]any{"id": 1, "name": "Migrated", "sort_order": 1, "version": 1, "created_at": at, "updated_at": at}},
		"audience_packages":               []any{map[string]any{"id": 10, "group_id": 1, "lifecycle": "active", "version": 2, "name": "Legacy audience", "definition": map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}, "refresh_mode": "manual", "member_count": 2, "created_at": at, "updated_at": at}},
		"audience_configuration_versions": []any{map[string]any{"package_id": 10, "version": 1, "definition": map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}, "refresh_mode": "manual", "created_at": at}},
		"automation_agents":               []any{map[string]any{"id": 20, "agent_name": "Fixed", "agent_code": "migrated-fixed", "automation_type": "fixed_script", "status": "paused", "draft_role_prompt": "", "draft_task_prompt": "", "published_role_prompt": "", "published_task_prompt": "", "draft_version": 1, "published_version": 1, "fixed_content_package_json": map[string]any{"content_text": "hello", "image_library_ids": []int64{}, "miniprogram_library_ids": []int64{}, "attachment_library_ids": []int64{}, "group_invite_library_ids": []int64{}}, "created_at": at, "updated_at": at}, map[string]any{"id": 21, "agent_name": "Invalid historical agent", "agent_code": "", "automation_type": "fixed_script", "status": "paused", "draft_version": 0, "published_version": 0, "fixed_content_package_json": map[string]any{}, "created_at": at, "updated_at": at}},
		"audience_bindings":               []any{map[string]any{"package_id": 10, "automation_agent_id": 20, "version": 1, "created_at": at, "updated_at": at}},
		"audience_senders":                []any{map[string]any{"package_id": 10, "sender_userid": "staff-provider-1", "sort_order": 1, "is_enabled": true, "created_at": at, "updated_at": at}},
		"audience_members":                []any{map[string]any{"segment_id": 10, "customer_id": 999999, "computed_at": at, "identities": []any{map[string]any{"kind": "unionid", "scope": "wechat-open-platform:fixture", "value": "union-fixture", "assurance": "verified", "source": "fixture"}}}, map[string]any{"segment_id": 10, "customer_id": 999998, "computed_at": at, "identities": []any{}}},
		"automation_policies":             []any{map[string]any{"id": 30, "status": "active", "created_at": at}},
		"automation_policy_versions":      []any{map[string]any{"automation_id": 30, "version": 2, "status": "published", "published_at": at.Add(time.Minute)}},
		"automation_enrollments":          []any{map[string]any{"id": 40, "state": "sent", "external_effect_id": "legacy-effect-40", "enrolled_at": at.Add(2 * time.Minute)}},
		"automation_actions":              []any{map[string]any{"id": 50, "status": "failed", "completed_at": at.Add(3 * time.Minute)}},
	}
	snapshot := segmentmigration.Snapshot{Manifest: segmentmigration.Manifest{SourceSystem: "fixture", DonorCommit: segmentmigration.DonorCommit, SnapshotAt: at, SchemaDigest: strings.Repeat("1", 64), Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	for _, name := range segmentmigration.LogicalTables {
		raw, err := json.Marshal(rows[name])
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Tables[name] = raw
		var decoded []json.RawMessage
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		snapshot.Manifest.Counts[name] = len(decoded)
		digest := sha256.Sum256(raw)
		snapshot.Manifest.Digests[name] = hex.EncodeToString(digest[:])
	}
	watermark := sha256.Sum256([]byte("fixture"))
	snapshot.Manifest.SourceWatermarkDigest = hex.EncodeToString(watermark[:])
	return snapshot
}
