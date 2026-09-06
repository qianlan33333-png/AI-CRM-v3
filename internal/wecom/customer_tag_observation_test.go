package wecom

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type externalContactReaderStub struct {
	contact wecomport.ExternalContact
	err     error
	calls   int
}

func (stub *externalContactReaderStub) ReadExternalContact(_ context.Context, externalUserID string) (wecomport.ExternalContact, error) {
	stub.calls++
	if stub.err != nil {
		return wecomport.ExternalContact{}, stub.err
	}
	return stub.contact, nil
}

func TestCustomerTagObservationRefreshPostgreSQLPersistsOnlyProviderReadback(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rawCustomerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rawCustomerID); err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(rawCustomerID)
	now := time.Date(2026, 9, 6, 9, 30, 0, 0, time.UTC)
	reader := &externalContactReaderStub{contact: wecomport.ExternalContact{
		ExternalUserID: "external-1",
		FollowInfo:     []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "provider-tag-1", Name: "Observed tag", Type: 1}}}},
	}}
	service := CustomerTagObservationService{Enabled: true, CorpID: "corp-1", Provider: reader, Store: PostgreSQLCustomerSyncStore{}, UOW: unit, Now: func() time.Time { return now }}
	if err = service.RefreshCustomerTagObservation(ctx, "eer_1", customerID, "staff-1", "external-1"); err != nil {
		t.Fatal(err)
	}
	var trigger, status, observedID, observedName string
	var runCount, effectCount int
	if err = pool.Native().QueryRow(ctx, `SELECT trigger_type,status FROM wecom_customer_sync_runs`).Scan(&trigger, &status); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		observations, readErr := (PostgreSQLCustomerSyncStore{}).CustomerTagObservations(tx, customerID)
		if readErr != nil {
			return readErr
		}
		if len(observations) != 1 {
			return errors.New("expected one observation")
		}
		observedID, observedName = observations[0].ProviderTagID, observations[0].ObservedName
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_sync_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectCount); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 || trigger != "tag_refresh" || status != "succeeded" || observedID != "provider-tag-1" || observedName != "Observed tag" || runCount != 1 || effectCount != 0 {
		t.Fatalf("calls=%d trigger=%q status=%q observation=%q/%q runs=%d effects=%d", reader.calls, trigger, status, observedID, observedName, runCount, effectCount)
	}

	reader.err = errors.New("directory read unavailable")
	if err = service.RefreshCustomerTagObservation(ctx, "eer_1", customerID, "staff-1", "external-1"); !errors.Is(err, reader.err) {
		t.Fatalf("read error=%v", err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_sync_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("failed read must not persist a refresh run, runs=%d", runCount)
	}
}

