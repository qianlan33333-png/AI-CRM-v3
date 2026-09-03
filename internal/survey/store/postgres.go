package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type Repository struct {
	pool   *pgxpool.Pool
	uow    platformport.UnitOfWork
	cipher *secure.Cipher
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork, cipher *secure.Cipher) (*Repository, error) {
	if pool == nil || uow == nil || cipher == nil {
		return nil, surveyport.ErrUnavailable
	}
	return &Repository{pool: pool, uow: uow, cipher: cipher}, nil
}

func tx(ctx context.Context) (pgx.Tx, error) { return platformpostgres.RequireTransaction(ctx) }

type scanner interface{ Scan(...any) error }

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return surveyport.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505", "23514", "40001", "40P01":
			return surveyport.ErrConflict
		case "23503":
			return surveyport.ErrReferenced
		}
	}
	return err
}

const baseColumns = `q.id,q.name,q.title,q.description,q.mode,q.answer_display_mode,q.slug,q.status,q.created_by,q.version,q.created_at,q.updated_at,q.active_definition_version_id`

func scanBase(row scanner) (surveyport.Questionnaire, *int64, error) {
	var q surveyport.Questionnaire
	var active *int64
	err := row.Scan(&q.ID, &q.Name, &q.Title, &q.Description, &q.Mode, &q.AnswerDisplayMode, &q.Slug, &q.Status, &q.CreatedBy, &q.Version, &q.CreatedAt, &q.UpdatedAt, &active)
	return q, active, mapError(err)
}

func (r *Repository) List(ctx context.Context, limit, offset int32, search string, status surveyport.QuestionnaireStatus) ([]surveyport.Questionnaire, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := `($1='' OR q.name ILIKE '%'||$1||'%' OR q.title ILIKE '%'||$1||'%' OR q.slug ILIKE '%'||$1||'%') AND ($2='' OR q.status=$2)`
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM survey_questionnaires q WHERE `+where, search, string(status)).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	rows, err := t.Query(ctx, `SELECT `+baseColumns+` FROM survey_questionnaires q WHERE `+where+` ORDER BY q.updated_at DESC,q.id DESC LIMIT $3 OFFSET $4`, search, string(status), limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	items := make([]surveyport.Questionnaire, 0)
	for rows.Next() {
		q, active, e := scanBase(rows)
		if e != nil {
			return nil, 0, e
		}
		if active != nil {
			if e = r.loadDefinition(ctx, t, &q, *active); e != nil {
				return nil, 0, e
			}
		}
		items = append(items, q)
	}
	return items, total, mapError(rows.Err())
}

func (r *Repository) Get(ctx context.Context, id surveyport.ID, lock bool) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	query := `SELECT ` + baseColumns + ` FROM survey_questionnaires q WHERE q.id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	q, active, err := scanBase(t.QueryRow(ctx, query, id))
	if err != nil {
		return q, err
	}
	if active == nil {
		return q, surveyport.ErrUnavailable
	}
	if err = r.loadDefinition(ctx, t, &q, *active); err != nil {
		return surveyport.Questionnaire{}, err
	}
	return q, nil
}

