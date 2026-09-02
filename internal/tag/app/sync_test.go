package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type syncStore struct {
	uow      *catalogUOW
	receipt  port.SyncReceipt
	accepted port.SyncReceipt
	reserve  int
	accept   int
}

func (store *syncStore) ReserveSync(_ context.Context, command port.SyncCommand) (port.SyncReceipt, error) {
	if store.uow == nil || !store.uow.in {
		return port.SyncReceipt{}, errors.New("reserve outside uow")
	}
	store.reserve++
	if store.receipt.ID == 0 {
		store.receipt = port.SyncReceipt{ID: 41, Command: command, State: port.SyncReserved}
	}
	return store.receipt, nil
}

func (store *syncStore) AcceptSync(_ context.Context, receiptID, eventID int64, effect port.SyncEffectReceipt) (port.SyncReceipt, error) {
	if store.uow == nil || !store.uow.in {
		return port.SyncReceipt{}, errors.New("accept outside uow")
	}
	store.accept++
	if receiptID != store.accepted.ID || eventID != store.accepted.EventID || effect.QueueJobID != store.accepted.Effect.QueueJobID {
		return port.SyncReceipt{}, errors.New("unexpected acceptance facts")
	}
	return store.accepted, nil
}

type syncJobs struct {
	uow   *catalogUOW
	job   port.SyncJob
	id    int64
	calls int
}

func (jobs *syncJobs) EnqueueSync(_ context.Context, job port.SyncJob) (port.SyncEffectReceipt, error) {
	if jobs.uow == nil || !jobs.uow.in {
		return port.SyncEffectReceipt{}, errors.New("enqueue outside uow")
	}
	jobs.calls++
	jobs.job = job
	return port.SyncEffectReceipt{QueueJobID: jobs.id, EffectID: 91, EffectRef: "eer_91", EffectState: "queued", AcceptReceiptID: "eerop_91", QueueReceiptID: "eerj_" + string(rune('0'+jobs.id))}, nil
}

func TestSyncServiceAcceptsManualAndDueInsideOneUOW(t *testing.T) {
	for _, kind := range []port.SyncKind{port.SyncManual, port.SyncDue} {
		t.Run(string(kind), func(t *testing.T) {
			uow := &catalogUOW{}
			command := port.SyncCommand{Actor: 7, IdempotencyKey: "sync-request-001", TraceID: "trace-sync-001", Kind: kind}
			store := &syncStore{uow: uow, accepted: port.SyncReceipt{ID: 41, Command: command, State: port.SyncAccepted, EventID: 1, Effect: port.SyncEffectReceipt{QueueJobID: 43, EffectID: 91, EffectRef: "eer_91", EffectState: "queued", AcceptReceiptID: "eerop_91", QueueReceiptID: "eerj_43"}}}
			events := &catalogEvents{uow: uow}
			jobs := &syncJobs{uow: uow, id: 43}
			service := NewSyncService(uow, store, events, jobs)
			service.now = func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }
			got, err := service.Request(context.Background(), command)
			want := port.SyncAcceptance{ReceiptID: 41, EventID: 1, QueueJobID: 43, EffectID: "eer_91", EffectState: "queued", AcceptReceiptID: "eerop_91", QueueReceiptID: "eerj_43", State: port.SyncQueued}
			if err != nil || got != want {
				t.Fatalf("Request() = %#v, %v", got, err)
			}
			if uow.calls != 1 || store.reserve != 1 || store.accept != 1 || jobs.calls != 1 || len(events.items) != 1 {
				t.Fatalf("calls = uow:%d reserve:%d accept:%d jobs:%d events:%d", uow.calls, store.reserve, store.accept, jobs.calls, len(events.items))
			}
			if jobs.job.ReceiptID != 41 || jobs.job.Actor != 7 || jobs.job.IdempotencyKey != command.IdempotencyKey || jobs.job.Kind != kind || jobs.job.TraceID != command.TraceID || events.items[0].Type != syncAcceptedEvent {
				t.Fatalf("job/event = %#v/%#v", jobs.job, events.items[0])
			}
		})
	}
}

