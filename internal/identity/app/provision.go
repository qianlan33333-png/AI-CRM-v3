// Package app coordinates Identity-owned use cases.
package app

import (
	"errors"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrInvalidService = errors.New("invalid identity service")

// Service is intentionally incomplete at bootstrap. Store, Customer and Event
// ports are added only with the executable GJ-01 contract.
type Service struct {
	UnitOfWork platformport.UnitOfWork
}

func ValidateVerifiedReference(reference identitydomain.Reference) error {
	normalized, err := identitydomain.Normalize(reference)
	if err != nil || normalized.Assurance != identitydomain.AssuranceVerified {
		return identitydomain.ErrInvalidReference
	}
	return nil
}
