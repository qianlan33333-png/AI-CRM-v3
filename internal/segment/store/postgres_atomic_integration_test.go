package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func TestPostgreSQLAudienceConfigurationAtomicity(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	group, _ := segmentdomain.NewGroup("新客", 10, 7, now)

	rollback := errors.New("inject rollback")
	err = uow.Within(ctx, func(tx context.Context) error {
		createdGroup, e := repo.CreateGroup(tx, group)
		if e != nil {
			return e
		}
		item, _ := segmentdomain.NewPackage("rollback", "回滚测试", &createdGroup.ID, 7, now)
		created, e := repo.CreatePackage(tx, item)
		if e != nil {
			return e
		}
		receipt, _, e := repo.Reserve(tx, reservationFor("rollback-key", json.RawMessage(`{"name":"回滚"}`), now))
		if e != nil {
			return e
		}
		if _, e = repo.Complete(tx, receipt.ID, json.RawMessage(`{"id":1}`), now); e != nil {
			return e
		}
		if _, e = repo.AppendMutationFacts(tx, testFact(created.ID, "rollback-key", now)); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback injection=%v", err)
	}
	assertSegmentCounts(t, ctx, native, [6]int{})

	err = uow.Within(ctx, func(tx context.Context) error {
		createdGroup, e := repo.CreateGroup(tx, group)
		if e != nil {
			return e
		}
		item, _ := segmentdomain.NewPackage("new-customers", "新客 30 天", &createdGroup.ID, 7, now)
		created, e := repo.CreatePackage(tx, item)
		if e != nil {
			return e
		}
		definition := json.RawMessage(`{"schema_version":1,"expression":{"kind":"all"}}`)
		configuration, _ := segmentdomain.NewConfigurationVersion(created.ID, 1, definition, "", 7, now)
		configuration, e = repo.CreateConfigurationVersion(tx, configuration)
		if e != nil {
			return e
		}
		if configuration.RefreshCronUTC != "" {
			t.Fatalf("nullable refresh cron=%q", configuration.RefreshCronUTC)
		}
		created, e = repo.SetCurrentConfiguration(tx, created.ID, configuration.ID, created.Version, 7, now)
		if e != nil {
			return e
		}
		if created.Version != 2 || created.CurrentConfigurationVersionID == nil {
			t.Fatalf("package=%+v", created)
		}
		receipt, _, e := repo.Reserve(tx, reservationFor("create-key", json.RawMessage(`{"code":"new-customers"}`), now))
		if e != nil {
			return e
		}
		if _, e = repo.Complete(tx, receipt.ID, json.RawMessage(`{"package_id":1}`), now); e != nil {
			return e
		}
		_, e = repo.AppendMutationFacts(tx, testFact(created.ID, "create-key", now))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSegmentCounts(t, ctx, native, [6]int{1, 1, 1, 1, 1, 1})
}

func TestPostgreSQLAudienceImmutableFacts(t *testing.T) {
	ctx := context.Background()
	native, cleanup := segmentDatabase(t, ctx)
	defer cleanup()
	statements := []string{
		`INSERT INTO segment_audience_groups(name,created_by,updated_by,created_at,updated_at) VALUES('g',1,1,now(),now())`,
		`INSERT INTO segment_audience_packages(code,name,created_by,updated_by,created_at,updated_at) VALUES('p','p',1,1,now(),now())`,
		`INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,digest,created_by,created_at) VALUES(1,1,1,'{}',decode(repeat('00',32),'hex'),1,now())`,
		`INSERT INTO segment_audience_audit_events(resource_kind,resource_id,operation,actor_id,occurred_at,payload_digest) VALUES('package',1,'create',1,now(),decode(repeat('00',32),'hex'))`,
		`INSERT INTO segment_audience_outbox(event_type,aggregate_kind,aggregate_id,payload,idempotency_digest,occurred_at) VALUES('created','package',1,'{}',decode(repeat('00',32),'hex'),now())`,
	}
	for _, statement := range statements {
		if _, err := native.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`UPDATE segment_audience_configuration_versions SET definition='{}'`,
		`UPDATE segment_audience_audit_events SET operation='changed'`,
		`UPDATE segment_audience_outbox SET event_type='changed'`,
	} {
		if _, err := native.Exec(ctx, statement); err == nil {
			t.Fatalf("append-only fact accepted mutation: %s", statement)
		}
	}
}
