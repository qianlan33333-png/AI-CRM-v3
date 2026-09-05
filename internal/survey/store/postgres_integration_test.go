package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

func TestPostgreSQLCompletionReceiptBindsReadsAndRollsBackAtomically(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)

	var actorID, customerID, questionnaireID, versionID, submissionID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-completion-test','$argon2id$test','Survey Completion Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Completion questionnaire','Completion questionnaire','','survey','all_in_one','completion-questionnaire','published',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Completion questionnaire','', '{}', $2, TRUE, $3, $4, $3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,$3,'resolved',$4,$5,'completion-questionnaire','Completion questionnaire','survey','{}',$6,$6) RETURNING id`, questionnaireID, versionID, customerID, make([]byte, 32), bytes32(1), now).Scan(&submissionID); err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("需要回访")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `INSERT INTO survey_submission_answers(submission_id,question_type,question_title_snapshot,text_value_ciphertext,answer_digest,created_at) VALUES($1,'textarea','需求',$2,$3,$4)`, submissionID, encrypted, bytes32(2), now); err != nil {
		t.Fatal(err)
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := "sha256:" + strings.Repeat("0", 64)
	identityCiphertext, err := cipher.Encrypt("union-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(sourceDigest))
	rollback := errors.New("force rollback")
	err = uow.Within(ctx, func(txCtx context.Context) error {
		if recordErr := repository.RecordCompletionEffect(txCtx, surveyport.ID(questionnaireID), surveyport.ID(submissionID), "local-webhook", "eer_rollback", "queued", digest, now); recordErr != nil {
			return recordErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error=%v", err)
	}
	var count int
	if err := native.QueryRow(ctx, `SELECT count(*) FROM survey_external_operation_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back receipt count=%d err=%v", count, err)
	}
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		if err := repository.RecordCompletionEffect(txCtx, surveyport.ID(questionnaireID), surveyport.ID(submissionID), "local-webhook", "eer_1", "queued", digest, now); err != nil {
			return err
		}
		return repository.RecordCompletionSnapshot(txCtx, surveyport.ID(questionnaireID), surveyport.ID(submissionID), surveyport.CompletionPolicy{ConfigurationReference: "local-webhook", ConfigurationVersion: "v1", ConfigurationDigest: sourceDigest}, strings.Repeat("a", 64), identityCiphertext, now)
	}); err != nil {
		t.Fatal(err)
	}
	var disabled surveyport.OperationReceipt
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var disabledErr error
		disabled, disabledErr = repository.RecordDisabledOperation(txCtx, surveyport.ID(questionnaireID), nil, "external_push", sha256.Sum256([]byte("disabled-operation-scan")), now)
		return disabledErr
	}); err != nil || disabled.ID < 1 || disabled.SourcePK != "" || disabled.ProviderCallAttempted != nil {
		t.Fatalf("disabled receipt=%+v err=%v", disabled, err)
	}

	var payload surveyport.CompletionPayload
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		payload, readErr = repository.ReadCompletionPayload(txCtx, sourceDigest)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if payload.CustomerID != customerID || payload.ExternalUserID != "union-snapshot" || len(payload.Answers) != 1 || payload.Answers[0].TextValue != "需要回访" {
		t.Fatalf("completion payload=%+v", payload)
	}
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		return repository.CompleteCompletionEffect(txCtx, "eer_1", "executed", true, true, boolPointer(true), sourceDigest, 1, now.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	var status string
	var callAttempted, realCall, resultReceived bool
	var providerAttempt int32
	if err := native.QueryRow(ctx, `SELECT status,provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id='eer_1'`).Scan(&status, &callAttempted, &realCall, &resultReceived, &providerAttempt); err != nil || status != "executed" || !callAttempted || !realCall || !resultReceived || providerAttempt != 1 {
		t.Fatalf("completion receipt status=%q call=%v real=%v result=%v attempt=%d err=%v", status, callAttempted, realCall, resultReceived, providerAttempt, err)
	}
}

func boolPointer(value bool) *bool { return &value }

func bytes32(value byte) []byte {
	result := make([]byte, 32)
	result[0] = value
	return result
}

