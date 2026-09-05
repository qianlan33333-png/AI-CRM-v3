package store

import (
	"context"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PaidAudienceOrders exposes only payer facts. The
// timestamp is the first transition to paid, never a mutable order update.
func (s *Repository) PaidAudienceOrders(ctx context.Context, reference time.Time) ([]orderport.PaidAudienceOrder, error) {
	if reference.IsZero() {
		return nil, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.payer_customer_id,i.product_code,''::text,(SELECT MIN(h.occurred_at) FROM order_status_history h WHERE h.order_id=o.id AND h.to_status='paid')
		FROM orders o JOIN order_items i ON i.order_id=o.id
		WHERE o.status='paid' AND o.payer_customer_id IS NOT NULL
		ORDER BY o.id,i.line_no`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []orderport.PaidAudienceOrder{}
	for rows.Next() {
		var item orderport.PaidAudienceOrder
		if err = rows.Scan(&item.CustomerID, &item.ProductCode, &item.OwnerReference, &item.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ orderport.PaidAudienceReader = (*Repository)(nil)
