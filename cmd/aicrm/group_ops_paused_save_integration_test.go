package main

import (
	"context"
	"errors"
	"testing"
	"time"

	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestGroupOpsPostgreSQLPausedBasicSave(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var actor, inactive int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active) VALUES('paused-save','$argon2id$test','Operator',true) RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active) VALUES('paused-inactive','$argon2id$test','Inactive',false) RETURNING id`).Scan(&inactive); err != nil {
		t.Fatal(err)
	}
	store, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := groupopsapp.NewService(uow, store, groupOpsStaffAdapter{access: accessstore.NewPostgreSQL(), owners: store}, store)
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Imported disabled plan", Actor: actor, IdempotencyKey: "paused-save-create"})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy imports are paused with no local owner. Reproduce that owner-owned
	// definition without enabling an incomplete plan just to make it editable.
	if err = uow.Within(ctx, func(tx context.Context) error {
		detail.Plan.Status = groupopsport.PlanPaused
		return store.Save(tx, detail)
	}); err != nil {
		t.Fatal(err)
	}
	command := groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: "Configured disabled plan", OwnerStaffID: actor, OwnerStaffIDSet: true, Actor: actor, IdempotencyKey: "paused-save-owner"}
	saved, err := service.Update(ctx, command)
	if err != nil || saved.Plan.Status != groupopsport.PlanPaused || saved.Plan.Revision != detail.Plan.Revision+1 || len(saved.Members) != 1 || saved.Members[0].StaffID != actor {
		t.Fatalf("save=%+v err=%v", saved, err)
	}
	replay, err := service.Update(ctx, command)
	if err != nil || replay.Plan.Revision != saved.Plan.Revision {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	command.IdempotencyKey = "paused-save-stale"
	if _, err = service.Update(ctx, command); !errors.Is(err, groupopsapp.ErrConflict) {
		t.Fatalf("stale=%v", err)
	}
	command.ExpectedRevision = saved.Plan.Revision
	command.IdempotencyKey = "paused-save-inactive"
	command.OwnerStaffID = inactive
	if _, err = service.Update(ctx, command); !errors.Is(err, groupopsapp.ErrInvalid) {
		t.Fatalf("inactive=%v", err)
	}
	got, err := service.Detail(ctx, saved.Plan.ID)
	if err != nil || got.Plan.Revision != saved.Plan.Revision || got.Plan.Name != saved.Plan.Name || got.Plan.Status != groupopsport.PlanPaused || len(got.Members) != 1 || got.Members[0].StaffID != actor {
		t.Fatalf("failed write changed owner=%+v err=%v", got, err)
	}
	// Incomplete content still cannot activate; saving basic fields never queues.
	if _, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: got.Plan.ID, ExpectedRevision: got.Plan.Revision, Actor: actor, IdempotencyKey: "paused-save-incomplete-enable"}); !errors.Is(err, groupopsapp.ErrStateConflict) {
		t.Fatalf("incomplete activation=%v", err)
	}
	for table, want := range map[string]int{"group_ops_operation_receipts": 2, "group_ops_audit_events": 2, "external_effects": 0, "river_job": 0} {
		var count int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
}
