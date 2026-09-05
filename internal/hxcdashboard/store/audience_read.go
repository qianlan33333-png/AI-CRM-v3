package store

import (
	"context"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	"time"
)

// AudienceMemberFacts only reads the published HXC projection. Its refresh is
// Provider read owned by HXC; Segment neither queries HuangYouCan nor uses
// directory updates or archived messages as a usage substitute.
func (store *PostgreSQL) AudienceMemberFacts(ctx context.Context, reference time.Time) ([]hxcport.AudienceMemberFact, error) {
	if store == nil || reference.IsZero() {
		return nil, ErrNotFound
	}
	rows, err := store.pool.Query(ctx, `SELECT r.customer_id,r.subscription_tier,CASE WHEN r.stage='registered_no_active_membership' THEN 'expired' ELSE 'active' END,r.subscription_expires_at,r.last_used_at,r.source_updated_at FROM hxc_dashboard_rows r JOIN hxc_dashboard_versions v ON v.id=r.projection_id AND v.status='published' WHERE r.identity_state='matched' AND r.customer_id IS NOT NULL AND r.source_updated_at <= $1 ORDER BY r.customer_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hxcport.AudienceMemberFact{}
	for rows.Next() {
		var item hxcport.AudienceMemberFact
		if err = rows.Scan(&item.CustomerID, &item.Tier, &item.Status, &item.ExpiresAt, &item.LastUsedAt, &item.SourceUpdatedAt); err != nil {
			return nil, err
		}
		item.Registered = true
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ hxcport.AudienceMemberReader = (*PostgreSQL)(nil)
