package port

import "context"

// PublicCompletionTargetResolver is composition-owned. Its input is only the
// Survey-owned opaque navigation reference; an implementation must enforce the
// allowlist and return a public HTTPS destination. It must never expose or
// reuse an outbound completion endpoint, its credentials, or its parameters.
type PublicCompletionTargetResolver interface {
	ResolvePublicCompletionTarget(context.Context, string) (url string, found bool, err error)
}
