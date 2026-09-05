package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func groupMessageEnvelope() effect.Envelope {
	return effect.Envelope{
		Owner:             effect.OwnerOutbound,
		Kind:              effect.KindGroupMessage,
		SourceRefDigest:   effect.Hash("group-ops.run", "1"),
		TargetRefDigest:   effect.Hash("group-ops.target", "group-a"),
		PayloadDigest:     effect.Hash("group-ops.payload", "message"),
		PolicyVersionHash: effect.Hash("group-ops.policy", "v1"),
	}
}

func TestDisabledGroupMessageProviderIsDeterministicAndNeverExecutes(t *testing.T) {
	provider := NewDisabledGroupMessageProvider()
	envelope := groupMessageEnvelope()
	first, err := provider.Execute(context.Background(), envelope, effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || first.Completion != effect.StateFinalFailed || !effect.ValidDigest(first.ReceiptDigest) || first.CallAttempted || first.RealExternalCallExecuted {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := provider.Execute(context.Background(), envelope, effect.Attempt{Number: 99, Generation: 7, Fence: 8})
	if err != nil || second.ReceiptDigest != first.ReceiptDigest || second.Completion != effect.StateFinalFailed || second.CallAttempted || second.RealExternalCallExecuted {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	invalid, err := provider.Execute(context.Background(), effect.Envelope{Kind: effect.KindGroupMessage}, effect.Attempt{})
	if err != nil || invalid.Completion != effect.StateFinalFailed || !effect.ValidDigest(invalid.ReceiptDigest) {
		t.Fatalf("invalid=%+v err=%v", invalid, err)
	}
}

type preparationWriterStub struct {
	called bool
}

func (writer *preparationWriterStub) RecordPreparedGroupOpsMaterials(_ context.Context, _ mediaport.GroupOpsMaterialPreparationCommand) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	writer.called = true
	return mediaport.GroupOpsMaterialPreparationReceipt{ID: 1}, nil
}

func TestGroupMessageProviderKeepsPreparationWriteDisabled(t *testing.T) {
	writer := &preparationWriterStub{}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{PreparationWriter: writer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.RecordPreparedMaterials(context.Background(), mediaport.GroupOpsMaterialPreparationCommand{}); !errors.Is(err, ErrGroupMessageProviderDisabled) || writer.called {
		t.Fatalf("disabled preparation write err=%v called=%t", err, writer.called)
	}
	result, err := provider.Execute(context.Background(), groupMessageEnvelope(), effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("disabled execution=%+v err=%v", result, err)
	}

	enabled, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, PreparationWriter: writer})
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := enabled.RecordPreparedMaterials(context.Background(), mediaport.GroupOpsMaterialPreparationCommand{}); err != nil || receipt.ID != 1 || !writer.called {
		t.Fatalf("approved preparation write receipt=%+v err=%v called=%t", receipt, err, writer.called)
	}
	result, err = enabled.Execute(context.Background(), groupMessageEnvelope(), effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("unconfigured execution=%+v err=%v", result, err)
	}
}

func TestEnabledGroupMessageProviderWithoutLeafReturnsNotConfigured(t *testing.T) {
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	envelope := groupMessageEnvelope()
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: "eer_11", Number: 1, Generation: 1, Fence: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := effect.Hash("group-ops.provider.v1", string(envelope.Fingerprint()), "eer_11", "1", "1", "1")
	if result.Completion != effect.StateFinalFailed || result.ReceiptDigest != effect.Hash(string(base), "not-configured") || result.CallAttempted || result.RealExternalCallExecuted {
		t.Fatalf("enabled but unconfigured provider=%+v", result)
	}
}

type groupDispatchReaderStub struct {
	value groupopsport.DispatchExecution
	err   error
}

func (s groupDispatchReaderStub) LoadDispatchExecution(_ context.Context, effectID string) (groupopsport.DispatchExecution, error) {
	if s.err != nil {
		return groupopsport.DispatchExecution{}, s.err
	}
	if effectID != s.value.ExternalEffectID {
		return groupopsport.DispatchExecution{}, errors.New("unexpected effect id")
	}
	return s.value, nil
}

type groupMessageSenderStub struct {
	request   wecomport.GroupMessageRequest
	attempted bool
	receipt   wecomport.GroupMessageReceipt
	err       error
}

type materialReadinessStub struct{ err error }

func (stub materialReadinessStub) VerifyMaterialReady(context.Context, json.RawMessage, json.RawMessage, string, time.Time) error {
	return stub.err
}

func (s *groupMessageSenderStub) SendGroupMessage(_ context.Context, request wecomport.GroupMessageRequest) (wecomport.GroupMessageReceipt, bool, error) {
	s.request = request
	return s.receipt, s.attempted, s.err
}

func TestGroupMessageProviderUsesEffectBoundSnapshotAndExactChat(t *testing.T) {
	content, err := json.Marshal(map[string]any{"schema_version": 1, "node_id": 4, "position": 2, "kind": "message", "message_text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	material := []byte(`{"schema_version":1,"references":[]}`)
	effectID := "eer_42"
	envelope := groupMessageEnvelope()
	reader := groupDispatchReaderStub{value: groupopsport.DispatchExecution{
		ExecutionID: 42, ExternalEffectID: effectID, State: groupopsport.ExecutionAccepted, TargetReference: "chat-42", SenderUserID: "owner-42",
		ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(content))),
		MaterialSnapshot: material, MaterialDigest: func() string {
			normalized, _ := canonicalGroupMessageJSON(material)
			return string(effect.Hash("group-ops.material.snapshot.v1", string(normalized)))
		}(),
		SourceRefDigest: string(envelope.SourceRefDigest), TargetRefDigest: string(envelope.TargetRefDigest), PayloadDigest: string(envelope.PayloadDigest), PolicyVersionHash: string(envelope.PolicyVersionHash),
	}}
	if _, requestErr := groupMessageRequest(reader.value); requestErr != nil {
		t.Fatalf("request err=%v", requestErr)
	}
	sender := &groupMessageSenderStub{attempted: true, receipt: wecomport.GroupMessageReceipt{MessageID: "msg-42"}}
	provider, err := NewGroupMessageProvider(GroupMessageProviderConfig{Enabled: true, Executions: reader, Materials: materialReadinessStub{}, Writer: sender})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), envelope, effect.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 9})
	if err != nil || result.Completion != effect.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if sender.request.SenderUserID != "owner-42" || sender.request.Text != "hello" || len(sender.request.ChatIDs) != 1 || sender.request.ChatIDs[0] != "chat-42" {
		t.Fatalf("exact WeCom request=%+v", sender.request)
	}
}

