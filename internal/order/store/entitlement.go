package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type servicePeriodMemberCursor struct {
	EndAt      string `json:"end_at"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	StartAt    string `json:"start_at,omitempty"`
	SnapshotAt string `json:"snapshot_at,omitempty"`
	Sort       string `json:"sort,omitempty"`
	ID         int64  `json:"id"`
}

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

func (r *Repository) ListServicePeriodMembers(ctx context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.ServicePeriodMemberPage{}, err
	}
	snapshot := query.SnapshotAt.UTC()
	var cursorEnd, cursorUpdated, cursorStart *time.Time
	var cursorID int64
	if query.Cursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(query.Cursor)
		var cursor servicePeriodMemberCursor
		if decodeErr != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.ID < 1 {
			return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
		}
		if cursor.Sort != "" && cursor.Sort != query.Sort {
			return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
		}
		if cursor.SnapshotAt != "" {
			parsed, parseErr := time.Parse(time.RFC3339Nano, cursor.SnapshotAt)
			if parseErr != nil {
				return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
			}
			snapshot = parsed.UTC()
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, cursor.EndAt)
		if parseErr != nil {
			return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
		}
		parsed = parsed.UTC()
		cursorEnd, cursorID = &parsed, cursor.ID
		if cursor.UpdatedAt != "" {
			parsed, e := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
			if e != nil {
				return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
			}
			parsed = parsed.UTC()
			cursorUpdated = &parsed
		}
		if cursor.StartAt != "" {
			parsed, e := time.Parse(time.RFC3339Nano, cursor.StartAt)
			if e != nil {
				return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
			}
			parsed = parsed.UTC()
			cursorStart = &parsed
		}
	}
	state := strings.TrimSpace(query.State)
	if state == "all" {
		state = ""
	}
	if state == "removed" {
		state = "refunded"
	}
	source := strings.TrimSpace(query.Source)
	if snapshot.IsZero() {
		snapshot = time.Now().UTC()
	}
	filterArgs := make([]any, 0, 3)
	nextPlaceholder := 10
	remainingClause, remainingArgs := servicePeriodRemainingDaysClause(query.RemainingDays, 9, nextPlaceholder)
	nextPlaceholder += len(remainingArgs)
	filterArgs = append(filterArgs, remainingArgs...)
	remarkClause, remarkArgs := servicePeriodRemarkClause(query.Remark, nextPlaceholder)
	filterArgs = append(filterArgs, remarkArgs...)
	joiner := " AND "
	if query.FilterLogic == "or" && query.RemainingDays != nil && query.Remark != nil {
		joiner = " OR "
	}
	memberFilter := "TRUE"
	if query.RemainingDays != nil && query.Remark != nil {
		memberFilter = "(" + remainingClause + ")" + joiner + "(" + remarkClause + ")"
	} else if query.RemainingDays != nil {
		memberFilter = remainingClause
	} else if query.Remark != nil {
		memberFilter = remarkClause
	}
	args := []any{query.ServiceProductID, state, source, cursorUpdated, cursorStart, query.Sort, cursorID, cursorEnd, snapshot}
	args = append(args, filterArgs...)
	args = append(args, query.Limit+1)
	groupCount := "0::bigint"
	if query.GroupByRemainingDays {
		groupCount = "COUNT(*) OVER (PARTITION BY " + servicePeriodRemainingDaysExpression(9) + ")"
	}
	rows, err := tx.Query(ctx, `SELECT id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at,source_system,`+groupCount+`
		FROM order_service_entitlements
		WHERE service_product_id=$1
		  AND ($2='' OR status=$2)
		  AND ($3='' OR ($3='paid_order' AND source_system='native-payment') OR ($3='manual' AND source_system<>'native-payment'))
		  AND (`+memberFilter+`)
		  AND (($6='updated_at_desc' AND ($4::timestamptz IS NULL OR (updated_at,id)<($4::timestamptz,$7::bigint)))
		    OR ($6='starts_at_desc' AND ($5::timestamptz IS NULL OR (start_at,id)<($5::timestamptz,$7::bigint)))
		    OR ($6='remaining_days_desc' AND ($8::timestamptz IS NULL OR (end_at,id)<($8::timestamptz,$7::bigint)))
		    OR ($6='remaining_days_asc' AND ($8::timestamptz IS NULL OR (end_at,id)>($8::timestamptz,$7::bigint)))
		    OR ($6='' AND ($8::timestamptz IS NULL OR (end_at,id)<($8::timestamptz,$7::bigint))))
		ORDER BY CASE WHEN $6='updated_at_desc' THEN updated_at WHEN $6='starts_at_desc' THEN start_at ELSE end_at END `+servicePeriodSortDirection(query.Sort)+`,id `+servicePeriodSortDirection(query.Sort)+` LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return orderport.ServicePeriodMemberPage{}, err
	}
	defer rows.Close()
	page := orderport.ServicePeriodMemberPage{Items: []orderport.Entitlement{}, SnapshotAt: snapshot}
	for rows.Next() {
		var item orderport.Entitlement
		if err = rows.Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt, &item.SourceSystem, &item.MemberGridGroupCount); err != nil {
			return orderport.ServicePeriodMemberPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return orderport.ServicePeriodMemberPage{}, err
	}
	if len(page.Items) <= int(query.Limit) {
		return page, nil
	}
	last := page.Items[query.Limit-1]
	encoded, marshalErr := json.Marshal(servicePeriodMemberCursor{EndAt: last.EndAt.UTC().Format(time.RFC3339Nano), UpdatedAt: last.UpdatedAt.UTC().Format(time.RFC3339Nano), StartAt: last.StartAt.UTC().Format(time.RFC3339Nano), SnapshotAt: snapshot.Format(time.RFC3339Nano), Sort: query.Sort, ID: last.ID})
	if marshalErr != nil {
		return orderport.ServicePeriodMemberPage{}, marshalErr
	}
	page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	page.Items = page.Items[:query.Limit]
	return page, nil
}

