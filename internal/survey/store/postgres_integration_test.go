package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestPostgreSQLAudienceChoicesReadFirstResolvedCompletion(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	var actorID, customerID int64
	if err := native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-audience-test','$argon2id$test','Survey Audience Test') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	var questionnaireID, definitionID, questionID, firstOptionID, secondOptionID int64
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at)
		VALUES('Audience source','Audience source','','survey','all_in_one','audience-source','published',$1,$1,$2,$2)
		RETURNING id`, actorID, now).Scan(&questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at)
		VALUES($1,1,'survey','all_in_one','Audience source','','{}',$2,TRUE,$3,$4,$3)
		RETURNING id`, questionnaireID, make([]byte, 32), now, actorID).Scan(&definitionID); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$1 WHERE id=$2`, definitionID, questionnaireID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_questions(definition_version_id,question_type,title,sort_order)
		VALUES($1,'multi_choice','Which choices?',0) RETURNING id`, definitionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order)
		VALUES($1,$2,'First choice',0) RETURNING id`, questionID, definitionID).Scan(&firstOptionID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,sort_order)
		VALUES($1,$2,'Second choice',1) RETURNING id`, questionID, definitionID).Scan(&secondOptionID); err != nil {
		t.Fatal(err)
	}

	insertSubmission := func(identityState, staffID string, customer *int64, submittedAt time.Time, key byte) int64 {
		t.Helper()
		keyDigest, payloadDigest := make([]byte, 32), make([]byte, 32)
		keyDigest[0], payloadDigest[0] = key, key+10
		var submissionID int64
		err := native.QueryRow(ctx, `
			INSERT INTO survey_submissions(
				questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,
				submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,
				result_snapshot,staff_id,submitted_at,created_at
			) VALUES($1,$2,1,$3,$4,$5,$6,'audience-source','Audience source','survey','{}',$7,$8,$8)
			RETURNING id`, questionnaireID, definitionID, customer, identityState, keyDigest, payloadDigest, staffID, submittedAt).Scan(&submissionID)
		if err != nil {
			t.Fatal(err)
		}
		return submissionID
	}
	insertAnswer := func(submissionID int64, options string, key byte) {
		t.Helper()
		digest := make([]byte, 32)
		digest[0] = key
		if _, err := native.Exec(ctx, `
			INSERT INTO survey_submission_answers(
				submission_id,definition_question_id,question_type,question_title_snapshot,
				selected_options_snapshot,answer_digest,created_at
			) VALUES($1,$2,'multi_choice','Which choices?',$3::jsonb,$4,$5)`,
			submissionID, questionID, options, digest, now); err != nil {
			t.Fatal(err)
		}
	}

	firstSubmissionID := insertSubmission("resolved", "owner-first", &customerID, now.Add(-48*time.Hour), 1)
	insertAnswer(firstSubmissionID, fmt.Sprintf(`[{"option_id":%d},{"option_id":%d}]`, firstOptionID, secondOptionID), 1)
	laterSubmissionID := insertSubmission("resolved", "owner-later", &customerID, now.Add(-24*time.Hour), 2)
	insertAnswer(laterSubmissionID, fmt.Sprintf(`[{"option_id":%d}]`, secondOptionID), 2)
	unresolvedSubmissionID := insertSubmission("unresolved", "owner-unresolved", nil, now.Add(-72*time.Hour), 3)
	insertAnswer(unresolvedSubmissionID, fmt.Sprintf(`[{"option_id":%d}]`, firstOptionID), 3)

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
	var facts []surveyport.AudienceChoiceAnswer
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		facts, readErr = repository.FirstCompleteAudienceChoices(txCtx, now)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts=%+v", facts)
	}
	fact := facts[0]
	if int64(fact.CustomerID) != customerID || fact.QuestionnaireID != surveyport.ID(questionnaireID) || fact.SubmissionID != surveyport.ID(firstSubmissionID) || fact.StaffID != "owner-first" || !fact.SubmittedAt.Equal(now.Add(-48*time.Hour)) || fact.QuestionID != surveyport.ID(questionID) {
		t.Fatalf("fact=%+v", fact)
	}
	if len(fact.OptionIDs) != 2 || fact.OptionIDs[0] != surveyport.ID(firstOptionID) || fact.OptionIDs[1] != surveyport.ID(secondOptionID) {
		t.Fatalf("option ids=%v", fact.OptionIDs)
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
	for _, migrationName := range []string{"0002_identity.sql", "0003_access.sql", "0018_survey.sql"} {
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