func TestPostgreSQLListLoadsActiveDefinitionsAfterClosingBaseRows(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()

	var actorID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-list-test','$argon2id$test','Survey List Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	var questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Imported questionnaire','Stale title','','survey','all_in_one','imported-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	var versionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Imported title','Imported description','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		keyDigest, payloadDigest := make([]byte, 32), make([]byte, 32)
		keyDigest[0], payloadDigest[0] = byte(index+1), byte(index+11)
		if _, err := native.Exec(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,identity_state,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,result_snapshot,submitted_at,created_at) VALUES($1,$2,1,'anonymous',$3,$4,'imported-questionnaire','Imported title','survey','{}',$5,$5)`, questionnaireID, versionID, keyDigest, payloadDigest, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}

	var items []surveyport.Questionnaire
	var total int64
	err = uow.Within(ctx, func(txCtx context.Context) error {
		var listErr error
		items, total, listErr = repository.List(txCtx, 50, 0, "", "")
		return listErr
	})
	if err != nil {
		t.Fatalf("list questionnaire with active definition: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("total=%d items=%d", total, len(items))
	}
	if items[0].ID != surveyport.ID(questionnaireID) || items[0].Title != "Imported title" || items[0].DefinitionVersion != 1 || items[0].SubmissionCount != 3 {
		t.Fatalf("item=%+v", items[0])
	}
}

func TestPostgreSQLOperationConfigurationVersionConflictPreservesConcurrentToggleAndReference(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)

	var actorID, questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-config-cas-test','$argon2id$test','Survey Config CAS Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Configuration CAS questionnaire','Configuration CAS questionnaire','','survey','all_in_one','configuration-cas-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	service := surveyapp.NewSubmissionService(uow, repository, cipher)

	initial, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	if err != nil || initial.Version != 0 {
		t.Fatalf("initial config=%+v err=%v", initial, err)
	}
	first, err := service.SaveOperationConfiguration(ctx, surveyport.OperationConfiguration{QuestionnaireID: surveyport.ID(questionnaireID), ExternalPushEnabled: true, ExternalPushConfigurationRef: "push.v1", ExternalPushMetadata: json.RawMessage(`{"remark":"first"}`), Version: initial.Version}, actorID, "survey-config-cas-first-0001")
	if err != nil || first.Version != 1 {
		t.Fatalf("first config=%+v err=%v", first, err)
	}

	// Request A has read v1. Request B changes the independent enable/ref
	// controls before A attempts its metadata-only save.
	requestA, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	if err != nil || requestA.Version != 1 {
		t.Fatalf("request A config=%+v err=%v", requestA, err)
	}
	requestB := requestA
	requestB.ExternalPushEnabled = false
	requestB.ExternalPushConfigurationRef = "push.v2"
	requestB.ExternalPushMetadata = json.RawMessage(`{"remark":"changed-by-b"}`)
	updatedByB, err := service.SaveOperationConfiguration(ctx, requestB, actorID, "survey-config-cas-second-0002")
	if err != nil || updatedByB.Version != 2 {
		t.Fatalf("request B config=%+v err=%v", updatedByB, err)
	}
	requestA.ExternalPushMetadata = json.RawMessage(`{"remark":"stale-a","custom_params":{"campaign":"autumn"}}`)
	if _, err = service.SaveOperationConfiguration(ctx, requestA, actorID, "survey-config-cas-stale-a-0003"); !errors.Is(err, surveyport.ErrConflict) {
		t.Fatalf("stale request error=%v want conflict", err)
	}

	stored, err := service.GetOperationConfiguration(ctx, surveyport.ID(questionnaireID))
	var storedMetadata map[string]string
	metadataErr := json.Unmarshal(stored.ExternalPushMetadata, &storedMetadata)
	if err != nil || metadataErr != nil || stored.Version != 2 || stored.ExternalPushEnabled || stored.ExternalPushConfigurationRef != "push.v2" || storedMetadata["remark"] != "changed-by-b" {
		t.Fatalf("stale save overwrote concurrent configuration: %+v err=%v", stored, err)
	}
	for table, want := range map[string]int64{"survey_audit_events": 2, "survey_outbox": 2} {
		var got int64
		if err := native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("stale configuration transaction left %s rows=%d want=%d", table, got, want)
		}
	}
}

func TestPostgreSQLSyntheticCompletionTestSnapshotReplaysWithoutCustomer(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	var actorID, questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-test-push','$argon2id$test','Survey Test Push') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Synthetic test push','Synthetic test push','','survey','all_in_one','synthetic-test-push','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	value := surveyapp.CompletionTestSnapshot{QuestionnaireID: surveyport.ID(questionnaireID), TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef", QuestionnaireTitle: "Synthetic test push", SubmittedAt: now, Policy: surveyport.CompletionPolicy{ConfigurationReference: "test-webhook", ConfigurationVersion: "v1", ConfigurationDigest: "sha256:" + strings.Repeat("a", 64), CustomParams: map[string]string{"campaign": "autumn"}}, SourceDigest: "sha256:" + strings.Repeat("b", 64), TargetDigest: "sha256:" + strings.Repeat("c", 64), PayloadDigest: "sha256:" + strings.Repeat("d", 64), PolicyDigest: "sha256:" + strings.Repeat("e", 64), IdempotencyKey: "survey-synthetic-test-push-0001"}
	var created bool
	if err = uow.Within(ctx, func(tx context.Context) error {
		stored, didCreate, recordErr := repository.RecordCompletionTestSnapshot(tx, value)
		if recordErr != nil || !didCreate || stored.TestRunID != value.TestRunID {
			t.Fatalf("store synthetic snapshot=%+v created=%v err=%v", stored, didCreate, recordErr)
		}
		created = didCreate
		digest := sha256.Sum256([]byte(value.SourceDigest))
		return repository.RecordCompletionTestEffect(tx, value.QuestionnaireID, value.TestRunID, value.Policy.ConfigurationReference, "eer_synthetic_1", "queued", digest, now)
	}); err != nil || !created {
		t.Fatalf("persist synthetic snapshot err=%v created=%v", err, created)
	}
	var payload surveyport.CompletionPayload
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		payload, readErr = repository.ReadCompletionPayload(tx, value.SourceDigest)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if !payload.SyntheticTest || payload.TestRunID != value.TestRunID || payload.CustomerID != 0 || payload.SubmissionID != 0 || payload.ExternalUserID != "questionnaire_test" || len(payload.Answers) != 0 || payload.Policy.CustomParams["campaign"] != "autumn" {
		t.Fatalf("synthetic payload=%+v", payload)
	}
	// The outbound provider runs after the accepting transaction has committed.
	// It must reconstruct this protected synthetic payload through the
	// repository's read-only pool path, not require a transaction that no
	// longer exists.
	payload, err = repository.ReadCompletionPayload(ctx, value.SourceDigest)
	if err != nil {
		t.Fatalf("synthetic payload outside transaction: %v", err)
	}
	if !payload.SyntheticTest || payload.TestRunID != value.TestRunID || payload.ExternalUserID != "questionnaire_test" || len(payload.Answers) != 0 || payload.Policy.CustomParams["campaign"] != "autumn" {
		t.Fatalf("outside transaction synthetic payload=%+v", payload)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, didCreate, recordErr := repository.RecordCompletionTestSnapshot(tx, value)
		if recordErr != nil || didCreate {
			t.Fatalf("replay snapshot created=%v err=%v", didCreate, recordErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drift := value
	drift.PayloadDigest = "sha256:" + strings.Repeat("f", 64)
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, _, recordErr := repository.RecordCompletionTestSnapshot(tx, drift)
		return recordErr
	}); !errors.Is(err, surveyport.ErrConflict) {
		t.Fatalf("synthetic drift error=%v", err)
	}
}

func TestPostgreSQLSyntheticCompletionTerminalReplayKeepsExecutionFacts(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 13, 30, 0, 0, time.UTC)
	var actorID, questionnaireID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-terminal-replay','$argon2id$test','Survey terminal replay') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Synthetic terminal replay','Synthetic terminal replay','','survey','all_in_one','synthetic-terminal-replay','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, terminal, wantStatus string
		callAttempted              bool
		resultReceived             *bool
	}{
		{name: "retryable", terminal: "retryable_failed", wantStatus: "attempted", callAttempted: true, resultReceived: boolPointer(true)},
		{name: "final", terminal: "final_failed", wantStatus: "failed", callAttempted: true, resultReceived: boolPointer(true)},
		{name: "cancelled", terminal: "cancelled", wantStatus: "queued", callAttempted: false, resultReceived: nil},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testRunID := "questionnaire-test-" + strings.Repeat(string(rune('a'+index)), 32)
			effectID := "eer_terminal_" + tc.name
			digest := sha256.Sum256([]byte("survey-terminal-replay:" + tc.name))
			if err := uow.Within(ctx, func(tx context.Context) error {
				return repository.RecordCompletionTestEffect(tx, surveyport.ID(questionnaireID), testRunID, "test-webhook", effectID, "queued", digest, now)
			}); err != nil {
				t.Fatal(err)
			}
			if tc.terminal != "cancelled" {
				if err := uow.Within(ctx, func(tx context.Context) error {
					return repository.CompleteCompletionEffect(tx, effectID, tc.terminal, tc.callAttempted, tc.callAttempted, tc.resultReceived, "sha256:"+strings.Repeat("a", 64), 1, now.Add(time.Minute))
				}); err != nil {
					t.Fatal(err)
				}
			}
			// EER can return a terminal projection when the same operator key is
			// replayed. The prospective insert must satisfy the legacy receipt
			// CHECK while the existing terminal execution facts remain unchanged.
			if err := uow.Within(ctx, func(tx context.Context) error {
				return repository.RecordCompletionTestEffect(tx, surveyport.ID(questionnaireID), testRunID, "test-webhook", effectID, tc.terminal, digest, now.Add(2*time.Minute))
			}); err != nil {
				t.Fatalf("terminal replay: %v", err)
			}
			var status string
			var callAttempted, realCall *bool
			var resultReceived *bool
			var attempt *int32
			if err := native.QueryRow(ctx, `SELECT status,provider_call_attempted,provider_real_call_executed,provider_result_received,provider_attempt_number FROM survey_external_operation_receipts WHERE effect_id=$1`, effectID).Scan(&status, &callAttempted, &realCall, &resultReceived, &attempt); err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus || (tc.terminal == "cancelled" && (callAttempted != nil || realCall != nil || resultReceived != nil || attempt != nil)) {
				t.Fatalf("terminal replay status=%q call=%v real=%v result=%v attempt=%v", status, callAttempted, realCall, resultReceived, attempt)
			}
			if tc.terminal != "cancelled" && (callAttempted == nil || !*callAttempted || realCall == nil || !*realCall || resultReceived == nil || !*resultReceived || attempt == nil || *attempt != 1) {
				t.Fatalf("terminal facts changed call=%v real=%v result=%v attempt=%v", callAttempted, realCall, resultReceived, attempt)
			}
		})
	}
}

func TestPostgreSQLSetStatusPersistsReceiptAuditAndOutboxAtomically(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()

	var actorID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-status-test','$argon2id$test','Survey Status Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	var questionnaireID, versionID int64
	if err := native.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at) VALUES('Imported status questionnaire','Imported status questionnaire','','survey','all_in_one','imported-status-questionnaire','disabled',$1,$1,$2,$2) RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at) VALUES($1,1,'survey','all_in_one','Imported status questionnaire','','{}',$2,TRUE,$3,$4,$3) RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, versionID, questionnaireID); err != nil {
		t.Fatal(err)
	}

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	service := surveyapp.NewService(uow, repository)

	updated, err := service.SetStatus(ctx, surveyport.ID(questionnaireID), 1, surveyport.StatusPublished, actorID, "survey-enable-integration-0001")
	if err != nil {
		t.Fatalf("enable imported questionnaire: %v", err)
	}
	if updated.Status != surveyport.StatusPublished || updated.Version != 2 {
		t.Fatalf("updated=%+v", updated)
	}
	for table, want := range map[string]int64{"survey_operation_receipts": 1, "survey_audit_events": 1, "survey_outbox": 1} {
		var got int64
		if err := native.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}
}

func surveyIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Survey PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_survey_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	for _, migrationName := range []string{"0002_identity.sql", "0003_access.sql", "0018_survey.sql", "0067_survey_completion_snapshots.sql", "0073_survey_completion_test_push_snapshots.sql", "0074_survey_external_operation_execution_facts.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", migrationName, execErr)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}
