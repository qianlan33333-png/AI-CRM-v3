package outbound

import (
	"context"
	"errors"
	"strconv"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type PrivateMessageIntent = outboundport.PrivateMessageIntent
type PrivateMessageTarget = outboundport.PrivateMessageTarget
type PrivateMessageAttachment = outboundport.PrivateMessageAttachment
type PrivateMessagePayload = outboundport.PrivateMessagePayload
type PrivateMessageProviderReceipt = outboundport.PrivateMessageProviderReceipt
type PrivateMessageIntentReader = outboundport.PrivateMessageIntentReader
type PrivateMessageTargetResolver = outboundport.PrivateMessageTargetResolver
type PrivateMessagePayloadReader = outboundport.PrivateMessagePayloadReader
type PrivateMessageSender = outboundport.PrivateMessageSender
type PrivateMessageSendError = outboundport.PrivateMessageSendError

type PrivateMessageProvider struct {
	enabled  bool
	intents  PrivateMessageIntentReader
	targets  PrivateMessageTargetResolver
	payloads PrivateMessagePayloadReader
	sender   PrivateMessageSender
}

func NewPrivateMessageProvider(enabled bool, intents PrivateMessageIntentReader, targets PrivateMessageTargetResolver, payloads PrivateMessagePayloadReader, sender PrivateMessageSender) (*PrivateMessageProvider, error) {
	if intents == nil || targets == nil || payloads == nil || sender == nil {
		return nil, errors.New("private message provider dependencies are required")
	}
	return &PrivateMessageProvider{enabled, intents, targets, payloads, sender}, nil
}
func (p *PrivateMessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	base := effectport.Hash("outbound.private-message", string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10))
	if p == nil || !p.enabled || envelope.Kind != effectport.KindOutboundMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "disabled")}, nil
	}
	intent, err := p.intents.PrivateMessageIntentForEnvelope(ctx, envelope)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "intent-unavailable")}, nil
	}
	target, err := p.targets.ResolvePrivateMessageTarget(ctx, intent.CustomerID, intent.StaffID)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "target-unavailable")}, nil
	}
	payload, err := p.payloads.LoadPrivateMessagePayload(ctx, intent.PayloadReference, intent.PayloadDigest)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "payload-unavailable")}, nil
	}
	receipt, attempted, err := p.sender.SendPrivateMessage(ctx, target, payload)
	if err != nil {
		state := effectport.StateRetryable
		if failure, ok := err.(PrivateMessageSendError); ok {
			state = effectport.StateFinalFailed
			if attempted && failure.OutcomeUnknown() {
				state = effectport.StateUnknown
			}
		} else if attempted {
			state = effectport.StateUnknown
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash(string(base), "provider-error"), CallAttempted: attempted, RealExternalCallExecuted: attempted}, nil
	}
	if !attempted || receipt.MessageID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-rejected")}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash(string(base), "provider-accepted", receipt.MessageID), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

var _ effectport.ProviderAdapter = (*PrivateMessageProvider)(nil)
