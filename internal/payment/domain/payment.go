package domain

import (
	"errors"
	"strings"
	"time"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

var ErrInvalid = errors.New("invalid payment")
var ErrTransition = errors.New("invalid payment transition")
var ErrVersion = errors.New("payment version conflict")

type Provider string

const (
	ProviderWeChatPay  Provider = "wechat_pay"
	ProviderWeChatShop Provider = "wechat_shop"
)

type Payment struct {
	ID, OrderID                                             int64
	Provider                                                Provider
	MerchantOrderNo                                         string
	PayerIdentityID, PayerCustomerID, BeneficiaryCustomerID int64
	AmountMinor                                             int64
	Currency                                                string
	Status                                                  Status
	EffectID                                                string
	ProviderTransactionDigest                               string
	Version                                                 int64
	CreatedAt, UpdatedAt                                    time.Time
}
type Refund struct {
	ID, PaymentID           int64
	Provider                Provider
	RefundNo, Reason        string
	AmountMinor             int64
	Status                  RefundStatus
	EffectID                string
	ProviderRefundReference string
	ProviderRefundDigest    string
	Version                 int64
	CreatedAt, UpdatedAt    time.Time
}

func NewPayment(order orderdomain.Snapshot, payerIdentityID int64, now time.Time) (Payment, error) {
	if order.ID < 1 || order.RecordOrigin != orderdomain.RecordOriginNative || !order.EffectEligible || order.PayerCustomerID == nil || order.BeneficiaryCustomerID == nil || payerIdentityID < 1 || now.IsZero() || order.Amount.Currency != "CNY" || order.Provider == orderdomain.ProviderAlipay {
		return Payment{}, ErrInvalid
	}
	provider := Provider(order.Provider)
	if provider != ProviderWeChatPay && provider != ProviderWeChatShop {
		return Payment{}, ErrInvalid
	}
	return Payment{OrderID: order.ID, Provider: provider, MerchantOrderNo: order.MerchantOrderNo, PayerIdentityID: payerIdentityID, PayerCustomerID: *order.PayerCustomerID, BeneficiaryCustomerID: *order.BeneficiaryCustomerID, AmountMinor: order.Amount.AmountMinor, Currency: order.Amount.Currency, Status: StatusAwaitingPrepay, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}
func (p Payment) BindEffect(expected int64, effectID string, now time.Time) (Payment, error) {
	if expected != p.Version {
		return Payment{}, ErrVersion
	}
	if p.Status != StatusAwaitingPrepay || !validEffectID(effectID) || now.Before(p.UpdatedAt) {
		return Payment{}, ErrTransition
	}
	p.EffectID = effectID
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}
func (p Payment) Settle(expected int64, status Status, now time.Time) (Payment, error) {
	if expected != p.Version {
		return Payment{}, ErrVersion
	}
	if now.Before(p.UpdatedAt) || (p.Status != StatusAwaitingPrepay && p.Status != StatusAwaitingPayment) || (status != StatusAwaitingPayment && status != StatusPaid && status != StatusFailed && status != StatusCancelled) {
		return Payment{}, ErrTransition
	}
	p.Status = status
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}
func NewRefund(payment Payment, refundNo string, amount int64, reason string, now time.Time) (Refund, error) {
	refundNo = strings.TrimSpace(refundNo)
	reason = strings.TrimSpace(reason)
	if payment.ID < 1 || payment.Status != StatusPaid || refundNo == "" || len(refundNo) > 200 || amount < 1 || amount > payment.AmountMinor || reason == "" || len(reason) > 500 || now.IsZero() {
		return Refund{}, ErrInvalid
	}
	return Refund{PaymentID: payment.ID, Provider: payment.Provider, RefundNo: refundNo, Reason: reason, AmountMinor: amount, Status: RefundRequested, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}
func (r Refund) BindEffect(expected int64, effectID string, now time.Time) (Refund, error) {
	if expected != r.Version {
		return Refund{}, ErrVersion
	}
	if r.Status != RefundRequested || !validEffectID(effectID) || now.Before(r.UpdatedAt) {
		return Refund{}, ErrTransition
	}
	r.Status = RefundEffectAccepted
	r.EffectID = effectID
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, nil
}
func (r Refund) Complete(expected int64, status RefundStatus, now time.Time) (Refund, error) {
	if expected != r.Version {
		return Refund{}, ErrVersion
	}
	if now.Before(r.UpdatedAt) || (r.Status != RefundEffectAccepted && r.Status != RefundOutcomeUnknown) || (status != RefundOutcomeUnknown && status != RefundCompleted && status != RefundFinalFailed) {
		return Refund{}, ErrTransition
	}
	r.Status = status
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, nil
}
func validEffectID(value string) bool {
	if !strings.HasPrefix(value, "eer_") || len(value) < 5 {
		return false
	}
	for _, r := range value[4:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value[4] != '0'
}
