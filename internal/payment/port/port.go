package port

import (
	"context"
	"errors"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

var ErrInvalid = errors.New("invalid payment command")
var ErrConflict = errors.New("payment conflict")
var ErrNotFound = errors.New("payment not found")
var ErrUnavailable = errors.New("payment unavailable")

type CreateCommand struct {
	OrderID                    int64
	SessionToken               string
	ActorScope, IdempotencyKey string
}
type RefundCommand struct {
	PaymentID, AmountMinor                       int64
	RefundNo, Reason, ActorScope, IdempotencyKey string
}
type SettlementCommand struct {
	PaymentID                 int64
	ExpectedVersion           int64
	Status                    domain.Status
	ProviderTransactionDigest string
	OccurredAt                time.Time
	ReceiptKey                string
}
type RefundSettlementCommand struct {
	RefundID             int64
	ExpectedVersion      int64
	Status               domain.RefundStatus
	ProviderRefundDigest string
	OccurredAt           time.Time
	ReceiptKey           string
}

type Application interface {
	Create(context.Context, CreateCommand) (domain.Payment, error)
	RequestRefund(context.Context, RefundCommand) (domain.Refund, error)
}
type Query interface {
	GetPayment(context.Context, int64) (domain.Payment, error)
	GetRefund(context.Context, int64) (domain.Refund, error)
}

type RefundProjection struct {
	Refund         domain.Refund
	OrderID        int64
	MerchantOrder  string
	TransactionRef string
	OrderAmount    int64
	Currency       string
}

type AdminQuery interface {
	FindPayment(context.Context, domain.Provider, string) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]RefundProjection, int64, error)
}
type SettlementWriter interface {
	SettlePayment(context.Context, SettlementCommand) (domain.Payment, error)
	SettleRefund(context.Context, RefundSettlementCommand) (domain.Refund, error)
}
type HistoricalImporter interface {
	ImportTerminalPayment(context.Context, domain.Payment, [32]byte, string) (domain.Payment, error)
	ImportTerminalRefund(context.Context, domain.Refund, [32]byte, string) (domain.Refund, error)
}

type SessionActor struct {
	PayerIdentityID       int64
	PayerCustomerID       int64
	BeneficiaryCustomerID int64
}

// SessionConsumer consumes a trusted, opaque payment handoff inside the
// caller's existing transaction. It never accepts raw identity claims.
type SessionConsumer interface {
	ConsumeWithin(context.Context, string, time.Time) (SessionActor, error)
}

// ProviderIntent is a Payment-owned, immutable request projection. It is read
// before a Provider call and outside the command transaction.
type ProviderIntent struct {
	Kind                    effectport.Kind
	PaymentID, RefundID     int64
	PayerIdentityID         int64
	MerchantOrderNo         string
	RefundNo, RefundReason  string
	AmountMinor, TotalMinor int64
	Currency                string
	SourceRefDigest         effectport.Digest
	PayloadDigest           effectport.Digest
}

type ProviderIntentReader interface {
	ProviderIntent(context.Context, effectport.Kind, effectport.Digest) (ProviderIntent, error)
}
