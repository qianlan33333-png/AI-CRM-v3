// Package port is the only cross-domain contract for submitting an opaque
// outbound external-effect intent. It contains no store, HTTP, worker, or
// provider dependency.
package port

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrReconciliationNotFound = errors.New("external effect reconciliation target not found")
	ErrReconciliationConflict = errors.New("external effect reconciliation conflict")
)

type Digest string

func Hash(parts ...string) Digest {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}
func ValidDigest(value Digest) bool {
	if !strings.HasPrefix(string(value), "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(string(value)[7:])
	return err == nil
}

type Owner string
type Kind string

const (
	OwnerOutbound         Owner = "outbound"
	OwnerPayment          Owner = "payment"
	KindOutboundMessage   Kind  = "outbound_message"
	KindAutomationMessage Kind  = "automation_message"
	KindOutboundMedia     Kind  = "outbound_media"
	KindWeComTagCatalog   Kind  = "wecom_tag_catalog"
	KindGroupMessage      Kind  = "group_message"
	KindChannelAsset      Kind  = "channel_acquisition_asset"
	KindChannelWelcome    Kind  = "channel_welcome_message"
	KindChannelEntryTag   Kind  = "channel_entry_tag"
	KindChannelLink       Kind  = "channel_acquisition_link_mutation"
	KindSidebarJSSDKSend  Kind  = "sidebar_jssdk_send"
	KindWeChatPayPrepay   Kind  = "wechat_pay_prepay_v1"
	KindWeChatPayRefund   Kind  = "wechat_pay_refund_v1"
	KindWeChatShopRefund  Kind  = "wechat_shop_refund_v1"
)

type State string

const (
	StateAccepted    State = "accepted"
	StateQueued      State = "queued"
	StateAttempted   State = "attempted"
	StateExecuted    State = "executed"
	StateUnknown     State = "outcome_unknown"
	StateReconciled  State = "reconciled"
	StateRetryable   State = "retryable_failed"
	StateFinalFailed State = "final_failed"
	StateCancelled   State = "cancelled"
)

type Envelope struct {
	Owner                                                              Owner
	Kind                                                               Kind
	SourceRefDigest, TargetRefDigest, PayloadDigest, PolicyVersionHash Digest
}

func (value Envelope) Valid() bool {
	kindValid := value.Owner == OwnerOutbound && (value.Kind == KindOutboundMessage || value.Kind == KindAutomationMessage || value.Kind == KindOutboundMedia || value.Kind == KindWeComTagCatalog || value.Kind == KindGroupMessage || value.Kind == KindChannelAsset || value.Kind == KindChannelWelcome || value.Kind == KindChannelEntryTag || value.Kind == KindChannelLink || value.Kind == KindSidebarJSSDKSend) ||
		value.Owner == OwnerPayment && (value.Kind == KindWeChatPayPrepay || value.Kind == KindWeChatPayRefund || value.Kind == KindWeChatShopRefund)
	return kindValid && ValidDigest(value.SourceRefDigest) && ValidDigest(value.TargetRefDigest) && ValidDigest(value.PayloadDigest) && ValidDigest(value.PolicyVersionHash)
}
func (value Envelope) Fingerprint() Digest {
	if !value.Valid() {
		return ""
	}
	return Hash(string(value.Owner), string(value.Kind), string(value.SourceRefDigest), string(value.TargetRefDigest), string(value.PayloadDigest), string(value.PolicyVersionHash))
}

type AcceptCommand struct {
	ReceiptKey Digest
	Envelope   Envelope
	// ScheduledAt is optional. A zero value keeps the existing immediate
	// acceptance semantics; a future value is persisted in the River job so
	// durable Group Ops delay nodes cannot run early.
	ScheduledAt time.Time
}

func (command AcceptCommand) Valid() bool {
	return ValidDigest(command.ReceiptKey) && command.Envelope.Valid()
}
func (command AcceptCommand) Digest() Digest {
	if !command.Valid() {
		return ""
	}
	if command.ScheduledAt.IsZero() {
		return Hash("accept", string(command.ReceiptKey), string(command.Envelope.Fingerprint()))
	}
	return Hash("accept", string(command.ReceiptKey), string(command.Envelope.Fingerprint()), command.ScheduledAt.UTC().Format(time.RFC3339Nano))
}

type Projection struct {
	ID           string    `json:"id"`
	Owner        Owner     `json:"owner"`
	Kind         Kind      `json:"kind"`
	State        State     `json:"state"`
	AttemptCount int32     `json:"attempt_count"`
	Generation   int64     `json:"generation"`
	UpdatedAt    time.Time `json:"updated_at"`
	QueueJobID   int64     `json:"queue_job_id,omitempty"`
}

type Receipt struct {
	ID               string    `json:"id"`
	EffectID         string    `json:"effect_id"`
	CommandDigest    Digest    `json:"command_digest"`
	ActorAdminUserID *int64    `json:"actor_admin_user_id,omitempty"`
	State            State     `json:"state"`
	CompletedAt      time.Time `json:"completed_at"`
	QueueReceiptID   string    `json:"queue_receipt_id,omitempty"`
}

// Accepter is implemented by the External Effects repository. Other domains
// depend on this contract rather than the concrete module package.
type Accepter interface {
	AcceptAndQueue(context.Context, AcceptCommand) (Projection, Receipt, error)
}

// TransactionalAccepter is the atomic variant for a domain command that is
// already inside a PostgreSQL Unit of Work. The context must carry that UoW's
// transaction; implementations must not begin or commit another transaction.
// Keeping pgx out of this port prevents business domains from importing the
// effects store or queue implementation.
type TransactionalAccepter interface {
	AcceptAndQueueWithin(context.Context, AcceptCommand) (Projection, Receipt, error)
}

// Reader exposes only the digest-safe effect projection required by owning
// domains when rendering an operator timeline. It intentionally exposes no
// payload, Provider response, or control operation.
type Reader interface {
	Get(context.Context, string) (Projection, error)
}

// ReconciliationCandidate is the exact expired attempt fence an owning
// domain must echo before an outcome_unknown effect can be closed manually.
type ReconciliationCandidate struct {
	Projection
	Fence          int64     `json:"fence"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

type ReconcileCommand struct {
	EffectID         string
	ReceiptKey       Digest
	EvidenceDigest   Digest
	ActorAdminUserID int64
	Generation       int64
	Fence            int64
	LeaseExpiresAt   time.Time
}

type TransactionalReconciler interface {
	ReconciliationCandidate(context.Context, string) (ReconciliationCandidate, error)
	ReconcileEffectWithin(context.Context, ReconcileCommand) (Projection, error)
}

type ClientCompletionCommand struct {
	EffectID       string
	ReceiptKey     Digest
	EvidenceDigest Digest
	State          State
}

// ClientCompleter closes a queued browser-owned JSSDK effect inside the
// Outbound caller's transaction. It never claims provider delivery.
type ClientCompleter interface {
	CompleteClientEffectWithin(context.Context, ClientCompletionCommand) (Projection, error)
}

type UnknownReconciler interface {
	ReconcileUnknownWithin(context.Context, ReconcileCommand) error
}

// ProviderAdapter is implemented by outbound. The effect kernel invokes it
// only after the attempted fact is committed, so it can never hold a database
// transaction across a provider network call.
type ProviderAdapter interface {
	Execute(context.Context, Envelope, Attempt) (AdapterResult, error)
}

type Attempt struct {
	// EffectID lets a Provider adapter load its owner-owned immutable dispatch
	// snapshot without placing business payloads in the External Effects tables.
	// It is supplied only after the attempted fact has committed.
	EffectID          string
	Number            int32
	Generation, Fence int64
}

type AdapterResult struct {
	Completion               State
	ReceiptDigest            Digest
	CallAttempted            bool
	RealExternalCallExecuted bool
	Artifact                 ResultArtifact
}

// ResultArtifact is a validated, opaque result. EER persists no business
// payload; a completion sink owned by composition routes it to its domain in
// the same completion transaction.
type ResultArtifact struct {
	Kind    string
	Digest  Digest
	Payload []byte
}

func (a ResultArtifact) Valid() bool {
	if a.Kind == "" || len(a.Kind) > 120 || len(a.Payload) == 0 || len(a.Payload) > 256<<10 || !ValidDigest(a.Digest) {
		return false
	}
	return a.Digest == Hash("external-effect.artifact.v1", a.Kind, string(a.Payload))
}

type CompletionSink interface {
	CompleteEffect(context.Context, string, Envelope, Attempt, AdapterResult) error
}
