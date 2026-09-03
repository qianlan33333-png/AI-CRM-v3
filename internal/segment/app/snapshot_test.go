package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type directUOW struct{}

func (directUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type enqueueStub struct{ calls int }

func (e *enqueueStub) EnqueueRefreshWithin(context.Context, int64) (int64, error) {
	e.calls++
	return 88, nil
}

type refreshStoreStub struct {
	RefreshStore
	run       segmentdomain.RefreshRun
	config    segmentdomain.ConfigurationVersion
	staged    int
	batches   int
	published int64
	facts     int
}

func (s *refreshStoreStub) GetPackage(context.Context, int64) (segmentdomain.Package, error) {
	return segmentdomain.Package{ID: 1, Lifecycle: segmentdomain.Paused}, nil
}
func (s *refreshStoreStub) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.config, nil
}
func (s *refreshStoreStub) Configuration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.config, nil
}
func (s *refreshStoreStub) ReserveRefresh(_ context.Context, run segmentdomain.RefreshRun) (segmentdomain.RefreshRun, bool, error) {
	run.ID = 7
	s.run = run
	return run, true, nil
}
func (s *refreshStoreStub) AttachRefreshJob(_ context.Context, runID, jobID int64, _ time.Time) (segmentdomain.RefreshRun, error) {
	s.run.ID = runID
	s.run.State = segmentdomain.RefreshQueued
	s.run.RiverJobID = &jobID
	return s.run, nil
}
func (s *refreshStoreStub) Refresh(context.Context, int64) (segmentdomain.RefreshRun, error) {
	return s.run, nil
}
func (s *refreshStoreStub) BeginRefresh(context.Context, int64, time.Time) (segmentdomain.RefreshRun, segmentdomain.Snapshot, error) {
	s.run.State = segmentdomain.RefreshEvaluating
	return s.run, segmentdomain.Snapshot{ID: 9}, nil
}
func (s *refreshStoreStub) StageRefreshBatch(_ context.Context, _ int64, _ int, ids []customerdomain.CustomerID, _ [32]byte, _ time.Time) error {
	s.staged += len(ids)
	s.batches++
	return nil
}
func (s *refreshStoreStub) PublishRefresh(_ context.Context, _ int64, count int64, _ [32]byte, _ [32]byte, _ int64, _ time.Time) (segmentdomain.Snapshot, error) {
	s.published = count
	s.run.State = segmentdomain.RefreshPublished
	return segmentdomain.Snapshot{ID: 9, MemberCount: count}, nil
}
func (s *refreshStoreStub) AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error) {
	s.facts++
	return 1, nil
}

type passthroughCanonical struct{}

func (passthroughCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

func TestRefreshAcceptanceQueuesInTheSameUnitOfWork(t *testing.T) {
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	store := &refreshStoreStub{config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, CreatedBy: 4}}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{}, passthroughCanonical{})
	enqueue := &enqueueStub{}
	service, _ := NewSnapshotService(directUOW{}, store, evaluator, enqueue)
	service.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	run, err := service.AcceptRefresh(context.Background(), RefreshCommand{PackageID: 1, Actor: 4, IdempotencyKey: "refresh-command-0001", ReferenceTime: time.Unix(900, 0).UTC()})
	if err != nil || run.State != segmentdomain.RefreshQueued || enqueue.calls != 1 || store.facts != 1 || run.RiverJobID == nil || *run.RiverJobID != 88 {
		t.Fatalf("run=%+v enqueue=%d facts=%d err=%v", run, enqueue.calls, store.facts, err)
	}
}

func TestRefreshStagesHundredThousandMembersAndPublishesOnce(t *testing.T) {
	ids := make([]customerdomain.CustomerID, 100000)
	for i := range ids {
		ids[i] = customerdomain.CustomerID(i + 1)
	}
	definition := json.RawMessage(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`)
	store := &refreshStoreStub{run: segmentdomain.RefreshRun{ID: 7, PackageID: 1, ConfigurationVersionID: 3, State: segmentdomain.RefreshQueued, ReferenceTime: time.Unix(900, 0).UTC()}, config: segmentdomain.ConfigurationVersion{ID: 3, PackageID: 1, Definition: definition, CreatedBy: 4}}
	evaluator, _ := NewEvaluator(segmentcompiler.Compiler{}, sourceStub{ids: ids}, passthroughCanonical{})
	service, _ := NewSnapshotService(directUOW{}, store, evaluator, &enqueueStub{})
	if err := service.ProcessRefresh(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if store.staged != 100000 || store.batches != 100 || store.published != 100000 {
		t.Fatalf("staged=%d batches=%d published=%d", store.staged, store.batches, store.published)
	}
	if err := service.ProcessRefresh(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if store.batches != 100 {
		t.Fatalf("published replay staged more batches: %d", store.batches)
	}
}
