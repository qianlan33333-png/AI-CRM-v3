package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// PaidAudienceOrder is the minimum immutable business fact needed by the
// legacy paid-order audience condition. It intentionally has no beneficiary
// or provider identifiers.
type PaidAudienceOrder struct {
	CustomerID  customerdomain.CustomerID
	ProductCode string
	// PaidAt is nil when this historical order has no trustworthy payment-time
	// evidence. It can match an unbounded paid audience, never a time window.
	PaidAt *time.Time
}

type PaidAudienceReader interface {
	PaidAudienceOrders(context.Context, time.Time) ([]PaidAudienceOrder, error)
}
