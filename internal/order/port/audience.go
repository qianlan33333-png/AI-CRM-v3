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
	PaidAt      time.Time
}

type PaidAudienceReader interface {
	PaidAudienceOrders(context.Context, time.Time) ([]PaidAudienceOrder, error)
}
