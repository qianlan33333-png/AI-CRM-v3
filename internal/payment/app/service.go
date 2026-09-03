package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Store interface {
	CreatePayment(context.Context, domain.Payment, [32]byte, [32]byte, string) (domain.Payment, bool, error)
	BindPaymentEffect(context.Context, domain.Payment, effectport.PaymentV1Intent, map[string]any) (domain.Payment, error)
	GetPayment(context.Context, int64, bool) (domain.Payment, error)
	CreateRefund(context.Context, domain.Refund, [32]byte, [32]byte, string) (domain.Refund, bool, error)
	BindRefundEffect(context.Context, domain.Refund, effectport.PaymentV1Intent, map[string]any) (domain.Refund, error)
	GetRefund(context.Context, int64, bool) (domain.Refund, error)
	GetPaymentByMerchantProvider(context.Context, domain.Provider, string, bool) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error)
	UpdatePaymentSettlement(context.Context, domain.Payment, string, string) (domain.Payment, error)
	UpdateRefundSettlement(context.Context, domain.Refund, string, string) (domain.Refund, error)
	GetPaymentByMerchant(context.Context, string, bool) (domain.Payment, error)
	GetRefundByNumber(context.Context, string, bool) (domain.Refund, error)
	ClaimCallback(context.Context, string, [32]byte, [32]byte, string, int64) (bool, error)
	ImportTerminalPayment(context.Context, domain.Payment, [32]byte, string) (domain.Payment, error)
	ImportTerminalRefund(context.Context, domain.Refund, [32]byte, string) (domain.Refund, error)
}

type Service struct {
	uow      platformport.UnitOfWork
	store    Store
	orders   orderport.PaymentReservationReader
	sessions paymentport.SessionConsumer
	effects  effectport.TransactionalAccepter
	now      func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store, orders orderport.PaymentReservationReader, sessions paymentport.SessionConsumer, effects effectport.TransactionalAccepter) *Service {
	return &Service{uow: uow, store: store, orders: orders, sessions: sessions, effects: effects, now: time.Now}
}

func (s *Service) Create(ctx context.Context, c paymentport.CreateCommand) (domain.Payment, error) {
	if !s.ready() || c.OrderID < 1 || len(c.SessionToken) < 20 || len(c.SessionToken) > 100 || !validScope(c.ActorScope) || !validKey(c.IdempotencyKey) {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(c)
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	payloadDigest := sha256.Sum256(payload)
	var result domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, err := s.sessions.ConsumeWithin(tx, c.SessionToken, now)
		if err != nil {
			return err
		}
		order, err := s.orders.ReservePaymentWithin(tx, c.OrderID)
		if err != nil {
			return err
		}
		if order.PayerCustomerID == nil || order.BeneficiaryCustomerID == nil || int64(*order.PayerCustomerID) != actor.PayerCustomerID || int64(*order.BeneficiaryCustomerID) != actor.BeneficiaryCustomerID {
			return paymentport.ErrConflict
		}
		payment, err := domain.NewPayment(order, actor.PayerIdentityID, now)
		if err != nil {
			return err
		}
		payment, _, err = s.store.CreatePayment(tx, payment, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		kind := effectport.KindWeChatPayPrepay
		if payment.Provider == domain.ProviderWeChatShop {
			kind = effectport.KindWeChatShopRefund
			return paymentport.ErrConflict
		}
		intent := effectport.PaymentV1Intent{Kind: kind, ReceiptKey: effectport.Hash("payment.create.v1", c.IdempotencyKey), SourceRefDigest: effectport.Hash("payment", strconv.FormatInt(payment.ID, 10)), TargetRefDigest: effectport.Hash("payment.identity", strconv.FormatInt(payment.PayerIdentityID, 10)), PayloadDigest: effectport.Hash("payment.payload", payment.MerchantOrderNo, strconv.FormatInt(payment.AmountMinor, 10), payment.Currency), PolicyVersionHash: effectport.Hash("payment.policy", "v1")}
		accept, ok := intent.AcceptCommand()
		if !ok {
			return paymentport.ErrInvalid
		}
		projection, _, err := s.effects.AcceptAndQueueWithin(tx, accept)
		if err != nil {
			return err
		}
		if payment.EffectID == "" {
			payment, err = payment.BindEffect(payment.Version, projection.ID, now)
			if err != nil {
				return err
			}
			payment, err = s.store.BindPaymentEffect(tx, payment, intent, map[string]any{"payment_id": payment.ID, "order_id": payment.OrderID, "amount_minor": payment.AmountMinor, "currency": payment.Currency})
			if err != nil {
				return err
			}
		} else if payment.EffectID != projection.ID {
			return paymentport.ErrConflict
		}
		result = payment
		return nil
	})
	if err != nil {
		return domain.Payment{}, classify(err)
	}
	return result, nil
}

