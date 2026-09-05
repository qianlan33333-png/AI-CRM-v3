package port

import (
	"context"
	"errors"
)

var ErrInvalidProductOptionQuery = errors.New("invalid product option query")

// ProductOptionType is the only product classification exposed to another
// domain for selecting a Product target. It is deliberately narrower than
// Product and contains no customer, order, entitlement, or provider facts.
type ProductOptionType string

const (
	ProductOptionStandard      ProductOptionType = "standard"
	ProductOptionServicePeriod ProductOptionType = "service_period"
	ProductOptionAll           ProductOptionType = "all"
	ProductOptionDefaultLimit                    = int32(50)
	ProductOptionMaximumLimit                    = int32(100)
	ProductOptionMaximumOffset                   = int32(1_000_000)
)

// ProductOptionQuery is a bounded read request for another domain's
// selection UI. ProductType may be standard, service_period, or all; an
// empty type is normalized to all by the Product application. q matches the
// Product code or display name and is never interpreted as SQL.
type ProductOptionQuery struct {
	Q           string            `json:"q,omitempty"`
	ProductType ProductOptionType `json:"product_type,omitempty"`
	Limit       int32             `json:"limit,omitempty"`
	Offset      int32             `json:"offset,omitempty"`
}

// ProductOption is the minimum target-selection projection. Prices are local
// CNY minor units: non-CNY rows are not eligible for this cross-domain
// projection and are omitted by the Product-owned implementation.
type ProductOption struct {
	ID          ID                `json:"id"`
	ProductType ProductOptionType `json:"product_type"`
	Name        string            `json:"name"`
	PriceMinor  int64             `json:"price_minor"`
	Currency    string            `json:"currency"`
}

type ProductOptionPage struct {
	Items  []ProductOption `json:"items"`
	Total  int64           `json:"total"`
	Limit  int32           `json:"limit"`
	Offset int32           `json:"offset"`
}

// ProductOptionReader is the canonical cross-domain Product port. Consumers
// must not import Product app/store/http packages or query products directly.
// The implementation owns filtering, pagination, currency eligibility, and
// the stable projection.
type ProductOptionReader interface {
	ListProductOptions(context.Context, ProductOptionQuery) (ProductOptionPage, error)
}

// ProductTargetReader validates one already-selected local Product target for
// a rule in another domain.  It intentionally returns the same bounded
// projection as the chooser: consumers cannot read Product tables or infer
// lifecycle, order, entitlement, or provider state.
type ProductTargetReader interface {
	ReadProductTarget(context.Context, ProductOptionType, ID) (ProductOption, error)
}

// CheckoutProduct is the immutable minimum required to freeze a sale into an
// Order. The Within method requires the caller's existing PostgreSQL UoW so a
// concurrent lifecycle/price change cannot be observed across transactions.
type CheckoutProduct struct {
	ID            ID
	ProductType   ProductOptionType
	Code          string
	Name          string
	PriceMinor    int64
	Currency      string
	Version       int64
	RequireMobile bool
	// Images are public detail media owned by Product; checkout persists none of them.
	Images []string
	// ServicePeriodDurationDays is positive only for a service_period item.
	// It is frozen by the Order checkout snapshot before payment begins.
	ServicePeriodDurationDays int32
}

type CheckoutProductReader interface {
	ReadCheckoutProductWithin(context.Context, ProductOptionType, ID) (CheckoutProduct, error)
}