func TestGroupMessageRequestCanonicalizesJSONBSnapshotsWithoutWeakeningDigestChecks(t *testing.T) {
	content := []byte(` { "message_text" : "hello", "kind":"message", "schema_version":1 } `)
	material := []byte(` { "references": [ ], "schema_version": 1 } `)
	canonicalContent, err := canonicalGroupMessageJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	canonicalMaterial, err := canonicalGroupMessageJSON(material)
	if err != nil {
		t.Fatal(err)
	}
	execution := groupopsport.DispatchExecution{ExecutionID: 7, TargetReference: "chat-7", SenderUserID: "owner-7", ContentSnapshot: content, ContentDigest: string(effect.Hash("group-ops.content.snapshot.v1", string(canonicalContent))), MaterialSnapshot: material, MaterialDigest: string(effect.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial)))}
	if _, err = groupMessageRequest(execution); err != nil {
		t.Fatalf("JSONB equivalent snapshots rejected: %v", err)
	}
	execution.ContentSnapshot = []byte(`{"schema_version":1,"kind":"message","message_text":"changed"}`)
	if _, err = groupMessageRequest(execution); err == nil {
		t.Fatal("semantic content change bypassed digest check")
	}
}

type groupMessageProjectionStub struct {
	effectRef        string
	state            groupopsport.ExecutionState
	providerAccepted bool
	deliveryProven   bool
	receiptDigest    string
	attempt          int32
	completedAt      time.Time
}

