// Package externaleffects is the closed, digest-only external-effect kernel.
package externaleffects

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid           = errors.New("invalid external effect command")
	ErrNotFound          = errors.New("external effect not found")
	ErrPayloadMismatch   = errors.New("external effect payload mismatch")
	ErrTransition        = errors.New("external effect transition forbidden")
	ErrReconcileRequired = errors.New("external effect reconciliation required")
)

type Digest string

func Hash(parts ...string) Digest {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}
func ValidDigest(v Digest) bool {
	if !strings.HasPrefix(string(v), "sha256:") || len(v) != 71 {
		return false
	}
	_, err := hex.DecodeString(string(v)[7:])
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

func (e Envelope) Valid() bool {
	return e.Owner == OwnerOutbound && (e.Kind == KindOutboundMessage || e.Kind == KindOutboundMedia || e.Kind == KindWeComTagCatalog || e.Kind == KindGroupMessage) && ValidDigest(e.SourceRefDigest) && ValidDigest(e.TargetRefDigest) && ValidDigest(e.PayloadDigest) && ValidDigest(e.PolicyVersionHash)
}
func (e Envelope) Fingerprint() Digest {
	if !e.Valid() {
		return ""
	}
	return Hash(string(e.Owner), string(e.Kind), string(e.SourceRefDigest), string(e.TargetRefDigest), string(e.PayloadDigest), string(e.PolicyVersionHash))
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
	ID            string    `json:"id"`
	EffectID      string    `json:"effect_id"`
	CommandDigest Digest    `json:"command_digest"`
	State         State     `json:"state"`
	CompletedAt   time.Time `json:"completed_at"`
}
type AcceptCommand struct {
	ReceiptKey Digest
	Envelope   Envelope
}

func (c AcceptCommand) Valid() bool { return ValidDigest(c.ReceiptKey) && c.Envelope.Valid() }
func (c AcceptCommand) Digest() Digest {
	if !c.Valid() {
		return ""
	}
	return Hash("accept", string(c.ReceiptKey), string(c.Envelope.Fingerprint()))
}

type ControlCommand struct {
	EffectID       string
	ReceiptKey     Digest
	EvidenceDigest Digest
}

func (c ControlCommand) Valid() bool {
	return strings.HasPrefix(c.EffectID, "eer_") && ValidDigest(c.ReceiptKey)
}
func (c ControlCommand) Digest(op string) Digest {
	if !c.Valid() || (op == "reconcile" && !ValidDigest(c.EvidenceDigest)) {
		return ""
	}
	return Hash(op, c.EffectID, string(c.ReceiptKey), string(c.EvidenceDigest))
}
func CanTransition(from, to State) bool {
	switch from {
	case StateAccepted:
		return to == StateQueued || to == StateCancelled
	case StateQueued:
		return to == StateAttempted || to == StateCancelled
	case StateAttempted:
		return to == StateExecuted || to == StateUnknown || to == StateRetryable || to == StateFinalFailed
	case StateRetryable:
		return to == StateQueued
	case StateUnknown:
		return to == StateReconciled
	}
	return false
}
