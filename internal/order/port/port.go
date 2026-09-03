// Package port is the only supported cross-domain Order contract.
package port

import (
	"context"
	"errors"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

var (
	ErrNotFound    = errors.New("order not found")
	ErrConflict    = errors.New("order conflict")
	ErrUnavailable = errors.New("order unavailable")
)

type CreateCommand struct {
	Input          domain.NewOrderInput
	Actor          int64
	IdempotencyKey string
}

type ListQuery struct {
	Cursor string
	Limit  int32
}

type Page struct {
	Items      []domain.Snapshot `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

type SettlementCommand struct {
	OrderID         int64
	ExpectedVersion int64
	Status          domain.Status
	RefundedMinor   int64
	OccurredAt      time.Time
	ActorScope      string
	IdempotencyKey  string
}

type HistoricalImportCommand struct {
	RunID        string
	SourceDigest [32]byte
	Order        domain.Snapshot
}

type CommandService interface {
	Create(context.Context, CreateCommand) (domain.Snapshot, error)
}

type Query interface {
	Get(context.Context, int64) (domain.Snapshot, error)
	List(context.Context, ListQuery) (Page, error)
}

type SettlementWriter interface {
	ApplySettlement(context.Context, SettlementCommand) (domain.Snapshot, error)
}

type HistoricalImporter interface {
	ImportHistorical(context.Context, HistoricalImportCommand) (domain.Snapshot, error)
}
