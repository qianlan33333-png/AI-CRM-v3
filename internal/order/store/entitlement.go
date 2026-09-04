package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ListCustomerEntitlements(ctx context.Context, customerID int64, limit int32) (orderport.EntitlementPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.EntitlementPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at FROM order_service_entitlements WHERE customer_id=$1 AND status IN ('active','expired') ORDER BY end_at DESC,id DESC LIMIT $2`, customerID, limit)
	if err != nil {
		return orderport.EntitlementPage{}, err
	}
	defer rows.Close()
	page := orderport.EntitlementPage{Items: []orderport.Entitlement{}}
	for rows.Next() {
		var item orderport.Entitlement
		if err = rows.Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM order_service_entitlements WHERE customer_id=$1 AND status IN ('active','expired')`, customerID).Scan(&page.Total)
	return page, err
}

func (r *Repository) FindEntitlementReceipt(ctx context.Context, key [32]byte) (orderport.Entitlement, [32]byte, string, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, [32]byte{}, "", false, err
	}
	var item orderport.Entitlement
	var digest, raw []byte
	var outcome string
	err = tx.QueryRow(ctx, `SELECT payload_digest,outcome,result_snapshot FROM order_entitlement_operation_receipts WHERE key_digest=$1`, key[:]).Scan(&digest, &outcome, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, [32]byte{}, "", false, nil
	}
	if err != nil {
		return item, [32]byte{}, "", false, err
	}
	var d [32]byte
	if len(digest) != 32 || json.Unmarshal(raw, &item) != nil {
		return item, d, outcome, false, orderport.ErrUnavailable
	}
	copy(d[:], digest)
	return item, d, outcome, true, nil
}

func (r *Repository) UpdateEntitlementRemark(ctx context.Context, command orderport.RemarkCommand, key, payload [32]byte, at time.Time) (orderport.Entitlement, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	var item orderport.Entitlement
	err = tx.QueryRow(ctx, `UPDATE order_service_entitlements SET remark=$4,version=version+1,updated_at=$5 WHERE id=$1 AND customer_id=$2 AND version=$3 AND status IN ('active','expired') RETURNING id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at`, command.EntitlementID, command.CustomerID, command.ExpectedVersion, command.Remark, at).Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, orderport.ErrConflict
	}
	if err != nil {
		return item, err
	}
	raw, _ := json.Marshal(item)
	actor := sha256.Sum256([]byte(command.EmployeeID))
	if _, err = tx.Exec(ctx, `INSERT INTO order_entitlement_operation_receipts(operation,key_digest,payload_digest,entitlement_id,outcome,result_snapshot) VALUES('remark',$1,$2,$3,'updated',$4::jsonb)`, key[:], payload[:], item.ID, raw); err != nil {
		return item, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_entitlement_audit_events(entitlement_id,operation,actor_digest,payload_digest,occurred_at) VALUES($1,'remark',$2,$3,$4)`, item.ID, actor[:], payload[:], at); err != nil {
		return item, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO order_entitlement_outbox(event_type,entitlement_id,payload,idempotency_digest,occurred_at) VALUES('order.entitlement.remark_updated.v1',$1,jsonb_build_object('entitlement_id',$1,'version',$2),$3,$4)`, item.ID, item.Version, key[:], at)
	return item, err
}

func (r *Repository) RecordEntitlementConflict(ctx context.Context, command orderport.RemarkCommand, key, payload [32]byte, item orderport.Entitlement, at time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(item)
	_, err = tx.Exec(ctx, `INSERT INTO order_entitlement_operation_receipts(operation,key_digest,payload_digest,entitlement_id,outcome,result_snapshot,created_at) VALUES('remark',$1,$2,$3,'version_conflict',$4::jsonb,$5)`, key[:], payload[:], item.ID, raw, at)
	return err
}

func (r *Repository) ImportHistoricalEntitlement(ctx context.Context, input orderport.HistoricalEntitlement) (orderport.Entitlement, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, false, err
	}
	var item orderport.Entitlement
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at,source_digest FROM order_service_entitlements WHERE source_system=$1 AND source_key=$2 FOR UPDATE`, input.SourceSystem, input.SourceKey).Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt, &digest)
	if err == nil {
		if len(digest) != 32 || string(digest) != string(input.SourceDigest[:]) {
			return item, false, orderport.ErrConflict
		}
		return item, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return item, false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at`, input.SourceSystem, input.SourceKey, input.CustomerID, input.ServiceProductID, input.ProductName, input.LastOrderID, input.Status, input.StartAt, input.EndAt, input.Remark, input.SourceDigest[:], input.CreatedAt, input.UpdatedAt).Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	return item, err == nil, err
}

var _ orderapp.EntitlementStore = (*Repository)(nil)
