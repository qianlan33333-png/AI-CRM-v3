package survey

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"net/http"
)

type ModuleRegistration struct{}
type HTTPBindings struct{ Survey http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
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
	return HTTPBindings{Survey: handler}, nil
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("survey module dependencies required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM unnest(ARRAY['survey_questionnaires','survey_definition_versions','survey_definition_questions','survey_definition_options','survey_score_rules','survey_submissions','survey_submission_answers','survey_result_tokens','survey_oauth_states','survey_identity_sessions','survey_operation_configurations','survey_external_operation_receipts','survey_audit_events','survey_outbox','survey_migration_batches','survey_migration_source_map','survey_migration_quarantine']) required(name) WHERE to_regclass(current_schema()||'.'||required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("survey schema is not ready")
	}
	return nil
}
