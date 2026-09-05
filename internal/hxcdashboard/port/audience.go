package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

// AudienceMemberFact is a current, published HXC fact already resolved to a
// canonical customer. It is not a raw HXC identity or chat archive record.
type AudienceMemberFact struct {
	CustomerID            customerdomain.CustomerID
	Registered, IsMember  bool
	Tier, Status          string
	ExpiresAt, LastUsedAt *time.Time
	SourceUpdatedAt       time.Time
}
type AudienceMemberReader interface {
	AudienceMemberFacts(context.Context, time.Time) ([]AudienceMemberFact, error)
}
