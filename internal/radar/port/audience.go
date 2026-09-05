package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type AudienceFirstClick struct {
	CustomerID     customerdomain.CustomerID
	RadarID        int64
	FirstClickedAt time.Time
}
type AudienceFirstClickReader interface {
	AudienceFirstClicks(context.Context, time.Time) ([]AudienceFirstClick, error)
}
