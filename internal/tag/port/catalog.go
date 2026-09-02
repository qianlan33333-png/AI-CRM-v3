// Package port defines the stable tag-domain boundaries.
package port

import (
	"context"
	"errors"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
)

// ErrNotFound and ErrConflict classify owned-store results without exposing a
// concrete adapter to the application or HTTP layers.
var (
	ErrNotFound = errors.New("tag catalog item not found")
	ErrConflict = errors.New("tag catalog conflict")
)

// CatalogStore is the Tag-owned transactional store contract. Implementations
// must access only tag-owned tables. Customer assignment/ removal is not part
// of this interface.
type CatalogStore interface {
	ListGroups(context.Context) ([]domain.Group, error)
	ListTags(context.Context) ([]domain.Tag, error)
	CreateGroup(context.Context, string) (domain.Group, error)
	CreateTag(context.Context, int64, string) (domain.Tag, error)
	UpdateGroup(context.Context, int64, string) (domain.Group, error)
	ArchiveGroup(context.Context, int64) (domain.Group, error)
	UpdateTag(context.Context, int64, string) (domain.Tag, error)
	ArchiveTag(context.Context, int64) (domain.Tag, error)
	GetGroup(context.Context, int64) (domain.Group, error)
	GetTag(context.Context, int64) (domain.Tag, error)
	ReorderGroups(context.Context, []int64) ([]domain.Group, error)
	ReorderTags(context.Context, []int64) ([]domain.Tag, error)
}

// ReferenceGuard is an optional cross-domain read port used to keep archive
// operations safe. It exposes counts only; it cannot mutate customer-tag
// relationships and must never be implemented by a Tag store querying a
// customer-owned table directly.
type ReferenceGuard interface {
	TagReferences(context.Context, int64) (int64, error)
	GroupReferences(context.Context, int64) (int64, error)
}

type MutationReceiptState string

const (
	MutationInProgress MutationReceiptState = "in_progress"
	MutationCompleted  MutationReceiptState = "completed"
)

type MutationReceipt struct {
	ID             int64
	Operation      string
	Actor          int64
	IdempotencyKey string
	PayloadDigest  []byte
	State          MutationReceiptState
	ResultIDs      []int64
}

type MutationReceiptReservation struct {
	Operation      string
	Actor          int64
	IdempotencyKey string
	PayloadDigest  []byte
}

// MutationReceiptStore persists idempotency facts in the caller's
// transaction. The digest must include every field that can affect a result.
type MutationReceiptStore interface {
	ReserveMutation(context.Context, MutationReceiptReservation) (MutationReceipt, bool, error)
	CompleteMutation(context.Context, int64, []int64, time.Time) (MutationReceipt, error)
}

type Event struct {
	Type           string
	Payload        []byte
	OccurredAt     time.Time
	IdempotencyKey string
}

// EventAppender is intentionally a tiny versioned-event boundary. The
// composition root adapts it to platform audit/outbox infrastructure while
// this domain remains independent of a concrete platform store.
type EventAppender interface {
	Append(context.Context, Event) (int64, error)
}

// SyncReceiptStore and SyncEnqueuer are the local acceptance boundary for a
// catalog refresh. They carry no CorpID or credential: Provider execution is
// a later outbound concern.
type SyncReceiptStore interface {
	ReserveSync(context.Context, SyncCommand) (SyncReceipt, error)
	AcceptSync(context.Context, int64, int64, SyncEffectReceipt) (SyncReceipt, error)
}

type SyncEnqueuer interface {
	EnqueueSync(context.Context, SyncJob) (SyncEffectReceipt, error)
}

type SyncKind string

const (
	SyncManual SyncKind = "manual"
	SyncDue    SyncKind = "due"
)

type SyncState string

const (
	SyncQueued         SyncState = "queued"
	SyncAttempted      SyncState = "attempted"
	SyncExecuted       SyncState = "executed"
	SyncOutcomeUnknown SyncState = "outcome_unknown"
	SyncReconciled     SyncState = "reconciled"
)

type SyncReceiptState string

const (
	SyncReserved SyncReceiptState = "reserved"
	SyncAccepted SyncReceiptState = "queued"
)

type SyncCommand struct {
	Actor          int64
	IdempotencyKey string
	TraceID        string
	Kind           SyncKind
}

type SyncReceipt struct {
	ID      int64
	Command SyncCommand
	State   SyncReceiptState
	EventID int64
	Effect  SyncEffectReceipt
}

// SyncEffectReceipt is an opaque outbound/EER acceptance fact. Tag owns the
// binding to this receipt but never owns a provider writer, queue or effect
// state machine.
type SyncEffectReceipt struct {
	QueueJobID, EffectID                                    int64
	EffectRef, EffectState, AcceptReceiptID, QueueReceiptID string
}

type SyncJob struct {
	ReceiptID      int64    `json:"receipt_id"`
	Actor          int64    `json:"actor"`
	IdempotencyKey string   `json:"-"`
	Kind           SyncKind `json:"kind"`
	TraceID        string   `json:"trace_id,omitempty"`
}

type SyncAcceptance struct {
	ReceiptID       int64     `json:"receipt_id"`
	EventID         int64     `json:"event_id"`
	QueueJobID      int64     `json:"queue_job_id"`
	EffectID        string    `json:"effect_id"`
	EffectState     string    `json:"effect_state"`
	AcceptReceiptID string    `json:"accept_receipt_id"`
	QueueReceiptID  string    `json:"queue_receipt_id"`
	State           SyncState `json:"state"`
}