func TestSyncServiceReplayDoesNotQueueAgain(t *testing.T) {
	uow := &catalogUOW{}
	command := port.SyncCommand{Actor: 7, IdempotencyKey: "sync-request-001", Kind: port.SyncManual}
	store := &syncStore{uow: uow, receipt: port.SyncReceipt{ID: 41, Command: command, State: port.SyncAccepted, EventID: 42, Effect: port.SyncEffectReceipt{QueueJobID: 43, EffectID: 91, EffectRef: "eer_91", EffectState: "queued", AcceptReceiptID: "eerop_91", QueueReceiptID: "eerj_43"}}}
	events := &catalogEvents{uow: uow}
	jobs := &syncJobs{uow: uow, id: 99}
	got, err := NewSyncService(uow, store, events, jobs).Request(context.Background(), command)
	if err != nil || got.State != port.SyncQueued || got.EventID != 42 || got.QueueJobID != 43 {
		t.Fatalf("replay Request() = %#v, %v", got, err)
	}
	if store.accept != 0 || jobs.calls != 0 || len(events.items) != 0 {
		t.Fatalf("replay calls = accept:%d jobs:%d events:%d", store.accept, jobs.calls, len(events.items))
	}
}

func TestSyncServiceCommitHookFailureBubblesAndStaysInsideUOW(t *testing.T) {
	uow := &catalogUOW{}
	command := port.SyncCommand{Actor: 7, IdempotencyKey: "sync-request-001", Kind: port.SyncManual}
	store := &syncStore{uow: uow, accepted: port.SyncReceipt{ID: 41, Command: command, State: port.SyncAccepted, EventID: 1, Effect: port.SyncEffectReceipt{QueueJobID: 43, EffectID: 91, EffectRef: "eer_91", EffectState: "queued", AcceptReceiptID: "eerop_91", QueueReceiptID: "eerj_43"}}}
	hookErr := errors.New("outbound acceptance failed")
	_, err := NewSyncService(uow, store, &catalogEvents{uow: uow}, &syncJobs{uow: uow, id: 43}).RequestWithCommitHook(context.Background(), command, func(_ context.Context, acceptance port.SyncAcceptance, replay bool) error {
		if !uow.in || replay || acceptance.ReceiptID != 41 {
			t.Fatalf("hook facts = %#v in:%v replay:%v", acceptance, uow.in, replay)
		}
		return hookErr
	})
	if !errors.Is(err, hookErr) {
		t.Fatalf("RequestWithCommitHook() error = %v", err)
	}
}

func TestSyncServiceRejectsInvalidCommandsAndUnknownRetry(t *testing.T) {
	uow := &catalogUOW{}
	command := port.SyncCommand{Actor: 7, IdempotencyKey: "sync-request-001", Kind: port.SyncManual}
	store := &syncStore{uow: uow, receipt: port.SyncReceipt{ID: 41, Command: command, State: port.SyncAccepted, EventID: 42, Effect: port.SyncEffectReceipt{QueueJobID: 43, EffectID: 91, EffectRef: "eer_91", EffectState: "queued", AcceptReceiptID: "eerop_91", QueueReceiptID: "eerj_43"}}}
	service := NewSyncService(uow, store, &catalogEvents{uow: uow}, &syncJobs{uow: uow, id: 43})
	for _, invalid := range []port.SyncCommand{{}, {Actor: 7, IdempotencyKey: " bad", Kind: port.SyncManual}, {Actor: 7, IdempotencyKey: "sync-request-001", Kind: "unknown"}} {
		if _, err := service.Request(context.Background(), invalid); !errors.Is(err, ErrInvalidSync) {
			t.Fatalf("invalid command %#v error = %v", invalid, err)
		}
	}
	for _, state := range []port.SyncState{port.SyncAttempted, port.SyncExecuted, port.SyncOutcomeUnknown, port.SyncReconciled} {
		if SyncCanAutoRetry(state) {
			t.Fatalf("state %q must require reconciliation", state)
		}
	}
	if !SyncCanAutoRetry(port.SyncQueued) {
		t.Fatal("queued state must remain eligible for first delivery")
	}
}