func servicePeriodSortDirection(sort string) string {
	if sort == "remaining_days_asc" {
		return "ASC"
	}
	return "DESC"
}

func servicePeriodRemainingDaysClause(filter *orderport.MemberGridNumberFilter, snapshotPlaceholder, start int) (string, []any) {
	if filter == nil {
		return "TRUE", nil
	}
	// dd8 defines a positive partial day as one day and clamps expired
	// entitlements to zero. Use the request's frozen snapshot rather than a
	// moving database clock so filtering and V3 row rendering agree.
	expression := servicePeriodRemainingDaysExpression(snapshotPlaceholder)
	if filter.Operator == "between" {
		return expression + " BETWEEN $" + strconv.Itoa(start) + " AND $" + strconv.Itoa(start+1), []any{filter.Values[0], filter.Values[1]}
	}
	op := map[string]string{"equals": "=", "not_equals": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[filter.Operator]
	return expression + " " + op + " $" + strconv.Itoa(start), []any{filter.Values[0]}
}

func servicePeriodRemainingDaysExpression(snapshotPlaceholder int) string {
	return "GREATEST(0, CEIL(EXTRACT(EPOCH FROM (end_at - $" + strconv.Itoa(snapshotPlaceholder) + "::timestamptz)) / 86400))::bigint"
}

func servicePeriodRemarkClause(filter *orderport.MemberGridTextFilter, start int) (string, []any) {
	if filter == nil {
		return "TRUE", nil
	}
	expression := "NULLIF(BTRIM(COALESCE(remark, '')), '')"
	switch filter.Operator {
	case "is_empty":
		return expression + " IS NULL", nil
	case "is_not_empty":
		return expression + " IS NOT NULL", nil
	case "contains", "not_contains":
		clause := "LOWER(COALESCE(remark, '')) LIKE $" + strconv.Itoa(start) + " ESCAPE E'\\\\'"
		if filter.Operator == "not_contains" {
			clause = "NOT (" + clause + ")"
		}
		return clause, []any{"%" + escapeServicePeriodLike(strings.ToLower(filter.Value)) + "%"}
	case "equals", "not_equals":
		clause := "LOWER(COALESCE(remark, '')) = $" + strconv.Itoa(start)
		if filter.Operator == "not_equals" {
			clause = "NOT (" + clause + ")"
		}
		return clause, []any{strings.ToLower(filter.Value)}
	default:
		return "FALSE", nil
	}
}

func escapeServicePeriodLike(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
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

func (r *Repository) GetCustomerServicePeriodEntitlement(ctx context.Context, customerID, serviceProductID int64) (orderport.Entitlement, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, false, err
	}
	var item orderport.Entitlement
	err = tx.QueryRow(ctx, `SELECT id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at
		FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=$2
		ORDER BY end_at DESC,id DESC LIMIT 1`, customerID, serviceProductID).
		Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return orderport.Entitlement{}, false, nil
	}
	if err != nil {
		return orderport.Entitlement{}, false, err
	}
	return item, true, nil
}

