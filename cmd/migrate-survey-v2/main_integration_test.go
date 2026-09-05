package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLFrozenSurveySnapshotImportReplayAndReconcile(t *testing.T) {
	targetURL, pool, cleanup := surveyMigrationIntegrationTarget(t)
	defer cleanup()
	snapshot := frozenSurveySnapshot(t, time.Date(2026, 9, 5, 7, 0, 0, 0, time.UTC))
	file, snapshotKey, dataKey := writeFrozenSnapshot(t, snapshot)
	args := []string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey, "--confirm-import"}
	if err := importSnapshot(args); err != nil {
		t.Fatalf("import frozen snapshot: %v", err)
	}
	if err := importSnapshot(args); err != nil {
		t.Fatalf("same frozen snapshot replay: %v", err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err != nil {
		t.Fatalf("reconcile frozen snapshot: %v", err)
	}
	ctx := context.Background()
	var questionnaires, submissions, unresolved, missingDefinition, receipts, quarantined, missingToken, mutableEffects, customers int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM survey_questionnaires),
		(SELECT count(*) FROM survey_submissions),
		(SELECT count(*) FROM survey_submissions WHERE identity_state='unresolved' AND customer_id IS NULL),
		(SELECT count(*) FROM survey_submission_answers WHERE legacy_definition_missing=TRUE AND definition_question_id IS NULL),
		(SELECT count(*) FROM survey_external_operation_receipts WHERE read_only_legacy=TRUE AND replayable=FALSE),
		(SELECT count(*) FROM survey_migration_quarantine WHERE reason_code='missing_questionnaire_association'),
		(SELECT count(*) FROM survey_migration_quarantine WHERE reason_code='missing_result_token' AND source_table='questionnaire_result_tokens'),
		(SELECT count(*) FROM survey_outbox),
		(SELECT count(*) FROM customers)`).Scan(&questionnaires, &submissions, &unresolved, &missingDefinition, &receipts, &quarantined, &missingToken, &mutableEffects, &customers); err != nil {
		t.Fatal(err)
	}
	if questionnaires != 1 || submissions != 2 || unresolved != 1 || missingDefinition != 1 || receipts != 3 || quarantined != 1 || missingToken != 1 || mutableEffects != 0 || customers != 0 {
		t.Fatalf("questionnaires=%d submissions=%d unresolved=%d missing_definition=%d receipts=%d quarantined=%d missing_token=%d outbox=%d customers=%d", questionnaires, submissions, unresolved, missingDefinition, receipts, quarantined, missingToken, mutableEffects, customers)
	}

	clean := func(stage string) {
		if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err != nil {
			t.Fatalf("clean %s reconcile: %v", stage, err)
		}
	}
	var submissionID int64
	var originalIdentity, originalTitle string
	var originalCustomer *int64
	if err := pool.QueryRow(ctx, `SELECT id,identity_state,customer_id,title_snapshot FROM survey_submissions WHERE identity_state='unresolved' ORDER BY id LIMIT 1`).Scan(&submissionID, &originalIdentity, &originalCustomer, &originalTitle); err != nil {
		t.Fatal(err)
	}
	var customerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_submissions SET identity_state='resolved',customer_id=$2 WHERE id=$1`, submissionID, customerID); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "submission target fact drift") {
		t.Fatalf("legacy customer misbinding err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_submissions SET identity_state=$2,customer_id=$3 WHERE id=$1`, submissionID, originalIdentity, originalCustomer); err != nil {
		t.Fatal(err)
	}
	clean("customer restore")
	if _, err := pool.Exec(ctx, `UPDATE survey_submissions SET title_snapshot='drifted title' WHERE id=$1`, submissionID); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "submission target fact drift") {
		t.Fatalf("submission snapshot drift err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_submissions SET title_snapshot=$2 WHERE id=$1`, submissionID, originalTitle); err != nil {
		t.Fatal(err)
	}
	clean("submission snapshot restore")

	var answerID int64
	var originalSelected string
	var originalCiphertext []byte
	if err := pool.QueryRow(ctx, `SELECT id,selected_options_snapshot::text,text_value_ciphertext FROM survey_submission_answers WHERE legacy_definition_missing=FALSE ORDER BY id LIMIT 1`).Scan(&answerID, &originalSelected, &originalCiphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_submission_answers SET selected_options_snapshot='[{"drift":true}]'::jsonb WHERE id=$1`, answerID); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "answer target fact drift") {
		t.Fatalf("answer field drift err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_submission_answers SET selected_options_snapshot=$2::jsonb,text_value_ciphertext=$3 WHERE id=$1`, answerID, originalSelected, originalCiphertext); err != nil {
		t.Fatal(err)
	}
	clean("answer option restore")
	if _, err := pool.Exec(ctx, `UPDATE survey_submission_answers SET text_value_ciphertext=decode('00','hex') WHERE id=$1`, answerID); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "empty protected answer ciphertext drift") {
		t.Fatalf("empty answer ciphertext drift err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_submission_answers SET selected_options_snapshot=$2::jsonb,text_value_ciphertext=$3 WHERE id=$1`, answerID, originalSelected, originalCiphertext); err != nil {
		t.Fatal(err)
	}
	clean("answer ciphertext restore")

	var receiptID int64
	var originalStatus string
	if err := pool.QueryRow(ctx, `SELECT id,status FROM survey_external_operation_receipts WHERE source_table='questionnaire_external_push_logs' AND source_pk='60'`).Scan(&receiptID, &originalStatus); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_external_operation_receipts SET status='failed' WHERE id=$1`, receiptID); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "legacy operation fact drift") {
		t.Fatalf("legacy receipt field drift err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_external_operation_receipts SET status=$2 WHERE id=$1`, receiptID, originalStatus); err != nil {
		t.Fatal(err)
	}
	clean("receipt restore")

	var operationQuarantineSafe []byte
	if err := pool.QueryRow(ctx, `SELECT safe_snapshot FROM survey_migration_quarantine WHERE source_table='questionnaire_external_push_logs' AND source_pk='61'`).Scan(&operationQuarantineSafe); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_migration_quarantine SET safe_snapshot='{"submission_source_id":999,"status":"failed"}'::jsonb WHERE source_table='questionnaire_external_push_logs' AND source_pk='61'`); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "quarantine fact drift") {
		t.Fatalf("operation quarantine fact drift err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_migration_quarantine SET safe_snapshot=$3::jsonb WHERE source_table=$1 AND source_pk=$2`, "questionnaire_external_push_logs", "61", operationQuarantineSafe); err != nil {
		t.Fatal(err)
	}
	clean("operation quarantine restore")

	var resultQuarantineSafe, resultQuarantineDigest []byte
	if err := pool.QueryRow(ctx, `SELECT safe_snapshot,record_digest FROM survey_migration_quarantine WHERE source_table='questionnaire_result_tokens' AND source_pk='42'`).Scan(&resultQuarantineSafe, &resultQuarantineDigest); err != nil {
		t.Fatal(err)
	}
	driftedResultQuarantineDigest := append([]byte(nil), resultQuarantineDigest...)
	if len(driftedResultQuarantineDigest) != 32 {
		t.Fatalf("result-token quarantine digest length=%d", len(driftedResultQuarantineDigest))
	}
	driftedResultQuarantineDigest[0] ^= 0xff
	if _, err := pool.Exec(ctx, `UPDATE survey_migration_quarantine SET record_digest=$3 WHERE source_table=$1 AND source_pk=$2`, "questionnaire_result_tokens", "42", driftedResultQuarantineDigest); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "missing result-token quarantine") {
		t.Fatalf("result-token quarantine digest drift err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_migration_quarantine SET safe_snapshot=$3::jsonb,record_digest=$4 WHERE source_table=$1 AND source_pk=$2`, "questionnaire_result_tokens", "42", resultQuarantineSafe, resultQuarantineDigest); err != nil {
		t.Fatal(err)
	}
	clean("result-token quarantine restore")

	manifestDrift := cloneFrozenSurveySnapshot(t, snapshot, snapshot.Manifest.SnapshotAt)
	var changed []questionnaire
	decodeTable(manifestDrift, "questionnaires", &changed)
	changed[0].Title = "changed manifest"
	setFrozenTable(t, &manifestDrift, "questionnaires", changed)
	manifestFile, manifestKey, manifestDataKey := writeFrozenSnapshot(t, manifestDrift)
	if err := importSnapshot([]string{"--target-url", targetURL, "--snapshot", manifestFile, "--snapshot-key-file", manifestKey, "--data-key-file", manifestDataKey, "--confirm-import"}); err == nil || !strings.Contains(err.Error(), "manifest mismatch") {
		t.Fatalf("same batch manifest drift err=%v", err)
	}

	childDrift := cloneFrozenSurveySnapshot(t, snapshot, snapshot.Manifest.SnapshotAt.Add(time.Second))
	var options []option
	decodeTable(childDrift, "questionnaire_options", &options)
	options[0].Text = "changed option"
	setFrozenTable(t, &childDrift, "questionnaire_options", options)
	childFile, childKey, childDataKey := writeFrozenSnapshot(t, childDrift)
	if err := importSnapshot([]string{"--target-url", targetURL, "--snapshot", childFile, "--snapshot-key-file", childKey, "--data-key-file", childDataKey, "--confirm-import"}); err == nil || !strings.Contains(err.Error(), "migration source drift") {
		t.Fatalf("mapped child drift err=%v", err)
	}
	var laterBatches int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM survey_migration_batches WHERE snapshot_at=$1`, childDrift.Manifest.SnapshotAt).Scan(&laterBatches); err != nil {
		t.Fatal(err)
	}
	if laterBatches != 0 {
		t.Fatalf("child drift left a batch row: %d", laterBatches)
	}

	tokenConflict := cloneFrozenSurveySnapshot(t, snapshot, snapshot.Manifest.SnapshotAt.Add(2*time.Second))
	var conflictSubmissions []submission
	decodeTable(tokenConflict, "questionnaire_submissions", &conflictSubmissions)
	conflictSubmissions = append(conflictSubmissions, submission{ID: 41, QuestionnaireID: 1, Token: "legacy-result-token", Result: json.RawMessage(`{}`), FinalTags: json.RawMessage(`[]`), SubmittedAt: tokenConflict.Manifest.SnapshotAt, CreatedAt: tokenConflict.Manifest.SnapshotAt})
	setFrozenTable(t, &tokenConflict, "questionnaire_submissions", conflictSubmissions)
	tokenFile, tokenKey, tokenDataKey := writeFrozenSnapshot(t, tokenConflict)
	if err := importSnapshot([]string{"--target-url", targetURL, "--snapshot", tokenFile, "--snapshot-key-file", tokenKey, "--data-key-file", tokenDataKey, "--confirm-import"}); err == nil || !strings.Contains(err.Error(), "result token conflicts") {
		t.Fatalf("cross-submission token conflict err=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM survey_submissions`).Scan(&submissions); err != nil {
		t.Fatal(err)
	}
	if submissions != 2 {
		t.Fatalf("token conflict left a submission: %d", submissions)
	}

	bad := cloneFrozenSurveySnapshot(t, snapshot, snapshot.Manifest.SnapshotAt.Add(3*time.Second))
	var badQuestionnaires []questionnaire
	decodeTable(bad, "questionnaires", &badQuestionnaires)
	badQuestionnaires = append(badQuestionnaires,
		questionnaire{ID: 2, Slug: "first-new-import", Name: "first new import", Title: "first new import", Display: "all_in_one", CreatedAt: snapshot.Manifest.SnapshotAt, UpdatedAt: snapshot.Manifest.SnapshotAt},
		questionnaire{ID: 3, Slug: "bad-import", Name: "bad import", Title: "bad import", Display: "all_in_one", CreatedAt: snapshot.Manifest.SnapshotAt.Add(time.Minute), UpdatedAt: snapshot.Manifest.SnapshotAt},
	)
	setFrozenTable(t, &bad, "questionnaires", badQuestionnaires)
	badFile, badKey, badDataKey := writeFrozenSnapshot(t, bad)
	if err := importSnapshot([]string{"--target-url", targetURL, "--snapshot", badFile, "--snapshot-key-file", badKey, "--data-key-file", badDataKey, "--confirm-import"}); err == nil {
		t.Fatal("mid-transaction target constraint failure was accepted")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM survey_questionnaires`).Scan(&questionnaires); err != nil {
		t.Fatal(err)
	}
	if questionnaires != 1 {
		t.Fatalf("failed import left target facts: questionnaires=%d", questionnaires)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM survey_submission_answers WHERE id=$1`, answerID); err != nil {
		t.Fatal(err)
	}
	if err := reconcile([]string{"--target-url", targetURL, "--snapshot", file, "--snapshot-key-file", snapshotKey, "--data-key-file", dataKey}); err == nil || !strings.Contains(err.Error(), "missing target fact") {
		t.Fatalf("target fact deletion reconcile err=%v", err)
	}
}

