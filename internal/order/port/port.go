// Package port is the only supported cross-domain Order contract.
package port

import (
	"context"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
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
	// NoCustomerMatch is set only after a read-only Customer/Identity Port
	// could not resolve a requested identity.  It keeps the resulting empty
	// page inside Order's Count/List predicate instead of dropping the filter
	// and accidentally returning every order.
	NoCustomerMatch bool
}

// CustomerFilter is the small composition seam used by the admin order list.
// Exactly one declared value is accepted.  It neither provisions a Customer
// nor attaches or merges an identity.
type CustomerFilter struct {
	Phone          string
	ExternalUserID string
}

type CustomerFilterStatus string

const (
	CustomerFilterFound       CustomerFilterStatus = "found"
	CustomerFilterNotFound    CustomerFilterStatus = "not_found"
	CustomerFilterConflict    CustomerFilterStatus = "conflict"
	CustomerFilterInvalid     CustomerFilterStatus = "invalid"
	CustomerFilterUnavailable CustomerFilterStatus = "unavailable"
)

type CustomerFilterResolution struct {
	Status     CustomerFilterStatus
	CustomerID customerdomain.CustomerID
}

// CustomerFilterResolver resolves only an existing canonical Customer for an
// order-list predicate.  The implementation belongs at composition and must
// use a trusted Identity Port; it must not create or mutate identity state.
type CustomerFilterResolver interface {
	ResolveOrderCustomerFilter(context.Context, CustomerFilter) (CustomerFilterResolution, error)
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

// CustomerScopedQuery reads one order reference through Order's own customer
// predicate. Callers that hold a customer-bounded credential must use this
// seam instead of fetching an unbounded order and filtering it afterwards.
type CustomerScopedQuery interface {
	GetByReferenceForCustomer(context.Context, string, int64) (domain.Snapshot, error)
}

type ProductSalesKey struct {
	ProductID   int64
	ProductCode string
}

type ProductOrderFact struct {
	OrderID       int64
	ProductID     *int64
	ProductCode   string
	OrderRefunded bool
}

// ProductSalesReader returns distinct orders that have authoritative paid
// history and match the requested products. It requires the caller's UoW.
type ProductSalesReader interface {
	ReadPaidProductOrdersWithin(context.Context, []ProductSalesKey) ([]ProductOrderFact, error)
}

type CustomerOrderSummary struct {
	Total    int64             `json:"total"`
	Paid     int64             `json:"paid"`
	Failed   int64             `json:"failed"`
	Refunded int64             `json:"refunded"`
	Recent   []domain.Snapshot `json:"recent"`
}

type CustomerOrderSummaryReader interface {
	CustomerOrderSummary(context.Context, int64, int32) (CustomerOrderSummary, error)
}

// CustomerActivityQuery is the Order-owned, canonical-customer query used by
// the V1 customer activity stream. The Host supplies an aggregate cursor;
// Order owns the per-type descending created_at/id keyset and never exposes an
// unbounded order read to a customer-scoped caller.
type CustomerActivityQuery struct {
	CustomerID int64
	Limit      int32
	Watermark  time.Time
	AfterAt    time.Time
	AfterID    int64
}

type CustomerActivity struct {
	OrderID       int64               `json:"order_id"`
	Relationship  string              `json:"relationship"`
	Provider      domain.Provider     `json:"provider"`
	Status        domain.Status       `json:"status"`
	Amount        domain.Money        `json:"amount"`
	RefundedMinor int64               `json:"refunded_minor"`
	RecordOrigin  domain.RecordOrigin `json:"record_origin"`
	OccurredAt    time.Time           `json:"occurred_at"`
}

type CustomerActivityPage struct {
	Items []CustomerActivity `json:"items"`
}

// CustomerActivityReader publishes the narrow Order projection required by an
// authorized customer activity feed. It does not return payer/beneficiary IDs
// or merchant/provider reference strings that belong to another party.
type CustomerActivityReader interface {
	CustomerActivities(context.Context, CustomerActivityQuery) (CustomerActivityPage, error)
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
	OrderID               int64
	RefundedDelta         int64
	Failed                bool
	ProviderTransactionNo string // verified Payment callback/query fact; only needed for first paid settlement
	OccurredAt            time.Time
	ReceiptKey            string
}

type PaymentOrderCommand struct {
	Provider                        domain.Provider
	MerchantOrderNo                 string
	PayerCustomerID                 int64
	BeneficiaryCustomerID           int64
	ProductID, CouponClaimID        int64
	ProductCode, ProductName        string
	ProductVersion, UnitAmountMinor int64
	ProductType                     string
	ServicePeriodDurationDays       int32
	Currency                        string
	MobileE164                      string
	ActorScope, IdempotencyKey      string
}

// CheckoutSnapshot is an Order-owned, immutable record of a native checkout.
// It freezes the gross price, coupon reservation and service-period term. It
// deliberately contains IDs as historical references only; Product and Coupon
// may later change without rewriting this sale fact.
type CheckoutSnapshot struct {
	OrderID                   int64
	ProductType               string
	ProductID                 int64
	ProductCode, ProductName  string
	ProductVersion            int64
	ServicePeriodDurationDays int32
	GrossAmountMinor          int64
	DiscountAmountMinor       int64
	PayableAmountMinor        int64
	Currency                  string
	CouponApplied             bool
	CouponReservationRef      string
	CouponClaimID, CouponID   int64
	CouponRuleVersion         int64
	ReservedAt                time.Time
}

// PaymentCoordinator is the only cross-domain write seam from Payment to
// Order. Both methods require the caller's existing PostgreSQL transaction.
type PaymentCoordinator interface {
	PaymentReservationReader
	CreatePaymentOrderWithin(context.Context, PaymentOrderCommand) (domain.Snapshot, error)
	SettlePaymentWithin(context.Context, PaymentSettlementCommand) (domain.Snapshot, error)
}
