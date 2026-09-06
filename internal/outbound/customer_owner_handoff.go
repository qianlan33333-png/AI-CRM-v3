package outbound

import (
	"context"
	"errors"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type CustomerOwnerHandoffProvider struct {
	reader customerport.OwnerHandoffExecutionReader
	writer wecomport.CustomerTransferWriter
}

func NewCustomerOwnerHandoffProvider(reader customerport.OwnerHandoffExecutionReader, writer wecomport.CustomerTransferWriter) (*CustomerOwnerHandoffProvider, error) {
	if reader == nil || writer == nil {
		return nil, errors.New("owner handoff provider dependencies are required")
	}
	return &CustomerOwnerHandoffProvider{reader: reader, writer: writer}, nil
}

func (provider *CustomerOwnerHandoffProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || provider.reader == nil || provider.writer == nil || envelope.Kind != effectport.KindCustomerOwnerHandoff || attempt.EffectID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.invalid")}, nil
	}
	execution, err := provider.reader.ReadOwnerHandoffExecution(ctx, attempt.EffectID)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("owner-handoff.intent-unavailable", attempt.EffectID)}, nil
	}
	if execution.EffectID != attempt.EffectID || execution.SourceRefDigest != string(envelope.SourceRefDigest) || execution.TargetRefDigest != string(envelope.TargetRefDigest) || execution.PayloadRefDigest != string(envelope.PayloadDigest) || execution.PolicyRefDigest != string(envelope.PolicyVersionHash) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.snapshot-mismatch", attempt.EffectID)}, nil
	}
	result, err := provider.writer.TransferCustomer(ctx, execution.SourceUserID, execution.TargetUserID, []string{execution.ExternalUserID}, execution.WelcomeMessage)
	if err != nil {
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateFinalFailed
		if attempted && wecomport.ProviderOutcomeUnknown(err) {
			state = effectport.StateUnknown
		}
		if !attempted && wecomport.ProviderRetryable(err) {
			state = effectport.StateRetryable
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("owner-handoff.provider-error", attempt.EffectID), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if len(result.AcceptedExternalUserIDs) != 1 || result.AcceptedExternalUserIDs[0] != execution.ExternalUserID || result.FailedCount != 0 {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("owner-handoff.provider-rejected", attempt.EffectID), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("owner-handoff.provider-accepted", attempt.EffectID), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

type CustomerOwnerHandoffCompletionSink struct {
	writer customerport.OwnerHandoffCompletionWriter
}

func NewCustomerOwnerHandoffCompletionSink(writer customerport.OwnerHandoffCompletionWriter) (*CustomerOwnerHandoffCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("owner handoff completion writer is required")
	}
	return &CustomerOwnerHandoffCompletionSink{writer: writer}, nil
}

func (sink *CustomerOwnerHandoffCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || sink.writer == nil || envelope.Kind != effectport.KindCustomerOwnerHandoff || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid owner handoff completion")
	}
	return sink.writer.CompleteOwnerHandoffEffect(ctx, customerport.OwnerHandoffCompletion{
		EffectID: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number,
	})
}

var _ effectport.ProviderAdapter = (*CustomerOwnerHandoffProvider)(nil)
var _ effectport.CompletionSink = (*CustomerOwnerHandoffCompletionSink)(nil)
