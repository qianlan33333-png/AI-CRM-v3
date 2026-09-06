package target

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ProductAppendRunner restores only the bounded, previously unmapped product
// definitions. It shares the existing immutable import batch and source-map
// tables so provenance remains global across the original and append imports.
type ProductAppendRunner struct {
	UOW      platformport.UnitOfWork
	Products productport.DefinitionImporter
}

type ProductAppendResult struct {
	BatchID  int64 `json:"batch_id"`
	NoOp     bool  `json:"no_op"`
	Products int   `json:"products"`
}

func (r ProductAppendRunner) Preflight(ctx context.Context, snapshot source.ProductAppend, digest [sha256.Size]byte, actor int64) error {
	if !r.ready(snapshot, actor) || !validProductAppendDigest(snapshot, digest) {
		return ErrInvalid
	}
	return r.UOW.Within(ctx, func(tx context.Context) error {
		t, err := platformpostgres.RequireTransaction(tx)
		if err != nil {
			return err
		}
		if err = lockProductAppendKeys(tx, t, snapshot); err != nil {
			return err
		}
		if err = activeActor(tx, t, actor); err != nil {
			return err
		}
		_, prior, found, err := existingProductAppend(tx, t, snapshot)
		if err != nil {
			return err
		}
		if found {
			if len(prior) != sha256.Size || string(prior) != string(digest[:]) {
				return ErrDrift
			}
			return nil
		}
		return preflightProductAppendRows(tx, t, snapshot)
	})
}

func (r ProductAppendRunner) Apply(ctx context.Context, snapshot source.ProductAppend, digest [sha256.Size]byte, actor int64) (out ProductAppendResult, err error) {
	manifest, valid := canonicalProductAppend(snapshot, digest)
	if !r.ready(snapshot, actor) || !valid {
		return out, ErrInvalid
	}
	err = r.UOW.Within(ctx, func(tx context.Context) error {
		t, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		if e = lockProductAppendKeys(tx, t, snapshot); e != nil {
			return e
		}
		if e = activeActor(tx, t, actor); e != nil {
			return e
		}
		priorBatchID, prior, found, e := existingProductAppend(tx, t, snapshot)
		if e != nil {
			return e
		}
		if found {
			if len(prior) != sha256.Size || string(prior) != string(digest[:]) {
				return ErrDrift
			}
			out.BatchID = priorBatchID
			out.NoOp = true
			return nil
		}
		if e = preflightProductAppendRows(tx, t, snapshot); e != nil {
			return e
		}
		if e = t.QueryRow(tx, `INSERT INTO config_definition_import_batches(source_system,batch_key,snapshot_digest,actor_admin_user_id,status,manifest) VALUES($1,$2,$3,$4,'applying',$5::jsonb) RETURNING id`, snapshot.SourceSystem, productAppendBatchKey(snapshot), digest[:], actor, manifest).Scan(&out.BatchID); e != nil {
			return e
		}
		for _, product := range snapshot.Products {
			projection, e := appendProductProjection(product)
			if e != nil {
				return e
			}
			imported, e := r.Products.ImportDefinition(tx, productport.DefinitionImport{ProductCode: product.ProductCode, Name: product.Name, Description: product.Description, PriceMinor: product.PriceMinor, Currency: product.Currency, LegacyAdminProjection: projection, Actor: actor, CreatedAt: product.CreatedAt, UpdatedAt: product.UpdatedAt})
			if e != nil {
				return fmt.Errorf("append product source %d: %w", product.ID, e)
			}
			if e = mapRow(tx, out.BatchID, snapshot.SourceSystem, "product", "wechat_pay_products", product.ID, product, int64(imported.ID), "products", nil); e != nil {
				return e
			}
			out.Products++
		}
		counts, e := json.Marshal(map[string]int{"products": out.Products})
		if e != nil {
			return e
		}
		_, e = t.Exec(tx, `UPDATE config_definition_import_batches SET status='applied',imported_counts=$2::jsonb,applied_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='applying'`, out.BatchID, counts)
		return e
	})
	return out, err
}

