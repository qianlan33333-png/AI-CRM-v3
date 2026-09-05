package port

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

var ErrInvalid = errors.New("invalid payment command")
var ErrConflict = errors.New("payment conflict")
var ErrNotFound = errors.New("payment not found")
var ErrUnavailable = errors.New("payment unavailable")
var ErrSessionRequired = errors.New("trusted payment session required")
var ErrSessionMismatch = errors.New("payment checkout session mismatch")

// TrustedSessionCookieName is shared by public Host adapters. Its opaque
// value is resolved only by Payment's SessionReader inside a PostgreSQL UoW;
// no adapter may treat it as a customer or identity claim.
const TrustedSessionCookieName = "aicrm_payment_session"

// CheckoutSessionBinding is an opaque, non-identity marker derived only from
// the HttpOnly Payment session. Public pages retain it with a recovery
// checkpoint, never the session token itself. A marker is useful only when
// presented together with the current HttpOnly cookie, so it is not a second
// credential or a browser-provided identity claim.
func CheckoutSessionBinding(token string) string {
	if len(token) < 20 || len(token) > 100 {
		return ""
	}
	digest := sha256.Sum256([]byte("payment.checkout.session-binding.v1\x00" + token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// MatchesCheckoutSessionBinding uses a constant-time comparison so the
// checkout mutation can reject a checkpoint from another trusted session
// before it reaches the payment/order Unit of Work.
func MatchesCheckoutSessionBinding(token, binding string) bool {
	expected := CheckoutSessionBinding(token)
	return expected != "" && len(binding) == len(expected) && subtle.ConstantTimeCompare([]byte(expected), []byte(binding)) == 1
}

type CreateCommand struct {
	OrderID, ProductID         int64
	CouponClaimID              int64
	ProductType                string
	SessionToken               string
	CheckoutSessionBinding     string
	MobileE164                 string
	BeneficiarySelection       BeneficiarySelection
	ActorScope, IdempotencyKey string
}
type RefundCommand struct {
	PaymentID, AmountMinor                        int64
	RefundNo, Reason, ActorScope, IdempotencyKey  string
	ProviderOrderID, ProductID, SKUID, ReasonCode string
	RefundCount                                   int64
}
type Application interface {
	Create(context.Context, CreateCommand) (domain.Payment, error)
	RequestRefund(context.Context, RefundCommand) (domain.Refund, error)
}
type Query interface {
	GetPayment(context.Context, int64) (domain.Payment, error)
	GetRefund(context.Context, int64) (domain.Refund, error)
}

// RefundExposureReader returns order IDs with a requested, in-flight,
// outcome-unknown, or completed refund. Final failures are excluded.
type RefundExposureReader interface {
	RefundRelatedOrderIDsWithin(context.Context, []int64) (map[int64]struct{}, error)
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
type HistoricalImporter interface {
	ImportTerminalPayment(context.Context, domain.Payment, [32]byte, string) (domain.Payment, error)
	ImportTerminalRefund(context.Context, domain.Refund, [32]byte, string) (domain.Refund, error)
}

// BeneficiarySelection records how a payment-session recipient was established.
// The public checkout exposes only PayerSelf; AdminAssisted is a server-only
// prebound session fact.
type BeneficiarySelection string

const (
	BeneficiarySelectionLegacyPrebound BeneficiarySelection = "legacy_prebound"
	BeneficiarySelectionUnresolved     BeneficiarySelection = "unresolved"
	BeneficiarySelectionPayerSelf      BeneficiarySelection = "payer_self"
	BeneficiarySelectionAdminAssisted  BeneficiarySelection = "admin_assisted"
)

type SessionActor struct {
	PayerIdentityID       int64
	PayerCustomerID       int64
	BeneficiaryCustomerID int64
	BeneficiarySelection  BeneficiarySelection
	Channel               domain.Channel
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

// SessionBeneficiarySelector records the only public recipient choice within
// the caller's existing transaction. It derives the recipient from the trusted
// payer; callers cannot submit a customer ID.
type SessionBeneficiarySelector interface {
	SelectPayerSelfWithin(context.Context, string, time.Time) (SessionActor, error)
}

// SessionLifecycle supports idempotent checkout replay: callers first read the
// still-valid actor, then consume only when a new mutation is persisted in the
// same transaction.
type SessionLifecycle interface {
	SessionConsumer
	SessionReader
	SessionBeneficiarySelector
}

// ProviderIntent is a Payment-owned, immutable request projection. It is read
// before a Provider call and outside the command transaction.
type ProviderIntent struct {
	Kind                    effectport.Kind
	PaymentID, RefundID     int64
	PayerIdentityID         int64
	Channel                 domain.Channel
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

type ReconciliationEnqueuer interface {
	EnqueueWithin(context.Context, ReconciliationTarget) error
}

type ReconciliationTarget struct {
	Provider            domain.Provider
	OrderID             int64
	PaymentID, RefundID int64
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
