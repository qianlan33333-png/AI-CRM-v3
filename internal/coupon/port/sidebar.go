package port

import (
	"context"
	"time"
)

type CustomerCoupon struct {
	ClaimID       int64      `json:"claim_id"`
	CouponID      int64      `json:"coupon_id"`
	Name          string     `json:"name"`
	DiscountMinor int64      `json:"discount_minor"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	ClaimNoMasked string     `json:"claim_no_masked,omitempty"`
	ClaimedAt     time.Time  `json:"claimed_at"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	RedeemedAt    *time.Time `json:"redeemed_at,omitempty"`
}

type CustomerCouponPage struct {
	Items []CustomerCoupon `json:"items"`
	Total int64            `json:"total"`
}

type CustomerCouponReader interface {
	ListCustomerCoupons(context.Context, int64, int32) (CustomerCouponPage, error)
}

// AdminCouponClaim is a masked, local Customer projection for the frozen
// coupon-data page. It exposes no channel identifier, payment or order fact.
type AdminCouponClaim struct {
	ClaimID       int64      `json:"claim_id"`
	CustomerID    int64      `json:"customer_id"`
	CouponID      int64      `json:"coupon_id"`
	Status        string     `json:"status"`
	ClaimNoMasked string     `json:"claim_no_masked,omitempty"`
	ClaimedAt     time.Time  `json:"claimed_at"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	RedeemedAt    *time.Time `json:"redeemed_at,omitempty"`
}

type AdminCouponClaimPage struct {
	Items  []AdminCouponClaim `json:"items"`
	Total  int64              `json:"total"`
	Limit  int32              `json:"limit"`
	Offset int32              `json:"offset"`
}

// CouponClaimAdminReader is Coupon-owned. It is deliberately separate from
// the Customer sidebar reader so an admin page cannot infer customer identity.
type CouponClaimAdminReader interface {
	ListCouponClaims(context.Context, ID, int32, int32) (AdminCouponClaimPage, error)
}

type HistoricalCustomerCoupon struct {
	SourceSystem  string
	SourceKey     string
	CustomerID    int64
	CouponID      int64
	Status        string
	ClaimNoMasked string
	ClaimedAt     time.Time
	ValidFrom     *time.Time
	ValidUntil    *time.Time
	RedeemedAt    *time.Time
	SourceDigest  [32]byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type HistoricalCustomerCouponImporter interface {
	ImportHistoricalCustomerCoupon(context.Context, HistoricalCustomerCoupon) (CustomerCoupon, bool, error)
}
