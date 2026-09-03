// Package module exposes Config's stable composition contract without making
// the registry package depend on its application layer.
package module

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	confighttp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/http"
)

type Registration struct{}
type HTTPBindings struct{ Config http.Handler }

func NewRegistration() *Registration { return &Registration{} }
func (m *Registration) Bind(settings *configapp.SettingsCompatibilityService, wizard *configapp.SetupWizardService, projections interface {
	ListReleaseProjections(context.Context) ([]map[string]any, error)
	ListDiagnosticSnapshots(context.Context) ([]map[string]any, error)
}, security confighttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("config module is required")
	}
	h, e := confighttp.NewHandler(settings, wizard, projections, security)
	return HTTPBindings{Config: h}, e
}
func (m *Registration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("config module dependencies are required")
	}
	var ready bool
	e := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['config_settings','config_audits','config_outbox','adminops_release_projections','adminops_diagnostic_snapshots']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if e != nil {
		return e
	}
	if !ready {
		return errors.New("config schema is not ready")
	}
	return nil
}
