package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// ScopedUnionID is deliberately the only HXC identity accepted by this port.
// It cannot carry a phone, OpenID, or an instruction to provision or merge.
type ScopedUnionID struct {
	Position int
	Scope    string
	UnionID  string
}

type ScopedUnionIDResult struct {
	Position   int
	Status     ResolveStatus
	CustomerID customerdomain.CustomerID
}

type HXCUnionIDBatchResolver interface {
	ResolveHXCUnionIDs(context.Context, []ScopedUnionID) ([]ScopedUnionIDResult, error)
}
