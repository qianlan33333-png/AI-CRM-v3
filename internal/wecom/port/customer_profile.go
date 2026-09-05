package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// Customer profile observations intentionally retain Provider identifiers only
// inside the WeCom boundary. Composition adapters must replace them with safe
// display names before returning Customer-domain DTOs.
type OwnerObservation struct {
	EmployeeID string
	Status     string
	ObservedAt time.Time
}

type TagObservation struct {
	ProviderTagID string
	ObservedName  string
	ProviderType  int16
	Status        string
	ObservedAt    time.Time
}

type CustomerProfileObservationReader interface {
	CustomerOwnerObservations(context.Context, customerdomain.CustomerID) ([]OwnerObservation, error)
	CustomerTagObservations(context.Context, customerdomain.CustomerID) ([]TagObservation, error)
}

// AudiencePrimaryOwner is the provider userid selected from a completed,
// trusted directory scope for a canonical customer.  Ambiguous means active
// provider scopes disagree and must never be resolved by choosing a row.
type AudiencePrimaryOwner struct {
	CustomerID  customerdomain.CustomerID
	CorpScope   string
	OwnerUserID string
	Status      string // known, unknown, ambiguous
}

// AudiencePrimaryOwnerReader is a bulk, read-only audience fact port.  It
// retains the provider corp scope inside WeCom and exposes only canonical
// CustomerIDs plus the provider userid needed for owner filtering.
type AudiencePrimaryOwnerReader interface {
	AudiencePrimaryOwners(context.Context, []customerdomain.CustomerID) ([]AudiencePrimaryOwner, error)
}
