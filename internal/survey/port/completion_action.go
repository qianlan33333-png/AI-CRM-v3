package port

import (
	"context"
	"encoding/json"
)

// PublicCompletionTargetResolver is composition-owned. Its input is only the
// Survey-owned opaque navigation reference; an implementation must enforce the
// allowlist and return a public HTTPS destination. It must never expose or
// reuse an outbound completion endpoint, its credentials, or its parameters.
type PublicCompletionTargetResolver interface {
	ResolvePublicCompletionTarget(context.Context, string) (url string, found bool, err error)
}

type CompletionEndpointManager interface {
	ReadSurveyCompletionEndpointWithin(context.Context, ID, string) (string, error)
	SaveSurveyCompletionEndpointWithin(context.Context, ID, string, string, json.RawMessage) (string, error)
}
