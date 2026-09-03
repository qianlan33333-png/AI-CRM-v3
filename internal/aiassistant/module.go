package aiassistant

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ModuleRegistration struct{}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("AI Assistant module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM unnest(ARRAY[
			'ai_assistant_plans','ai_assistant_plan_recipients','ai_assistant_content_versions',
			'ai_assistant_review_decisions','ai_assistant_effect_bindings','ai_assistant_operation_receipts',
			'ai_assistant_audit_events','ai_assistant_outbox'
		]) AS required(name)
		WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
	)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("AI Assistant schema is not ready")
	}
	return nil
}
