package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrNoEligibleCoupon = errors.New("no eligible coupon")

type CheckoutStore interface {
	ClaimCoupon(context.Context, couponport.ClaimCommand, [32]byte, [32]byte, time.Time) (couponport.CustomerCoupon, error)
	ReserveCoupon(context.Context, couponport.ReserveCommand, [32]byte, [32]byte, time.Time) (couponport.ReservationSnapshot, error)
	ConsumeCoupon(context.Context, couponport.ConsumeCommand, [32]byte, [32]byte, time.Time) (couponport.ReservationSnapshot, error)
	ReleaseCoupon(context.Context, couponport.ReleaseCommand, [32]byte, [32]byte, time.Time) (couponport.ReservationSnapshot, error)
}

// CheckoutService owns Coupon's local claim lifecycle and its narrow
// transaction-bound Order seam. It never resolves identities or opens an
// independent transaction for an Order mutation.
type CheckoutService struct {
	uow   platformport.UnitOfWork
	store CheckoutStore
	now   func() time.Time
}

var _ couponport.ClaimApplication = (*CheckoutService)(nil)
var _ couponport.OrderCouponCoordinator = (*CheckoutService)(nil)

func NewCheckoutService(uow platformport.UnitOfWork, store CheckoutStore) (*CheckoutService, error) {
	if uow == nil || store == nil {
		return nil, errors.New("coupon checkout dependencies are required")
	}
	return &CheckoutService{uow: uow, store: store, now: time.Now}, nil
}
func (s *CheckoutService) Claim(ctx context.Context, c couponport.ClaimCommand) (couponport.CustomerCoupon, error) {
	if s == nil || s.uow == nil || s.store == nil {
		return couponport.CustomerCoupon{}, ErrUnavailable
	}
	c.ClaimedAt = checkoutTime(c.ClaimedAt, s.now)
	if c.CouponID < 1 || c.HolderCustomerID < 1 || !validScope(c.ActorScope) || !validKey(c.IdempotencyKey) || c.ClaimedAt.IsZero() {
		return couponport.CustomerCoupon{}, ErrInvalidCoupon
	}
	payload, e := json.Marshal(struct {
		CouponID couponport.ID
		Holder   int64
		Scope    string
		At       time.Time
	}{c.CouponID, c.HolderCustomerID, c.ActorScope, c.ClaimedAt})
	if e != nil {
		return couponport.CustomerCoupon{}, ErrUnavailable
	}
	key, digest := sha256.Sum256([]byte(c.IdempotencyKey)), sha256.Sum256(payload)
	var result couponport.CustomerCoupon
	e = s.uow.Within(ctx, func(tx context.Context) error {
		var x error
		result, x = s.store.ClaimCoupon(tx, c, key, digest, c.ClaimedAt)
		return x
	})
	if e != nil {
		return couponport.CustomerCoupon{}, classifyCheckout(e)
	}
	return result, nil
}
func (s *CheckoutService) ReserveWithin(ctx context.Context, c couponport.ReserveCommand) (couponport.ReservationSnapshot, error) {
	if s == nil || s.store == nil {
		return couponport.ReservationSnapshot{}, ErrUnavailable
	}
	c.ReservedAt = checkoutTime(c.ReservedAt, s.now)
	if !validReserve(c) {
		return couponport.ReservationSnapshot{}, ErrInvalidCoupon
	}
	payload, e := json.Marshal(struct {
		Holder, Claim, Product             int64
		Code, Type, Currency, Order, Scope string
		Gross                              int64
		At                                 time.Time
	}{c.HolderCustomerID, c.ClaimID, c.ProductID, c.ProductCode, c.ProductType, c.Currency, c.OrderReference, c.ActorScope, c.GrossAmountMinor, c.ReservedAt})
	if e != nil {
		return couponport.ReservationSnapshot{}, ErrUnavailable
	}
	r, e := s.store.ReserveCoupon(ctx, c, sha256.Sum256([]byte(c.IdempotencyKey)), sha256.Sum256(payload), c.ReservedAt)
	if e != nil {
		return couponport.ReservationSnapshot{}, classifyCheckout(e)
	}
	return r, nil
}
func (s *CheckoutService) ConsumeWithin(ctx context.Context, c couponport.ConsumeCommand) (couponport.ReservationSnapshot, error) {
	if s == nil || s.store == nil {
		return couponport.ReservationSnapshot{}, ErrUnavailable
	}
	c.SettledAt = checkoutTime(c.SettledAt, s.now)
	if !validConsume(c) {
		return couponport.ReservationSnapshot{}, ErrInvalidCoupon
	}
	payload, e := json.Marshal(struct {
		Ref, Order, Currency, Scope string
		Amount                      int64
		At                          time.Time
	}{c.ReservationRef, c.OrderReference, c.SettledCurrency, c.ActorScope, c.SettledAmountMinor, c.SettledAt})
	if e != nil {
		return couponport.ReservationSnapshot{}, ErrUnavailable
	}
	r, e := s.store.ConsumeCoupon(ctx, c, sha256.Sum256([]byte(c.IdempotencyKey)), sha256.Sum256(payload), c.SettledAt)
	if e != nil {
		return couponport.ReservationSnapshot{}, classifyCheckout(e)
	}
	return r, nil
}
func (s *CheckoutService) ReleaseWithin(ctx context.Context, c couponport.ReleaseCommand) (couponport.ReservationSnapshot, error) {
	if s == nil || s.store == nil {
		return couponport.ReservationSnapshot{}, ErrUnavailable
	}
	c.ClosedAt = checkoutTime(c.ClosedAt, s.now)
	c.CloseReason = strings.TrimSpace(c.CloseReason)
	if !validRelease(c) {
		return couponport.ReservationSnapshot{}, ErrInvalidCoupon
	}
	payload, e := json.Marshal(struct {
		Ref, Order, Reason, Scope string
		At                        time.Time
	}{c.ReservationRef, c.OrderReference, c.CloseReason, c.ActorScope, c.ClosedAt})
	if e != nil {
		return couponport.ReservationSnapshot{}, ErrUnavailable
	}
	r, e := s.store.ReleaseCoupon(ctx, c, sha256.Sum256([]byte(c.IdempotencyKey)), sha256.Sum256(payload), c.ClosedAt)
	if e != nil {
		return couponport.ReservationSnapshot{}, classifyCheckout(e)
	}
	return r, nil
}
func checkoutTime(v time.Time, clock func() time.Time) time.Time {
	if v.IsZero() && clock != nil {
		v = clock()
	}
	return v.UTC().Truncate(time.Microsecond)
}
func validScope(v string) bool { return v != "" && len(v) <= 200 && strings.TrimSpace(v) == v }
func validKey(v string) bool   { return len(v) >= 16 && len(v) <= 128 && strings.TrimSpace(v) == v }
func validRef(v string) bool   { return v != "" && len(v) <= 200 && strings.TrimSpace(v) == v }
func validReserve(c couponport.ReserveCommand) bool {
	return c.HolderCustomerID > 0 && c.ClaimID >= 0 && c.ProductID > 0 && validRef(c.ProductCode) && (c.ProductType == "standard_product" || c.ProductType == "service_period") && c.GrossAmountMinor > 0 && c.Currency == "CNY" && validRef(c.OrderReference) && validScope(c.ActorScope) && validKey(c.IdempotencyKey) && !c.ReservedAt.IsZero()
}
func validConsume(c couponport.ConsumeCommand) bool {
	return validReservationRef(c.ReservationRef) && validRef(c.OrderReference) && c.SettledAmountMinor > 0 && c.SettledCurrency == "CNY" && validScope(c.ActorScope) && validKey(c.IdempotencyKey) && !c.SettledAt.IsZero()
}
func validRelease(c couponport.ReleaseCommand) bool {
	return validReservationRef(c.ReservationRef) && validRef(c.OrderReference) && c.CloseReason != "" && len(c.CloseReason) <= 120 && validScope(c.ActorScope) && validKey(c.IdempotencyKey) && !c.ClosedAt.IsZero()
}
func validReservationRef(v string) bool {
	if !strings.HasPrefix(v, "cr_") || len(v) <= 3 || len(v) > 40 {
		return false
	}
	for _, c := range v[3:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func classifyCheckout(e error) error {
	if errors.Is(e, ErrInvalidCoupon) || errors.Is(e, ErrConflict) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrNoEligibleCoupon) {
		return e
	}
	return ErrUnavailable
}