func frozenSurveySnapshot(t *testing.T, at time.Time) Snapshot {
	t.Helper()
	q := questionnaire{ID: 1, Slug: "legacy-checkin", Name: "legacy checkin", Title: "Legacy check-in", Description: "frozen", Display: "all_in_one", CreatedAt: at, UpdatedAt: at}
	min, max := 1.0, 5.0
	s := Snapshot{Manifest: Manifest{SourceSystem: "frozen-survey-v2", SnapshotAt: at, Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	setFrozenTable(t, &s, "questionnaires", []questionnaire{q})
	setFrozenTable(t, &s, "questionnaire_questions", []question{{ID: 10, QuestionnaireID: 1, Type: "single_choice", Title: "How are you?", Required: true, Sort: 0}})
	setFrozenTable(t, &s, "questionnaire_options", []option{{ID: 20, QuestionID: 10, Text: "Good", Score: 5, Tags: json.RawMessage(`[]`), Sort: 0}})
	setFrozenTable(t, &s, "questionnaire_score_rules", []rule{{ID: 30, QuestionnaireID: 1, Min: &min, Max: &max, Tags: json.RawMessage(`[]`), Sort: 0}})
	setFrozenTable(t, &s, "questionnaire_submissions", []submission{{ID: 40, QuestionnaireID: 1, UnionID: "unresolved-union", Result: json.RawMessage(`{}`), FinalTags: json.RawMessage(`[]`), Token: "legacy-result-token", SubmittedAt: at, CreatedAt: at}, {ID: 42, QuestionnaireID: 1, Result: json.RawMessage(`{}`), FinalTags: json.RawMessage(`[]`), SubmittedAt: at, CreatedAt: at}})
	setFrozenTable(t, &s, "questionnaire_submission_answers", []answer{
		{ID: 50, SubmissionID: 40, QuestionID: 999, Type: "textarea", Title: "removed question", Text: "protected legacy answer", OptionIDs: json.RawMessage(`[]`), OptionTexts: json.RawMessage(`[]`), OptionScores: json.RawMessage(`[]`), OptionTags: json.RawMessage(`[]`), CreatedAt: at},
		{ID: 51, SubmissionID: 40, QuestionID: 10, Type: "single_choice", Title: "How are you?", OptionIDs: json.RawMessage(`[20]`), OptionTexts: json.RawMessage(`["Good"]`), OptionScores: json.RawMessage(`[5]`), OptionTags: json.RawMessage(`[[]]`), Score: 5, CreatedAt: at},
	})
	setFrozenTable(t, &s, "questionnaire_external_push_logs", []operation{{ID: 60, QuestionnaireID: 1, SubmissionID: 40, Status: "success", OccurredAt: at}, {ID: 61, QuestionnaireID: 999, SubmissionID: 0, Status: "failed", FailureCategory: "provider_failure", OccurredAt: at}, {ID: 62, QuestionnaireID: 999, SubmissionID: 40, Status: "success", OccurredAt: at}})
	setFrozenTable(t, &s, "questionnaire_scrm_apply_logs", []operation{{ID: 70, QuestionnaireID: 1, SubmissionID: 40, Status: "identity_unresolved", FailureCategory: "identity_unresolved", OccurredAt: at}})
	return s
}

// cloneFrozenSurveySnapshot preserves every frozen source record verbatim.
// Derived fixtures may vary the manifest timestamp and their explicitly
// targeted source fact, so a token/rollback assertion cannot be masked by an
// accidental source-drift failure caused by regenerated record timestamps.
func cloneFrozenSurveySnapshot(t *testing.T, source Snapshot, snapshotAt time.Time) Snapshot {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var clone Snapshot
	if err = json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	clone.Manifest.SnapshotAt = snapshotAt
	return clone
}

func setFrozenTable(t *testing.T, snapshot *Snapshot, table string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Tables[table] = raw
	snapshot.Manifest.Counts[table] = jsonArrayLength(t, raw)
	digest := recordDigest(json.RawMessage(raw))
	snapshot.Manifest.Digests[table] = hex.EncodeToString(digest[:])
}

func jsonArrayLength(t *testing.T, raw []byte) int {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func writeFrozenSnapshot(t *testing.T, snapshot Snapshot) (string, string, string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeKey := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "snapshot.enc")
	if err := os.WriteFile(file, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	return file, writeKey("snapshot.key"), writeKey("data.key")
}

func surveyMigrationIntegrationTarget(t *testing.T) (string, *pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	schema := "survey_history_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
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
	root := filepath.Join("..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0018_survey.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(raw)); execErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("migration %s: %v", name, execErr)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('migration-admin','$argon2id$fixture','Migration Admin')`); err != nil {
		pool.Close()
		admin.Close(ctx)
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		pool.Close()
		admin.Close(ctx)
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(cleanup)
	}
}
