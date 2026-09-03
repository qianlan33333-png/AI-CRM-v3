package groupops

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// ModuleRegistration is the Group Ops composition boundary. The module
// publishes local HTTP/runtime bindings and readiness only; donor frontend
// files remain in the existing web build and are mounted by UI adapter.
type ModuleRegistration struct{}

type HTTPBindings struct {
	GroupOps http.Handler
	UI       http.Handler
}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Bind(application groupopshttp.Application, runtime groupopshttp.RuntimeApplication, security groupopshttp.RequestSecurity, protocols groupopshttp.ProtocolAuthenticator, contentDelivery ...mediaport.ContentDeliveryService) (HTTPBindings, error) {
	if m == nil || application == nil || runtime == nil || security == nil {
		return HTTPBindings{}, errors.New("Group Ops module dependencies are required")
	}
	handler, err := groupopshttp.NewHandlerWithRuntime(application, runtime, security, protocols, contentDelivery...)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{GroupOps: handler}, nil
}

// BindWithHistory adds the minimal v3 host binding needed by the frozen donor
// history pages. The donor files still own the URL, markup and interaction;
// this only supplies their authenticated local read transport.
func (m *ModuleRegistration) BindWithHistory(application groupopshttp.Application, runtime groupopshttp.RuntimeApplication, history groupopshttp.HistoryApplication, security groupopshttp.RequestSecurity, protocols groupopshttp.ProtocolAuthenticator, contentDelivery ...mediaport.ContentDeliveryService) (HTTPBindings, error) {
	if m == nil || application == nil || runtime == nil || history == nil || security == nil {
		return HTTPBindings{}, errors.New("Group Ops history module dependencies are required")
	}
	handler, err := groupopshttp.NewHandlerWithRuntimeAndHistory(application, runtime, history, security, protocols, contentDelivery...)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{GroupOps: handler}, nil
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("Group Ops module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['group_ops_plans','group_ops_plan_members','group_ops_plan_group_assets','group_ops_plan_nodes','group_ops_plan_webhook_descriptors','group_ops_operation_receipts','group_ops_audit_events','group_ops_outbox','group_ops_runs','group_ops_executions','group_ops_directory_groups','group_ops_directory_refresh_receipts','group_ops_protocol_replays','group_ops_v1_history_plans','group_ops_v1_history_directory','group_ops_v1_history_groups','group_ops_v1_history_nodes']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("Group Ops schema is not ready")
	}
	return nil
}
