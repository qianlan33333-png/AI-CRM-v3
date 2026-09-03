package main

import (
	"context"
	"errors"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type composedProviderRouter struct {
	outbound effectport.ProviderAdapter
	payment  effectport.ProviderAdapter
}

type paymentProviderRouter struct {
	wechatPay  effectport.ProviderAdapter
	wechatShop effectport.ProviderAdapter
}

func (router paymentProviderRouter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	switch envelope.Kind {
	case effectport.KindWeChatPayPrepay, effectport.KindWeChatPayRefund:
		if router.wechatPay == nil {
			return effectport.AdapterResult{}, errors.New("wechat pay provider unavailable")
		}
		return router.wechatPay.Execute(ctx, envelope, attempt)
	case effectport.KindWeChatShopRefund:
		if router.wechatShop == nil {
			return effectport.AdapterResult{}, errors.New("wechat shop provider unavailable")
		}
		return router.wechatShop.Execute(ctx, envelope, attempt)
	default:
		return effectport.AdapterResult{}, errors.New("payment effect kind unavailable")
	}
}

func (router composedProviderRouter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Owner == effectport.OwnerPayment {
		if router.payment == nil {
			return effectport.AdapterResult{}, errors.New("payment provider unavailable")
		}
		return router.payment.Execute(ctx, envelope, attempt)
	}
	if router.outbound == nil {
		return effectport.AdapterResult{}, errors.New("outbound provider unavailable")
	}
	return router.outbound.Execute(ctx, envelope, attempt)
}

type composedCompletionRouter struct {
	outbound effectport.CompletionSink
	payment  effectport.CompletionSink
}

func (router composedCompletionRouter) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if envelope.Owner == effectport.OwnerPayment {
		if router.payment == nil {
			return errors.New("payment completion unavailable")
		}
		return router.payment.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	}
	if router.outbound == nil {
		return errors.New("outbound completion unavailable")
	}
	return router.outbound.CompleteEffect(ctx, effectRef, envelope, attempt, result)
}

var _ effectport.ProviderAdapter = composedProviderRouter{}
var _ effectport.ProviderAdapter = paymentProviderRouter{}
var _ effectport.CompletionSink = composedCompletionRouter{}
