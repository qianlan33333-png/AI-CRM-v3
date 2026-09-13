package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

type webhookRuntimeStoreStub struct {
	groupopsport.RuntimeStore
	planID int64
	run    groupopsport.Run
	found  bool
}

func (s webhookRuntimeStoreStub) FindPlanByWebhookReference(context.Context, string) (int64, error) {
	return s.planID, nil
}
func (s webhookRuntimeStoreStub) FindRunBySourceKey(context.Context, int64, groupopsport.RunTrigger, [32]byte) (groupopsport.Run, bool, error) {
	return s.run, s.found, nil
}
func (s webhookRuntimeStoreStub) ReadRunSummary(context.Context, int64) (groupopsport.RunSummary, error) {
	return groupopsport.RunSummary{Run: s.run, Executions: []groupopsport.Execution{}, PendingIntents: []groupopsport.ExecutionIntent{}}, nil
}

type webhookMiniProgramResolverStub struct{ calls int }

func (s *webhookMiniProgramResolverStub) ResolveMaterialSnapshot(context.Context, groupopsport.MaterialPlan, time.Time) (json.RawMessage, string, error) {
	return nil, "", errors.New("not used")
}
func (s *webhookMiniProgramResolverStub) ResolveWebhookMiniProgram(context.Context, groupopsport.WebhookMiniProgramRequest) (groupopsport.MaterialReference, error) {
	s.calls++
	return groupopsport.MaterialReference{Kind: "miniprogram", ID: 8}, nil
}

type webhookEffectsStub struct{}

func (webhookEffectsStub) AcceptAndQueueWithin(context.Context, effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	return effectport.Projection{}, effectport.Receipt{}, errors.New("not used")
}

type webhookSenderStub struct{}

func (webhookSenderStub) ResolveExecutionSender(context.Context, string) (string, bool, error) {
	return "owner", true, nil
}

func webhookRuntimePlan(status groupopsport.PlanStatus, bindings []groupopsport.GroupAsset) groupopsport.Detail {
	now := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	return groupopsport.Detail{
		Plan:              groupopsport.Plan{ID: 12, Type: groupopsport.PlanTypeWebhook, Name: "正式群运营计划测试", Status: status, Revision: 4, CreatedBy: 7, UpdatedBy: 7, CreatedAt: now, UpdatedAt: now},
		Members:           []groupopsport.Member{{StaffID: 7}},
		GroupAssets:       bindings,
		Nodes:             []groupopsport.Node{},
		WebhookDescriptor: descriptor("groupops-hook"),
		Safety:            groupopsport.LocalSafety(),
	}
}

func webhookCommand(target string) groupopsport.WebhookInboundCommand {
	return groupopsport.WebhookInboundCommand{WebhookReference: "groupops-hook", TargetChatReferences: []string{target}, Messages: []groupopsport.WebhookMessage{{Type: "miniprogram", AppID: "wx-course", Path: "pages/course/index", Title: "课程详情"}}}
}

func TestWebhookRejectsInactiveOrUnboundTargetBeforeMiniProgramResolution(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   groupopsport.PlanStatus
		target   string
		bindings []groupopsport.GroupAsset
	}{
		{name: "paused", status: groupopsport.PlanPaused, target: "bound-chat", bindings: []groupopsport.GroupAsset{{ID: 1, AssetRef: "bound-chat"}}},
		{name: "unbound target", status: groupopsport.PlanActive, target: "other-chat", bindings: []groupopsport.GroupAsset{{ID: 1, AssetRef: "bound-chat"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plans := &testStore{details: map[int64]groupopsport.Detail{12: webhookRuntimePlan(test.status, test.bindings)}}
			resolver := &webhookMiniProgramResolverStub{}
			runtime := &RuntimeService{uow: testUOW{}, plans: plans, runtime: webhookRuntimeStoreStub{planID: 12}, effects: webhookEffectsStub{}, senders: webhookSenderStub{}, materials: resolver, now: func() time.Time { return time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC) }, dispatchEnabled: true}
			if _, err := runtime.AcceptWebhook(context.Background(), "groupops-hook", "webhook-event-key-0001", webhookCommand(test.target)); !errors.Is(err, ErrStateConflict) || resolver.calls != 0 {
				t.Fatalf("err=%v resolver_calls=%d", err, resolver.calls)
			}
		})
	}
}

func TestWebhookRuntimeRejectsSameEventKeyWithDifferentFrozenPayload(t *testing.T) {
	first := groupopsport.WebhookInboundCommand{WebhookReference: "groupops-hook", TargetChatReferences: []string{"bound-chat"}, Messages: []groupopsport.WebhookMessage{{Type: "text", Text: "第一条"}}}
	digest, err := webhookPayloadDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	plans := &testStore{details: map[int64]groupopsport.Detail{12: webhookRuntimePlan(groupopsport.PlanPaused, []groupopsport.GroupAsset{{ID: 1, AssetRef: "bound-chat"}})}}
	runtime := &RuntimeService{uow: testUOW{}, plans: plans, runtime: webhookRuntimeStoreStub{planID: 12, found: true, run: groupopsport.Run{ID: 91, WebhookPayloadDigest: digest}}, effects: webhookEffectsStub{}, senders: webhookSenderStub{}, now: time.Now, dispatchEnabled: true}
	second := first
	second.Messages = []groupopsport.WebhookMessage{{Type: "text", Text: "第二条"}}
	if _, err = runtime.AcceptWebhook(context.Background(), "groupops-hook", "webhook-event-key-0001", second); !errors.Is(err, ErrConflict) {
		t.Fatalf("different payload replay err=%v", err)
	}
}
