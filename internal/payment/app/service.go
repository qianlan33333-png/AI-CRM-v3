package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type Store interface {
	CreatePayment(context.Context, domain.Payment, [32]byte, [32]byte, string) (domain.Payment, bool, error)
	ReplayPayment(context.Context, [32]byte, [32]byte, string) (domain.Payment, bool, error)
	BindPaymentEffect(context.Context, domain.Payment, effectport.PaymentV1Intent, map[string]any) (domain.Payment, error)
	GetPayment(context.Context, int64, bool) (domain.Payment, error)
	GetHandoff(context.Context, int64) (paymentport.Handoff, error)
	ReservedRefundMinor(context.Context, int64) (int64, error)
	CreateRefund(context.Context, domain.Refund, [32]byte, [32]byte, string) (domain.Refund, bool, error)
	ReplayRefund(context.Context, [32]byte, [32]byte, string) (domain.Refund, bool, error)
	BindRefundEffect(context.Context, domain.Refund, effectport.PaymentV1Intent, map[string]any) (domain.Refund, error)
	GetRefund(context.Context, int64, bool) (domain.Refund, error)
	GetRefundByProviderReference(context.Context, domain.Provider, string, bool) (domain.Refund, error)
	GetShopRefundMaterial(context.Context, int64) (paymentport.ShopRefundMaterial, error)
	RecordReconciliation(context.Context, int64, effectport.Digest, string, time.Time) (bool, error)
	RecordPaymentReconciliation(context.Context, int64, effectport.Digest, string, time.Time) (bool, error)
	GetPaymentByMerchantProvider(context.Context, domain.Provider, string, bool) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error)
	ListEffectBindings(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error)
	UpdatePaymentSettlement(context.Context, domain.Payment, string, string) (domain.Payment, error)
	UpdateRefundSettlement(context.Context, domain.Refund, string, string) (domain.Refund, error)
	GetPaymentByMerchant(context.Context, string, bool) (domain.Payment, error)
	GetRefundByNumber(context.Context, string, bool) (domain.Refund, error)
	ClaimCallback(context.Context, string, [32]byte, [32]byte, string, string, int64) (bool, error)
	ImportTerminalPayment(context.Context, domain.Payment, [32]byte, string) (domain.Payment, error)
	ImportTerminalRefund(context.Context, domain.Refund, [32]byte, string) (domain.Refund, error)
}

type Service struct {
	uow            platformport.UnitOfWork
	store          Store
	orders         orderport.PaymentCoordinator
	sessions       paymentport.SessionLifecycle
	effects        effectport.TransactionalAccepter
	effectReader   effectport.Reader
	shopReconciler paymentport.ShopRefundReconciler
	payReconciler  paymentport.WeChatPayReconciler
	reconcileJobs  paymentport.ReconciliationEnqueuer
	products       productport.CheckoutProductReader
	miniAppID      string
	h5AppID        string
	now            func() time.Time
}

func (s *Service) SetPaymentChannelAppIDs(miniProgramAppID, h5OfficialAccountAppID string) error {
	if s == nil || !validScope(miniProgramAppID) || h5OfficialAccountAppID != "" && !validScope(h5OfficialAccountAppID) {
		return paymentport.ErrInvalid
	}
	s.miniAppID, s.h5AppID = miniProgramAppID, h5OfficialAccountAppID
	return nil
}

func (s *Service) SetReconciliationEnqueuer(enqueuer paymentport.ReconciliationEnqueuer) error {
	if s == nil || enqueuer == nil {
		return paymentport.ErrInvalid
	}
	s.reconcileJobs = enqueuer
	return nil
}

func (s *Service) SetCheckoutProductReader(reader productport.CheckoutProductReader) error {
	if s == nil || reader == nil {
		return paymentport.ErrInvalid
	}
	s.products = reader
	return nil
}

func (s *Service) SetShopReconciler(reconciler paymentport.ShopRefundReconciler) error {
	if s == nil || reconciler == nil {
		return paymentport.ErrInvalid
	}
	s.shopReconciler = reconciler
	return nil
}

func (s *Service) SetWeChatPayReconciler(reconciler paymentport.WeChatPayReconciler) error {
	if s == nil || reconciler == nil {
		return paymentport.ErrInvalid
	}
	s.payReconciler = reconciler
	return nil
}

