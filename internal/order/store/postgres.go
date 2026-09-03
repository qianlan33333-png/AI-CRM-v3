// Package store owns Order PostgreSQL persistence. Every operation requires a
// transaction-bound context supplied by the shared platform Unit of Work.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrInvalid = errors.New("invalid order persistence request")

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.uow == nil || fn == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, fn)
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

type rowScanner interface{ Scan(...any) error }

const orderColumns = `id,provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at`

func scanOrder(row rowScanner) (domain.Snapshot, error) {
	var snapshot domain.Snapshot
	err := row.Scan(
		&snapshot.ID, &snapshot.Provider, &snapshot.SourceSystem, &snapshot.SourceKey,
		&snapshot.MerchantOrderNo, &snapshot.ProviderTransactionNo,
		&snapshot.PayerCustomerID, &snapshot.BeneficiaryCustomerID,
		&snapshot.Amount.AmountMinor, &snapshot.RefundedMinor, &snapshot.Amount.Currency,
		&snapshot.Status, &snapshot.RecordOrigin, &snapshot.EffectEligible,
		&snapshot.Version, &snapshot.CreatedAt, &snapshot.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	if err != nil {
		return domain.Snapshot{}, mapError(err)
	}
	return snapshot, nil
}

func (r *Repository) Reserve(ctx context.Context, reservation orderapp.Reservation) (orderapp.Receipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderapp.Receipt{}, false, err
	}
	if reservation.Operation == "" || reservation.ActorScope == "" || reservation.CreatedAt.IsZero() {
		return orderapp.Receipt{}, false, ErrInvalid
	}
	var receipt orderapp.Receipt
	err = tx.QueryRow(ctx, `INSERT INTO order_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at)
VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING
RETURNING id`, reservation.Operation, reservation.ActorScope, reservation.KeyDigest[:], reservation.PayloadDigest[:], reservation.CreatedAt.UTC()).Scan(&receipt.ID)
	if err == nil {
		receipt.Operation, receipt.ActorScope, receipt.KeyDigest, receipt.PayloadDigest, receipt.State = reservation.Operation, reservation.ActorScope, reservation.KeyDigest, reservation.PayloadDigest, "in_progress"
		return receipt, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return orderapp.Receipt{}, false, mapError(err)
	}
	var keyRaw, payloadRaw, result []byte
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,COALESCE(result_snapshot,'null'::jsonb) FROM order_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3 FOR UPDATE`, reservation.Operation, reservation.ActorScope, reservation.KeyDigest[:]).Scan(
		&receipt.ID, &receipt.Operation, &receipt.ActorScope, &keyRaw, &payloadRaw, &receipt.State, &result,
	)
	if err == nil {
		if len(keyRaw) != 32 || len(payloadRaw) != 32 {
			return orderapp.Receipt{}, false, orderport.ErrUnavailable
		}
		copy(receipt.KeyDigest[:], keyRaw)
		copy(receipt.PayloadDigest[:], payloadRaw)
		if string(result) != "null" {
			receipt.ResultSnapshot = append(json.RawMessage(nil), result...)
		}
	}
	return receipt, false, mapError(err)
}

func (r *Repository) Complete(ctx context.Context, id int64, snapshot json.RawMessage, completedAt time.Time) (orderapp.Receipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderapp.Receipt{}, err
	}
	if id < 1 || !json.Valid(snapshot) || completedAt.IsZero() {
		return orderapp.Receipt{}, ErrInvalid
	}
	var receipt orderapp.Receipt
	var keyRaw, payloadRaw, result []byte
	err = tx.QueryRow(ctx, `UPDATE order_operation_receipts SET state='completed',result_snapshot=$2,completed_at=$3 WHERE id=$1 AND state='in_progress' RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot`, id, snapshot, completedAt.UTC()).Scan(
		&receipt.ID, &receipt.Operation, &receipt.ActorScope, &keyRaw, &payloadRaw, &receipt.State, &result,
	)
	if err == nil {
		if len(keyRaw) != 32 || len(payloadRaw) != 32 || !json.Valid(result) {
			return orderapp.Receipt{}, orderport.ErrUnavailable
		}
		copy(receipt.KeyDigest[:], keyRaw)
		copy(receipt.PayloadDigest[:], payloadRaw)
		receipt.ResultSnapshot = append(json.RawMessage(nil), result...)
	}
	return receipt, mapError(err)
}

func (r *Repository) Insert(ctx context.Context, order domain.Order, actor int64, now time.Time) (domain.Order, error) {
	if actor < 1 {
		return domain.Order{}, ErrInvalid
	}
	return r.insert(ctx, order, "admin:"+strconv.FormatInt(actor, 10), now, nil, "order.created")
}

func (r *Repository) insert(ctx context.Context, order domain.Order, actorScope string, now time.Time, sourceDigest []byte, eventType string) (domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	snapshot := order.Snapshot()
	if snapshot.ID != 0 || actorScope == "" || now.IsZero() {
		return domain.Order{}, ErrInvalid
	}
	err = tx.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`, snapshot.Provider, snapshot.SourceSystem, snapshot.SourceKey, snapshot.MerchantOrderNo, snapshot.ProviderTransactionNo, snapshot.PayerCustomerID, snapshot.BeneficiaryCustomerID, snapshot.Amount.AmountMinor, snapshot.RefundedMinor, snapshot.Amount.Currency, snapshot.Status, snapshot.RecordOrigin, snapshot.EffectEligible, sourceDigest, snapshot.Version, snapshot.CreatedAt, snapshot.UpdatedAt).Scan(&snapshot.ID)
	if err != nil {
		return domain.Order{}, mapError(err)
	}
	for _, item := range snapshot.Items {
		if _, err = tx.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, snapshot.ID, item.LineNo, item.ProductID, item.ProductCode, item.ProductName, item.UnitAmountMinor, item.Quantity, item.LineAmountMinor); err != nil {
			return domain.Order{}, mapError(err)
		}
	}
	if err = r.appendFacts(ctx, tx, snapshot, nil, actorScope, eventType, now); err != nil {
		return domain.Order{}, err
	}
	return domain.Restore(snapshot)
}

