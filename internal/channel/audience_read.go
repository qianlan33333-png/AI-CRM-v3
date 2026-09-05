package channel

import (
	"context"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

// AudienceEntries chooses the latest reconciled fact per channel/source
// contact. It never treats an import timestamp as an entry timestamp.
func (store *PostgreSQLStore) AudienceEntries(ctx context.Context, reference time.Time) ([]channelport.AudienceEntry, error) {
	if reference.IsZero() {
		return nil, ErrInvalidCatalogCommand
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH latest AS (SELECT DISTINCT ON (h.channel_id,h.source_contact_id) h.* FROM channel_history_contacts h JOIN channel_history_import_runs r ON r.id=h.import_run_id WHERE r.state IN ('completed','reconciled') AND h.last_entered_at <= $1 ORDER BY h.channel_id,h.source_contact_id,r.snapshot_timestamp DESC,h.import_run_id DESC) SELECT customer_id,channel_id,owner_reference,first_entered_at,last_entered_at FROM latest WHERE customer_id IS NOT NULL ORDER BY channel_id,customer_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []channelport.AudienceEntry{}
	for rows.Next() {
		var item channelport.AudienceEntry
		if err = rows.Scan(&item.CustomerID, &item.ChannelID, &item.OwnerReference, &item.FirstEnteredAt, &item.LastEnteredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ channelport.AudienceEntryReader = (*PostgreSQLStore)(nil)
