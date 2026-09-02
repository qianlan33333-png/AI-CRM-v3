// Package port freezes the public Identity boundary.
package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type ResolveStatus string

const (
	ResolveFound    ResolveStatus = "found"
	ResolveNotFound ResolveStatus = "not_found"
	ResolveConflict ResolveStatus = "conflict"
)

type ResolveResult struct {
	Status     ResolveStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
}

type Resolver interface {
	Resolve(context.Context, identitydomain.Reference) (ResolveResult, error)
}

type ProvisionCommand struct {
	Fact           identitydomain.VerifiedFact
	IdempotencyKey string
}

type ProvisionResult struct {
	CustomerID customerdomain.CustomerID
	IdentityID int64
	Created    bool
}

type VerifiedProvisioner interface {
	ProvisionVerifiedIdentity(context.Context, ProvisionCommand) (ProvisionResult, error)
}
