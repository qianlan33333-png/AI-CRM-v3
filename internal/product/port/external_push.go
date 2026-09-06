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
	ProductName            string                  `json:"-"`
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
	AttemptCount             int32                   `json:"attempt_count"`
	ProviderAccepted         bool                    `json:"provider_accepted"`
	DeliveryProven           bool                    `json:"delivery_proven"`
	RealExternalCallExecuted bool                    `json:"real_external_call_executed"`
	AutoRetryAllowed         bool                    `json:"auto_retry_allowed"`
	CreatedAt                time.Time               `json:"created_at"`
	UpdatedAt                time.Time               `json:"updated_at"`
}

// ExternalPushTestStatus is an Outbound-owned, digest-safe delivery
// projection for a Product-owned test binding. It carries no endpoint, signed
// body, identity value, Provider response, or retry control. An executed call
// is never presented as delivery proof.
type ExternalPushTestStatus struct {
	EffectID                 string    `json:"effect_id"`
	State                    string    `json:"state"`
	AttemptCount             int32     `json:"attempt_count"`
	ProviderCallAttempted    bool      `json:"provider_call_attempted"`
	RealExternalCallExecuted bool      `json:"real_external_call_executed"`
	ProviderResultReceived   *bool     `json:"provider_result_received"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type CommerceExternalPushApplication interface {
	GetExternalPushConfiguration(context.Context, ID, ExternalPushProductKind) (ExternalPushConfiguration, error)
	SaveExternalPushConfiguration(context.Context, SaveExternalPushConfigurationCommand) (ExternalPushConfiguration, error)
	QueueExternalPushTest(context.Context, QueueExternalPushTestCommand) (ExternalPushTest, error)
	ListExternalPushTests(context.Context, ID, ExternalPushProductKind) ([]ExternalPushTest, error)
}

// ExternalPushConfigurationReader is the Product-owned read boundary used by
// Outbound for a frozen Order item. The read locks the Product row used by
// configuration writes for the caller's Unit of Work, so the first paid event freezes one
// revision before a concurrent administrator update can take effect. Product
// URLs, credentials, and mutable product rows do not cross the boundary.
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

// ExternalPushTestStatusReader is implemented by Outbound. Product reads this
// only after loading its own immutable test binding; it never reads Outbound
// tables or controls retry or reconciliation.
type ExternalPushTestStatusReader interface {
	ReadExternalPushTestStatus(context.Context, ID, string) (ExternalPushTestStatus, error)
}
