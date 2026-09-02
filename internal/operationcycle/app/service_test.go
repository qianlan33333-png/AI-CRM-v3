package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type operationCycleTestUOW struct{ calls int }

func (uow *operationCycleTestUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	return callback(ctx)
}

var _ platformport.UnitOfWork = (*operationCycleTestUOW)(nil)

type operationCycleTestEvents struct{ events []operationport.Event }

func (events *operationCycleTestEvents) Append(_ context.Context, event operationport.Event) (operationport.EventID, error) {
	events.events = append(events.events, event)
	return operationport.EventID(len(events.events)), nil
}

type operationCycleTestDeliveries struct{ accepted []operationport.EventID }

func (deliveries *operationCycleTestDeliveries) Accept(_ context.Context, eventID operationport.EventID, consumer string) error {
	if consumer != operationport.ConsumerOperationCycleFact {
		return errors.New("unexpected consumer")
	}
	deliveries.accepted = append(deliveries.accepted, eventID)
	return nil
}

type operationCycleStoreStub struct {
	report func(context.Context, ReportCommand, time.Time) (map[string]any, bool, error)
	event  func(context.Context, ActionEventCommand, time.Time) (map[string]any, bool, error)
}

func (stub *operationCycleStoreStub) Report(ctx context.Context, command ReportCommand, now time.Time) (map[string]any, bool, error) {
	if stub.report == nil {
		return nil, false, ErrUnavailable
	}
	return stub.report(ctx, command, now)
}
func (*operationCycleStoreStub) ListStrategies(context.Context, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) GetStrategy(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) ListRuns(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) GetRun(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) Start(context.Context, StartCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, ErrUnavailable
}
func (*operationCycleStoreStub) CurrentAction(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) GetActionResult(context.Context, string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) Claim(context.Context, string, string, time.Time, time.Duration) (map[string]any, bool, error) {
	return nil, false, ErrUnavailable
}
func (stub *operationCycleStoreStub) RecordActionEvent(ctx context.Context, command ActionEventCommand, now time.Time) (map[string]any, bool, error) {
	if stub.event == nil {
		return nil, false, ErrUnavailable
	}
	return stub.event(ctx, command, now)
}
func (*operationCycleStoreStub) Heartbeat(context.Context, RunnerHeartbeatCommand, time.Time) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) ContextIndex(context.Context, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) StrategyContext(context.Context, string, string, int32, int32, map[string]string) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) CreateProposal(context.Context, ProposalCommand, time.Time) (map[string]any, bool, error) {
	return nil, false, ErrUnavailable
}
func (*operationCycleStoreStub) ListProposals(context.Context, string, int32, int32) (map[string]any, error) {
	return nil, ErrUnavailable
}
func (*operationCycleStoreStub) DecideProposal(context.Context, string, string, string, time.Time) (map[string]any, error) {
	return nil, ErrUnavailable
}

func TestReportIdempotentReplayDoesNotAppendAnotherEvent(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	deliveries := &operationCycleTestDeliveries{}
	storeCalls := 0
	service := NewService(uow, &operationCycleStoreStub{report: func(_ context.Context, _ ReportCommand, _ time.Time) (map[string]any, bool, error) {
		storeCalls++
		return map[string]any{"accepted": true}, storeCalls > 1, nil
	}}, events, deliveries)
	command := ReportCommand{IdempotencyKey: "report-1", ReporterID: "runner-a", ClientID: "client-a", Snapshot: map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "growth", "run_key": "run-1",
	}}
	if _, err := service.Report(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Report(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if uow.calls != 2 || len(events.events) != 1 || len(deliveries.accepted) != 1 {
		t.Fatalf("uow/events/deliveries=%d/%d/%d, want 2/1/1", uow.calls, len(events.events), len(deliveries.accepted))
	}
	if events.events[0].Type != operationport.EvOperationCycleFact {
		t.Fatalf("event type=%q", events.events[0].Type)
	}
}

func TestCompletedOutcomeUnknownIsRecordedWithoutAutomaticRetry(t *testing.T) {
	uow := &operationCycleTestUOW{}
	events := &operationCycleTestEvents{}
	deliveries := &operationCycleTestDeliveries{}
	storeCalls := 0
	service := NewService(uow, &operationCycleStoreStub{event: func(_ context.Context, command ActionEventCommand, _ time.Time) (map[string]any, bool, error) {
		storeCalls++
		if command.EventType != "completed" || command.Result["outcome"] != "outcome_unknown" {
			return nil, false, ErrInvalid
		}
		return map[string]any{"request_id": command.RequestID, "status": "completed", "result": command.Result}, false, nil
	}}, events, deliveries)
	_, err := service.RecordActionEvent(context.Background(), ActionEventCommand{
		RequestID: "ocact_0123456789012345678901234567", EventID: "event-1", EventType: "completed", LeaseToken: "lease-1",
		Result: map[string]any{"outcome": "outcome_unknown"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if storeCalls != 1 || len(events.events) != 1 || len(deliveries.accepted) != 1 {
		t.Fatalf("store/events/deliveries=%d/%d/%d, want one terminal fact only", storeCalls, len(events.events), len(deliveries.accepted))
	}
	var payload map[string]any
	if err = json.Unmarshal(events.events[0].Payload, &payload); err != nil || payload["fact_type"] != "operation_cycle.action_completed" {
		t.Fatalf("payload=%v err=%v", payload, err)
	}
}

func TestDigestTreatsEquivalentJSONNumbersTheSameAndRejectsForbiddenScopeInput(t *testing.T) {
	integerDigest, err := Digest(map[string]any{"value": 1})
	if err != nil {
		t.Fatal(err)
	}
	decimalDigest, err := Digest(map[string]any{"value": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if integerDigest != decimalDigest {
		t.Fatal("numeric-equivalent JSON digests drifted")
	}
	service := NewService(&operationCycleTestUOW{}, &operationCycleStoreStub{}, &operationCycleTestEvents{}, &operationCycleTestDeliveries{})
	_, err = service.Report(context.Background(), ReportCommand{IdempotencyKey: "report-scope", ReporterID: "runner-a", ClientID: "client-a", Snapshot: map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "growth", "run_key": "run-1", "ten" + "ant_id": "forbidden",
	}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("forbidden scope report error=%v, want ErrInvalid", err)
	}
}

func TestStrategyContextRejectsExcludedDataPlaneFilters(t *testing.T) {
	service := NewService(&operationCycleTestUOW{}, &operationCycleStoreStub{}, &operationCycleTestEvents{}, &operationCycleTestDeliveries{})
	for _, key := range []string{"order_id", "entitlement_id", "membership_id", "subscription_id"} {
		_, err := service.StrategyContext(context.Background(), "weekly.review", "review", 10, 0, map[string]string{key: "opaque"})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("filter %q error=%v, want ErrInvalid", key, err)
		}
	}
}
