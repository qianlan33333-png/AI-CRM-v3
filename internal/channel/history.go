package channel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type HistoryContact struct {
	ID, ChannelID, SourceContactID int64
	CustomerID                     *int64
	OwnerReference                 string
	FirstEnteredAt, LastEnteredAt  time.Time
	EnterCount                     int64
	CreatedAt, UpdatedAt           time.Time
}

type HistoryAssignee struct {
	ID, ChannelID, SourceAssigneeID     int64
	StaffReference, DisplayNameSnapshot string
	Priority                            int
	RatioPercent, MaxScans24h           *int
	Status                              string
	SourceCreatedAt, SourceUpdatedAt    time.Time
}

type RecentEntrant struct {
	ReceiptID, CustomerID int64
	AddedAt               time.Time
}

type HistoryStore interface {
	ListHistory(context.Context, int64, int, int) ([]HistoryContact, int64, []HistoryAssignee, error)
	ListRecent(context.Context, int64, int, int64) ([]RecentEntrant, error)
}

func (store *PostgreSQLStore) ListHistory(ctx context.Context, channelID int64, limit, offset int) ([]HistoryContact, int64, []HistoryAssignee, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM channel_history_contacts WHERE channel_id=$1`, channelID).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, channel_id, source_contact_id, customer_id, owner_reference, first_entered_at, last_entered_at, enter_count, created_at, updated_at FROM channel_history_contacts WHERE channel_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, channelID, limit, offset)
	if err != nil {
		return nil, 0, nil, err
	}
	contacts := make([]HistoryContact, 0, limit)
	for rows.Next() {
		var item HistoryContact
		if err = rows.Scan(&item.ID, &item.ChannelID, &item.SourceContactID, &item.CustomerID, &item.OwnerReference, &item.FirstEnteredAt, &item.LastEnteredAt, &item.EnterCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, 0, nil, err
		}
		contacts = append(contacts, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, nil, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, channel_id, source_assignee_id, staff_reference, display_name_snapshot, priority, ratio_percent, max_scans_24h, status, source_created_at, source_updated_at FROM channel_history_assignees WHERE channel_id=$1 ORDER BY id LIMIT 200`, channelID)
	if err != nil {
		return nil, 0, nil, err
	}
	assignees := make([]HistoryAssignee, 0)
	for rows.Next() {
		var item HistoryAssignee
		if err = rows.Scan(&item.ID, &item.ChannelID, &item.SourceAssigneeID, &item.StaffReference, &item.DisplayNameSnapshot, &item.Priority, &item.RatioPercent, &item.MaxScans24h, &item.Status, &item.SourceCreatedAt, &item.SourceUpdatedAt); err != nil {
			rows.Close()
			return nil, 0, nil, err
		}
		assignees = append(assignees, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, nil, err
	}
	rows.Close()
	return contacts, total, assignees, nil
}

func (store *PostgreSQLStore) ListRecent(ctx context.Context, channelID int64, limit int, beforeID int64) ([]RecentEntrant, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH channel_entries AS (
		SELECT r.id receipt_id, r.customer_id, r.occurred_at FROM channel_acquisition_entrant_receipts r JOIN channel_acquisition_state_bindings b ON b.id=r.binding_id WHERE r.status='channel_attributed' AND b.channel_id=$1
		UNION ALL
		SELECT r.id, rr.customer_id, rr.reconciled_at FROM channel_acquisition_entrant_reconciliation_receipts rr JOIN channel_acquisition_entrant_receipts r ON r.id=rr.entrant_receipt_id JOIN channel_acquisition_state_bindings b ON b.id=rr.binding_id WHERE b.channel_id=$1
	), latest AS (SELECT max(receipt_id) receipt_id, customer_id, min(occurred_at) added_at FROM channel_entries WHERE ($2::bigint=0 OR receipt_id < $2) GROUP BY customer_id)
	SELECT receipt_id, customer_id, added_at FROM latest ORDER BY receipt_id DESC LIMIT $3`, channelID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RecentEntrant, 0, limit)
	for rows.Next() {
		var item RecentEntrant
		if err = rows.Scan(&item.ReceiptID, &item.CustomerID, &item.AddedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type HistoryService struct {
	uow     platformport.UnitOfWork
	catalog *CatalogService
	store   HistoryStore
}

func NewHistoryService(uow platformport.UnitOfWork, catalog *CatalogService, store HistoryStore) *HistoryService {
	return &HistoryService{uow: uow, catalog: catalog, store: store}
}
func (service *HistoryService) History(ctx context.Context, channelID int64, limit, offset int) ([]HistoryContact, int64, []HistoryAssignee, error) {
	if service == nil || service.uow == nil || service.catalog == nil || service.store == nil || limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, nil, ErrInvalidCatalogCommand
	}
	if _, err := service.catalog.Get(ctx, channelID); err != nil {
		return nil, 0, nil, err
	}
	var contacts []HistoryContact
	var total int64
	var assignees []HistoryAssignee
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		contacts, total, assignees, readErr = service.store.ListHistory(tx, channelID, limit, offset)
		return readErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, nil, ErrCatalogNotFound
		}
		return nil, 0, nil, errors.Join(ErrCatalogUnavailable, err)
	}
	return contacts, total, assignees, nil
}
func (service *HistoryService) Recent(ctx context.Context, channelID int64, limit int, beforeID int64) ([]RecentEntrant, error) {
	if service == nil || service.uow == nil || service.catalog == nil || service.store == nil || limit < 1 || limit > 51 || beforeID < 0 {
		return nil, ErrInvalidCatalogCommand
	}
	if _, err := service.catalog.Get(ctx, channelID); err != nil {
		return nil, err
	}
	var items []RecentEntrant
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		items, readErr = service.store.ListRecent(tx, channelID, limit, beforeID)
		return readErr
	})
	if err != nil {
		return nil, errors.Join(ErrCatalogUnavailable, err)
	}
	return items, nil
}
func safeCustomerLabel(id int64) string { return fmt.Sprintf("CID-%d", id) }
