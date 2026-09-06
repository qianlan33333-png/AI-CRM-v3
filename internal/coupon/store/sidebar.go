package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ListCustomerCoupons(ctx context.Context, customerID int64, limit int32) (couponport.CustomerCouponPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.CustomerCouponPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id WHERE claim.customer_id=$1 ORDER BY claim.claimed_at DESC,claim.id DESC LIMIT $2`, customerID, limit)
	if err != nil {
		return couponport.CustomerCouponPage{}, err
	}
	defer rows.Close()
	page := couponport.CustomerCouponPage{Items: []couponport.CustomerCoupon{}}
	for rows.Next() {
		var item couponport.CustomerCoupon
		if err = rows.Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE customer_id=$1`, customerID).Scan(&page.Total)
	return page, err
}

func (r *Repository) ListCouponClaims(ctx context.Context, couponID couponport.ID, limit, offset int32) (couponport.AdminCouponClaimPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.AdminCouponClaimPage{}, err
	}
	if couponID < 1 || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return couponport.AdminCouponClaimPage{}, couponapp.ErrInvalidCoupon
	}
	page := couponport.AdminCouponClaimPage{Items: []couponport.AdminCouponClaim{}, Limit: limit, Offset: offset}
	rows, err := tx.Query(ctx, `SELECT id,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at FROM coupon_customer_claims WHERE coupon_id=$1 ORDER BY claimed_at DESC,id DESC LIMIT $2 OFFSET $3`, couponID, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item couponport.AdminCouponClaim
		if err = rows.Scan(&item.ClaimID, &item.CustomerID, &item.CouponID, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1`, couponID).Scan(&page.Total)
	return page, err
}

func (r *Repository) ImportHistoricalCustomerCoupon(ctx context.Context, input couponport.HistoricalCustomerCoupon) (couponport.CustomerCoupon, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.CustomerCoupon{}, false, err
	}
	var item couponport.CustomerCoupon
	var digest []byte
	query := `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at,claim.source_digest FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id WHERE claim.source_system=$1 AND claim.source_key=$2 FOR UPDATE OF claim`
	err = tx.QueryRow(ctx, query, input.SourceSystem, input.SourceKey).Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt, &digest)
	if err == nil {
		if len(digest) != 32 || string(digest) != string(input.SourceDigest[:]) {
			return item, false, couponapp.ErrConflict
		}
		return item, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return item, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,redeemed_at,source_digest,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`, input.SourceSystem, input.SourceKey, input.CustomerID, input.CouponID, input.Status, input.ClaimNoMasked, input.ClaimedAt, input.ValidFrom, input.ValidUntil, input.RedeemedAt, input.SourceDigest[:], input.CreatedAt, input.UpdatedAt).Scan(&id)
	if err != nil {
		return item, false, err
	}
	err = tx.QueryRow(ctx, `SELECT claim.id,rule.id,rule.name,rule.discount_amount_total,rule.currency,claim.status,claim.claim_no_masked,claim.claimed_at,claim.valid_from,claim.valid_until,claim.redeemed_at FROM coupon_customer_claims claim JOIN coupon_rules rule ON rule.id=claim.coupon_id WHERE claim.id=$1`, id).Scan(&item.ClaimID, &item.CouponID, &item.Name, &item.DiscountMinor, &item.Currency, &item.Status, &item.ClaimNoMasked, &item.ClaimedAt, &item.ValidFrom, &item.ValidUntil, &item.RedeemedAt)
	return item, err == nil, err
}

var _ couponapp.CustomerCouponStore = (*Repository)(nil)
var _ couponapp.CouponClaimAdminStore = (*Repository)(nil)
