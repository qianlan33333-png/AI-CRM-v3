package port

import (
	"context"
	"time"
)

// LocalProductLifecycle is the CRM-local state used by the legacy WeChat-pay
// product controls. It deliberately says nothing about provider configuration,
// purchase availability, or payment/entitlement effects.
type LocalProductLifecycle string

const (
	LocalProductDraft    LocalProductLifecycle = "draft"
	LocalProductEnabled  LocalProductLifecycle = "enabled"
	LocalProductDisabled LocalProductLifecycle = "disabled"
)

// LocalProduct is a closed projection for local lifecycle operations. The
// legacy_admin_projection is intentionally not exposed as an opaque browser
// contract; the write service preserves it only through its typed Product
// repository boundary.
type LocalProduct struct {
	ID            ID                    `json:"id"`
	ProductCode   string                `json:"product_code"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	PriceMinor    int64                 `json:"price_minor"`
	Currency      string                `json:"currency"`
	StockQuantity int32                 `json:"stock_quantity"`
	Images        []string              `json:"images"`
	CreatedBy     int64                 `json:"created_by"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Lifecycle     LocalProductLifecycle `json:"lifecycle"`
	Enabled       bool                  `json:"enabled"`
	Version       int64                 `json:"version"`
}

type SetLocalProductEnabledCommand struct {
	ID              ID
	ExpectedVersion int64
	Enabled         bool
	Actor           int64
	IdempotencyKey  string
}

type CopyLocalProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

type DeleteLocalProductCommand struct {
	ID              ID
	ExpectedVersion int64
	Actor           int64
	IdempotencyKey  string
}

type DeleteLocalProductResult struct {
	ProductID ID   `json:"product_id"`
	Deleted   bool `json:"deleted"`
}

// LocalProductShare is deliberately closed. A false Available value must be
// accompanied by a reason; URL fields are only populated after an explicitly
// authoritative public-purchase route is wired by a later integration lane.
type LocalProductShare struct {
	ProductID   ID                    `json:"product_id"`
	ProductCode string                `json:"product_code"`
	Lifecycle   LocalProductLifecycle `json:"lifecycle"`
	Available   bool                  `json:"available"`
	Reason      string                `json:"reason"`
	PurchaseURL string                `json:"purchase_url,omitempty"`
	QRCodeURL   string                `json:"qr_code_url,omitempty"`
}

// LocalProductLifecycleApplication is the transport-neutral contract exposed
// to a legacy HTTP adapter. Implementations are local-only and must not call a
// payment provider or claim that a product is purchasable.
type LocalProductLifecycleApplication interface {
	SetLocalProductEnabled(context.Context, SetLocalProductEnabledCommand) (LocalProduct, error)
	CopyLocalProduct(context.Context, CopyLocalProductCommand) (LocalProduct, error)
	DeleteLocalProduct(context.Context, DeleteLocalProductCommand) (DeleteLocalProductResult, error)
	ShareLocalProduct(context.Context, ID) (LocalProductShare, error)
}
