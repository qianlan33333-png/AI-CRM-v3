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