func NewService(uow platformport.UnitOfWork, store Store, orders orderport.PaymentCoordinator, sessions paymentport.SessionLifecycle, effects effectport.TransactionalAccepter, readers ...effectport.Reader) *Service {
	service := &Service{uow: uow, store: store, orders: orders, sessions: sessions, effects: effects, now: time.Now}
	if len(readers) > 0 {
		service.effectReader = readers[0]
	}
	return service
}

func (s *Service) Create(ctx context.Context, c paymentport.CreateCommand) (domain.Payment, error) {
	fromExistingOrder := c.OrderID > 0 && c.ProductID == 0 && c.ProductType == ""
	fromProduct := c.OrderID == 0 && c.ProductID > 0 && (c.ProductType == string(productport.ProductOptionStandard) || c.ProductType == string(productport.ProductOptionServicePeriod))
	if !s.ready() || (!fromExistingOrder && !fromProduct) || fromProduct && s.products == nil || len(c.SessionToken) < 20 || len(c.SessionToken) > 100 || !validScope(c.ActorScope) || !validKey(c.IdempotencyKey) {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	now := s.now().UTC()
	sessionDigest := sha256.Sum256([]byte(c.SessionToken))
	merchantDigest := sha256.Sum256([]byte("payment.checkout.v1\x00" + c.SessionToken + "\x00" + c.IdempotencyKey))
	merchantOrderNo := "v3pay_" + hex.EncodeToString(merchantDigest[:16])
	payload, _ := json.Marshal(c)
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	payloadDigest := sha256.Sum256(payload)
	var result domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, err := s.sessions.LookupWithin(tx, c.SessionToken, now)
		if err != nil {
			return err
		}
		if fromProduct {
			switch actor.BeneficiarySelection {
			case paymentport.BeneficiarySelectionUnresolved:
				if c.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf {
					return paymentport.ErrConflict
				}
				actor, err = s.sessions.SelectPayerSelfWithin(tx, c.SessionToken, now)
				if err != nil {
					return err
				}
			case paymentport.BeneficiarySelectionPayerSelf:
				if c.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || actor.BeneficiaryCustomerID != actor.PayerCustomerID {
					return paymentport.ErrConflict
				}
			case paymentport.BeneficiarySelectionAdminAssisted:
				// This state is set only by the trusted server-side session issuer.
				// A public browser cannot select or replace its prebound recipient.
				if c.BeneficiarySelection != "" || actor.BeneficiaryCustomerID < 1 {
					return paymentport.ErrConflict
				}
			default:
				// Legacy rows retain their original value but are never re-labelled as
				// a fresh user confirmation by the public purchase path.
				return paymentport.ErrConflict
			}
		}
		channel := actor.Channel
		if channel == "" {
			channel = domain.ChannelMiniProgram
		}
		if channel != domain.ChannelMiniProgram && channel != domain.ChannelH5Official {
			return paymentport.ErrConflict
		}
		replay, found, err := s.store.ReplayPayment(tx, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		if found {
			if (fromExistingOrder && replay.OrderID != c.OrderID) || (fromProduct && replay.MerchantOrderNo != merchantOrderNo) || replay.PayerIdentityID != actor.PayerIdentityID || replay.PayerCustomerID != actor.PayerCustomerID || replay.BeneficiaryCustomerID != actor.BeneficiaryCustomerID || replay.Channel != channel {
				return paymentport.ErrConflict
			}
			result = replay
			return nil
		}
		if !selectedBeneficiary(actor) {
			// Pre-migration sessions retain their recipient value only to authorize
			// an exact payment replay above. They cannot create a new command or
			// silently turn an inferred recipient into a fresh confirmation.
			return paymentport.ErrConflict
		}
		var order orderdomain.Snapshot
		if fromExistingOrder {
			order, err = s.orders.ReservePaymentWithin(tx, c.OrderID)
		} else {
			product, productErr := s.products.ReadCheckoutProductWithin(tx, productport.ProductOptionType(c.ProductType), productport.ID(c.ProductID))
			if productErr != nil || int64(product.ID) != c.ProductID || product.ProductType != productport.ProductOptionType(c.ProductType) || product.PriceMinor < 1 || product.Currency != "CNY" || product.Version < 1 {
				if productErr != nil {
					return productErr
				}
				return paymentport.ErrConflict
			}
			if channel == domain.ChannelH5Official {
				if product.RequireMobile != validMainlandMobileE164(c.MobileE164) {
					return paymentport.ErrConflict
				}
			} else if c.MobileE164 != "" {
				return paymentport.ErrConflict
			}
			order, err = s.orders.CreatePaymentOrderWithin(tx, orderport.PaymentOrderCommand{
				Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: merchantOrderNo,
				PayerCustomerID: actor.PayerCustomerID, BeneficiaryCustomerID: actor.BeneficiaryCustomerID,
				ProductID: int64(product.ID), ProductCode: product.Code, ProductName: product.Name,
				ProductVersion: product.Version, UnitAmountMinor: product.PriceMinor, Currency: product.Currency,
				MobileE164: c.MobileE164,
				ActorScope: "payment-session:" + hex.EncodeToString(sessionDigest[:]), IdempotencyKey: c.IdempotencyKey,
			})
		}
		if err != nil {
			return err
		}
		if order.PayerCustomerID == nil || order.BeneficiaryCustomerID == nil || int64(*order.PayerCustomerID) != actor.PayerCustomerID || int64(*order.BeneficiaryCustomerID) != actor.BeneficiaryCustomerID {
			return paymentport.ErrConflict
		}
		payment, err := domain.NewPayment(order, actor.PayerIdentityID, now, channel)
		if err != nil {
			return err
		}
		var created bool
		payment, created, err = s.store.CreatePayment(tx, payment, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		if created {
			consumed, consumeErr := s.sessions.ConsumeWithin(tx, c.SessionToken, now)
			if consumeErr != nil {
				return consumeErr
			}
			if consumed != actor {
				return paymentport.ErrConflict
			}
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
	payload, _ := json.Marshal(c)
	keyDigest := sha256.Sum256([]byte(c.IdempotencyKey))
	payloadDigest := sha256.Sum256(payload)
	var result domain.Refund
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var found bool
		var replayErr error
		result, found, replayErr = s.store.ReplayRefund(tx, keyDigest, payloadDigest, c.ActorScope)
		if replayErr != nil || found {
			return replayErr
		}
		result = domain.Refund{}
		return nil
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	if result.ID > 0 {
		return result, nil
	}
	if c.ProviderOrderID != "" {
		if s.shopReconciler == nil || !validShopRefundCommand(c) {
			return domain.Refund{}, paymentport.ErrInvalid
		}
		var candidate domain.Payment
		if err := s.uow.Within(ctx, func(tx context.Context) error {
			var readErr error
			candidate, readErr = s.store.GetPayment(tx, c.PaymentID, false)
			return readErr
		}); err != nil {
			return domain.Refund{}, classify(err)
		}
		if candidate.Provider != domain.ProviderWeChatShop || candidate.MerchantOrderNo != c.ProviderOrderID || candidate.Status != domain.StatusPaid {
			return domain.Refund{}, paymentport.ErrConflict
		}
		if err := s.shopReconciler.ValidateRefundMaterial(ctx, paymentport.ShopRefundMaterial{AmountMinor: c.AmountMinor, ProviderOrderID: c.ProviderOrderID, ProductID: c.ProductID, SKUID: c.SKUID, RefundCount: c.RefundCount, ReasonCode: c.ReasonCode, Currency: "CNY"}); err != nil {
			return domain.Refund{}, paymentport.ErrUnavailable
		}
	}
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		payment, err := s.store.GetPayment(tx, c.PaymentID, true)
		if err != nil {
			return err
		}
		if payment.Provider == domain.ProviderWeChatShop && !validShopRefundCommand(c) {
			return paymentport.ErrInvalid
		}
		replay, found, err := s.store.ReplayRefund(tx, keyDigest, payloadDigest, c.ActorScope)
		if err != nil {
			return err
		}
		if found {
			if replay.PaymentID != payment.ID || replay.Provider != payment.Provider {
				return paymentport.ErrConflict
			}
			result = replay
			return nil
		}
		reserved, err := s.store.ReservedRefundMinor(tx, payment.ID)
		if err != nil {
			return err
		}
		if reserved < 0 || c.AmountMinor > payment.AmountMinor-reserved {
			return paymentport.ErrConflict
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
		intent := effectport.PaymentV1Intent{Kind: kind, ReceiptKey: effectport.Hash("payment.refund.v1", c.IdempotencyKey), SourceRefDigest: effectport.Hash("payment.refund", strconv.FormatInt(refund.ID, 10)), TargetRefDigest: effectport.Hash("payment", strconv.FormatInt(payment.ID, 10)), PayloadDigest: effectport.Hash("payment.refund.payload", refund.RefundNo, strconv.FormatInt(refund.AmountMinor, 10), refund.Reason, c.ProviderOrderID, c.ProductID, c.SKUID, strconv.FormatInt(c.RefundCount, 10), c.ReasonCode), PolicyVersionHash: effectport.Hash("payment.refund.policy", "v1")}
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
			refund, err = s.store.BindRefundEffect(tx, refund, intent, map[string]any{"refund_id": refund.ID, "payment_id": payment.ID, "amount_minor": refund.AmountMinor, "currency": payment.Currency, "provider_order_id": c.ProviderOrderID, "product_id": c.ProductID, "sku_id": c.SKUID, "refund_count": c.RefundCount, "reason_code": c.ReasonCode})
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

func (s *Service) GetCheckout(ctx context.Context, merchantOrderNo, sessionToken string) (paymentport.Handoff, error) {
	if s == nil || s.uow == nil || s.store == nil || s.sessions == nil || !validScope(merchantOrderNo) || len(sessionToken) < 20 || len(sessionToken) > 100 {
		return paymentport.Handoff{}, paymentport.ErrInvalid
	}
	now := s.now().UTC()
	var out paymentport.Handoff
	err := s.uow.Within(ctx, func(tx context.Context) error {
		actor, err := s.sessions.LookupWithin(tx, sessionToken, now)
		if err != nil {
			return err
		}
		payment, err := s.store.GetPaymentByMerchantProvider(tx, domain.ProviderWeChatPay, merchantOrderNo, false)
		if err != nil {
			return err
		}
		if payment.PayerIdentityID != actor.PayerIdentityID || payment.PayerCustomerID != actor.PayerCustomerID || payment.BeneficiaryCustomerID != actor.BeneficiaryCustomerID {
			return paymentport.ErrConflict
		}
		out = paymentport.Handoff{PaymentID: payment.ID, MerchantOrder: payment.MerchantOrderNo, Status: payment.Status}
		if payment.Status == domain.StatusAwaitingPrepay {
			return nil
		}
		handoff, err := s.store.GetHandoff(tx, payment.ID)
		if errors.Is(err, paymentport.ErrNotFound) && payment.Status != domain.StatusAwaitingPayment {
			return nil
		}
		if err != nil {
			return err
		}
		if !handoff.ExpiresAt.After(now) {
			return paymentport.ErrConflict
		}
		out.Payload, out.ExpiresAt = handoff.Payload, handoff.ExpiresAt
		return nil
	})
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

func (s *Service) ListOrderEffects(ctx context.Context, provider domain.Provider, merchantOrderNo string) ([]paymentport.EffectProjection, error) {
	if s == nil || s.uow == nil || s.store == nil || s.effectReader == nil ||
		(provider != domain.ProviderWeChatPay && provider != domain.ProviderWeChatShop) || !validScope(merchantOrderNo) {
		return nil, paymentport.ErrInvalid
	}
	var bindings []paymentport.EffectProjection
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		bindings, inner = s.store.ListEffectBindings(tx, provider, merchantOrderNo)
		return inner
	})
	if err != nil {
		return nil, classify(err)
	}
	for index := range bindings {
		projection, readErr := s.effectReader.Get(ctx, bindings[index].EffectID)
		if readErr != nil || projection.Owner != effectport.OwnerPayment || projection.Kind != bindings[index].Kind {
			return nil, paymentport.ErrConflict
		}
		bindings[index].State = projection.State
		bindings[index].AttemptCount = projection.AttemptCount
		bindings[index].UpdatedAt = projection.UpdatedAt
	}
	return bindings, nil
}

func (s *Service) ReconcileShopRefund(ctx context.Context, refundID int64) (domain.Refund, error) {
	if s == nil || s.uow == nil || s.store == nil || s.shopReconciler == nil || refundID < 1 {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var current domain.Refund
	var material paymentport.ShopRefundMaterial
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = s.store.GetRefund(tx, refundID, false)
		if inner != nil {
			return inner
		}
		if current.Provider != domain.ProviderWeChatShop || current.ProviderRefundReference == "" || (current.Status != domain.RefundEffectAccepted && current.Status != domain.RefundOutcomeUnknown && current.Status != domain.RefundCompleted) {
			return paymentport.ErrConflict
		}
		material, inner = s.store.GetShopRefundMaterial(tx, current.ID)
		return inner
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	if current.Status == domain.RefundCompleted {
		return current, nil
	}
	query, err := s.shopReconciler.QueryRefund(ctx, current.ProviderRefundReference)
	if err != nil {
		return domain.Refund{}, paymentport.ErrUnavailable
	}
	if query.AfterSaleID != current.ProviderRefundReference || query.ProviderOrderID != material.ProviderOrderID || query.ProductID != material.ProductID || query.SKUID != material.SKUID || query.Count != material.RefundCount || query.AmountMinor != current.AmountMinor || query.Currency != "CNY" || !effectport.ValidDigest(query.EvidenceDigest) || !effectport.ValidDigest(query.ProviderRefundDigest) || query.OccurredAt.IsZero() {
		return domain.Refund{}, paymentport.ErrConflict
	}
	outcome := "pending"
	if query.Status == "MERCHANT_REFUND_SUCCESS" {
		outcome = "refunded"
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetRefund(tx, refundID, true)
		if inner != nil {
			return inner
		}
		if locked.Status == domain.RefundCompleted {
			current = locked
			return nil
		}
		if locked.ProviderRefundReference != query.AfterSaleID || (locked.Status != domain.RefundEffectAccepted && locked.Status != domain.RefundOutcomeUnknown) {
			return paymentport.ErrConflict
		}
		_, inner = s.store.RecordReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC())
		if inner != nil || outcome != "refunded" {
			current = locked
			return inner
		}
		locked, inner = locked.Complete(locked.Version, domain.RefundCompleted, query.OccurredAt)
		if inner != nil {
			return inner
		}
		receiptKey := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdateRefundSettlement(tx, locked, string(query.ProviderRefundDigest), receiptKey)
		if inner != nil {
			return inner
		}
		payment, inner := s.store.GetPayment(tx, current.PaymentID, false)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: current.AmountMinor, OccurredAt: query.OccurredAt, ReceiptKey: receiptKey})
		return inner
	})
	return current, classify(err)
}

func (s *Service) ApplyVerifiedShopCallback(ctx context.Context, callback paymentport.ShopRefundCallback) error {
	if s == nil || s.uow == nil || s.store == nil || s.shopReconciler == nil || s.reconcileJobs == nil || callback.AfterSaleID == "" || callback.ProviderOrderID == "" || callback.Status == "" || callback.EventDigest == ([32]byte{}) || callback.PayloadDigest == ([32]byte{}) || callback.OccurredAt.IsZero() {
		return paymentport.ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		refund, inner := s.store.GetRefundByProviderReference(tx, domain.ProviderWeChatShop, callback.AfterSaleID, true)
		if inner != nil {
			return inner
		}
		material, inner := s.store.GetShopRefundMaterial(tx, refund.ID)
		if inner != nil || material.ProviderOrderID != callback.ProviderOrderID {
			if inner != nil {
				return inner
			}
			return paymentport.ErrConflict
		}
		replay, inner := s.store.ClaimCallback(tx, "wechat_shop", callback.EventDigest, callback.PayloadDigest, "refund", "query_required", refund.ID)
		if inner != nil || replay {
			return inner
		}
		return s.reconcileJobs.EnqueueWithin(tx, paymentport.ReconciliationTarget{Provider: domain.ProviderWeChatShop, RefundID: refund.ID})
	})
	return classify(err)
}

func (s *Service) ReconcileWeChatPayPayment(ctx context.Context, paymentID int64) (domain.Payment, error) {
	if s == nil || s.uow == nil || s.store == nil || s.payReconciler == nil || paymentID < 1 {
		return domain.Payment{}, paymentport.ErrInvalid
	}
	var current domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = s.store.GetPayment(tx, paymentID, false)
		if inner != nil {
			return inner
		}
		if current.Provider != domain.ProviderWeChatPay || (current.Status != domain.StatusAwaitingPayment && current.Status != domain.StatusAwaitingPrepay && current.Status != domain.StatusPaid) {
			return paymentport.ErrConflict
		}
		return nil
	})
	if err != nil {
		return domain.Payment{}, classify(err)
	}
	query, err := s.payReconciler.QueryPayment(ctx, current.MerchantOrderNo)
	if err != nil {
		return domain.Payment{}, paymentport.ErrUnavailable
	}
	if query.MerchantOrderNo != current.MerchantOrderNo || query.AmountMinor != current.AmountMinor || query.Currency != current.Currency || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return domain.Payment{}, paymentport.ErrConflict
	}
	outcome := "pending"
	switch query.Status {
	case "SUCCESS":
		if !effectport.ValidDigest(query.TransactionDigest) {
			return domain.Payment{}, paymentport.ErrConflict
		}
		outcome = "paid"
	case "CLOSED", "REVOKED", "PAYERROR":
		outcome = "final_failed"
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetPayment(tx, paymentID, true)
		if inner != nil {
			return inner
		}
		_, inner = s.store.RecordPaymentReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC())
		if inner != nil || outcome == "pending" || locked.Status == domain.StatusPaid || locked.Status == domain.StatusFailed {
			current = locked
			return inner
		}
		next := domain.StatusPaid
		if outcome == "final_failed" {
			next = domain.StatusFailed
		}
		locked, inner = locked.Settle(locked.Version, next, query.OccurredAt)
		if inner != nil {
			return inner
		}
		receiptKey := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdatePaymentSettlement(tx, locked, string(query.TransactionDigest), receiptKey)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: current.OrderID, Failed: outcome == "final_failed", OccurredAt: query.OccurredAt, ReceiptKey: receiptKey})
		return inner
	})
	return current, classify(err)
}

