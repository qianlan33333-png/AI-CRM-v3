package automation

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

// ModuleRegistration is the stable local-configuration composition seam.
type ModuleRegistration struct{}
type HTTPBindings struct{ Agents, Runtime http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) Bind(service automationport.AgentService, security automationhttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("automation module is required")
	}
	h, e := automationhttp.NewHandler(service, security)
	return HTTPBindings{Agents: h}, e
}
func (m *ModuleRegistration) BindRuntime(service *automationapp.RuntimeService, security automationhttp.RequestSecurity) (http.Handler, error) {
	if m == nil {
		return nil, errors.New("automation module is required")
	}
	return automationhttp.NewRuntimeHandler(service, security)
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("automation module dependencies are required")
	}
	var ready bool
	e := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['automation_agents','automation_operation_receipts','automation_audit_events','automation_outbox','automation_policies','automation_policy_versions','automation_enrollments','automation_run_previews','automation_runs','automation_run_recipients','automation_run_reconciliations','automation_runtime_operation_receipts','automation_runtime_audit_events','automation_runtime_outbox']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if e != nil {
		return e
	}
	if !ready {
		return errors.New("automation schema is not ready")
	}
	return nil
}