func (r *Repository) loadDefinition(ctx context.Context, t pgx.Tx, q *surveyport.Questionnaire, versionID int64) error {
	var assessment []byte
	var mode surveyport.QuestionnaireMode
	var display surveyport.AnswerDisplayMode
	if err := t.QueryRow(ctx, `SELECT mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,version_number FROM survey_definition_versions WHERE id=$1 AND questionnaire_id=$2`, versionID, q.ID).Scan(&mode, &display, &q.Title, &q.Description, &assessment, &q.DefinitionVersion); err != nil {
		return mapError(err)
	}
	q.Mode, q.AnswerDisplayMode, q.AssessmentConfig = mode, display, append(json.RawMessage(nil), assessment...)
	rows, err := t.Query(ctx, `SELECT id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation FROM survey_definition_questions WHERE definition_version_id=$1 ORDER BY sort_order,id`, versionID)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	q.Questions = []surveyport.Question{}
	for rows.Next() {
		var item surveyport.Question
		var validation []byte
		if err = rows.Scan(&item.ID, &item.Type, &item.Title, &item.AssessmentDimensionKey, &item.SidebarProfileField, &item.Required, &item.SortOrder, &item.Placeholder, &validation); err != nil {
			return mapError(err)
		}
		if json.Unmarshal(validation, &item.Validation) != nil {
			return surveyport.ErrUnavailable
		}
		item.Options = []surveyport.Option{}
		q.Questions = append(q.Questions, item)
	}
	if err = rows.Err(); err != nil {
		return mapError(err)
	}
	for index := range q.Questions {
		optionRows, e := t.Query(ctx, `SELECT id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order FROM survey_definition_options WHERE question_id=$1 ORDER BY sort_order,id`, q.Questions[index].ID)
		if e != nil {
			return mapError(e)
		}
		for optionRows.Next() {
			var option surveyport.Option
			var tags []byte
			if e = optionRows.Scan(&option.ID, &option.Text, &option.Score, &option.AssessmentTypeKey, &tags, &option.IsOther, &option.OtherPlaceholder, &option.OtherMaximumLength, &option.SortOrder); e != nil {
				optionRows.Close()
				return mapError(e)
			}
			if json.Unmarshal(tags, &option.TagCodes) != nil {
				optionRows.Close()
				return surveyport.ErrUnavailable
			}
			q.Questions[index].Options = append(q.Questions[index].Options, option)
		}
		e = optionRows.Err()
		optionRows.Close()
		if e != nil {
			return mapError(e)
		}
	}
	ruleRows, err := t.Query(ctx, `SELECT minimum_score,maximum_score,tag_codes,sort_order FROM survey_score_rules WHERE definition_version_id=$1 ORDER BY sort_order,id`, versionID)
	if err != nil {
		return mapError(err)
	}
	defer ruleRows.Close()
	q.ScoreRules = []surveyport.ScoreRule{}
	for ruleRows.Next() {
		var rule surveyport.ScoreRule
		var tags []byte
		if err = ruleRows.Scan(&rule.MinimumScore, &rule.MaximumScore, &tags, &rule.SortOrder); err != nil {
			return mapError(err)
		}
		if json.Unmarshal(tags, &rule.TagCodes) != nil {
			return surveyport.ErrUnavailable
		}
		q.ScoreRules = append(q.ScoreRules, rule)
	}
	return mapError(ruleRows.Err())
}

func (r *Repository) Create(ctx context.Context, q surveyport.Questionnaire, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return q, err
	}
	err = t.QueryRow(ctx, `INSERT INTO survey_questionnaires(name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,1,$9,$9) RETURNING id,created_at,updated_at`, q.Name, q.Title, q.Description, q.Mode, q.AnswerDisplayMode, q.Slug, q.Status, actor, now).Scan(&q.ID, &q.CreatedAt, &q.UpdatedAt)
	if err != nil {
		return q, mapError(err)
	}
	q.CreatedBy, q.Version = actor, 1
	versionID, err := r.insertVersion(ctx, t, q, 1, actor, now)
	if err != nil {
		return q, err
	}
	if _, err = t.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, q.ID, versionID); err != nil {
		return q, mapError(err)
	}
	return r.Get(ctx, q.ID, false)
}

