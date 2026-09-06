package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
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
	return customerport.OwnerHandoffExecution{EffectID: "eer_9", SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadRefDigest: string(envelope.PayloadDigest), PolicyRefDigest: string(envelope.PolicyVersionHash), SourceUserID: "source", TargetUserID: "target", SourceDigest: "sha256:source-snapshot", TargetDigest: "sha256:target-snapshot", PayloadDigest: string(envelope.PayloadDigest), PolicyDigest: string(envelope.PolicyVersionHash), Lines: []customerport.OwnerHandoffExecutionLine{{Line: 1, CustomerID: 1, ExternalUserID: "external"}}}
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
	if err != nil || result.Completion != effectport.StateUnknown || !result.CallAttempted {
		t.Fatalf("cross-target result=%+v err=%v", result, err)
	}
}

func TestCustomerOwnerHandoffProviderTreatsDefinitiveProviderRejectionAsFinal(t *testing.T) {
	writer := &ownerHandoffWriterStub{err: wecomport.WrapProviderWriteOutcome(errors.New("provider rejected request"), true, false)}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: ownerHandoffExecution()}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, callErr := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if callErr == nil || result.Completion != effectport.StateFinalFailed || !result.CallAttempted || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, callErr)
	}
}

func TestCustomerOwnerHandoffProviderReturnsPerLineArtifactForPartialAndRefusedBatch(t *testing.T) {
	execution := ownerHandoffExecution()
	execution.Lines = []customerport.OwnerHandoffExecutionLine{
		{Line: 1, CustomerID: 11, ExternalUserID: "external-1"},
		{Line: 2, CustomerID: 12, ExternalUserID: "external-2"},
		{Line: 3, CustomerID: 13, ExternalUserID: "external-3"},
	}
	writer := &ownerHandoffWriterStub{result: wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{"external-1"}, RejectedExternalUserIDs: []string{"external-2", "external-3"}, FailedCount: 2}}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: execution}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateExecuted || !result.Artifact.Valid() || result.Artifact.Kind != customerOwnerHandoffArtifactKind || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
	var artifact customerOwnerHandoffArtifact
	if err = json.Unmarshal(result.Artifact.Payload, &artifact); err != nil || len(artifact.Lines) != 3 || artifact.Lines[0].Line != 1 || artifact.Lines[0].State != "provider_accepted" || artifact.Lines[1].State != "final_failed" || artifact.Lines[2].State != "final_failed" || artifact.Lines[0].EvidenceDigest == "" {
		t.Fatalf("artifact=%+v err=%v", artifact, err)
	}
}

func TestCustomerOwnerHandoffProviderTreatsMissingOrAmbiguousBatchRowsAsUnknown(t *testing.T) {
	execution := ownerHandoffExecution()
	execution.Lines = []customerport.OwnerHandoffExecutionLine{
		{Line: 1, CustomerID: 11, ExternalUserID: "external-1"},
		{Line: 2, CustomerID: 12, ExternalUserID: "external-2"},
	}
	writer := &ownerHandoffWriterStub{result: wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{"external-1"}}}
	provider, err := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: execution}, writer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), ownerHandoffEnvelope(), effectport.Attempt{EffectID: "eer_9"})
	if err != nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || !result.Artifact.Valid() || writer.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, writer.calls, err)
	}
	var artifact customerOwnerHandoffArtifact
	if err = json.Unmarshal(result.Artifact.Payload, &artifact); err != nil || len(artifact.Lines) != 2 || artifact.Lines[0].State != "provider_accepted" || artifact.Lines[1].State != "outcome_unknown" {
		t.Fatalf("artifact=%+v err=%v", artifact, err)
	}
}

type ownerHandoffCompletionWriterStub struct {
	completions []customerport.OwnerHandoffCompletion
}

func (stub *ownerHandoffCompletionWriterStub) CompleteOwnerHandoffEffect(_ context.Context, completion customerport.OwnerHandoffCompletion) error {
	stub.completions = append(stub.completions, completion)
	return nil
}

func TestCustomerOwnerHandoffLeafProviderAndSinkPreservePartial101Fixture(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/transfer_customer":
			calls++
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			ids, ok := body["external_userid"].([]any)
			if !ok || (len(ids) != 100 && len(ids) != 1) {
				t.Fatalf("batch ids=%v", body)
			}
			rows := make([]map[string]any, 0, len(ids))
			for index, rawID := range ids {
				id, ok := rawID.(string)
				if !ok {
					t.Fatalf("non-string external id=%v", rawID)
				}
				if len(ids) == 100 && index == 99 { // missing response row
					continue
				}
				row := map[string]any{"external_userid": id, "errcode": 0}
				if len(ids) == 100 && index == 98 { // explicit per-row refusal
					row["errcode"] = 40003
				}
				rows = append(rows, row)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": rows})
		default:
			t.Fatalf("unexpected endpoint=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp", ContactSecret: "secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	envelope := ownerHandoffEnvelope()
	makeExecution := func(effectID string, first, count int) customerport.OwnerHandoffExecution {
		execution := ownerHandoffExecution()
		execution.EffectID = effectID
		execution.Lines = make([]customerport.OwnerHandoffExecutionLine, 0, count)
		for index := first; index < first+count; index++ {
			execution.Lines = append(execution.Lines, customerport.OwnerHandoffExecutionLine{Line: int64(index + 1), CustomerID: customerport.OwnerHandoffExecutionLine{}.CustomerID + 1, ExternalUserID: fmt.Sprintf("external-%03d", index)})
		}
		return execution
	}
	first := makeExecution("eer_100", 0, 100)
	second := makeExecution("eer_101", 100, 1)
	writer := &ownerHandoffCompletionWriterStub{}
	sink, err := NewCustomerOwnerHandoffCompletionSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range []customerport.OwnerHandoffExecution{first, second} {
		provider, providerErr := NewCustomerOwnerHandoffProvider(ownerHandoffReaderStub{value: execution}, client)
		if providerErr != nil {
			t.Fatal(providerErr)
		}
		result, executeErr := provider.Execute(context.Background(), envelope, effectport.Attempt{EffectID: execution.EffectID, Number: 1, Generation: 1, Fence: 1})
		if executeErr != nil || !result.Artifact.Valid() {
			t.Fatalf("effect=%s result=%+v err=%v", execution.EffectID, result, executeErr)
		}
		if execution.EffectID == "eer_100" && result.Completion != effectport.StateUnknown {
			t.Fatalf("partial 100 completion=%s", result.Completion)
		}
		if execution.EffectID == "eer_101" && result.Completion != effectport.StateExecuted {
			t.Fatalf("final 1 completion=%s", result.Completion)
		}
		if err = sink.CompleteEffect(context.Background(), execution.EffectID, envelope, effectport.Attempt{EffectID: execution.EffectID, Number: 1, Generation: 1, Fence: 1}, result); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(writer.completions) != 2 || writer.completions[0].State != string(effectport.StateUnknown) || len(writer.completions[0].Lines) != 100 || writer.completions[0].Lines[97].State != "provider_accepted" || writer.completions[0].Lines[98].State != "final_failed" || writer.completions[0].Lines[99].State != "outcome_unknown" || writer.completions[1].State != string(effectport.StateExecuted) || len(writer.completions[1].Lines) != 1 || writer.completions[1].Lines[0].State != "provider_accepted" {
		t.Fatalf("calls=%d completions=%+v", calls, writer.completions)
	}
}
