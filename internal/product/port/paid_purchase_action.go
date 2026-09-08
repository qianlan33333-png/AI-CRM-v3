package port

import (
	"context"
	"time"
)

// PaidPurchaseActionMode is the buyer-facing action frozen when an eligible
// native checkout first settles. It is deliberately limited to locally
// rendered QR guidance and a safe redirect; neither mode is a Provider write.
type PaidPurchaseActionMode string

const (
	PaidPurchaseActionNone     PaidPurchaseActionMode = "none"
	PaidPurchaseActionQR       PaidPurchaseActionMode = "qr"
	PaidPurchaseActionRedirect PaidPurchaseActionMode = "redirect"
)

// PaidPurchaseAction is a Product-owned immutable snapshot. Payment authorizes
// the caller before exposing it, so this type does not carry a Customer,
// identity, session, Provider identifier, or External Effect payload.
type PaidPurchaseAction struct {
	OrderPaidEventID int64
	OrderID          int64
	ProductID        ID
	ProductVersion   int64
	SourceDigest     [32]byte `json:"-"`
	Enabled          bool
	Mode             PaidPurchaseActionMode
	LeadChannelID    int64
	LeadQRTitle      string
	LeadQRSubtitle   string
	RedirectURL      string
	TagState         string
	CreatedAt        time.Time
}

// PaidPurchaseActionReader is bound at composition to Payment's authenticated
// checkout-status route. Product owns the stored snapshot; Payment remains the
// sole owner of the trusted payer-session authorization.
type PaidPurchaseActionReader interface {
	ReadPaidPurchaseAction(context.Context, int64) (PaidPurchaseAction, error)
}
