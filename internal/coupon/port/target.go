package port

import "context"

// ProductRuleTarget is the only cross-domain fact Coupon needs to validate a
// product applicability rule before publication. It carries no customer,
// order, payment, entitlement, or Product store implementation detail.
type ProductRuleTarget struct {
	ID         int64
	Currency   string
	PriceMinor int64
}

// ProductReader is a temporary Coupon-owned compatibility port. Terra should
// adapt it to the canonical Product port at the composition boundary; Coupon
// must never import Product app/store/http packages or read Product tables.
type ProductReader interface {
	Get(context.Context, int64) (ProductRuleTarget, error)
}