func (r *Repository) insertVersion(ctx context.Context, t pgx.Tx, q surveyport.Questionnaire, number, actor int64, now time.Time) (int64, error) {
	assessment := q.AssessmentConfig
	if len(assessment) == 0 {
		assessment = json.RawMessage(`{}`)
	}
	definitionRaw, _ := json.Marshal(struct {
		Mode        surveyport.QuestionnaireMode `json:"mode"`
		Display     surveyport.AnswerDisplayMode `json:"answer_display_mode"`
		Title       string                       `json:"title"`
		Description string                       `json:"description"`
		Assessment  json.RawMessage              `json:"assessment_config"`
		Questions   []surveyport.Question        `json:"questions"`
		Rules       []surveyport.ScoreRule       `json:"score_rules"`
	}{q.Mode, q.AnswerDisplayMode, q.Title, q.Description, assessment, q.Questions, q.ScoreRules})
	digest := sha256.Sum256(definitionRaw)
	var versionID int64
	err := t.QueryRow(ctx, `INSERT INTO survey_definition_versions(questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, q.ID, number, q.Mode, q.AnswerDisplayMode, q.Title, q.Description, assessment, digest[:], actor, now).Scan(&versionID)
	if err != nil {
		return 0, mapError(err)
	}
	if err = r.insertChildren(ctx, t, versionID, q); err != nil {
		return 0, err
	}
	return versionID, nil
}

func (r *Repository) insertChildren(ctx context.Context, t pgx.Tx, versionID int64, q surveyport.Questionnaire) error {
	for _, question := range q.Questions {
		validation, _ := json.Marshal(question.Validation)
		var questionID int64
		err := t.QueryRow(ctx, `INSERT INTO survey_definition_questions(definition_version_id,question_type,title,assessment_dimension_key,sidebar_profile_field,required,sort_order,placeholder_text,validation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, versionID, question.Type, question.Title, question.AssessmentDimensionKey, question.SidebarProfileField, question.Required, question.SortOrder, question.Placeholder, validation).Scan(&questionID)
		if err != nil {
			return mapError(err)
		}
		for _, option := range question.Options {
			tags, _ := json.Marshal(option.TagCodes)
			if _, err = t.Exec(ctx, `INSERT INTO survey_definition_options(question_id,definition_version_id,option_text,score,assessment_type_key,tag_codes,is_other,other_placeholder,other_max_length,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, questionID, versionID, option.Text, option.Score, option.AssessmentTypeKey, tags, option.IsOther, option.OtherPlaceholder, option.OtherMaximumLength, option.SortOrder); err != nil {
				return mapError(err)
			}
		}
	}
	for _, rule := range q.ScoreRules {
		tags, _ := json.Marshal(rule.TagCodes)
		if _, err := t.Exec(ctx, `INSERT INTO survey_score_rules(definition_version_id,minimum_score,maximum_score,tag_codes,sort_order) VALUES($1,$2,$3,$4,$5)`, versionID, rule.MinimumScore, rule.MaximumScore, tags, rule.SortOrder); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (r *Repository) Replace(ctx context.Context, q surveyport.Questionnaire, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return q, err
	}
	current, err := r.Get(ctx, q.ID, true)
	if err != nil {
		return q, err
	}
	if current.Version != expected {
		return q, surveyport.ErrConflict
	}
	var activeID int64
	var immutable bool
	if err = t.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, q.ID).Scan(&activeID); err != nil {
		return q, mapError(err)
	}
	if err = t.QueryRow(ctx, `SELECT is_immutable FROM survey_definition_versions WHERE id=$1`, activeID).Scan(&immutable); err != nil {
		return q, mapError(err)
	}
	if immutable {
		activeID, err = r.insertVersion(ctx, t, q, expected+1, actor, now)
		if err != nil {
			return q, err
		}
		q.Status = surveyport.StatusDraft
	} else {
		if _, err = t.Exec(ctx, `DELETE FROM survey_definition_questions WHERE definition_version_id=$1`, activeID); err != nil {
			return q, mapError(err)
		}
		if _, err = t.Exec(ctx, `DELETE FROM survey_score_rules WHERE definition_version_id=$1`, activeID); err != nil {
			return q, mapError(err)
		}
		assessment := q.AssessmentConfig
		if len(assessment) == 0 {
			assessment = json.RawMessage(`{}`)
		}
		if _, err = t.Exec(ctx, `UPDATE survey_definition_versions SET mode=$2,answer_display_mode=$3,title_snapshot=$4,description_snapshot=$5,assessment_config=$6,version_number=$7 WHERE id=$1`, activeID, q.Mode, q.AnswerDisplayMode, q.Title, q.Description, assessment, expected+1); err != nil {
			return q, mapError(err)
		}
		if err = r.insertChildren(ctx, t, activeID, q); err != nil {
			return q, err
		}
	}
	command, err := t.Exec(ctx, `UPDATE survey_questionnaires SET name=$2,title=$3,description=$4,mode=$5,answer_display_mode=$6,slug=$7,status=$8,active_definition_version_id=$9,updated_by=$10,updated_at=$11,version=version+1 WHERE id=$1 AND version=$12`, q.ID, q.Name, q.Title, q.Description, q.Mode, q.AnswerDisplayMode, q.Slug, q.Status, activeID, actor, now, expected)
	if err != nil {
		return q, mapError(err)
	}
	if command.RowsAffected() != 1 {
		return q, surveyport.ErrConflict
	}
	return r.Get(ctx, q.ID, false)
}

func (r *Repository) Publish(ctx context.Context, id surveyport.ID, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	current, err := r.Get(ctx, id, true)
	if err != nil {
		return current, err
	}
	if current.Version != expected {
		return current, surveyport.ErrConflict
	}
	var active int64
	if err = t.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1`, id).Scan(&active); err != nil {
		return current, mapError(err)
	}
	if _, err = t.Exec(ctx, `UPDATE survey_definition_versions SET is_immutable=TRUE,published_at=$2 WHERE id=$1 AND is_immutable=FALSE`, active, now); err != nil {
		return current, mapError(err)
	}
	return r.updateStatus(ctx, t, id, surveyport.StatusPublished, expected, actor, now)
}

func (r *Repository) SetStatus(ctx context.Context, id surveyport.ID, status surveyport.QuestionnaireStatus, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	if status == surveyport.StatusPublished {
		var immutable bool
		if err = t.QueryRow(ctx, `SELECT v.is_immutable FROM survey_questionnaires q JOIN survey_definition_versions v ON v.id=q.active_definition_version_id WHERE q.id=$1 FOR UPDATE OF q`, id).Scan(&immutable); err != nil {
			return surveyport.Questionnaire{}, mapError(err)
		}
		if !immutable {
			return surveyport.Questionnaire{}, surveyport.ErrConflict
		}
	}
	return r.updateStatus(ctx, t, id, status, expected, actor, now)
}
func (r *Repository) updateStatus(ctx context.Context, t pgx.Tx, id surveyport.ID, status surveyport.QuestionnaireStatus, expected, actor int64, now time.Time) (surveyport.Questionnaire, error) {
	cmd, err := t.Exec(ctx, `UPDATE survey_questionnaires SET status=$2,updated_by=$3,updated_at=$4,version=version+1 WHERE id=$1 AND version=$5`, id, status, actor, now, expected)
	if err != nil {
		return surveyport.Questionnaire{}, mapError(err)
	}
	if cmd.RowsAffected() != 1 {
		return surveyport.Questionnaire{}, surveyport.ErrConflict
	}
	return r.Get(ctx, id, false)
}

func (r *Repository) DeleteDraft(ctx context.Context, id surveyport.ID, expected int64) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	var status surveyport.QuestionnaireStatus
	var version int64
	if err = t.QueryRow(ctx, `SELECT status,version FROM survey_questionnaires WHERE id=$1 FOR UPDATE`, id).Scan(&status, &version); err != nil {
		return mapError(err)
	}
	if status != surveyport.StatusDraft || version != expected {
		return surveyport.ErrConflict
	}
	var referenced bool
	if err = t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM survey_submissions WHERE questionnaire_id=$1)`, id).Scan(&referenced); err != nil {
		return mapError(err)
	}
	if referenced {
		return surveyport.ErrReferenced
	}
	if _, err = t.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=NULL WHERE id=$1`, id); err != nil {
		return mapError(err)
	}
	if _, err = t.Exec(ctx, `DELETE FROM survey_definition_versions WHERE questionnaire_id=$1`, id); err != nil {
		return mapError(err)
	}
	cmd, err := t.Exec(ctx, `DELETE FROM survey_questionnaires WHERE id=$1`, id)
	if err != nil {
		return mapError(err)
	}
	if cmd.RowsAffected() != 1 {
		return surveyport.ErrNotFound
	}
	return nil
}

