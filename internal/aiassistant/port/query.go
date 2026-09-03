package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type ReviewState string

const (
	ReviewPending    ReviewState = "pending_review"
	ReviewApproved   ReviewState = "approved"
	ReviewRejected   ReviewState = "rejected"
	ReviewIneligible ReviewState = "ineligible"
)

type PlanState string

const (
	PlanPendingReview         PlanState = "pending_review"
	PlanPartiallyApproved     PlanState = "partially_approved"
	PlanApproved              PlanState = "approved"
	PlanRejected              PlanState = "rejected"
	PlanDispatching           PlanState = "dispatching"
	PlanNeedsAttention        PlanState = "needs_attention"
	PlanCompletedWithFailures PlanState = "completed_with_failures"
	PlanCompleted             PlanState = "completed"
)

type ExecutionState string

const (
	ExecutionNotAccepted      ExecutionState = "not_accepted"
	ExecutionAccepted         ExecutionState = "accepted"
	ExecutionQueued           ExecutionState = "queued"
	ExecutionAttempted        ExecutionState = "attempted"
	ExecutionProviderAccepted ExecutionState = "provider_accepted"
	ExecutionRetryableFailed  ExecutionState = "retryable_failed"
	ExecutionOutcomeUnknown   ExecutionState = "outcome_unknown"
	ExecutionReconciled       ExecutionState = "reconciled"
	ExecutionFinalFailed      ExecutionState = "final_failed"
	ExecutionDeliveryProven   ExecutionState = "delivery_proven"
)

type Plan struct {
	ID                  PlanID
	Name                string
	SourceKind          string
	SourceDigest        effectport.Digest
	State               PlanState
	Version             int64
	TargetCount         int
	PendingCount        int
	ApprovedCount       int
	RejectedCount       int
	IneligibleCount     int
	NeedsAttentionCount int
	CreatedBy           int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Recipient struct {
	ID               RecipientID
	PlanID           PlanID
	CustomerID       customerdomain.CustomerID
	CustomerName     string
	OneIDLabel       string
	StaffID          int64
	StaffDisplayName string
	ReviewState      ReviewState
	ExecutionState   ExecutionState
	Version          int64
	ContentVersionID ContentVersionID
	EffectID         string
	UpdatedAt        time.Time
}

type ContentVersion struct {
	ID          ContentVersionID
	RecipientID RecipientID
	Version     int64
	Digest      effectport.Digest
	Blocks      []ContentBlock
	CreatedAt   time.Time
}

type PlanListQuery struct {
	Keyword string
	State   PlanState
	Cursor  string
	Limit   int
}

type PlanPage struct {
	Items      []Plan
	NextCursor string
}

type RecipientPageQuery struct {
	PlanID PlanID
	State  ReviewState
	Cursor string
	Limit  int
}

type RecipientPage struct {
	Items      []Recipient
	NextCursor string
}

type Reader interface {
	ListPlans(context.Context, PlanListQuery) (PlanPage, error)
	GetPlan(context.Context, PlanID) (Plan, error)
	ListRecipients(context.Context, RecipientPageQuery) (RecipientPage, error)
	GetRecipient(context.Context, PlanID, RecipientID) (Recipient, ContentVersion, error)
}

type UpdateContentCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	RecipientID     RecipientID
	ExpectedVersion int64
	Blocks          []ContentBlock
}

type ReviewRecipientCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	RecipientID     RecipientID
	ExpectedVersion int64
	Decision        ReviewState
	Reason          string
}

type PreviewApprovalCommand struct {
	Actor           Actor
	PlanID          PlanID
	ExpectedVersion int64
}

type ApprovalPreview struct {
	PlanID        PlanID
	PlanVersion   int64
	EligibleCount int
	PreviewDigest effectport.Digest
}

type ApprovePlanCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	ExpectedVersion int64
	PreviewDigest   effectport.Digest
}

type RejectPlanCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	ExpectedVersion int64
	Reason          string
}

type ReconcileEffectCommand struct {
	Actor          Actor
	IdempotencyKey string
	EffectID       string
	Generation     int64
	Fence          int64
	EvidenceDigest effectport.Digest
	Reason         string
}

type Reviewer interface {
	UpdateContent(context.Context, UpdateContentCommand) (ContentVersion, error)
	ReviewRecipient(context.Context, ReviewRecipientCommand) (Recipient, error)
	PreviewApproval(context.Context, PreviewApprovalCommand) (ApprovalPreview, error)
	ApprovePlan(context.Context, ApprovePlanCommand) (Plan, error)
	RejectPlan(context.Context, RejectPlanCommand) (Plan, error)
	ReconcileEffect(context.Context, ReconcileEffectCommand) (Recipient, error)
}
