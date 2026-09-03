package port

import (
	"context"
	"time"
)

// CompletionIntent contains only opaque references and digests. It cannot
// carry a URL, credential, external identity, phone number, answer or message
// body across the Survey boundary.
type CompletionIntent struct {
	QuestionnaireID        ID
	SubmissionID           ID
	ConfigurationReference string
	SourceDigest           string
	TargetDigest           string
	PayloadDigest          string
	PolicyDigest           string
	IdempotencyKey         string
	ScheduledAt            time.Time
}

type EffectBinding struct {
	EffectID string
	State    string
}

// CompletionIntentAccepter must join the caller's PostgreSQL transaction.
// Provider execution is explicitly outside this contract and transaction.
type CompletionIntentAccepter interface {
	AcceptCompletionWithin(context.Context, CompletionIntent) (EffectBinding, error)
}
