// Package port is the only cross-domain contract for submitting an opaque
// outbound external-effect intent. It contains no store, HTTP, worker, or
// provider dependency.
package port

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
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
	OwnerOutbound       Owner = "outbound"
	KindOutboundMessage Kind  = "outbound_message"
	KindOutboundMedia   Kind  = "outbound_media"
	KindWeComTagCatalog Kind  = "wecom_tag_catalog"
	KindGroupMessage    Kind  = "group_message"
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
	return value.Owner == OwnerOutbound && (value.Kind == KindOutboundMessage || value.Kind == KindOutboundMedia || value.Kind == KindWeComTagCatalog || value.Kind == KindGroupMessage) && ValidDigest(value.SourceRefDigest) && ValidDigest(value.TargetRefDigest) && ValidDigest(value.PayloadDigest) && ValidDigest(value.PolicyVersionHash)
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
}

func (command AcceptCommand) Valid() bool {
	return ValidDigest(command.ReceiptKey) && command.Envelope.Valid()
}
func (command AcceptCommand) Digest() Digest {
	if !command.Valid() {
		return ""
	}
	return Hash("accept", string(command.ReceiptKey), string(command.Envelope.Fingerprint()))
}

type Projection struct {
	ID           string    `json:"id"`
	Owner        Owner     `json:"owner"`
	Kind         Kind      `json:"kind"`
	State        State     `json:"state"`
	AttemptCount int32     `json:"attempt_count"`
	Generation   int64     `json:"generation"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Receipt struct {
	ID               string    `json:"id"`
	EffectID         string    `json:"effect_id"`
	CommandDigest    Digest    `json:"command_digest"`
	ActorAdminUserID *int64    `json:"actor_admin_user_id,omitempty"`
	State            State     `json:"state"`
	CompletedAt      time.Time `json:"completed_at"`
}

// Accepter is implemented by the External Effects repository. Other domains
// depend on this contract rather than the concrete module package.
type Accepter interface {
	AcceptAndQueue(context.Context, AcceptCommand) (Projection, Receipt, error)
}

// ProviderAdapter is implemented by outbound. The effect kernel invokes it
// only after the attempted fact is committed, so it can never hold a database
// transaction across a provider network call.
type ProviderAdapter interface {
	Execute(context.Context, Envelope, Attempt) (AdapterResult, error)
}

type Attempt struct {
	Number            int32
	Generation, Fence int64
}

type AdapterResult struct {
	Completion               State
	ReceiptDigest            Digest
	CallAttempted            bool
	RealExternalCallExecuted bool
}
