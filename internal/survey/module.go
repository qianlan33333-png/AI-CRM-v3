package survey

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"net/http"
)

type ModuleRegistration struct{ completionProviderEnabled bool }
type HTTPBindings struct{ Survey http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) SetCompletionProviderEnabled(enabled bool) *ModuleRegistration {
	if m != nil {
		m.completionProviderEnabled = enabled
	}
	return m
}
func (m *ModuleRegistration) Bind(definitions surveyport.DefinitionApplication, submissions interface {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
}, security surveyhttp.RequestSecurity, oauth ...surveyhttp.OAuthApplication) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("survey module required")
	}
	handler, err := surveyhttp.NewHandler(definitions, submissions, security, oauth...)
	if err != nil {
		return HTTPBindings{}, err
	}
	handler.SetCompletionProviderEnabled(m.completionProviderEnabled)
	return HTTPBindings{Survey: handler}, nil
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("survey module dependencies required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM unnest(ARRAY['survey_questionnaires','survey_definition_versions','survey_definition_questions','survey_definition_options','survey_score_rules','survey_submissions','survey_submission_answers','survey_result_tokens','survey_oauth_states','survey_identity_sessions','survey_phone_binding_receipts','survey_operation_configurations','survey_external_operation_receipts','survey_completion_test_push_snapshots','survey_audit_events','survey_outbox','survey_migration_batches','survey_migration_source_map','survey_migration_quarantine']) required(name) WHERE to_regclass(current_schema()||'.'||required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("survey schema is not ready")
	}
	var redirectConstraintReady bool
	err = pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1
		FROM pg_constraint constraint
		JOIN pg_class relation ON relation.oid=constraint.conrelid
		JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		WHERE namespace.nspname=current_schema()
		  AND relation.relname='survey_oauth_states'
		  AND constraint.conname='survey_oauth_states_redirect'
		  AND pg_get_constraintdef(constraint.oid) LIKE '%/h5/(all|one)\.html\?slug=%' ESCAPE ''
	)`).Scan(&redirectConstraintReady)
	if err != nil {
		return err
	}
	if !redirectConstraintReady {
		return errors.New("survey OAuth state redirect constraint is not ready")
	}
	return nil
}
