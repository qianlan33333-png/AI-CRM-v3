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
