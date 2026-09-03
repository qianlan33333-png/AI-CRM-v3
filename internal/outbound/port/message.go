// Package port defines the stable Outbound message boundary.
package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type MessageIntent struct {
	SourceKind            string
	SourceID              int64
	RunRecipientID        int64
	CustomerID            customerdomain.CustomerID
	IdentityID            int64
	SenderStaffID         int64
	AgentID               int64
	AgentPublishedVersion int64
	ContentReference      string
	SourceDigest          [32]byte
	TargetDigest          [32]byte
	PayloadDigest         [32]byte
	PolicyDigest          [32]byte
	ReceiptKey            string
}

type MessageAcceptance struct {
	MessageIntentID int64
	EffectID        string
	QueueReceiptID  string
	Replayed        bool
}

// TransactionalMessageAccepter writes the Outbound intent/binding and accepts
// the External Effect inside the caller's transaction-bound context.
type TransactionalMessageAccepter interface {
	AcceptMessageWithin(context.Context, MessageIntent) (MessageAcceptance, error)
}

type CompletionState string

const (
	CompletionProviderAccepted CompletionState = "provider_accepted"
	CompletionDeliveryProven   CompletionState = "delivery_proven"
	CompletionRetryableFailed  CompletionState = "retryable_failed"
	CompletionFinalFailed      CompletionState = "final_failed"
	CompletionOutcomeUnknown   CompletionState = "outcome_unknown"
	CompletionReconciled       CompletionState = "reconciled"
)

type MessageCompletion struct {
	EffectID      string
	State         CompletionState
	ReceiptDigest [32]byte
	AttemptCount  int32
}

type MessageCompletionProjector interface {
	ProjectMessageCompletion(context.Context, MessageCompletion) error
}