func (r *Repository) Reserve(ctx context.Context, res surveyapp.Reservation) (surveyapp.Receipt, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.Receipt{}, false, err
	}
	var out surveyapp.Receipt
	err = t.QueryRow(ctx, `INSERT INTO survey_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id,operation,actor_scope,key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb)`, res.Operation, res.ActorScope, res.KeyDigest[:], res.PayloadDigest[:], res.CreatedAt).Scan(&out.ID, &out.Operation, &out.ActorScope, &out.KeyDigest, &out.PayloadDigest, &out.State, &out.Result)
	if err == nil {
		return out, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, false, mapError(err)
	}
	err = t.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb) FROM survey_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3 FOR UPDATE`, res.Operation, res.ActorScope, res.KeyDigest[:]).Scan(&out.ID, &out.Operation, &out.ActorScope, &out.KeyDigest, &out.PayloadDigest, &out.State, &out.Result)
	return out, false, mapError(err)
}
func (r *Repository) Complete(ctx context.Context, id int64, result json.RawMessage, now time.Time) (surveyapp.Receipt, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyapp.Receipt{}, err
	}
	var out surveyapp.Receipt
	err = t.QueryRow(ctx, `UPDATE survey_operation_receipts SET state='completed',result_snapshot=$2,completed_at=$3 WHERE id=$1 AND state='in_progress' RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, id, result, now).Scan(&out.ID, &out.Operation, &out.ActorScope, &out.KeyDigest, &out.PayloadDigest, &out.State, &out.Result)
	return out, mapError(err)
}
func (r *Repository) AppendAuditAndOutbox(ctx context.Context, event string, id surveyport.ID, actor string, payload json.RawMessage, key string, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	if _, err = t.Exec(ctx, `INSERT INTO survey_audit_events(event_type,aggregate_type,aggregate_id,actor_scope,metadata,occurred_at) VALUES($1,'questionnaire',$2,$3,$4,$5)`, event, id, actor, payload, now); err != nil {
		return mapError(err)
	}
	if _, err = t.Exec(ctx, `INSERT INTO survey_outbox(event_type,aggregate_type,aggregate_id,payload,idempotency_key,occurred_at) VALUES($1,'questionnaire',$2,$3,$4,$5)`, event, id, payload, key, now); err != nil {
		return mapError(err)
	}
	return nil
}

