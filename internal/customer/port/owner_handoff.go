package port

import (
	"context"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var ErrOwnerHandoffConflict = errors.New("customer owner handoff conflict")

type LocalOwner struct {
	CustomerID customerdomain.CustomerID
	StaffID    int64
	Version    int64
	Source     string
	UpdatedAt  time.Time
}

type OwnerHandoffMode string

const (
	OwnerHandoffLocalOnly    OwnerHandoffMode = "local_only"
	OwnerHandoffWeComThenCRM OwnerHandoffMode = "wecom_then_crm"
)

type OwnerHandoffPreviewRow struct {
	Line            int64
	CustomerID      customerdomain.CustomerID
	ExpectedOwnerID int64
	ExpectedVersion int64
	State           string
	Reason          string
}

type OwnerHandoffPreview struct {
	ID                 string
	Mode               OwnerHandoffMode
	SourceStaffID      int64
	TargetStaffID      int64
	CorpScope          string
	Hash               string
	ConfirmationPhrase string
	ExpiresAt          time.Time
	Rows               []OwnerHandoffPreviewRow
}

type OwnerHandoffLine struct {
	Line       int64
	CustomerID customerdomain.CustomerID
	State      string
	EffectID   string
	ObservedAt *time.Time
}

type OwnerHandoffBatch struct {
	ID        string
	Mode      OwnerHandoffMode
	State     string
	Lines     []OwnerHandoffLine
	CreatedAt time.Time
	UpdatedAt time.Time
}

type OwnerHandoffReader interface {
	OwnerHandoffPreview(context.Context, string) (OwnerHandoffPreview, error)
	OwnerHandoffBatch(context.Context, string) (OwnerHandoffBatch, error)
}

type OwnerHandoffExecution struct {
	EffectID string
	// SourceRefDigest and TargetRefDigest bind this exact frozen batch line to
	// its opaque EER envelope. They are deliberately distinct from provider IDs.
	SourceRefDigest  string
	TargetRefDigest  string
	PayloadRefDigest string
	PolicyRefDigest  string
	SourceUserID     string
	TargetUserID     string
	ExternalUserID   string
	WelcomeMessage   string
	SourceDigest     string
	TargetDigest     string
	PayloadDigest    string
	PolicyDigest     string
}

type OwnerHandoffExecutionReader interface {
	ReadOwnerHandoffExecution(context.Context, string) (OwnerHandoffExecution, error)
}

type OwnerHandoffCompletion struct {
	EffectID     string
	State        string
	ResultDigest string
	Attempt      int32
}

type OwnerHandoffCompletionWriter interface {
	CompleteOwnerHandoffEffect(context.Context, OwnerHandoffCompletion) error
}

type OwnerHandoffCandidate struct {
	CustomerID           customerdomain.CustomerID
	ExpectedLocalOwnerID int64
	ExpectedLocalVersion int64 // zero means no explicit local owner at preview
	RelationshipDigest   [32]byte
	State                string // ready, excluded, conflict, unresolved
	Reason               string
	// The following values remain inside the Customer command boundary and are
	// encrypted before persistence. They are never emitted by Reader DTOs or
	// EER envelopes.
	SourceUserID   string
	TargetUserID   string
	ExternalUserID string
}

type OwnerHandoffCandidateResolver interface {
	ResolveOwnerHandoffCandidates(context.Context, int64, int64, string, []customerdomain.CustomerID) ([]OwnerHandoffCandidate, error)
}

type OwnerHandoffPreviewCommand struct {
	ActorAdminUserID   int64
	Mode               OwnerHandoffMode
	SourceStaffID      int64
	TargetStaffID      int64
	CorpScope          string
	CustomerIDs        []customerdomain.CustomerID
	WelcomeMessage     string
	ConfirmationPhrase string
	IdempotencyKey     string
}

type OwnerHandoffConfirmCommand struct {
	ActorAdminUserID   int64
	PreviewID          string
	PreviewHash        string
	ConfirmationPhrase string
	IdempotencyKey     string
}

type OwnerHandoffService interface {
	PreviewOwnerHandoff(context.Context, OwnerHandoffPreviewCommand) (OwnerHandoffPreview, error)
	ConfirmOwnerHandoff(context.Context, OwnerHandoffConfirmCommand) (OwnerHandoffBatch, error)
}

// OwnerHandoffPreviewRecord is an internal Customer-owner persistence value.
// Its candidate provider identifiers are encrypted before the store writes it.
type OwnerHandoffPreviewRecord struct {
	ActorAdminUserID int64
	Preview          OwnerHandoffPreview
	WelcomeMessage   string
	Candidates       []OwnerHandoffCandidate
	RequestDigest    [32]byte
}

type OwnerHandoffBatchRecord struct {
	Preview       OwnerHandoffPreviewRecord
	ActorID       int64
	Idempotency   string
	RequestDigest [32]byte
	Lines         []OwnerHandoffLine
}

// OwnerHandoffEffectBinding binds a Customer-owned frozen line to the single
// opaque EER receipt accepted in the same PostgreSQL Unit of Work.
type OwnerHandoffEffectBinding struct {
	BatchID   string
	Line      int64
	EffectID  string
	ReceiptID string
}
