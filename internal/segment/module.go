// Package segment registers the Segment/Audience domain in the composition root.
package segment

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
)

type ModuleRegistration struct{}
type HTTPBindings struct{ Audience http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Bind(service segmenthttp.ConfigurationApplication, security segmenthttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("segment module is required")
	}
	handler, err := segmenthttp.NewHandler(service, security)
	return HTTPBindings{Audience: handler}, err
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("segment module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM unnest(ARRAY[
			'segment_audience_groups',
			'segment_audience_packages',
			'segment_audience_configuration_versions',
			'segment_audience_operation_receipts',
			'segment_audience_audit_events',
			'segment_audience_outbox'
		]) AS required(name)
		WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
	)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("segment audience schema is not ready")
	}
	return nil
}