func (r *Repository) GetPublishedBySlug(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Questionnaire{}, err
	}
	q, active, err := scanBase(t.QueryRow(ctx, `SELECT `+baseColumns+` FROM survey_questionnaires q WHERE q.slug=$1 AND q.status='published'`, slug))
	if err != nil {
		return q, err
	}
	if active == nil {
		return q, surveyport.ErrUnavailable
	}
	if err = r.loadDefinition(ctx, t, &q, *active); err != nil {
		return surveyport.Questionnaire{}, err
	}
	return q, nil
}

func (r *Repository) CreateSubmission(ctx context.Context, input surveyapp.PersistSubmission) (surveyport.Submission, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, false, err
	}
	var definitionID int64
	if err = t.QueryRow(ctx, `SELECT active_definition_version_id FROM survey_questionnaires WHERE id=$1 AND status='published' FOR SHARE`, input.Questionnaire.ID).Scan(&definitionID); err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	resultRaw, _ := json.Marshal(input.Result)
	var submissionID int64
	var existingPayload []byte
	err = t.QueryRow(ctx, `INSERT INTO survey_submissions(questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,identity_reason,evidence_digest,submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,total_score,result_snapshot,source_channel,campaign_id,staff_id,submitted_at,created_at) VALUES($1,$2,$3,$4,$5,'',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17) ON CONFLICT(questionnaire_id,submission_key_digest) DO NOTHING RETURNING id`, input.Questionnaire.ID, definitionID, input.Questionnaire.DefinitionVersion, input.Command.Identity.CustomerID, input.Command.Identity.State, decodeEvidence(input.Command.Identity.EvidenceDigest), input.SubmissionKeyDigest[:], input.PayloadDigest[:], input.Questionnaire.Slug, input.Questionnaire.Title, input.Questionnaire.Mode, input.TotalScore, resultRaw, input.Command.SourceChannel, input.Command.CampaignID, input.Command.StaffID, input.Now).Scan(&submissionID)
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = t.QueryRow(ctx, `SELECT id,payload_digest FROM survey_submissions WHERE questionnaire_id=$1 AND submission_key_digest=$2 FOR UPDATE`, input.Questionnaire.ID, input.SubmissionKeyDigest[:]).Scan(&submissionID, &existingPayload)
		if err != nil {
			return surveyport.Submission{}, false, mapError(err)
		}
		if string(existingPayload) != string(input.PayloadDigest[:]) {
			return surveyport.Submission{}, false, surveyport.ErrConflict
		}
		stored, getErr := r.GetSubmission(ctx, surveyport.ID(submissionID))
		return stored, false, getErr
	}
	if err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	if _, err = t.Exec(ctx, `INSERT INTO survey_result_tokens(submission_id,token_digest,created_at) VALUES($1,$2,$3)`, submissionID, input.TokenDigest[:], input.Now); err != nil {
		return surveyport.Submission{}, false, mapError(err)
	}
	for _, answer := range input.Answers {
		var encrypted []byte
		if answer.TextValue != "" {
			encrypted, err = r.cipher.Encrypt(answer.TextValue)
			if err != nil {
				return surveyport.Submission{}, false, surveyport.ErrUnavailable
			}
		}
		options, _ := json.Marshal(answer.SelectedOptions)
		answerRaw, _ := json.Marshal(answer)
		answerDigest := sha256.Sum256(answerRaw)
		var questionID any
		if answer.QuestionID != nil {
			questionID = *answer.QuestionID
		}
		if _, err = t.Exec(ctx, `INSERT INTO survey_submission_answers(submission_id,definition_question_id,legacy_source_question_id,question_type,question_title_snapshot,question_sort_order,required_snapshot,selected_options_snapshot,text_value_ciphertext,text_value_masked,answer_digest,score_snapshot,legacy_definition_missing,created_at) VALUES($1,$2,$3,$4,$5,$6,FALSE,$7,$8,$9,$10,$11,$12,$13)`, submissionID, questionID, answer.LegacySourceQuestionID, answer.QuestionType, answer.QuestionTitle, answer.SortOrder, options, encrypted, answer.TextValueMasked, answerDigest[:], answer.Score, answer.LegacyDefinitionMissing, input.Now); err != nil {
			return surveyport.Submission{}, false, mapError(err)
		}
	}
	stored, err := r.GetSubmission(ctx, surveyport.ID(submissionID))
	return stored, created, err
}

