// Package segment registers the Segment/Audience domain in the composition root.
package segment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ModuleRegistration struct{}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

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