func (s *Service) RequestRefund(ctx context.Context, c paymentport.RefundCommand) (domain.Refund, error) {
	if !s.ready() || c.PaymentID < 1 || c.AmountMinor < 1 || !validScope(c.ActorScope) || !validKey(c.IdempotencyKey) {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(c)
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	payloadDigest := sha256.Sum256(payload)
	var result domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error {
		payment, err := s.store.GetPayment(tx, c.PaymentID, true)
		if err != nil {
			return err
		}
		refund, err := domain.NewRefund(payment, c.RefundNo, c.AmountMinor, c.Reason, now)
		if err != nil {
			return err
		}
		refund, _, err = s.store.CreateRefund(tx, refund, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		kind := effectport.KindWeChatPayRefund
		if refund.Provider == domain.ProviderWeChatShop {
			kind = effectport.KindWeChatShopRefund
		}
		intent := effectport.PaymentV1Intent{Kind: kind, ReceiptKey: effectport.Hash("payment.refund.v1", c.IdempotencyKey), SourceRefDigest: effectport.Hash("payment.refund", strconv.FormatInt(refund.ID, 10)), TargetRefDigest: effectport.Hash("payment", strconv.FormatInt(payment.ID, 10)), PayloadDigest: effectport.Hash("payment.refund.payload", refund.RefundNo, strconv.FormatInt(refund.AmountMinor, 10), refund.Reason), PolicyVersionHash: effectport.Hash("payment.refund.policy", "v1")}
		accept, ok := intent.AcceptCommand()
		if !ok {
			return paymentport.ErrInvalid
		}
		projection, _, err := s.effects.AcceptAndQueueWithin(tx, accept)
		if err != nil {
			return err
		}
		if refund.EffectID == "" {
			refund, err = refund.BindEffect(refund.Version, projection.ID, now)
			if err != nil {
				return err
			}
			refund, err = s.store.BindRefundEffect(tx, refund, intent, map[string]any{"refund_id": refund.ID, "payment_id": payment.ID, "amount_minor": refund.AmountMinor, "currency": payment.Currency})
			if err != nil {
				return err
			}
		} else if refund.EffectID != projection.ID {
			return paymentport.ErrConflict
		}
		result = refund
		return nil
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	return result, nil
}

func (s *Service) GetPayment(ctx context.Context, id int64) (domain.Payment, error) {
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.GetPayment(tx, id, false); return e })
	return out, classify(err)
}
func (s *Service) GetRefund(ctx context.Context, id int64) (domain.Refund, error) {
	var out domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.GetRefund(tx, id, false); return e })
	return out, classify(err)
}
func (s *Service) FindPayment(ctx context.Context, provider domain.Provider, merchantOrderNo string) (domain.Payment, error) {
	if s == nil || s.uow == nil || s.store == nil || (provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || !validScope(merchantOrderNo) {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		out, inner = s.store.GetPaymentByMerchantProvider(tx, provider, merchantOrderNo, false)
		return inner
	})
	return out, classify(err)
}
func (s *Service) ListRefunds(ctx context.Context, limit, offset int32) ([]paymentport.RefundProjection, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return nil, 0, paymentport.ErrInvalid
	}
	var out []paymentport.RefundProjection
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		out, total, inner = s.store.ListRefunds(tx, limit, offset)
		return inner
	})
	return out, total, classify(err)
}
func (s *Service) SettlePayment(ctx context.Context, c paymentport.SettlementCommand) (domain.Payment, error) {
	if !s.ready() || c.PaymentID < 1 || c.ExpectedVersion < 1 || !validKey(c.ReceiptKey) {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		p, e := s.store.GetPayment(tx, c.PaymentID, true)
		if e != nil {
			return e
		}
		p, e = p.Settle(c.ExpectedVersion, c.Status, c.OccurredAt)
		if e != nil {
			return e
		}
		out, e = s.store.UpdatePaymentSettlement(tx, p, c.ProviderTransactionDigest, c.ReceiptKey)
		return e
	})
	return out, classify(err)
}
func (s *Service) SettleRefund(ctx context.Context, c paymentport.RefundSettlementCommand) (domain.Refund, error) {
	if !s.ready() || c.RefundID < 1 || c.ExpectedVersion < 1 || !validKey(c.ReceiptKey) {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var out domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error {
		r, e := s.store.GetRefund(tx, c.RefundID, true)
		if e != nil {
			return e
		}
		r, e = r.Complete(c.ExpectedVersion, c.Status, c.OccurredAt)
		if e != nil {
			return e
		}
		out, e = s.store.UpdateRefundSettlement(tx, r, c.ProviderRefundDigest, c.ReceiptKey)
		return e
	})
	return out, classify(err)
}