func decodeEvidence(value string) []byte {
	if value == "" {
		return nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil
	}
	return decoded
}

func (r *Repository) GetSubmissionByTokenDigest(ctx context.Context, digest [32]byte) (surveyport.Submission, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, err
	}
	var id surveyport.ID
	err = t.QueryRow(ctx, `SELECT s.id FROM survey_result_tokens token JOIN survey_submissions s ON s.id=token.submission_id WHERE token.token_digest=$1 AND token.revoked_at IS NULL AND (token.expires_at IS NULL OR token.expires_at>CURRENT_TIMESTAMP)`, digest[:]).Scan(&id)
	if err != nil {
		return surveyport.Submission{}, mapError(err)
	}
	return r.GetSubmission(ctx, id)
}
func (r *Repository) GetSubmission(ctx context.Context, id surveyport.ID) (surveyport.Submission, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Submission{}, err
	}
	return r.loadSubmission(ctx, t, id)
}
func (r *Repository) loadSubmission(ctx context.Context, t pgx.Tx, id surveyport.ID) (surveyport.Submission, error) {
	var s surveyport.Submission
	var customer *int64
	var evidence []byte
	var resultRaw []byte
	err := t.QueryRow(ctx, `SELECT id,questionnaire_id,definition_version_number,customer_id,identity_state,evidence_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,total_score,result_snapshot,source_channel,campaign_id,staff_id,submitted_at FROM survey_submissions WHERE id=$1`, id).Scan(&s.ID, &s.QuestionnaireID, &s.DefinitionVersion, &customer, &s.Identity.State, &evidence, &s.QuestionnaireSlug, &s.QuestionnaireTitle, &s.Mode, &s.TotalScore, &resultRaw, &s.SourceChannel, &s.CampaignID, &s.StaffID, &s.SubmittedAt)
	if err != nil {
		return s, mapError(err)
	}
	if customer != nil {
		typed := customerdomain.CustomerID(*customer)
		s.Identity.CustomerID = &typed
	}
	if len(evidence) == 32 {
		s.Identity.EvidenceDigest = hex.EncodeToString(evidence)
	}
	if json.Unmarshal(resultRaw, &s.Result) != nil {
		return s, surveyport.ErrUnavailable
	}
	s.Answers = []surveyport.AnswerSnapshot{}
	rows, err := t.Query(ctx, `SELECT id,definition_question_id,legacy_source_question_id,question_type,question_title_snapshot,question_sort_order,selected_options_snapshot,text_value_ciphertext,text_value_masked,score_snapshot,legacy_definition_missing FROM survey_submission_answers WHERE submission_id=$1 ORDER BY question_sort_order,id`, id)
	if err != nil {
		return s, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a surveyport.AnswerSnapshot
		var qid *int64
		var options, encrypted []byte
		if err = rows.Scan(&a.ID, &qid, &a.LegacySourceQuestionID, &a.QuestionType, &a.QuestionTitle, &a.SortOrder, &options, &encrypted, &a.TextValueMasked, &a.Score, &a.LegacyDefinitionMissing); err != nil {
			return s, mapError(err)
		}
		if qid != nil {
			typed := surveyport.ID(*qid)
			a.QuestionID = &typed
		}
		if json.Unmarshal(options, &a.SelectedOptions) != nil {
			return s, surveyport.ErrUnavailable
		}
		if len(encrypted) > 0 {
			a.TextValue, err = r.cipher.Decrypt(encrypted)
			if err != nil {
				return s, surveyport.ErrUnavailable
			}
		}
		s.Answers = append(s.Answers, a)
	}
	return s, mapError(rows.Err())
}

