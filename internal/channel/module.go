package channel

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ModuleRegistration struct{}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (module *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if module == nil || pool == nil {
		return errors.New("channel module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['channels','channel_config_versions','channel_assignees','channel_operation_receipts','channel_acquisition_state_bindings','channel_acquisition_entrant_receipts']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("channel schema is not ready")
	}
	return nil
}