func (s *Service) ReconcileWeChatPayRefund(ctx context.Context, refundID int64) (domain.Refund, error) {
	if s == nil || s.uow == nil || s.store == nil || s.payReconciler == nil || refundID < 1 {
		return domain.Refund{}, paymentport.ErrInvalid
	}
	var current domain.Refund
	var payment domain.Payment
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var inner error
		current, inner = s.store.GetRefund(tx, refundID, false)
		if inner != nil {
			return inner
		}
		if current.Provider != domain.ProviderWeChatPay || (current.Status != domain.RefundEffectAccepted && current.Status != domain.RefundOutcomeUnknown && current.Status != domain.RefundCompleted) {
			return paymentport.ErrConflict
		}
		payment, inner = s.store.GetPayment(tx, current.PaymentID, false)
		return inner
	})
	if err != nil {
		return domain.Refund{}, classify(err)
	}
	query, err := s.payReconciler.QueryRefund(ctx, current.RefundNo)
	if err != nil {
		return domain.Refund{}, paymentport.ErrUnavailable
	}
	if query.RefundNo != current.RefundNo || query.AmountMinor != current.AmountMinor || query.TotalMinor != payment.AmountMinor || query.Currency != payment.Currency || !effectport.ValidDigest(query.EvidenceDigest) || query.OccurredAt.IsZero() {
		return domain.Refund{}, paymentport.ErrConflict
	}
	outcome := "pending"
	switch query.Status {
	case "SUCCESS":
		if !effectport.ValidDigest(query.RefundDigest) {
			return domain.Refund{}, paymentport.ErrConflict
		}
		outcome = "refunded"
	case "CLOSED":
		outcome = "final_failed"
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		locked, inner := s.store.GetRefund(tx, refundID, true)
		if inner != nil {
			return inner
		}
		_, inner = s.store.RecordReconciliation(tx, locked.ID, query.EvidenceDigest, outcome, s.now().UTC())
		if inner != nil || outcome == "pending" || locked.Status == domain.RefundCompleted || locked.Status == domain.RefundFinalFailed {
			current = locked
			return inner
		}
		next := domain.RefundCompleted
		if outcome == "final_failed" {
			next = domain.RefundFinalFailed
		}
		locked, inner = locked.Complete(locked.Version, next, query.OccurredAt)
		if inner != nil {
			return inner
		}
		receiptKey := "reconcile:" + string(query.EvidenceDigest)
		current, inner = s.store.UpdateRefundSettlement(tx, locked, string(query.RefundDigest), receiptKey)
		if inner != nil || outcome == "final_failed" {
			return inner
		}
		payment, inner = s.store.GetPayment(tx, current.PaymentID, false)
		if inner != nil {
			return inner
		}
		_, inner = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: current.AmountMinor, OccurredAt: query.OccurredAt, ReceiptKey: receiptKey})
		return inner
	})
	return current, classify(err)
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
			if payment.AmountMinor != callback.AmountMinor || payment.Currency != callback.Currency || !s.callbackAppIDMatches(payment, callback.AppID) {
				return paymentport.ErrConflict
			}
			replay, err := s.store.ClaimCallback(tx, "wechat_pay", callback.EventDigest, callback.BodyDigest, "payment", "settled", payment.ID)
			if err != nil || replay {
				return err
			}
			payment, err = payment.Settle(payment.Version, domain.StatusPaid, callback.OccurredAt)
			if err != nil {
				return err
			}
			receiptKey := "callback:" + hexDigest(callback.EventDigest)
			if _, err = s.store.UpdatePaymentSettlement(tx, payment, callback.ProviderTransactionDigest, receiptKey); err != nil {
				return err
			}
			_, err = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, OccurredAt: callback.OccurredAt, ReceiptKey: receiptKey})
			return err
		}
		refund, err := s.store.GetRefundByNumber(tx, callback.RefundNo, true)
		if err != nil {
			return err
		}
		if refund.AmountMinor != callback.AmountMinor {
			return paymentport.ErrConflict
		}
		payment, err := s.store.GetPayment(tx, refund.PaymentID, false)
		if err != nil || !s.callbackAppIDMatches(payment, callback.AppID) {
			return paymentport.ErrConflict
		}
		replay, err := s.store.ClaimCallback(tx, "wechat_pay", callback.EventDigest, callback.BodyDigest, "refund", "settled", refund.ID)
		if err != nil || replay {
			return err
		}
		refund, err = refund.Complete(refund.Version, domain.RefundCompleted, callback.OccurredAt)
		if err != nil {
			return err
		}
		receiptKey := "callback:" + hexDigest(callback.EventDigest)
		if _, err = s.store.UpdateRefundSettlement(tx, refund, callback.ProviderRefundDigest, receiptKey); err != nil {
			return err
		}
		_, err = s.orders.SettlePaymentWithin(tx, orderport.PaymentSettlementCommand{OrderID: payment.OrderID, RefundedDelta: refund.AmountMinor, OccurredAt: callback.OccurredAt, ReceiptKey: receiptKey})
		return err
	}))
}

