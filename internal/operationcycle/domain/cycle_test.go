package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStrategyValidationAndLocalStatusTransitions(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	strategy := Strategy{
		Key: "weekly.review", Title: "每周复盘", Status: StatusActive, Version: 2,
		Definition: json.RawMessage(`{"schema_version":"operation_cycle_definition.v1","stages":["review"]}`),
		Snapshot:   json.RawMessage(`{"schema_version":"operation_cycle_snapshot.v1","strategy_key":"weekly.review","run_key":"run-1"}`),
		UpdatedAt:  now,
	}
	if err := ValidateStrategy(strategy); err != nil {
		t.Fatalf("ValidateStrategy() error = %v", err)
	}
	if CanTransitionStrategyStatus(StatusActive, StatusPaused) == false || !CanTransitionStrategyStatus(StatusPaused, StatusActive) || CanTransitionStrategyStatus(StatusArchived, StatusActive) {
		t.Fatal("strategy status transitions do not enforce local enable/pause/archive lifecycle")
	}
	strategy.Definition = json.RawMessage(`{"customer_id":42}`)
	if err := ValidateStrategy(strategy); err != ErrInvalidStrategy {
		t.Fatalf("customer-scoped strategy error = %v", err)
	}
}

func TestActionStageTransitionAndLifecycleValidation(t *testing.T) {
	for _, transition := range [][2]string{
		{ActionQueued, ActionClaimed},
		{ActionClaimed, ActionThreadBound},
		{ActionThreadBound, ActionTurnStarted},
		{ActionTurnStarted, ActionCompleted},
		{ActionQueued, ActionFailed},
	} {
		if !CanTransitionActionStatus(transition[0], transition[1]) {
			t.Fatalf("transition %q -> %q was rejected", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]string{{ActionCompleted, ActionClaimed}, {ActionFailed, ActionCompleted}, {ActionQueued, "unknown"}} {
		if CanTransitionActionStatus(transition[0], transition[1]) {
			t.Fatalf("transition %q -> %q was accepted", transition[0], transition[1])
		}
	}

	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	action := ActionRequest{
		ID: "ocact_0123456789012345678901234567", StrategyKey: "weekly.review", RunKey: "run-1",
		ActionKey: "review", ActionTitle: "复盘", StrategyVersion: 1, RunnerID: "runner-1", Status: ActionCompleted,
		CreatedBy: "operator", CreatedAt: now, UpdatedAt: now,
	}
	if err := ValidateAction(action); err == nil {
		t.Fatal("completed action without completion time was accepted")
	}
	completed := now.Add(time.Minute)
	action.CompletedAt = &completed
	if err := ValidateAction(action); err != nil {
		t.Fatalf("completed action validation error = %v", err)
	}
}

func TestContainsForbiddenScopeRejectsIDsButAllowsLabels(t *testing.T) {
	if ContainsForbidden(map[string]any{"audience": "全量运营标签"}) {
		t.Fatal("human-readable audience label was rejected")
	}
	if ContainsForbidden(map[string]any{"order": "阶段顺序", "entitlement": "权益说明"}) {
		t.Fatal("human-readable labels were rejected")
	}
	for _, value := range []any{
		map[string]any{"customer_id": 1},
		map[string]any{"segment_ids": []any{1}},
		map[string]any{"campaign_id": "campaign-1"},
		map[string]any{"recipient_id": "recipient-1"},
		map[string]any{"order_id": "order-1"},
		map[string]any{"entitlement_id": "entitlement-1"},
		map[string]any{"membership_id": "membership-1"},
		map[string]any{"subscription_id": "subscription-1"},
		map[string]any{"service_period_id": "service-period-1"},
		map[string]string{"order_id": "order-1"},
		map[string]string{"identity_id": "identity-1"},
		json.RawMessage(`{"external_userid":"wx-user"}`),
	} {
		if !ContainsForbidden(value) {
			t.Fatalf("excluded scope was accepted: %#v", value)
		}
	}
}
