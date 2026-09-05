package store

import (
	"context"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

// AudienceFirstClicks uses resolved content-open facts. It aggregates the
// immutable event stream, so later reads never reset the first-click anchor.
func (Postgres) AudienceFirstClicks(ctx context.Context, reference time.Time) ([]radarport.AudienceFirstClick, error) {
	if reference.IsZero() {
		return nil, radar.ErrInvalidArgument
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,radar_id,MIN(occurred_at) FROM radar_events WHERE attribution_status='resolved' AND customer_id IS NOT NULL AND stage IN ('content_opened','redirected','image_loaded','pdf_opened') AND occurred_at <= $1 GROUP BY customer_id,radar_id ORDER BY radar_id,customer_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []radarport.AudienceFirstClick{}
	for rows.Next() {
		var item radarport.AudienceFirstClick
		if err = rows.Scan(&item.CustomerID, &item.RadarID, &item.FirstClickedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ radarport.AudienceFirstClickReader = Postgres{}