func (r *Repository) UpdateEntitlementRemark(ctx context.Context, command orderport.RemarkCommand, key, payload [32]byte, at time.Time) (orderport.Entitlement, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	var item orderport.Entitlement
	err = tx.QueryRow(ctx, `UPDATE order_service_entitlements SET remark=$5,version=version+1,updated_at=$6 WHERE id=$1 AND ($2=0 OR customer_id=$2) AND service_product_id=$3 AND version=$4 AND status IN ('active','expired') RETURNING id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at`, command.EntitlementID, command.CustomerID, command.ServiceProductID, command.ExpectedVersion, command.Remark, at).Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
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
	_, err = tx.Exec(ctx, `INSERT INTO order_entitlement_outbox(event_type,entitlement_id,payload,idempotency_digest,occurred_at) VALUES('order.entitlement.remark_updated.v1',$1,jsonb_build_object('entitlement_id',$1::bigint,'version',$2::bigint),$3,$4)`, item.ID, item.Version, key[:], at)
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

func (r *Repository) GrantPaidServicePeriod(ctx context.Context, command orderport.ServicePeriodGrantCommand, payload [32]byte) (orderport.Entitlement, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	if err = lockServicePeriodEntitlement(ctx, tx, command.BeneficiaryCustomerID, command.ServiceProductID); err != nil {
		return orderport.Entitlement{}, err
	}
	if prior, found, err := entitlementFulfillmentReceipt(ctx, tx, "grant", command.SourceOrderID, payload); err != nil || found {
		return prior, err
	}
	item, found, err := latestServicePeriodEntitlement(ctx, tx, command.BeneficiaryCustomerID, command.ServiceProductID)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	operation := "grant"
	var priorActiveEnd *time.Time
	var priorHistoricalEnd *time.Time
	if !found {
		sourceKey := fmt.Sprintf("native-service-period:%d:%d", command.BeneficiaryCustomerID, command.ServiceProductID)
		sourceDigest := sha256.Sum256([]byte(sourceKey))
		end := command.PaidAt.AddDate(0, 0, int(command.DurationDays))
		err = tx.QueryRow(ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,source_digest,created_at,updated_at)
			VALUES('native-payment',$1,$2,$3,$4,$5,'active',$6,$7,'',$8,$9,$9)
			RETURNING id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at`, sourceKey, command.BeneficiaryCustomerID, command.ServiceProductID, command.ProductName, command.SourceOrderID, command.PaidAt, end, sourceDigest[:], command.ProcessedAt).
			Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	} else {
		start, end := command.PaidAt, command.PaidAt.AddDate(0, 0, int(command.DurationDays))
		if item.Status == "active" && item.EndAt.After(command.ProcessedAt) {
			frozen := item.EndAt
			priorActiveEnd = &frozen
			priorHistoricalEnd, err = independentHistoricalBaselineEnd(ctx, tx, item.ID, item.EndAt, command.ProcessedAt)
			if err != nil {
				return orderport.Entitlement{}, err
			}
			operation, start, end = "renew", item.StartAt, item.EndAt.AddDate(0, 0, int(command.DurationDays))
		}
		err = tx.QueryRow(ctx, `UPDATE order_service_entitlements SET product_name=$2,last_order_id=$3,status='active',start_at=$4,end_at=$5,version=version+1,updated_at=$6 WHERE id=$1
			RETURNING id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at`, item.ID, command.ProductName, command.SourceOrderID, start, end, command.ProcessedAt).
			Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	}
	if err != nil {
		return orderport.Entitlement{}, err
	}
	if err = recordEntitlementFulfillment(ctx, tx, "grant", command.SourceOrderID, payload, item, command.DurationDays, priorActiveEnd, priorHistoricalEnd, 0, operation, command.ProcessedAt); err != nil {
		return orderport.Entitlement{}, err
	}
	return item, nil
}

func (r *Repository) ApplyServicePeriodRefund(ctx context.Context, command orderport.ServicePeriodRefundCommand, payload [32]byte) (orderport.Entitlement, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	if err = lockServicePeriodRefund(ctx, tx, command.SourceOrderID); err != nil {
		return orderport.Entitlement{}, err
	}
	if prior, found, err := entitlementFulfillmentReceipt(ctx, tx, "refund", command.SourceOrderID, payload); err != nil || found {
		return prior, err
	}
	var entitlementID int64
	var duration int32
	err = tx.QueryRow(ctx, `SELECT entitlement_id,duration_days FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$1 FOR UPDATE`, command.SourceOrderID).Scan(&entitlementID, &duration)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT entitlement_id,issued_duration_days FROM order_entitlement_historical_sources WHERE source_order_id=$1 FOR UPDATE`, command.SourceOrderID).Scan(&entitlementID, &duration)
		if errors.Is(err, pgx.ErrNoRows) {
			return orderport.Entitlement{}, orderport.ErrNotFound
		}
	}
	if err != nil {
		return orderport.Entitlement{}, err
	}
	item, err := servicePeriodEntitlementByID(ctx, tx, entitlementID)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	var otherUnrefunded int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT source_order_id FROM order_entitlement_fulfillment_receipts
		WHERE operation='grant' AND entitlement_id=$1
		UNION
		SELECT source_order_id FROM order_entitlement_historical_sources
		WHERE entitlement_id=$1
	) sources
	WHERE source_order_id<>$2
	AND NOT EXISTS (SELECT 1 FROM order_entitlement_fulfillment_receipts refund_receipt WHERE refund_receipt.operation='refund' AND refund_receipt.source_order_id=sources.source_order_id)`, item.ID, command.SourceOrderID).Scan(&otherUnrefunded)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	baselineEnd, err := independentHistoricalBaselineEnd(ctx, tx, item.ID, item.EndAt, command.ProcessedAt)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	mappedEnd, err := activeMappedHistoricalEnd(ctx, tx, item.ID, command.SourceOrderID)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	status, end := "refunded", command.ProcessedAt
	if otherUnrefunded > 0 {
		end = item.EndAt.AddDate(0, 0, -int(duration))
		if baselineEnd != nil && baselineEnd.After(end) {
			end = *baselineEnd
		}
		if mappedEnd != nil && mappedEnd.After(end) {
			end = *mappedEnd
		}
		if end.Before(command.ProcessedAt) {
			end = command.ProcessedAt
		}
		if end.After(command.ProcessedAt) {
			status = "active"
		} else {
			status = "expired"
		}
	} else if baselineEnd != nil && baselineEnd.After(command.ProcessedAt) {
		end, status = *baselineEnd, "active"
	} else if mappedEnd != nil && mappedEnd.After(command.ProcessedAt) {
		end, status = *mappedEnd, "active"
	}
	err = tx.QueryRow(ctx, `UPDATE order_service_entitlements SET status=$2,end_at=$3,version=version+1,updated_at=$4 WHERE id=$1
		RETURNING id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at`, item.ID, status, end, command.ProcessedAt).
		// Processing time is the update time; entitlement end may lie in the
		// future when another unrefunded order remains.
		Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	if err != nil {
		return orderport.Entitlement{}, err
	}
	if err = recordEntitlementFulfillment(ctx, tx, "refund", command.SourceOrderID, payload, item, duration, nil, nil, command.RefundAmountMinor, "refund", command.ProcessedAt); err != nil {
		return orderport.Entitlement{}, err
	}
	return item, nil
}

func (r *Repository) RecordHistoricalServicePeriodSource(ctx context.Context, command orderport.HistoricalServicePeriodSourceCommand) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var beneficiary, customerID *int64
	var serviceProductID int64
	if err = tx.QueryRow(ctx, `SELECT beneficiary_customer_id FROM orders WHERE id=$1 AND status='paid' FOR KEY SHARE`, command.SourceOrderID).Scan(&beneficiary); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return orderport.ErrNotFound
		}
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT customer_id,service_product_id FROM order_service_entitlements WHERE id=$1 FOR KEY SHARE`, command.EntitlementID).Scan(&customerID, &serviceProductID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return orderport.ErrNotFound
		}
		return err
	}
	if beneficiary == nil || customerID == nil || *beneficiary != *customerID || serviceProductID != command.ServiceProductID {
		return orderport.ErrConflict
	}
	var matched int64
	if err = tx.QueryRow(ctx, `SELECT 1 FROM order_items WHERE order_id=$1 AND line_no=$2 AND product_id=$3 AND product_code=$4 FOR KEY SHARE`, command.SourceOrderID, command.SourceLineNo, command.ServiceProductID, command.ServiceProductCode).Scan(&matched); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return orderport.ErrConflict
		}
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_entitlement_historical_sources(source_order_id,entitlement_id,source_line_no,product_id,product_code,issued_duration_days,source_start_at,source_end_at,imported_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (source_order_id) DO NOTHING`, command.SourceOrderID, command.EntitlementID, command.SourceLineNo, command.ServiceProductID, command.ServiceProductCode, command.DurationDays, command.StartAt, command.EndAt, command.ImportedAt); err != nil {
		return err
	}
	var existing orderport.HistoricalServicePeriodSourceCommand
	err = tx.QueryRow(ctx, `SELECT source_order_id,entitlement_id,source_line_no,product_id,product_code,issued_duration_days,source_start_at,source_end_at,imported_at FROM order_entitlement_historical_sources WHERE source_order_id=$1`, command.SourceOrderID).
		Scan(&existing.SourceOrderID, &existing.EntitlementID, &existing.SourceLineNo, &existing.ServiceProductID, &existing.ServiceProductCode, &existing.DurationDays, &existing.StartAt, &existing.EndAt, &existing.ImportedAt)
	if err != nil {
		return err
	}
	if existing.EntitlementID != command.EntitlementID || existing.SourceLineNo != command.SourceLineNo || existing.ServiceProductID != command.ServiceProductID || existing.ServiceProductCode != command.ServiceProductCode || existing.DurationDays != command.DurationDays || !existing.StartAt.Equal(command.StartAt) || !existing.EndAt.Equal(command.EndAt) {
		return orderport.ErrConflict
	}
	return nil
}

func entitlementFulfillmentReceipt(ctx context.Context, tx pgx.Tx, operation string, sourceOrderID int64, payload [32]byte) (orderport.Entitlement, bool, error) {
	var item orderport.Entitlement
	var prior, raw []byte
	err := tx.QueryRow(ctx, `SELECT payload_digest,result_snapshot FROM order_entitlement_fulfillment_receipts WHERE operation=$1 AND source_order_id=$2 FOR UPDATE`, operation, sourceOrderID).Scan(&prior, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, err
	}
	if len(prior) != 32 || json.Unmarshal(raw, &item) != nil {
		return item, false, orderport.ErrConflict
	}
	// A refund receipt denotes that this source order has already revoked its
	// complete period. Later partial/full refunds have different monetary facts
	// but are valid no-ops by the frozen legacy rule.
	if operation != "refund" && string(prior) != string(payload[:]) {
		return item, false, orderport.ErrConflict
	}
	return item, true, nil
}

func latestServicePeriodEntitlement(ctx context.Context, tx pgx.Tx, customerID, productID int64) (orderport.Entitlement, bool, error) {
	var item orderport.Entitlement
	err := tx.QueryRow(ctx, `SELECT id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at
		FROM order_service_entitlements WHERE customer_id=$1 AND service_product_id=$2 ORDER BY end_at DESC,id DESC LIMIT 1 FOR UPDATE`, customerID, productID).
		Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, false, nil
	}
	return item, err == nil, err
}

func servicePeriodEntitlementByID(ctx context.Context, tx pgx.Tx, id int64) (orderport.Entitlement, error) {
	var item orderport.Entitlement
	err := tx.QueryRow(ctx, `SELECT id,customer_id,service_product_id,product_name,last_order_id,status,start_at,end_at,remark,version,updated_at FROM order_service_entitlements WHERE id=$1 FOR UPDATE`, id).
		Scan(&item.ID, &item.CustomerID, &item.ServiceProductID, &item.ProductName, &item.LastOrderID, &item.Status, &item.StartAt, &item.EndAt, &item.Remark, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, orderport.ErrNotFound
	}
	return item, err
}

// independentHistoricalBaselineEnd returns only a baseline that has no
// source-order mapping. A mapped historical order remains dynamically
// revocable; copying it into every later grant receipt would let it survive
// its own refund. Once any native grant exists, a nil baseline stays nil: the
// aggregate's current end may already contain native days and cannot be
// reclassified as history on a later renewal.
func independentHistoricalBaselineEnd(ctx context.Context, tx pgx.Tx, entitlementID int64, currentEnd, processedAt time.Time) (*time.Time, error) {
	var sourceSystem string
	if err := tx.QueryRow(ctx, `SELECT source_system FROM order_service_entitlements WHERE id=$1 FOR KEY SHARE`, entitlementID).Scan(&sourceSystem); err != nil {
		return nil, err
	}
	if sourceSystem == "native-payment" {
		return nil, nil
	}
	var mapped bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM order_entitlement_historical_sources WHERE entitlement_id=$1)`, entitlementID).Scan(&mapped); err != nil {
		return nil, err
	}
	if mapped {
		return nil, nil
	}
	var grants int64
	var prior *time.Time
	if err := tx.QueryRow(ctx, `SELECT count(*),max(prior_historical_end_at) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND entitlement_id=$1`, entitlementID).Scan(&grants, &prior); err != nil {
		return nil, err
	}
	if grants > 0 {
		if prior == nil || !prior.After(processedAt) {
			return nil, nil
		}
		return prior, nil
	}
	if !currentEnd.After(processedAt) {
		return nil, nil
	}
	frozen := currentEnd
	return &frozen, nil
}