func TestCustomerTagRefreshWinsOverInProgressFullSyncAndIsHiddenFromSyncListPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := PostgreSQLCustomerSyncStore{}
	var rawCustomerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rawCustomerID); err != nil {
		t.Fatal(err)
	}
	customerID := customerdomain.CustomerID(rawCustomerID)
	var full CustomerSyncRun
	if err = unit.Within(ctx, func(tx context.Context) error {
		var createErr error
		full, _, createErr = store.Create(tx, CreateCustomerSyncRun{RunKey: "full-sync-before-tag-refresh", Trigger: "manual", CorpScope: "wecom-corp:corp-1"})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.Transition(tx, full.ID, full.Version, SyncQueued, SyncListingStaff)
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(tx context.Context) error {
		var getErr error
		full, getErr = store.Get(tx, full.ID)
		return getErr
	}); err != nil || full.StartedAt == nil {
		t.Fatalf("full run=%+v err=%v", full, err)
	}
	oldPage := []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "old-tag", Name: "Old", Type: 1}}}}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.UpsertProfileObservations(tx, full.ID, "wecom-corp:corp-1", customerID, oldPage, full.StartedAt.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	refreshAt := full.StartedAt.Add(time.Minute)
	reader := &externalContactReaderStub{contact: wecomport.ExternalContact{ExternalUserID: "external-1", FollowInfo: []wecomport.ExternalContactFollowInfo{{EmployeeID: "staff-1", Tags: []wecomport.ExternalContactTag{{ProviderTagID: "fresh-tag", Name: "Fresh", Type: 1}}}}}}
	service := CustomerTagObservationService{Enabled: true, CorpID: "corp-1", Provider: reader, Store: store, UOW: unit, Now: func() time.Time { return refreshAt }}
	if err = service.RefreshCustomerTagObservation(ctx, "eer_2", customerID, "staff-1", "external-1"); err != nil {
		t.Fatal(err)
	}
	// This page belongs to the full run but may have been read before the later
	// single-contact refresh. It must not resurrect old-tag.
	if err = unit.Within(ctx, func(tx context.Context) error {
		if applyErr := store.UpsertProfileObservations(tx, full.ID, "wecom-corp:corp-1", customerID, oldPage, refreshAt.Add(time.Minute)); applyErr != nil {
			return applyErr
		}
		return store.ReconcileProfileObservations(tx, full.ID, refreshAt.Add(2*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	var activeID string
	var staleCount int
	if err = pool.Native().QueryRow(ctx, `SELECT provider_tag_id FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='stale'`, customerID).Scan(&staleCount); err != nil {
		t.Fatal(err)
	}
	var listed []CustomerSyncRun
	if err = unit.Within(ctx, func(tx context.Context) error {
		var listErr error
		listed, listErr = store.List(tx, 10)
		return listErr
	}); err != nil {
		t.Fatal(err)
	}
	if activeID != "fresh-tag" || staleCount != 1 || len(listed) != 1 || listed[0].ID != full.ID {
		t.Fatalf("active=%q stale=%d listed=%+v", activeID, staleCount, listed)
	}
}

func TestCustomerTagRefreshUsesReadTimeForEmptyAndDelayedObservationsPostgreSQL(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rawCustomerID int64
	if err = pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&rawCustomerID); err != nil {
		t.Fatal(err)
	}
	store := PostgreSQLCustomerSyncStore{}
	customerID := customerdomain.CustomerID(rawCustomerID)
	earlierRead := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	laterRead := earlierRead.Add(time.Minute)
	fresh := []wecomport.ExternalContactTag{{ProviderTagID: "fresh-tag", Name: "Fresh", Type: 1}}
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", fresh, laterRead, "tag-refresh-later-read")
	}); err != nil {
		t.Fatal(err)
	}
	// This models an earlier request whose Provider response was delayed until
	// after the later read was committed. Its persisted response must not win.
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", []wecomport.ExternalContactTag{{ProviderTagID: "old-tag", Name: "Old", Type: 1}}, earlierRead, "tag-refresh-earlier-read")
	}); err != nil {
		t.Fatal(err)
	}
	var active []string
	if err = pool.Native().QueryRow(ctx, `SELECT coalesce(array_agg(provider_tag_id ORDER BY provider_tag_id),ARRAY[]::text[]) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0] != "fresh-tag" {
		t.Fatalf("delayed old response replaced fresh observation: %v", active)
	}
	// A current empty Provider response is a complete observation, so it must
	// clear the prior active set rather than leave requested tags as observed.
	if err = unit.Within(ctx, func(tx context.Context) error {
		return store.RecordCustomerTagRefresh(tx, "wecom-corp:corp-1", customerID, "staff-1", nil, laterRead.Add(time.Minute), "tag-refresh-empty-current")
	}); err != nil {
		t.Fatal(err)
	}
	if err = pool.Native().QueryRow(ctx, `SELECT coalesce(array_agg(provider_tag_id ORDER BY provider_tag_id),ARRAY[]::text[]) FROM wecom_customer_tag_observations WHERE customer_id=$1 AND observation_status='active'`, customerID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("empty complete observation left active tags: %v", active)
	}
}
