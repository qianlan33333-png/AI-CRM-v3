package migration

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQLRuns struct{ Pool *pgxpool.Pool }

var ErrRunConflict = errors.New("commerce migration run conflicts with persisted manifest")

func (store PostgreSQLRuns) Begin(ctx context.Context, runKey string, digest [32]byte, input int64) error {
	schema := sha256.Sum256([]byte(SchemaVersion))
	result, err := store.Pool.Exec(ctx, `INSERT INTO order_import_runs(run_key,source_manifest_digest,source_schema_digest,status,input_count) VALUES($1,$2,$3,'applying',$4) ON CONFLICT(run_key) DO UPDATE SET status='applying',input_count=EXCLUDED.input_count,completed_at=NULL WHERE order_import_runs.source_manifest_digest=EXCLUDED.source_manifest_digest AND order_import_runs.source_schema_digest=EXCLUDED.source_schema_digest`, runKey, digest[:], schema[:], input)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return err
}

func (store PostgreSQLRuns) Complete(ctx context.Context, runKey string, imported int64) error {
	result, err := store.Pool.Exec(ctx, `UPDATE order_import_runs SET status='applied',imported_count=$2,completed_at=clock_timestamp() WHERE run_key=$1 AND status='applying'`, runKey, imported)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return err
}

func (store PostgreSQLRuns) CompleteOrders(ctx context.Context, runKey string, processed int64) error {
	result, err := store.Pool.Exec(ctx, `UPDATE order_import_runs run SET status='applied',imported_count=(SELECT count(*) FROM order_import_receipts WHERE run_id=run.id AND outcome='imported'),replayed_count=(SELECT count(*) FROM order_import_receipts WHERE run_id=run.id AND outcome='replayed'),completed_at=clock_timestamp() WHERE run_key=$1 AND status='applying' AND (SELECT count(*) FROM order_import_receipts WHERE run_id=run.id AND outcome IN ('imported','replayed'))=$2`, runKey, processed)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return err
}

func (store PostgreSQLRuns) ReconcileOrders(ctx context.Context, manifest Manifest) (OrderOnlyReconciliation, error) {
	if store.Pool == nil {
		return OrderOnlyReconciliation{}, errors.New("order migration store is not configured")
	}
	if err := ValidateOrderOnly(manifest); err != nil {
		return OrderOnlyReconciliation{}, err
	}
	var result OrderOnlyReconciliation
	var persistedDigest []byte
	err := store.Pool.QueryRow(ctx, `
		SELECT run.source_manifest_digest,
		       count(*) FILTER (WHERE receipt.outcome IN ('imported','replayed')),
		       COALESCE(sum(o.amount_minor) FILTER (WHERE receipt.outcome IN ('imported','replayed')),0),
		       count(*) FILTER (WHERE receipt.outcome='imported'),
		       count(*) FILTER (WHERE receipt.outcome='replayed'),
		       count(*) FILTER (WHERE receipt.outcome IN ('imported','replayed') AND o.payer_customer_id IS NULL AND o.beneficiary_customer_id IS NULL),
		       count(*) FILTER (WHERE receipt.outcome IN ('imported','replayed') AND o.effect_eligible)
		FROM order_import_runs run
		LEFT JOIN order_import_receipts receipt ON receipt.run_id=run.id
		LEFT JOIN orders o ON o.id=receipt.order_id
		WHERE run.run_key=$1 AND run.status IN ('applied','reconciled')
		GROUP BY run.id`, manifest.RunKey).Scan(&persistedDigest, &result.Orders, &result.AmountMinor, &result.Imported, &result.Replayed, &result.Floating, &result.EffectEligible)
	if err != nil {
		return OrderOnlyReconciliation{}, err
	}
	summary := manifest.Summary()
	result.Matched = len(persistedDigest) == sha256.Size && subtle.ConstantTimeCompare(persistedDigest, manifest.Digest[:]) == 1 && result.Orders == int64(summary.OrderRows) && result.AmountMinor == summary.AmountMinor && result.Floating == result.Orders && result.EffectEligible == 0
	if !result.Matched {
		return result, errors.New("order-only reconciliation mismatch")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE order_import_runs SET status='reconciled',completed_at=clock_timestamp() WHERE run_key=$1 AND source_manifest_digest=$2 AND status IN ('applied','reconciled')`, manifest.RunKey, manifest.Digest[:])
	if err != nil || command.RowsAffected() != 1 {
		if err != nil {
			return result, err
		}
		return result, ErrRunConflict
	}
	return result, nil
}
