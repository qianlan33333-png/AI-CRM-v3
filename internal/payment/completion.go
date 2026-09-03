package payment

import (
	"context"
	"errors"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type EffectProjector interface {
	CompleteEffectWithin(context.Context, string, effectport.Envelope, effectport.Attempt, effectport.AdapterResult) error
}

type CompletionSink struct{ projector EffectProjector }

func NewCompletionSink(projector EffectProjector) (*CompletionSink, error) {
	if projector == nil {
		return nil, errors.New("payment completion projector is required")
	}
	return &CompletionSink{projector: projector}, nil
}

func (sink *CompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || envelope.Owner != effectport.OwnerPayment {
		return errors.New("unsupported payment completion")
	}
	return sink.projector.CompleteEffectWithin(ctx, effectRef, envelope, attempt, result)
}

var _ effectport.CompletionSink = (*CompletionSink)(nil)
