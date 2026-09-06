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
	Revision               int64                   `json:"revision"`
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

// ExternalPushConfigurationReader is the Product-owned read boundary used by
// Outbound for a frozen Order item. It exposes only the opaque target choice
// and its local revision; Product URLs, credentials, and mutable product rows
// do not cross the boundary.
type ExternalPushConfigurationReader interface {
	ReadExternalPushConfigurationForOrder(context.Context, ID) (ExternalPushConfiguration, error)
}

// ExternalPushTestIntent is Product's opaque synthetic-operation handoff to
// Outbound. The Product receipt key digest prevents a different administrator
// operation from being mistaken for a replay; Provider details remain Outbound
// owned.
type ExternalPushTestIntent struct {
	ProductID              ID
	ProductKind            ExternalPushProductKind
	ConfigurationReference string
	ConfigurationRevision  int64
	ReceiptKeyDigest       [32]byte
}

// ExternalPushTestAccepter joins the current Product Unit of Work. A returned
// accepted/queued state is only an EER local fact; it is never a Provider
// receipt or delivery claim.
type ExternalPushTestAccepter interface {
	AcceptExternalPushTestWithin(context.Context, ExternalPushTestIntent) (ExternalPushTest, error)
}
