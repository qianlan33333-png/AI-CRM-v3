package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type (
	PolicyID       int64
	PolicyVersion  int64
	RunID          int64
	RunRecipientID int64
)

type TriggerKind string

const (
	TriggerAudienceMemberEnteredV1 TriggerKind = "audience.member_entered.v1"
	TriggerCustomerTagAppliedV1    TriggerKind = "customer.tag_applied.v1"
)

type ActionKind string

const (
	ActionRecord          ActionKind = "record"
	ActionOutboundMessage ActionKind = "outbound_message"
)

type RunState string

const (
	RunPreparing      RunState = "preparing"
	RunReady          RunState = "ready"
	RunExecuting      RunState = "executing"
	RunCompleted      RunState = "completed"
	RunPartialFailed  RunState = "partial_failed"
	RunOutcomeUnknown RunState = "outcome_unknown"
	RunCancelled      RunState = "cancelled"
)

type RecipientState string

const (
	RecipientSkipped          RecipientState = "skipped"
	RecipientAccepted         RecipientState = "accepted"
	RecipientAttempted        RecipientState = "attempted"
	RecipientProviderAccepted RecipientState = "provider_accepted"
	RecipientDeliveryProven   RecipientState = "delivery_proven"
	RecipientRetryableFailed  RecipientState = "retryable_failed"
	RecipientFinalFailed      RecipientState = "final_failed"
	RecipientOutcomeUnknown   RecipientState = "outcome_unknown"
	RecipientReconciled       RecipientState = "reconciled"
)

type PublishedAgent struct {
	AgentID          AgentID
	AutomationType   AutomationType
	Status           AgentStatus
	PublishedVersion int64
	ContentDigest    [32]byte
	MaterialsDigest  [32]byte
}

type PublishedAgentReader interface {
	PublishedAgent(context.Context, AgentID) (PublishedAgent, bool, error)
}

type Run struct {
	ID                    RunID
	PolicyID              PolicyID
	PolicyVersion         PolicyVersion
	PackageID             segmentport.PackageID
	SnapshotID            segmentport.SnapshotID
	AgentID               AgentID
	AgentPublishedVersion int64
	PreviewDigest         [32]byte
	State                 RunState
	TargetCount           int64
	SkippedCount          int64
	CreatedAt             time.Time
}

type RunRecipient struct {
	ID         RunRecipientID
	RunID      RunID
	CustomerID customerdomain.CustomerID
	State      RecipientState
	EffectID   string
}

type RunReader interface {
	Run(context.Context, RunID) (Run, bool, error)
	RunRecipients(context.Context, RunID, string, int) ([]RunRecipient, string, error)
}