// activeMappedHistoricalEnd computes the coverage left after the current
// source order has been revoked. It deliberately excludes that source before
// the refund receipt is written, so a single mapped historical source does
// not keep itself alive through the in-flight refund transaction.
func activeMappedHistoricalEnd(ctx context.Context, tx pgx.Tx, entitlementID, excludingSourceOrderID int64) (*time.Time, error) {
	var end *time.Time
	err := tx.QueryRow(ctx, `SELECT max(source_end_at)
		FROM order_entitlement_historical_sources source
		WHERE source.entitlement_id=$1 AND source.source_order_id<>$2
		  AND NOT EXISTS (SELECT 1 FROM order_entitlement_fulfillment_receipts refund_receipt WHERE refund_receipt.operation='refund' AND refund_receipt.source_order_id=source.source_order_id)`, entitlementID, excludingSourceOrderID).Scan(&end)
	if err != nil {
		return nil, err
	}
	return end, nil
}

func recordEntitlementFulfillment(ctx context.Context, tx pgx.Tx, receiptOperation string, sourceOrderID int64, payload [32]byte, item orderport.Entitlement, duration int32, priorActiveEnd, priorHistoricalEnd *time.Time, refundAmountMinor int64, eventOperation string, at time.Time) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_entitlement_fulfillment_receipts(operation,source_order_id,payload_digest,entitlement_id,result_snapshot,duration_days,prior_active_end_at,prior_historical_end_at,refund_amount_minor,created_at) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10)`, receiptOperation, sourceOrderID, payload[:], item.ID, raw, duration, priorActiveEnd, priorHistoricalEnd, refundAmountMinor, at); err != nil {
		return err
	}
	actor := sha256.Sum256([]byte("payment:service-period"))
	if _, err = tx.Exec(ctx, `INSERT INTO order_entitlement_audit_events(entitlement_id,operation,actor_digest,payload_digest,occurred_at) VALUES($1,$2,$3,$4,$5)`, item.ID, eventOperation, actor[:], payload[:], at); err != nil {
		return err
	}
	eventType := map[string]string{"grant": "order.entitlement.granted.v1", "renew": "order.entitlement.renewed.v1", "refund": "order.entitlement.refunded.v1"}[eventOperation]
	idempotency := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", receiptOperation, sourceOrderID)))
	_, err = tx.Exec(ctx, `INSERT INTO order_entitlement_outbox(event_type,entitlement_id,payload,idempotency_digest,occurred_at) VALUES($1,$2,jsonb_build_object('entitlement_id',$2::bigint,'source_order_id',$3::bigint),$4,$5)`, eventType, item.ID, sourceOrderID, idempotency[:], at)
	return err
}

func lockServicePeriodEntitlement(ctx context.Context, tx pgx.Tx, customerID, productID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("order:service-period:%d:%d", customerID, productID))
	return err
}

func lockServicePeriodRefund(ctx context.Context, tx pgx.Tx, sourceOrderID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("order:service-period-refund:%d", sourceOrderID))
	return err
}

var _ orderapp.EntitlementFulfillmentStore = (*Repository)(nil)
