package port

import (
	"context"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

const (
	DefaultLimit int32 = 20
	MaximumLimit int32 = 100
)

type ListQuery struct {
	Search        string
	ContentType   radar.ContentType
	Status        radar.Status
	AuthPolicy    radar.AuthPolicy
	Limit         int32
	Offset        int32
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type LinkSummary struct {
	Link            radar.Link `json:"link"`
	TotalLandings   int64      `json:"total_landings"`
	AuthorizedUsers int64      `json:"authorized_users"`
	ViewCount       int64      `json:"view_count"`
	LastViewedAt    *time.Time `json:"last_viewed_at,omitempty"`
}

type LinkPage struct {
	Items   []LinkSummary `json:"items"`
	Total   int64         `json:"total"`
	Limit   int32         `json:"limit"`
	Offset  int32         `json:"offset"`
	HasMore bool          `json:"has_more"`
}

type LinkDetail struct {
	Link  radar.Link `json:"link"`
	Stats Stats      `json:"stats"`
}

type Stats struct {
	TotalLandings   int64   `json:"total_landings"`
	AuthorizedUsers int64   `json:"authorized_users"`
	ViewCount       int64   `json:"view_count"`
	ConversionRate  float64 `json:"conversion_rate"`
}

type AttributionStatus string

const (
	AttributionAnonymous AttributionStatus = "anonymous"
	AttributionResolved  AttributionStatus = "resolved"
	AttributionPending   AttributionStatus = "pending"
	AttributionConflict  AttributionStatus = "conflict"
	AttributionFailed    AttributionStatus = "failed"
)

type EventStage string

const (
	EventLanding          EventStage = "landing"
	EventOAuthStarted     EventStage = "oauth_started"
	EventOAuthVerified    EventStage = "oauth_verified"
	EventIdentityResolved EventStage = "identity_resolved"
	EventContentOpened    EventStage = "content_opened"
	EventRedirected       EventStage = "redirected"
	EventImageLoaded      EventStage = "image_loaded"
	EventPDFOpened        EventStage = "pdf_opened"
	EventFailed           EventStage = "failed"
)

// EventProjection intentionally contains no raw UnionID, OpenID,
// external_userid, phone, IP, user agent, referrer, OAuth code or token.
type EventProjection struct {
	ReceiptID       string            `json:"receipt_id"`
	RadarID         radar.RadarID     `json:"radar_id"`
	Version         radar.LinkVersion `json:"version"`
	Stage           EventStage        `json:"stage"`
	Attribution     AttributionStatus `json:"attribution"`
	CustomerRef     string            `json:"customer_ref,omitempty"`
	CustomerDisplay string            `json:"customer_display,omitempty"`
	OccurredAt      time.Time         `json:"occurred_at"`
}

type EventQuery struct {
	RadarID     radar.RadarID
	Stage       EventStage
	Attribution AttributionStatus
	Start       *time.Time
	End         *time.Time
	Limit       int32
	Offset      int32
}

type EventPage struct {
	Items   []EventProjection `json:"items"`
	Total   int64             `json:"total"`
	Limit   int32             `json:"limit"`
	Offset  int32             `json:"offset"`
	HasMore bool              `json:"has_more"`
}

type QueryService interface {
	Stats(context.Context, radar.RadarID) (Stats, error)
	Events(context.Context, EventQuery) (EventPage, error)
}
