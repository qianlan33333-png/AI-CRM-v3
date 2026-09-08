package main

import (
	"context"
	"errors"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// orderPaidEventFanout is composition-only. Both consumers join Order's
// existing settlement transaction, so a paid event, Product action snapshot,
// Customer tag intent, Outbound effect acceptance, and Commerce push intent
// cannot split across commits.
type orderPaidEventFanout struct {
	commerce orderport.PaidEventConsumer
	purchase orderport.PaidEventConsumer
}

func (f orderPaidEventFanout) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if f.commerce == nil || f.purchase == nil {
		return errors.New("order paid-event consumers are required")
	}
	if err := f.commerce.ConsumePaidEventWithin(ctx, event); err != nil {
		return err
	}
	return f.purchase.ConsumePaidEventWithin(ctx, event)
}

var _ orderport.PaidEventConsumer = orderPaidEventFanout{}
