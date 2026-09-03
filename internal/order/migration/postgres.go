package migration

import (
	"context"
	"crypto/sha256"
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
