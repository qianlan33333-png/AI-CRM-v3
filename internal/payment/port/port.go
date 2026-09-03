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
	PaymentID, AmountMinor                        int64
	RefundNo, Reason, ActorScope, IdempotencyKey  string
	ProviderOrderID, ProductID, SKUID, ReasonCode string
	RefundCount                                   int64
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

type EffectProjection struct {
	EffectID     string
	Kind         effectport.Kind
	State        effectport.State
	AttemptCount int32
	UpdatedAt    time.Time
}

type Handoff struct {
	PaymentID     int64
	MerchantOrder string
	Status        domain.Status
	Payload       []byte
	ExpiresAt     time.Time
}

type AdminQuery interface {
	FindPayment(context.Context, domain.Provider, string) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]RefundProjection, int64, error)
	ListOrderEffects(context.Context, domain.Provider, string) ([]EffectProjection, error)
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

// SessionReader authorizes polling after the one-shot checkout mutation has
// consumed the token. It never renews or mutates the session.
type SessionReader interface {
	LookupWithin(context.Context, string, time.Time) (SessionActor, error)
}

// ProviderIntent is a Payment-owned, immutable request projection. It is read
// before a Provider call and outside the command transaction.
type ProviderIntent struct {
	Kind                    effectport.Kind
	PaymentID, RefundID     int64
	PayerIdentityID         int64
	MerchantOrderNo         string
	RefundNo, RefundReason  string
	ProviderOrderID         string
	ProductID, SKUID        string
	RefundCount             int64
	ReasonCode              string
	AmountMinor, TotalMinor int64
	Currency                string
	SourceRefDigest         effectport.Digest
	PayloadDigest           effectport.Digest
}

type ShopRefundQuery struct {
	AfterSaleID, ProviderOrderID, ProductID, SKUID string
	Count, AmountMinor                             int64
	Currency, Status                               string
	OccurredAt                                     time.Time
	EvidenceDigest, ProviderRefundDigest           effectport.Digest
}

type ShopRefundMaterial struct {
	RefundID, PaymentID, AmountMinor int64
	RefundNo, ProviderOrderID        string
	ProductID, SKUID                 string
	RefundCount                      int64
	ReasonCode, Currency             string
}

type ShopRefundCallback struct {
	AfterSaleID, ProviderOrderID, Status string
	EventDigest, PayloadDigest           [32]byte
	OccurredAt                           time.Time
}

type ShopCallbackVerifier interface {
	VerifyURL(context.Context, map[string]string) (string, error)
	VerifyRefund(context.Context, []byte, map[string]string) (ShopRefundCallback, error)
}

type ShopRefundReconciler interface {
	ValidateRefundMaterial(context.Context, ShopRefundMaterial) error
	QueryRefund(context.Context, string) (ShopRefundQuery, error)
}

type WeChatPayPaymentQuery struct {
	MerchantOrderNo, Currency, Status string
	AmountMinor                       int64
	OccurredAt                        time.Time
	EvidenceDigest, TransactionDigest effectport.Digest
}

type WeChatPayRefundQuery struct {
	RefundNo, Currency, Status   string
	AmountMinor, TotalMinor      int64
	OccurredAt                   time.Time
	EvidenceDigest, RefundDigest effectport.Digest
}

type WeChatPayReconciler interface {
	QueryPayment(context.Context, string) (WeChatPayPaymentQuery, error)
	QueryRefund(context.Context, string) (WeChatPayRefundQuery, error)
}

type ProviderIntentReader interface {
	ProviderIntent(context.Context, effectport.Kind, effectport.Digest) (ProviderIntent, error)
}
