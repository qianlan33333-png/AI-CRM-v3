package port

import (
	"context"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"time"
)

type AudienceEntry struct {
	CustomerID                    customerdomain.CustomerID
	ChannelID                     int64
	OwnerReference                string
	FirstEnteredAt, LastEnteredAt time.Time
}
type AudienceEntryReader interface {
	AudienceEntries(context.Context, time.Time) ([]AudienceEntry, error)
}
