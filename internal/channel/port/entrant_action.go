package port

import (
	"context"
	"encoding/json"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type WelcomeMaterialPlan struct {
	ImageIDs, MiniProgramIDs, AttachmentIDs, GroupInviteIDs []int64
}

// WelcomeMaterialSnapshotResolver is implemented by the Composition Root
// over Media's stable capture/freezer ports. It must be called in the same
// Unit of Work that accepts the entrant actions, so mutable library records
// can never be reopened by an Outbound worker.
type WelcomeMaterialSnapshotResolver interface {
	ResolveWelcomeMaterialSnapshot(context.Context, WelcomeMaterialPlan, time.Time) (json.RawMessage, string, error)
}

type EntrantActionCommand struct {
	CallbackID      string
	CustomerID      customerdomain.CustomerID
	Resolution      channeldomain.StateResolution
	WelcomeGrantRef string
	OccurredAt      time.Time
}

type EntrantActionAccepter interface {
	AcceptEntrantActions(context.Context, EntrantActionCommand) error
}

type PublishedEntrantAction struct {
	ActionID, ChannelID, ConfigVersion, CustomerID, StaffID int64
	Kind, EffectRef, WelcomeGrantRef, WelcomeMessage        string
	WelcomeMaterialSnapshot                                 json.RawMessage
	LocalTagID                                              int64
}

type PublishedEntrantActionReader interface {
	ReadPublishedEntrantAction(context.Context, string) (PublishedEntrantAction, error)
}

type EntrantActionCompletion struct {
	EffectRef, State, ResultDigest string
	Attempt                        int32
	CompletedAt                    time.Time
}

type EntrantActionCompletionWriter interface {
	CompleteEntrantAction(context.Context, EntrantActionCompletion) error
}
