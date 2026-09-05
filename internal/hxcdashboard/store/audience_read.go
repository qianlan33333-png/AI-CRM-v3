package store

import (
	"context"
	"fmt"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

// AudienceMemberFacts only reads the published HXC projection. Its refresh is
// Provider read owned by HXC; Segment neither queries HuangYouCan nor uses
// directory updates or archived messages as a usage substitute.
func (store *PostgreSQL) AudienceMemberFacts(ctx context.Context, reference time.Time) ([]hxcport.AudienceMemberFact, error) {
	if store == nil || reference.IsZero() {
		return nil, ErrNotFound
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	var published bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hxc_dashboard_versions WHERE status='published')`).Scan(&published); err != nil {
		return nil, fmt.Errorf("read published HXC projection: %w", err)
	}
	if !published {
		// An uninitialised projection is not factual evidence for an empty
		// audience. Fail closed so callers expose source-unavailable instead.
		return nil, ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT r.customer_id,r.subscription_tier,CASE WHEN r.stage='registered_no_active_membership' THEN 'expired' ELSE 'active' END,(r.stage IN ('active_used','active_unused')),r.subscription_expires_at,r.last_used_at,r.source_updated_at FROM hxc_dashboard_rows r JOIN hxc_dashboard_versions v ON v.id=r.projection_id AND v.status='published' WHERE r.identity_state='matched' AND r.customer_id IS NOT NULL AND r.source_updated_at <= $1 ORDER BY r.customer_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hxcport.AudienceMemberFact{}
	for rows.Next() {
		var item hxcport.AudienceMemberFact
		if err = rows.Scan(&item.CustomerID, &item.Tier, &item.Status, &item.IsMember, &item.ExpiresAt, &item.LastUsedAt, &item.SourceUpdatedAt); err != nil {
			return nil, err
		}
		item.Registered = true
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ hxcport.AudienceMemberReader = (*PostgreSQL)(nil)
