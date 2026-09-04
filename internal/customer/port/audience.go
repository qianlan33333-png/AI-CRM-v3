package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

// AudienceReader is a Customer-owned, read-only projection boundary. It emits
// canonical-looking local IDs only; Segment still resolves aliases before use.
type AudienceReader interface {
	ActiveWithin(context.Context, time.Time, int) ([]customerdomain.CustomerID, time.Time, error)
}
