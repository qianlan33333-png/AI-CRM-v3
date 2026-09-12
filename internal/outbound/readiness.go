package outbound

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Readiness verifies the Outbound-owned immutable-content and material-refresh
// schema before a worker can claim an automatic message. The migrations are
// required because a missing column or source-count constraint would otherwise
// surface later as a retryable runtime error.
func Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("outbound readiness requires PostgreSQL")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_message_intents' AND column_name='content_snapshot')
		AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_message_intents' AND column_name='content_snapshot_digest')
		AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='outbound_message_intents'::regclass AND conname='outbound_message_intents_content_snapshot_shape')
		AND to_regclass(current_schema() || '.outbound_commerce_push_intents') IS NOT NULL
 AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='outbound_commerce_push_intents' AND column_name='payload_mode')
		AND to_regclass(current_schema() || '.outbound_commerce_push_endpoints') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_commerce_push_history_batches') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_commerce_push_history_rows') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_commerce_push_history_batch_rows') IS NOT NULL
		AND to_regclass(current_schema() || '.outbound_material_refresh_items') IS NOT NULL
		AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='outbound_material_refresh_items'::regclass AND conname='outbound_material_refresh_items_source_count_check' AND pg_get_constraintdef(oid) LIKE '%source_count >= 0%')
		AND EXISTS(SELECT 1 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE d.adrelid='outbound_material_refresh_items'::regclass AND a.attname='source_count' AND pg_get_expr(d.adbin,d.adrelid)='0')`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("outbound content snapshot schema is not ready")
	}
	return nil
}