func (s *Service) ApplyVerifiedCallback(ctx context.Context, callback paymentprovider.CallbackResult) error {
	if !s.ready() || (callback.Kind != "payment" && callback.Kind != "refund") || callback.AmountMinor < 1 || callback.Currency != "CNY" || callback.OccurredAt.IsZero() {
		return paymentport.ErrInvalid
	}
	return classify(s.uow.Within(ctx, func(tx context.Context) error {
		if callback.Kind == "payment" {
			payment, err := s.store.GetPaymentByMerchant(tx, callback.MerchantOrderNo, true)
			if err != nil {
				return err
			}
			if payment.AmountMinor != callback.AmountMinor || payment.Currency != callback.Currency {
				return paymentport.ErrConflict
			}
			replay, err := s.store.ClaimCallback(tx, "wechat_pay", callback.EventDigest, callback.BodyDigest, "payment", payment.ID)
			if err != nil || replay {
				return err
			}
			payment, err = payment.Settle(payment.Version, domain.StatusPaid, callback.OccurredAt)
			if err != nil {
				return err
			}
			_, err = s.store.UpdatePaymentSettlement(tx, payment, callback.ProviderTransactionDigest, "callback:"+hexDigest(callback.EventDigest))
			return err
		}
		refund, err := s.store.GetRefundByNumber(tx, callback.RefundNo, true)
		if err != nil {
			return err
		}
		if refund.AmountMinor != callback.AmountMinor {
			return paymentport.ErrConflict
		}
		replay, err := s.store.ClaimCallback(tx, "wechat_pay", callback.EventDigest, callback.BodyDigest, "refund", refund.ID)
		if err != nil || replay {
			return err
		}
		refund, err = refund.Complete(refund.Version, domain.RefundCompleted, callback.OccurredAt)
		if err != nil {
			return err
		}
		_, err = s.store.UpdateRefundSettlement(tx, refund, callback.ProviderRefundDigest, "callback:"+hexDigest(callback.EventDigest))
		return err
	}))
}

func hexDigest(value [32]byte) string { return fmt.Sprintf("%x", value[:]) }

func (s *Service) ImportTerminalPayment(ctx context.Context, payment domain.Payment, digest [32]byte, runID string) (domain.Payment, error) {
	if s == nil || s.uow == nil || s.store == nil || digest == ([32]byte{}) || !validScope(runID) || payment.ID != 0 || payment.OrderID < 1 || payment.PayerIdentityID < 1 || payment.AmountMinor < 1 || payment.Currency != "CNY" || (payment.Status != domain.StatusPaid && payment.Status != domain.StatusFailed && payment.Status != domain.StatusCancelled) || payment.EffectID != "" {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var out domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.ImportTerminalPayment(tx, payment, digest, runID)
		return err
	})
	return out, classify(err)
}

func (s *Service) ImportTerminalRefund(ctx context.Context, refund domain.Refund, digest [32]byte, runID string) (domain.Refund, error) {
	if s == nil || s.uow == nil || s.store == nil || digest == ([32]byte{}) || !validScope(runID) || refund.ID != 0 || refund.PaymentID < 1 || refund.AmountMinor < 1 || refund.Status != domain.RefundCompleted || refund.EffectID != "" {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var out domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.ImportTerminalRefund(tx, refund, digest, runID)
		return err
	})
	return out, classify(err)
}
func (s *Service) ready() bool {
	return s != nil && s.uow != nil && s.store != nil && s.orders != nil && s.sessions != nil && s.effects != nil && s.now != nil
}
func validKey(v string) bool   { return v == strings.TrimSpace(v) && len(v) >= 16 && len(v) <= 200 }
func validScope(v string) bool { return v == strings.TrimSpace(v) && len(v) > 0 && len(v) <= 200 }
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, paymentport.ErrNotFound), errors.Is(err, orderport.ErrNotFound):
		return paymentport.ErrNotFound
	case errors.Is(err, paymentport.ErrConflict), errors.Is(err, orderport.ErrConflict), errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrTransition), errors.Is(err, domain.ErrVersion):
		return paymentport.ErrConflict
	default:
		return paymentport.ErrUnavailable
	}
}