func (s *Service) callbackAppIDMatches(payment domain.Payment, appID string) bool {
	if s.miniAppID == "" { // Tests and provider-disabled compositions have no accepted callback surface.
		return true
	}
	if payment.Channel == domain.ChannelH5Official {
		return s.h5AppID != "" && appID == s.h5AppID
	}
	return payment.Channel == domain.ChannelMiniProgram && appID == s.miniAppID
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

func validShopRefundCommand(command paymentport.RefundCommand) bool {
	if !validScope(command.ProviderOrderID) || !validScope(command.ProductID) || !validScope(command.SKUID) || command.RefundCount < 1 || command.RefundCount > 1_000_000 {
		return false
	}
	switch command.ReasonCode {
	case "10000000", "10000001", "10000002", "10000006", "10000007", "10000008", "10000014", "10000015", "10000017", "10000021":
		return true
	default:
		return false
	}
}
func validKey(v string) bool   { return v == strings.TrimSpace(v) && len(v) >= 16 && len(v) <= 200 }
func validScope(v string) bool { return v == strings.TrimSpace(v) && len(v) > 0 && len(v) <= 200 }

func selectedBeneficiary(actor paymentport.SessionActor) bool {
	switch actor.BeneficiarySelection {
	case paymentport.BeneficiarySelectionPayerSelf:
		return actor.PayerCustomerID > 0 && actor.BeneficiaryCustomerID == actor.PayerCustomerID
	case paymentport.BeneficiarySelectionAdminAssisted:
		return actor.BeneficiaryCustomerID > 0
	default:
		return false
	}
}

func validMainlandMobileE164(v string) bool {
	if len(v) != 14 || !strings.HasPrefix(v, "+861") || v[4] < '3' || v[4] > '9' {
		return false
	}
	return strings.IndexFunc(v[1:], func(r rune) bool { return r < '0' || r > '9' }) < 0
}
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
