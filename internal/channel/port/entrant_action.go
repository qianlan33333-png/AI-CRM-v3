package port

import (
	"context"
	"time"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

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
