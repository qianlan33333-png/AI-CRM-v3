package store

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

// ReadOrderDistribution returns Distribution-owned, immutable order snapshots
// in bounded batches. It intentionally receives Order IDs rather than calling
// back into Order or reusing the attribution-detail reader.
func (r *Repository) ReadOrderDistribution(ctx context.Context, orderIDs []int64) (map[int64][]distributionport.OrderDistributionLine, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	ids := distinctOrderIDs(orderIDs)
	if len(ids) > 200 {
		return nil, distributiondomain.ErrInvalid
	}
	if len(ids) == 0 {
		return map[int64][]distributionport.OrderDistributionLine{}, nil
	}
	values := make(map[int64][]distributionport.OrderDistributionLine)
	byCommission := make(map[int64]struct{})
	rows, err := tx.Query(ctx, `SELECT
		a.order_id,a.id,a.order_item_line,a.product_name,d.customer_id,
		a.commission_rate_basis_points,a.wait_days,a.policy_version,
		COALESCE(c.id,0),COALESCE(c.initial_minor,0),COALESCE(c.current_payable_minor,0),COALESCE(c.paid_minor,0),
		COALESCE(c.status,''),COALESCE(c.hold_reason,''),COALESCE(c.cancel_reason,''),COALESCE(c.exception_reason,''),
		c.due_at,
		(SELECT MAX(ae.occurred_at) FROM distribution_audit_events ae
		 WHERE ae.aggregate_type='commission' AND ae.aggregate_id=c.id
		   AND ae.event_type='distribution.settlement_paid.v1'),
		'CNY'
		FROM distribution_order_attributions a
		JOIN distribution_distributors d ON d.id=a.distributor_id
		LEFT JOIN distribution_commissions c ON c.attribution_id=a.id
		WHERE a.order_id=ANY($1::bigint[])
		ORDER BY a.order_id,a.order_item_line,a.id`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item distributionport.OrderDistributionLine
		if err := rows.Scan(&item.OrderID, &item.AttributionID, &item.ItemLine, &item.ProductName, &item.DistributorCustomerID,
			&item.RateBasisPoints, &item.WaitDays, &item.PolicyVersion,
			&item.CommissionID, &item.InitialMinor, &item.CurrentPayableMinor, &item.PaidMinor,
			&item.Status, &item.HoldReason, &item.CancelReason, &item.ExceptionReason,
			&item.DueAt, &item.SettlementConfirmedAt, &item.Currency); err != nil {
			return nil, mapError(err)
		}
		item.HasCommission = item.CommissionID > 0
		values[item.OrderID] = append(values[item.OrderID], item)
		if item.CommissionID > 0 {
			byCommission[item.CommissionID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	commissionIDs := mapKeys(byCommission)
	if len(commissionIDs) == 0 {
		return values, nil
	}
	if err := r.attachOrderAdjustments(ctx, tx, values, commissionIDs); err != nil {
		return nil, err
	}
	if err := r.attachOrderSettlements(ctx, tx, values, commissionIDs); err != nil {
		return nil, err
	}
	if err := r.attachOrderExceptions(ctx, tx, values, commissionIDs); err != nil {
		return nil, err
	}
	return values, nil
}

func (r *Repository) attachOrderAdjustments(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, values map[int64][]distributionport.OrderDistributionLine, commissionIDs []int64) error {
	rows, err := tx.Query(ctx, `SELECT commission_id,kind,delta_minor,resulting_payable_minor,reason,occurred_at FROM distribution_commission_adjustments WHERE commission_id=ANY($1::bigint[]) ORDER BY commission_id,id`, commissionIDs)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var commissionID int64
		var item distributionport.OrderDistributionAdjustment
		if err := rows.Scan(&commissionID, &item.Kind, &item.DeltaMinor, &item.ResultingPayableMinor, &item.Reason, &item.OccurredAt); err != nil {
			return mapError(err)
		}
		appendOrderLine(values, commissionID, func(line *distributionport.OrderDistributionLine) { line.Adjustments = append(line.Adjustments, item) })
	}
	return mapError(rows.Err())
}

func (r *Repository) attachOrderSettlements(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, values map[int64][]distributionport.OrderDistributionLine, commissionIDs []int64) error {
	rows, err := tx.Query(ctx, `SELECT s.commission_id,s.settlement_reference,s.amount_minor,s.currency,s.state,s.provider_deadline_at,
		(SELECT MAX(ae.occurred_at) FROM distribution_audit_events ae
		 WHERE ae.aggregate_type='commission' AND ae.aggregate_id=s.commission_id
		   AND ae.event_type='distribution.settlement_paid.v1'
		   AND ae.payload->>'settlement_reference'=s.settlement_reference),
		s.created_at,s.updated_at
		FROM distribution_settlements s WHERE s.commission_id=ANY($1::bigint[]) ORDER BY s.commission_id,s.id`, commissionIDs)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var commissionID int64
		var item distributionport.OrderDistributionSettlement
		if err := rows.Scan(&commissionID, &item.Reference, &item.AmountMinor, &item.Currency, &item.State, &item.ProviderDeadlineAt, &item.SettlementConfirmedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return mapError(err)
		}
		appendOrderLine(values, commissionID, func(line *distributionport.OrderDistributionLine) { line.Settlements = append(line.Settlements, item) })
	}
	return mapError(rows.Err())
}

func (r *Repository) attachOrderExceptions(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, values map[int64][]distributionport.OrderDistributionLine, commissionIDs []int64) error {
	rows, err := tx.Query(ctx, `SELECT commission_id,kind,status,amount_minor,reason,evidence_reference,created_at,updated_at FROM distribution_exceptions WHERE commission_id=ANY($1::bigint[]) ORDER BY commission_id,id`, commissionIDs)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var commissionID int64
		var item distributionport.OrderDistributionException
		if err := rows.Scan(&commissionID, &item.Kind, &item.Status, &item.AmountMinor, &item.Reason, &item.EvidenceReference, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return mapError(err)
		}
		appendOrderLine(values, commissionID, func(line *distributionport.OrderDistributionLine) { line.Exceptions = append(line.Exceptions, item) })
	}
	return mapError(rows.Err())
}

func appendOrderLine(values map[int64][]distributionport.OrderDistributionLine, commissionID int64, mutate func(*distributionport.OrderDistributionLine)) {
	for orderID, lines := range values {
		for i := range lines {
			if lines[i].CommissionID == commissionID {
				mutate(&lines[i])
			}
		}
		values[orderID] = lines
	}
}

func distinctOrderIDs(orderIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(orderIDs))
	for _, id := range orderIDs {
		if id > 0 {
			seen[id] = struct{}{}
		}
	}
	return mapKeys(seen)
}

func mapKeys(values map[int64]struct{}) []int64 {
	ids := make([]int64, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
