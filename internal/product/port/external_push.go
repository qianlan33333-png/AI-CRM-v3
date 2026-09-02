package port

import (
	"context"
	"time"
)

// ExternalPushProductKind distinguishes the two CRM-local product projections
// that can own the same closed commerce external-push configuration. It does
// not identify a provider, destination, URL, credential, or payload.
type ExternalPushProductKind string

const (
	ExternalPushWeChatPay     ExternalPushProductKind = "wechat_pay"
	ExternalPushServicePeriod ExternalPushProductKind = "service_period"
)

// ExternalPushConfiguration is a Product-owned local choice. The reference
// is an opaque local handle only; URLs, secrets, targets and retry policy are
// deliberately outside this contract.
type ExternalPushConfiguration struct {
	ProductID              ID                      `json:"product_id"`
	ProductKind            ExternalPushProductKind `json:"product_kind"`
	Enabled                bool                    `json:"enabled"`
	ConfigurationReference string                  `json:"configuration_reference,omitempty"`
	UpdatedAt              time.Time               `json:"updated_at"`
}

type SaveExternalPushConfigurationCommand struct {
	ProductID              ID
	ProductKind            ExternalPushProductKind
	Enabled                bool
	ConfigurationReference string
	Actor                  int64
	IdempotencyKey         string
}

type QueueExternalPushTestCommand struct {
	ProductID      ID
	ProductKind    ExternalPushProductKind
	Actor          int64
	IdempotencyKey string
}

// ExternalPushTest is a local EER acceptance projection. State=accepted or
// queued is never evidence of Provider acceptance or delivery.
type ExternalPushTest struct {
	ProductID                ID                      `json:"product_id"`
	ProductKind              ExternalPushProductKind `json:"product_kind"`
	EffectID                 string                  `json:"effect_id"`
	State                    string                  `json:"state"`
	ProviderAccepted         bool                    `json:"provider_accepted"`
	DeliveryProven           bool                    `json:"delivery_proven"`
	RealExternalCallExecuted bool                    `json:"real_external_call_executed"`
	AutoRetryAllowed         bool                    `json:"auto_retry_allowed"`
	CreatedAt                time.Time               `json:"created_at"`
}

type CommerceExternalPushApplication interface {
	GetExternalPushConfiguration(context.Context, ID, ExternalPushProductKind) (ExternalPushConfiguration, error)
	SaveExternalPushConfiguration(context.Context, SaveExternalPushConfigurationCommand) (ExternalPushConfiguration, error)
	QueueExternalPushTest(context.Context, QueueExternalPushTestCommand) (ExternalPushTest, error)
}
