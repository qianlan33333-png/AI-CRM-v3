package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// InsertCheckoutSnapshot persists the immutable native-checkout fact in the
// caller's Order transaction. It deliberately does not reference Product or
// Coupon tables: their later lifecycle changes cannot rewrite the sale.
func (r *Repository) InsertCheckoutSnapshot(ctx context.Context, snapshot orderport.CheckoutSnapshot) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if !validCheckoutSnapshot(snapshot) {
		return ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO order_checkout_snapshots(
order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,
gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,
coupon_claim_id,coupon_id,coupon_rule_version,reserved_at,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`,
		snapshot.OrderID, snapshot.ProductType, snapshot.ProductID, snapshot.ProductCode, snapshot.ProductName,
		snapshot.ProductVersion, snapshot.ServicePeriodDurationDays, snapshot.GrossAmountMinor, snapshot.DiscountAmountMinor,
		snapshot.PayableAmountMinor, snapshot.Currency, snapshot.CouponApplied, snapshot.CouponReservationRef,
		nullableCheckoutID(snapshot.CouponClaimID), nullableCheckoutID(snapshot.CouponID), nullableCheckoutID(snapshot.CouponRuleVersion), snapshot.ReservedAt.UTC())
	return mapError(err)
}

func (r *Repository) ReadCheckoutSnapshot(ctx context.Context, orderID int64) (orderport.CheckoutSnapshot, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return orderport.CheckoutSnapshot{}, err
	}
	if orderID < 1 {
		return orderport.CheckoutSnapshot{}, orderport.ErrNotFound
	}
	var snapshot orderport.CheckoutSnapshot
	err = tx.QueryRow(ctx, `SELECT order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,
gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,
COALESCE(coupon_claim_id,0),COALESCE(coupon_id,0),COALESCE(coupon_rule_version,0),reserved_at
FROM order_checkout_snapshots WHERE order_id=$1`, orderID).Scan(
		&snapshot.OrderID, &snapshot.ProductType, &snapshot.ProductID, &snapshot.ProductCode, &snapshot.ProductName,
		&snapshot.ProductVersion, &snapshot.ServicePeriodDurationDays, &snapshot.GrossAmountMinor, &snapshot.DiscountAmountMinor,
		&snapshot.PayableAmountMinor, &snapshot.Currency, &snapshot.CouponApplied, &snapshot.CouponReservationRef,
		&snapshot.CouponClaimID, &snapshot.CouponID, &snapshot.CouponRuleVersion, &snapshot.ReservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return orderport.CheckoutSnapshot{}, orderport.ErrNotFound
	}
	if err != nil {
		return orderport.CheckoutSnapshot{}, mapError(err)
	}
	if !validCheckoutSnapshot(snapshot) {
		return orderport.CheckoutSnapshot{}, orderport.ErrUnavailable
	}
	return snapshot, nil
}

func nullableCheckoutID(value int64) any {
	if value < 1 {
		return nil
	}
	return value
}

func validCheckoutSnapshot(snapshot orderport.CheckoutSnapshot) bool {
	if snapshot.OrderID < 1 || snapshot.ProductID < 1 || snapshot.ProductCode == "" || len(snapshot.ProductCode) > 200 || snapshot.ProductName == "" || len(snapshot.ProductName) > 500 || snapshot.ProductVersion < 1 || snapshot.GrossAmountMinor < 1 || snapshot.PayableAmountMinor < 1 || snapshot.DiscountAmountMinor < 0 || snapshot.PayableAmountMinor != snapshot.GrossAmountMinor-snapshot.DiscountAmountMinor || snapshot.Currency != "CNY" || snapshot.ReservedAt.IsZero() {
		return false
	}
	if (snapshot.ProductType == "standard_product" && snapshot.ServicePeriodDurationDays != 0) || (snapshot.ProductType == "service_period" && snapshot.ServicePeriodDurationDays < 1) || (snapshot.ProductType != "standard_product" && snapshot.ProductType != "service_period") {
		return false
	}
	if !snapshot.CouponApplied {
		return snapshot.CouponReservationRef == "" && snapshot.CouponClaimID == 0 && snapshot.CouponID == 0 && snapshot.CouponRuleVersion == 0 && snapshot.DiscountAmountMinor == 0 && snapshot.PayableAmountMinor == snapshot.GrossAmountMinor
	}
	return snapshot.CouponReservationRef != "" && len(snapshot.CouponReservationRef) <= 240 && snapshot.CouponClaimID > 0 && snapshot.CouponID > 0 && snapshot.CouponRuleVersion > 0 && snapshot.DiscountAmountMinor > 0 && snapshot.DiscountAmountMinor < snapshot.GrossAmountMinor
}
