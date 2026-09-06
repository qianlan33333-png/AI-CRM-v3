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
	Line           int64
	CustomerID     customerdomain.CustomerID
	State          string
	EffectID       string
	ObservedAt     *time.Time
	TransferStatus int
	TakeoverAt     *time.Time
}

type OwnerHandoffBatch struct {
	ID        string
	Mode      OwnerHandoffMode
	State     string
	Lines     []OwnerHandoffLine
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OwnerHandoffBatchSegment is a Customer-owned durable work slice. It carries
// no provider identifiers: outbound reads that separately from the immutable
// line after the effect has been accepted.
type OwnerHandoffBatchSegment struct {
	BatchID       string
	PreviewID     string
	ActorID       int64
	TargetStaffID int64
	Mode          OwnerHandoffMode
	Lines         []OwnerHandoffSegmentLine
	HasNext       bool
}

type OwnerHandoffSegmentLine struct {
	OwnerHandoffLine
	ExpectedLocalVersion int64
}

type OwnerHandoffReader interface {
	OwnerHandoffPreview(context.Context, string) (OwnerHandoffPreview, error)
	OwnerHandoffBatch(context.Context, string) (OwnerHandoffBatch, error)
}

type OwnerHandoffExecution struct {
	EffectID      string
	SourceStaffID int64
	TargetStaffID int64
	CorpScope     string
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
	Generation   int64
	Fence        int64
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
	ResolveOwnerHandoffCandidates(context.Context, OwnerHandoffMode, int64, int64, string, []customerdomain.CustomerID) ([]OwnerHandoffCandidate, error)
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

// OwnerHandoffTransferResultCommand requests one bounded, read-only WeCom
// transfer-result page for an already accepted batch. It cannot create a
// transfer or change local ownership; the accepted effect completion remains
// the only path that performs that CAS.
type OwnerHandoffTransferResultCommand struct {
	ActorAdminUserID int64
	BatchID          string
	// IdempotencyKey identifies this explicit readback command. Replaying the
	// same key is safe; a later operator refresh receives a new key so a changed
	// final observation is not hidden behind an earlier empty cursor.
	IdempotencyKey string
}

type OwnerHandoffTransferResultService interface {
	RefreshOwnerHandoffTransferResult(context.Context, OwnerHandoffTransferResultCommand) (OwnerHandoffBatch, error)
}

// OwnerHandoffTransferRead and OwnerHandoffTransferObservation stay inside
// the Customer/WeCom adapter boundary. Reader DTOs and HTTP responses only
// expose their safe status and timestamp projection.
type OwnerHandoffTransferRead struct {
	BatchID      string
	ActorAdminID int64
	SourceUserID string
	TargetUserID string
	Cursor       string
}

type OwnerHandoffTransferObservation struct {
	ExternalUserID string
	Status         int
	TakeoverTime   int64
}

// OwnerHandoffPreviewRecord is an internal Customer-owner persistence value.
// Its candidate provider identifiers are encrypted before the store writes it.
type OwnerHandoffPreviewRecord struct {
	ActorAdminUserID int64
	// ExecutedBatchID is the immutable one-time confirmation binding. A replay
	// with its original idempotency key returns that batch; any other key must
	// not create a second transfer or local migration from the same preview.
	ExecutedBatchID string
	Preview         OwnerHandoffPreview
	WelcomeMessage  string
	Candidates      []OwnerHandoffCandidate
	RequestDigest   [32]byte
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
