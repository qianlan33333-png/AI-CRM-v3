package port

import "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"

type HistoricalVerifiedInput struct {
	Kind, Scope, Value, Source string
}

// HistoricalFactFactory is implemented only by an audited provider-history
// adapter. Migration orchestration cannot mint verified facts itself.
type HistoricalFactFactory interface {
	VerifiedHistoricalFact(HistoricalVerifiedInput) (domain.VerifiedFact, error)
}
