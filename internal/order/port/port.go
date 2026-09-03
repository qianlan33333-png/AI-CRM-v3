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
	Cursor      string
	Limit       int32
	Offset      int32
	Provider    domain.Provider
	Status      domain.Status
	OrderRef    string
	CustomerID  int64
	Product     string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

type Page struct {
	Items      []domain.Snapshot `json:"items"`
	NextCursor string            `json:"next_cursor"`
	Total      int64             `json:"total"`
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
	GetByReference(context.Context, string) (domain.Snapshot, error)
	List(context.Context, ListQuery) (Page, error)
}

type ExportPreview struct {
	Rows      int  `json:"total"`
	Truncated bool `json:"truncated"`
}

type ExportResult struct {
	ReceiptID     int64
	Rows          int
	Bytes         int
	Content       []byte
	ContentDigest [32]byte
}

type Exporter interface {
	PreviewExport(context.Context, ListQuery) (ExportPreview, error)
	ExportCSV(context.Context, ListQuery, int64, string) (ExportResult, error)
}

type SettlementWriter interface {
	ApplySettlement(context.Context, SettlementCommand) (domain.Snapshot, error)
}

type HistoricalImporter interface {
	ImportHistorical(context.Context, HistoricalImportCommand) (domain.Snapshot, error)
}

// PaymentReservationReader locks and validates a native effect-eligible order
// inside the caller's existing PostgreSQL Unit of Work.
type PaymentReservationReader interface {
	ReservePaymentWithin(context.Context, int64) (domain.Snapshot, error)
}

type PaymentSettlementCommand struct {
	OrderID       int64
	RefundedDelta int64
	Failed        bool
	OccurredAt    time.Time
	ReceiptKey    string
}

type PaymentOrderCommand struct {
	Provider                        domain.Provider
	MerchantOrderNo                 string
	PayerCustomerID                 int64
	BeneficiaryCustomerID           int64
	ProductID                       int64
	ProductCode, ProductName        string
	ProductVersion, UnitAmountMinor int64
	Currency                        string
	ActorScope, IdempotencyKey      string
}

// PaymentCoordinator is the only cross-domain write seam from Payment to
// Order. Both methods require the caller's existing PostgreSQL transaction.
type PaymentCoordinator interface {
	PaymentReservationReader
	CreatePaymentOrderWithin(context.Context, PaymentOrderCommand) (domain.Snapshot, error)
	SettlePaymentWithin(context.Context, PaymentSettlementCommand) (domain.Snapshot, error)
}