func (r *Repository) ListSubmissions(ctx context.Context, id surveyport.ID, limit, offset int32, state surveyport.IdentityState) ([]surveyport.Submission, int64, error) {
	return r.listSubmissionQuery(ctx, `questionnaire_id=$1 AND ($2='' OR identity_state=$2)`, []any{id, string(state)}, limit, offset)
}
func (r *Repository) CustomerHistory(ctx context.Context, customer int64, limit, offset int32) ([]surveyport.Submission, int64, error) {
	return r.listSubmissionQuery(ctx, `customer_id=$1`, []any{customer}, limit, offset)
}
func (r *Repository) listSubmissionQuery(ctx context.Context, where string, args []any, limit, offset int32) ([]surveyport.Submission, int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = t.QueryRow(ctx, `SELECT count(*) FROM survey_submissions WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}
	limitPosition := len(args) + 1
	offsetPosition := len(args) + 2
	args = append(args, limit, offset)
	rows, err := t.Query(ctx, `SELECT id FROM survey_submissions WHERE `+where+` ORDER BY submitted_at DESC,id DESC LIMIT $`+fmt.Sprint(limitPosition)+` OFFSET $`+fmt.Sprint(offsetPosition), args...)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()
	ids := []surveyport.ID{}
	for rows.Next() {
		var id surveyport.ID
		if err = rows.Scan(&id); err != nil {
			return nil, 0, mapError(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, mapError(err)
	}
	items := make([]surveyport.Submission, 0, len(ids))
	for _, id := range ids {
		item, e := r.loadSubmission(ctx, t, id)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, item)
	}
	return items, total, nil
}
func (r *Repository) SubmissionAnalytics(ctx context.Context, id surveyport.ID) (surveyport.Analytics, error) {
	t, err := tx(ctx)
	if err != nil {
		return surveyport.Analytics{}, err
	}
	var a surveyport.Analytics
	err = t.QueryRow(ctx, `SELECT q.id,v.version_number,q.slug,q.status,count(s.id),COALESCE(avg(s.total_score),0) FROM survey_questionnaires q JOIN survey_definition_versions v ON v.id=q.active_definition_version_id LEFT JOIN survey_submissions s ON s.questionnaire_id=q.id WHERE q.id=$1 GROUP BY q.id,v.version_number`, id).Scan(&a.QuestionnaireID, &a.DefinitionVersion, &a.Slug, &a.State, &a.SubmissionCount, &a.AverageScore)
	return a, mapError(err)
}

func (r *Repository) String() string { return fmt.Sprintf("survey.Repository(%p)", r) }

var _ surveyapp.Store = (*Repository)(nil)
var _ surveyapp.SubmissionStore = (*Repository)(nil)
