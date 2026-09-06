package outbound

import (
	"context"
	"errors"
	"testing"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ownerHandoffReaderStub struct {
	value customerport.OwnerHandoffExecution
	err   error
}

func (s ownerHandoffReaderStub) ReadOwnerHandoffExecution(context.Context, string) (customerport.OwnerHandoffExecution, error) {
	return s.value, s.err
}

type ownerHandoffWriterStub struct {
	result wecomport.CustomerTransferResult
	err    error
	calls  int
}

func (s *ownerHandoffWriterStub) TransferCustomer(context.Context, string, string, []string, string) (wecomport.CustomerTransferResult, error) {
	s.calls++
	return s.result, s.err
}

func (s *ownerHandoffWriterStub) TransferResult(context.Context, string, string, string) (wecomport.CustomerTransferResult, error) {
	return wecomport.CustomerTransferResult{}, nil
}

func ownerHandoffEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerOwnerHandoff, SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload"), PolicyVersionHash: effectport.Hash("policy")}
}

func ownerHandoffExecution() customerport.OwnerHandoffExecution {
	envelope := ownerHandoffEnvelope()
	return customerport.OwnerHandoffExecution{EffectID: "eer_9", SourceUserID: "source", TargetUserID: "target", ExternalUserID: "external", SourceDigest: string(envelope.SourceRefDigest), TargetDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyDigest: string(envelope.PolicyVersionHash)}
}

func TestCustomerOwnerHandoffProviderDoesNotRetryAttemptedFailure(t *testing.T) {
	writer := &ownerHandoffWriterStub{err: wecomport.WrapProviderWriteError(errors.New("lost response"), true)}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: ownerHandoffExecution()}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err == nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
}

func TestCustomerOwnerHandoffProviderAcceptsOnlyExactFrozenTarget(t *testing.T) {
	writer := &ownerHandoffWriterStub{result: wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{"external"}}}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: ownerHandoffExecution()}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateExecuted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	writer.result.AcceptedExternalUserIDs = []string{"wrong-customer"}
	result, err = provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateFinalFailed {
		t.Fatalf("cross-target result=%+v err=%v", result, err)
	}
}
