// Package port contains stable Customer-domain boundaries.
package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type DirectoryProjection struct {
	CustomerID      customerdomain.CustomerID
	CustomerStatus  customerdomain.Status
	DisplayName     string
	AvatarURL       string
	Gender          int16
	ContactType     int16
	CorpName        string
	OneIDLabel      string
	PhoneMasked     string
	PhoneAssurance  identitydomain.Assurance
	ActivationState string
	Source          string
	SourceVersion   int64
	LastSyncedAt    time.Time
	UpdatedAt       time.Time
}

// ProjectionWriter is called by versioned event consumers. It does not grant
// access to Identity or WeCom-owned tables.
type ProjectionWriter interface {
	UpsertDirectoryProjection(context.Context, DirectoryProjection) error
	MarkDirectoryStale(context.Context, []customerdomain.CustomerID, time.Time) (int64, error)
	UpdateDirectoryPhone(context.Context, customerdomain.CustomerID, string, identitydomain.Assurance, int64, time.Time) error
	ClearDirectoryPhone(context.Context, customerdomain.CustomerID, time.Time) error
}

// CallbackProjectionWriter exposes only the narrow mutation required by a
// verified WeCom callback. It cannot write profile or phone attributes.
type CallbackProjectionWriter interface {
	ActivateDirectoryCustomer(context.Context, customerdomain.CustomerID, string, time.Time) error
}
