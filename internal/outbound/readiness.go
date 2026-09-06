package outbound

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Readiness verifies the Outbound-owned immutable-content columns before a
// worker can claim an automatic message. The migration is required because a
// missing column would otherwise surface later as a retryable runtime error.
func Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("outbound readiness requires PostgreSQL")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_message_intents' AND column_name='content_snapshot')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_message_intents' AND column_name='content_snapshot_digest')
		AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='outbound_message_intents'::regclass AND conname='outbound_message_intents_content_snapshot_shape')`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("outbound content snapshot schema is not ready")
	}
	return nil
}