func (r *Repository) Get(ctx context.Context, id int64, forUpdate bool) (domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	if id < 1 {
		return domain.Order{}, orderport.ErrNotFound
	}
	query := `SELECT ` + orderColumns + ` FROM orders WHERE id=$1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	snapshot, err := scanOrder(tx.QueryRow(ctx, query, id))
	if err != nil {
		return domain.Order{}, err
	}
	snapshot.Items, err = loadItems(ctx, tx, id)
	if err != nil {
		return domain.Order{}, err
	}
	order, err := domain.Restore(snapshot)
	if err != nil {
		return domain.Order{}, orderport.ErrUnavailable
	}
	return order, nil
}

func (r *Repository) List(ctx context.Context, before *orderapp.Cursor, limit int32, filter orderapp.ListFilter) ([]domain.Order, error) {
	if limit > orderapp.MaximumLimit+1 {
		return nil, ErrInvalid
	}
	return r.queryOrders(ctx, before, limit, filter)
}

func (r *Repository) Export(ctx context.Context, filter orderapp.ListFilter, limit int32) ([]domain.Order, error) {
	if limit < 1 || limit > 10001 {
		return nil, ErrInvalid
	}
	return r.queryOrders(ctx, nil, limit, filter)
}

func (r *Repository) queryOrders(ctx context.Context, before *orderapp.Cursor, limit int32, filter orderapp.ListFilter) ([]domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, ErrInvalid
	}
	query := `SELECT ` + orderColumns + ` FROM orders`
	args := []any{}
	conditions := []string{}
	add := func(template string, value any) {
		args = append(args, value)
		conditions = append(conditions, strings.ReplaceAll(template, "?", "$"+strconv.Itoa(len(args))))
	}
	if before != nil {
		if before.ID < 1 || before.CreatedAt.IsZero() {
			return nil, ErrInvalid
		}
		args = append(args, before.CreatedAt.UTC(), before.ID)
		conditions = append(conditions, `(created_at,id) < ($`+strconv.Itoa(len(args)-1)+`,$`+strconv.Itoa(len(args))+`)`)
	}
	if filter.Provider != "" {
		add(`provider=?`, filter.Provider)
	}
	if filter.Status != "" {
		add(`status=?`, filter.Status)
	}
	if filter.OrderRef != "" {
		args = append(args, filter.OrderRef)
		placeholder := "$" + strconv.Itoa(len(args))
		conditions = append(conditions, `(merchant_order_no=`+placeholder+` OR provider_transaction_no=`+placeholder+` OR source_key=`+placeholder+`)`)
	}
	if filter.CustomerID > 0 {
		args = append(args, filter.CustomerID)
		placeholder := "$" + strconv.Itoa(len(args))
		conditions = append(conditions, `(payer_customer_id=`+placeholder+` OR beneficiary_customer_id=`+placeholder+`)`)
	}
	if filter.Product != "" {
		args = append(args, "%"+filter.Product+"%")
		placeholder := "$" + strconv.Itoa(len(args))
		conditions = append(conditions, `EXISTS (SELECT 1 FROM order_items oi WHERE oi.order_id=orders.id AND (oi.product_code ILIKE `+placeholder+` OR oi.product_name ILIKE `+placeholder+`))`)
	}
	if filter.CreatedFrom != nil {
		add(`created_at>=?`, filter.CreatedFrom.UTC())
	}
	if filter.CreatedTo != nil {
		add(`created_at<?`, filter.CreatedTo.UTC())
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	if before == nil && filter.Offset > 0 {
		query += ` OFFSET $` + strconv.Itoa(len(args)+1)
		args = append(args, filter.Offset)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	snapshots := make([]domain.Snapshot, 0)
	for rows.Next() {
		snapshot, scanErr := scanOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		snapshots = append(snapshots, snapshot)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	orders := make([]domain.Order, 0, len(snapshots))
	for _, snapshot := range snapshots {
		snapshot.Items, err = loadItems(ctx, tx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		order, restoreErr := domain.Restore(snapshot)
		if restoreErr != nil {
			return nil, orderport.ErrUnavailable
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func (r *Repository) FindByReference(ctx context.Context, reference string) ([]domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if reference == "" || len(reference) > 200 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+orderColumns+` FROM orders WHERE merchant_order_no=$1 OR provider_transaction_no=$1 OR source_key=$1 ORDER BY id LIMIT 2`, reference)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := []domain.Order{}
	for rows.Next() {
		snapshot, scanErr := scanOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		snapshot.Items, scanErr = loadItems(ctx, tx, snapshot.ID)
		if scanErr != nil {
			return nil, scanErr
		}
		order, restoreErr := domain.Restore(snapshot)
		if restoreErr != nil {
			return nil, orderport.ErrUnavailable
		}
		result = append(result, order)
	}
	return result, mapError(rows.Err())
}

func (r *Repository) RecordExport(ctx context.Context, receipt orderapp.ExportReceipt) (orderapp.ExportReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderapp.ExportReceipt{}, false, err
	}
	if receipt.Actor < 1 || receipt.RowCount < 0 || receipt.RowCount > 10000 || receipt.ByteCount < 0 || receipt.ByteCount > 5<<20 || receipt.KeyDigest == ([32]byte{}) || receipt.FilterDigest == ([32]byte{}) || receipt.ContentDigest == ([32]byte{}) || receipt.CreatedAt.IsZero() {
		return orderapp.ExportReceipt{}, false, ErrInvalid
	}
	err = tx.QueryRow(ctx, `INSERT INTO order_export_receipts(actor_admin_user_id,key_digest,filter_digest,row_count,byte_count,content_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(actor_admin_user_id,key_digest) DO NOTHING RETURNING id`, receipt.Actor, receipt.KeyDigest[:], receipt.FilterDigest[:], receipt.RowCount, receipt.ByteCount, receipt.ContentDigest[:], receipt.CreatedAt.UTC()).Scan(&receipt.ID)
	if err == nil {
		return receipt, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return orderapp.ExportReceipt{}, false, mapError(err)
	}
	var key, filter, content []byte
	stored := orderapp.ExportReceipt{}
	err = tx.QueryRow(ctx, `SELECT id,actor_admin_user_id,key_digest,filter_digest,row_count,byte_count,content_digest,created_at FROM order_export_receipts WHERE actor_admin_user_id=$1 AND key_digest=$2 FOR UPDATE`, receipt.Actor, receipt.KeyDigest[:]).Scan(&stored.ID, &stored.Actor, &key, &filter, &stored.RowCount, &stored.ByteCount, &content, &stored.CreatedAt)
	if err != nil || len(key) != 32 || len(filter) != 32 || len(content) != 32 {
		return orderapp.ExportReceipt{}, false, mapError(err)
	}
	copy(stored.KeyDigest[:], key)
	copy(stored.FilterDigest[:], filter)
	copy(stored.ContentDigest[:], content)
	return stored, false, nil
}

func (r *Repository) UpdateSettlement(ctx context.Context, order domain.Order, event domain.StatusEvent, actorScope string) (domain.Order, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, err
	}
	snapshot := order.Snapshot()
	if snapshot.ID < 1 || snapshot.Version < 2 || actorScope == "" || event.Version != snapshot.Version {
		return domain.Order{}, ErrInvalid
	}
	command, err := tx.Exec(ctx, `UPDATE orders SET status=$2,refunded_minor=$3,version=$4,updated_at=$5 WHERE id=$1 AND version=$6`, snapshot.ID, snapshot.Status, snapshot.RefundedMinor, snapshot.Version, snapshot.UpdatedAt, snapshot.Version-1)
	if err != nil {
		return domain.Order{}, mapError(err)
	}
	if command.RowsAffected() != 1 {
		return domain.Order{}, orderport.ErrConflict
	}
	if err = r.appendFacts(ctx, tx, snapshot, &event.From, actorScope, "order.status_changed", event.OccurredAt); err != nil {
		return domain.Order{}, err
	}
	return domain.Restore(snapshot)
}

func (r *Repository) Import(ctx context.Context, runKey string, digest [32]byte, order domain.Order) (domain.Order, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Order{}, false, err
	}
	if runKey == "" || digest == ([32]byte{}) || order.RecordOrigin != domain.RecordOriginHistory || order.EffectEligible {
		return domain.Order{}, false, ErrInvalid
	}
	var runID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM order_import_runs WHERE run_key=$1 AND status='applying' FOR UPDATE`, runKey).Scan(&runID); err != nil {
		return domain.Order{}, false, mapError(err)
	}
	var receiptDigest []byte
	var receiptOrderID int64
	err = tx.QueryRow(ctx, `SELECT source_row_digest,order_id FROM order_import_receipts WHERE run_id=$1 AND source_system=$2 AND source_key=$3`, runID, order.SourceSystem, order.SourceKey).Scan(&receiptDigest, &receiptOrderID)
	if err == nil {
		if string(receiptDigest) != string(digest[:]) {
			return domain.Order{}, false, orderport.ErrConflict
		}
		persisted, getErr := r.Get(ctx, receiptOrderID, false)
		return persisted, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, false, mapError(err)
	}
	var existingID int64
	var existingDigest []byte
	err = tx.QueryRow(ctx, `SELECT id,source_row_digest FROM orders WHERE source_system=$1 AND source_key=$2`, order.SourceSystem, order.SourceKey).Scan(&existingID, &existingDigest)
	if err == nil {
		if string(existingDigest) != string(digest[:]) {
			return domain.Order{}, false, orderport.ErrConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO order_import_receipts(run_id,source_system,source_key,source_row_digest,outcome,order_id) VALUES($1,$2,$3,$4,'replayed',$5)`, runID, order.SourceSystem, order.SourceKey, digest[:], existingID); err != nil {
			return domain.Order{}, false, mapError(err)
		}
		persisted, getErr := r.Get(ctx, existingID, false)
		return persisted, false, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, false, mapError(err)
	}
	persisted, err := r.insert(ctx, order, "migration:"+runKey, order.CreatedAt, digest[:], "order.history_imported")
	if err != nil {
		return domain.Order{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_import_receipts(run_id,source_system,source_key,source_row_digest,outcome,order_id) VALUES($1,$2,$3,$4,'imported',$5)`, runID, order.SourceSystem, order.SourceKey, digest[:], persisted.ID); err != nil {
		return domain.Order{}, false, mapError(err)
	}
	return persisted, true, nil
}

func (r *Repository) appendFacts(ctx context.Context, tx pgx.Tx, snapshot domain.Snapshot, from *domain.Status, actorScope, eventType string, occurredAt time.Time) error {
	var fromValue any
	if from != nil {
		fromValue = string(*from)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_status_history(order_id,from_status,to_status,refunded_minor,order_version,actor_scope,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, snapshot.ID, fromValue, snapshot.Status, snapshot.RefundedMinor, snapshot.Version, actorScope, occurredAt.UTC()); err != nil {
		return mapError(err)
	}
	payload, _ := json.Marshal(map[string]any{"order_id": snapshot.ID, "status": snapshot.Status, "version": snapshot.Version, "record_origin": snapshot.RecordOrigin})
	if _, err := tx.Exec(ctx, `INSERT INTO order_audit_events(event_type,order_id,actor_scope,payload,occurred_at) VALUES($1,$2,$3,$4,$5)`, eventType, snapshot.ID, actorScope, payload, occurredAt.UTC()); err != nil {
		return mapError(err)
	}
	idempotencyKey := eventType + ":" + strconv.FormatInt(snapshot.ID, 10) + ":" + strconv.FormatInt(snapshot.Version, 10)
	_, err := tx.Exec(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES($1,$2,$3,$4,$5)`, eventType, idempotencyKey, snapshot.ID, payload, occurredAt.UTC())
	return mapError(err)
}

func loadItems(ctx context.Context, tx pgx.Tx, orderID int64) ([]domain.ItemSnapshot, error) {
	rows, err := tx.Query(ctx, `SELECT line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor FROM order_items WHERE order_id=$1 ORDER BY line_no`, orderID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	items := make([]domain.ItemSnapshot, 0)
	for rows.Next() {
		var item domain.ItemSnapshot
		if err = rows.Scan(&item.LineNo, &item.ProductID, &item.ProductCode, &item.ProductName, &item.UnitAmountMinor, &item.Quantity, &item.LineAmountMinor); err != nil {
			return nil, mapError(err)
		}
		items = append(items, item)
	}
	return items, mapError(rows.Err())
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return orderport.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23514" || pgErr.Code == "23503") {
		return orderport.ErrConflict
	}
	return err
}