func (s *groupMessageProjectionStub) CompleteEffect(_ context.Context, effectRef string, state groupopsport.ExecutionState, providerAccepted, deliveryProven bool, receiptDigest string, attempt int32, completedAt time.Time) error {
	s.effectRef = effectRef
	s.state = state
	s.providerAccepted = providerAccepted
	s.deliveryProven = deliveryProven
	s.receiptDigest = receiptDigest
	s.attempt = attempt
	s.completedAt = completedAt
	return nil
}

func TestGroupMessageCompletionSinkProjectsTerminalAndUnknownStates(t *testing.T) {
	projector := &groupMessageProjectionStub{}
	sink, err := NewGroupMessageCompletionSink(projector)
	if err != nil {
		t.Fatal(err)
	}
	envelope := groupMessageEnvelope()
	receipt := effect.Hash("provider", "receipt")
	if err := sink.CompleteEffect(context.Background(), "eer_7", envelope, effect.Attempt{Number: 2}, effect.AdapterResult{Completion: effect.StateFinalFailed, ReceiptDigest: receipt}); err != nil {
		t.Fatal(err)
	}
	if projector.effectRef != "eer_7" || projector.state != groupopsport.ExecutionFinalFailed || projector.providerAccepted || projector.deliveryProven || projector.receiptDigest != string(receipt) || projector.attempt != 2 || projector.completedAt.IsZero() {
		t.Fatalf("terminal projection=%+v", projector)
	}
	if err := sink.CompleteEffect(context.Background(), "eer_7", envelope, effect.Attempt{Number: 3}, effect.AdapterResult{Completion: effect.StateUnknown, ReceiptDigest: receipt, CallAttempted: true}); err != nil {
		t.Fatal(err)
	}
	if projector.state != groupopsport.ExecutionOutcomeUnknown || projector.providerAccepted || projector.deliveryProven || projector.attempt != 3 {
		t.Fatalf("unknown projection=%+v", projector)
	}
}

type groupMessageReceiptWriterStub struct {
	task groupopsport.GroupMessageReceipt
	err  error
}

func (s *groupMessageReceiptWriterStub) RecordGroupMessageTask(_ context.Context, task groupopsport.GroupMessageReceipt) error {
	s.task = task
	return s.err
}

func TestGroupMessageCompletionSinkPersistsOnlyValidatedTaskArtifact(t *testing.T) {
	projector := &groupMessageProjectionStub{}
	writer := &groupMessageReceiptWriterStub{}
	sink, err := NewGroupMessageCompletionSink(projector, writer)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"execution_id":"42","effect_id":"eer_42","msgid":"msg-42","sender":"owner-42","chat_id":"chat-42"}`)
	artifact := effect.ResultArtifact{Kind: "group-ops.wecom-task.v1", Payload: payload}
	artifact.Digest = effect.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
	if err = sink.CompleteEffect(context.Background(), "eer_42", groupMessageEnvelope(), effect.Attempt{Number: 1}, effect.AdapterResult{Completion: effect.StateExecuted, ReceiptDigest: effect.Hash("receipt", "42"), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}); err != nil {
		t.Fatal(err)
	}
	if writer.task.ExecutionID != 42 || writer.task.MessageID != "msg-42" || writer.task.ExternalEffectID != "eer_42" || writer.task.TaskEvidenceDigest != string(artifact.Digest) {
		t.Fatalf("task=%+v", writer.task)
	}
	if projector.state != groupopsport.ExecutionProviderAccepted || !projector.providerAccepted || projector.deliveryProven {
		t.Fatalf("projection=%+v", projector)
	}
}