func (r ProductAppendRunner) Verify(ctx context.Context, snapshot source.ProductAppend, digest [sha256.Size]byte) (out ProductAppendResult, err error) {
	if r.UOW == nil || !validProductAppendDigest(snapshot, digest) {
		return out, ErrInvalid
	}
	err = r.UOW.Within(ctx, func(tx context.Context) error {
		t, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		if e = lockProductAppendKeys(tx, t, snapshot); e != nil {
			return e
		}
		var status string
		if e = t.QueryRow(tx, `SELECT id,status FROM config_definition_import_batches WHERE source_system=$1 AND batch_key=$2 AND snapshot_digest=$3 FOR UPDATE`, snapshot.SourceSystem, productAppendBatchKey(snapshot), digest[:]).Scan(&out.BatchID, &status); e != nil {
			return e
		}
		if status != "applied" && status != "verified" {
			return ErrInvalid
		}
		for _, product := range snapshot.Products {
			expectedSourceDigest, digestErr := sourceRowDigest(product)
			if digestErr != nil {
				return ErrInvalid
			}
			var mappedSourceDigest []byte
			var productCode string
			var version int64
			e = t.QueryRow(tx, `SELECT m.source_digest,p.product_code,p.version
				FROM config_definition_import_source_maps m
				JOIN products p ON p.id=m.target_id
				WHERE m.batch_id=$1 AND m.domain='product' AND m.source_kind='wechat_pay_products'
					AND m.target_table='products' AND m.source_key=$2`, out.BatchID, fmt.Sprint(product.ID)).Scan(&mappedSourceDigest, &productCode, &version)
			if e != nil || len(mappedSourceDigest) != sha256.Size || !bytes.Equal(mappedSourceDigest, expectedSourceDigest[:]) || productCode != product.ProductCode || version != 1 {
				return ErrInvalid
			}
		}
		out.Products = len(snapshot.Products)
		if status == "applied" {
			tag, e := t.Exec(tx, `UPDATE config_definition_import_batches SET status='verified',verified_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='applied'`, out.BatchID)
			if e != nil || tag.RowsAffected() != 1 {
				return ErrInvalid
			}
		}
		return nil
	})
	return out, err
}

func (r ProductAppendRunner) ready(snapshot source.ProductAppend, actor int64) bool {
	return r.UOW != nil && r.Products != nil && actor > 0 && snapshot.Validate() == nil
}

func validProductAppendDigest(snapshot source.ProductAppend, digest [sha256.Size]byte) bool {
	_, ok := canonicalProductAppend(snapshot, digest)
	return ok
}

func canonicalProductAppend(snapshot source.ProductAppend, digest [sha256.Size]byte) ([]byte, bool) {
	// Validate before Canonical so callers cannot rely on normalization to make
	// an unordered caller-provided manifest acceptable at this mutation boundary.
	if snapshot.Validate() != nil {
		return nil, false
	}
	manifest, actual, err := snapshot.Canonical()
	if err != nil || actual != digest {
		return nil, false
	}
	return manifest, true
}

func activeActor(ctx context.Context, tx pgx.Tx, actor int64) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1 AND is_active)`, actor).Scan(&active); err != nil || !active {
		return ErrInvalid
	}
	return nil
}

func existingProductAppend(ctx context.Context, tx pgx.Tx, snapshot source.ProductAppend) (int64, []byte, bool, error) {
	var batchID int64
	var prior []byte
	err := tx.QueryRow(ctx, `SELECT id,snapshot_digest FROM config_definition_import_batches WHERE source_system=$1 AND batch_key=$2 FOR UPDATE`, snapshot.SourceSystem, productAppendBatchKey(snapshot)).Scan(&batchID, &prior)
	if err == nil {
		return batchID, prior, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	return 0, nil, false, err
}

func preflightProductAppendRows(ctx context.Context, tx pgx.Tx, snapshot source.ProductAppend) error {
	keys := productAppendSourceKeys(snapshot)
	codes := make([]string, 0, len(snapshot.Products))
	for _, product := range snapshot.Products {
		codes = append(codes, product.ProductCode)
	}
	var mapped, existing int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM config_definition_import_source_maps WHERE source_system=$1 AND domain='product' AND source_kind='wechat_pay_products' AND source_key=ANY($2)`, snapshot.SourceSystem, keys).Scan(&mapped); err != nil || mapped != 0 {
		return ErrInvalid
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE product_code=ANY($1)`, codes).Scan(&existing); err != nil || existing != 0 {
		return ErrInvalid
	}
	return nil
}

func appendProductProjection(product source.Product) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"schema_version":   1,
		"status":           "enabled",
		"enabled":          true,
		"buy_button_text":  product.BuyButtonText,
		"require_mobile":   product.RequireMobile,
		"lead_qr_title":    product.LeadQRTitle,
		"lead_qr_subtitle": product.LeadQRSubtitle,
	})
}

func productAppendBatchKey(snapshot source.ProductAppend) string {
	keys := productAppendSourceKeys(snapshot)
	digest := sha256.Sum256([]byte(snapshot.SourceSystem + "\x00" + snapshot.SourceRevision + "\x00" + joinSourceKeys(keys)))
	return "product-append:" + snapshot.SourceRevision + ":" + hex.EncodeToString(digest[:])
}

func productAppendSourceKeys(snapshot source.ProductAppend) []string {
	keys := make([]string, 0, len(snapshot.Products))
	for _, product := range snapshot.Products {
		keys = append(keys, fmt.Sprint(product.ID))
	}
	sort.Strings(keys)
	return keys
}

func joinSourceKeys(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	out := keys[0]
	for _, key := range keys[1:] {
		out += "\x00" + key
	}
	return out
}

func lockProductAppendKeys(ctx context.Context, tx pgx.Tx, snapshot source.ProductAppend) error {
	for _, key := range productAppendSourceKeys(snapshot) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "config-product-append:"+snapshot.SourceSystem+":"+key); err != nil {
			return err
		}
	}
	return nil
}
