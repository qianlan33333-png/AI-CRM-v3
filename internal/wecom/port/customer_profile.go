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
