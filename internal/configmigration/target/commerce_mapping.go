package target

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func existingCommerceMapping(ctx context.Context, system, kind string, sourceID int64, row any, wantTable string) (int64, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var id int64
	var prior []byte
	var table string
	err = tx.QueryRow(ctx, `SELECT target_id,source_digest,target_table FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind=$2 AND source_key=$3 FOR UPDATE`, system, kind, fmt.Sprint(sourceID)).Scan(&id, &prior, &table)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	raw, err := json.Marshal(row)
	if err != nil {
		return 0, false, err
	}
	d := sha256.Sum256(raw)
	if table != wantTable || string(prior) != string(d[:]) {
		return 0, false, ErrDrift
	}
	var version int64
	switch table {
	case "products":
		err = tx.QueryRow(ctx, `SELECT version FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&version)
	case "coupon_rules":
		err = tx.QueryRow(ctx, `SELECT version FROM coupon_rules WHERE id=$1 FOR UPDATE`, id).Scan(&version)
	default:
		return 0, false, ErrDrift
	}
	if err != nil || version != 1 {
		return 0, false, ErrDrift
	}
	return id, true, nil
}

// Every historical commerce source mapping must remain represented. Removed
// source definitions/bindings require an explicit reconciliation decision.
func verifyCommerceCoverage(ctx context.Context, system string, keys map[string][]string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	for kind, ids := range keys {
		var missing bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM config_definition_import_source_maps WHERE source_system=$1 AND source_kind=$2 AND NOT (source_key=ANY($3::text[])))`, system, kind, ids).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrDrift
		}
	}
	return nil
}
