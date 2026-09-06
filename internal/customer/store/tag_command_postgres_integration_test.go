package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTagCommandPostgreSQLCompletionFenceAndReplay(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'); INSERT INTO tag_groups(group_name,sort_order) VALUES('g',1); INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES(1,'t',1)`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := TagCommandPostgreSQL{}
	var commandID int64
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		commandID, e = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "tag-command-replay", IdempotencyKey: "tag-command-replay", OccurredAt: time.Now()}, [32]byte{1})
		if e != nil {
			return e
		}
		_, e = store.CreateTagCommandLine(tx, commandID, customerport.FrozenTagCommandTarget{TagCommandTarget: customerport.TagCommandTarget{CustomerID: customerdomain.CustomerID(1), StaffID: 1, AddTagIDs: []int64{1}}, BindingDigest: string(effectport.Hash("binding")), TargetDigest: string(effectport.Hash("target"))}, string(effectport.Hash("source")), effectport.Projection{ID: "eer_1", State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_1", QueueReceiptID: "eerop_2"})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := customerport.TagCommandCompletion{EffectRef: "eer_1", State: "executed", ResultDigest: string(effectport.Hash("done")), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, complete) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, complete) }); err != nil {
		t.Fatalf("same completion replay: %v", err)
	}
	stale := complete
	stale.Fence = 0
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, stale) }); err == nil {
		t.Fatal("stale completion must reject")
	}
	var state string
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, commandID).Scan(&state); err != nil || state != "executed" {
		t.Fatalf("state=%q err=%v", state, err)
	}
}
func tagCommandPGPool(t *testing.T, ctx context.Context, url string) (*platformpostgres.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_tag_command_" + hex.EncodeToString(raw)
	id := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+id); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0093_customer_tag_commands.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(raw)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+id+" CASCADE")
		admin.Close()
	}
}

func TestTagCommandPostgreSQLCompletionProjectsPartialAndRejected(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-2','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active'); INSERT INTO tag_groups(group_name,sort_order) VALUES('g',1); INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES(1,'t',1)`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store := TagCommandPostgreSQL{}
	var commandID int64
	err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		commandID, e = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "partial-command", IdempotencyKey: "partial-command", OccurredAt: time.Now()}, [32]byte{2})
		if e != nil {
			return e
		}
		_, e = store.CreateTagCommandLine(tx, commandID, customerport.FrozenTagCommandTarget{TagCommandTarget: customerport.TagCommandTarget{CustomerID: 1, StaffID: 1, AddTagIDs: []int64{1}}, BindingDigest: string(effectport.Hash("binding-2")), TargetDigest: string(effectport.Hash("target-2"))}, string(effectport.Hash("source-2")), effectport.Projection{ID: "eer_2", State: effectport.StateQueued}, effectport.Receipt{ID: "eerop_2", QueueReceiptID: "eerop_3"})
		if e != nil {
			return e
		}
		_, e = store.CreateRejectedTagCommandLine(tx, commandID, customerport.TagCommandTarget{CustomerID: 2, StaffID: 1, AddTagIDs: []int64{1}}, "target_unavailable")
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := customerport.TagCommandCompletion{EffectRef: "eer_2", State: "final_failed", ResultDigest: string(effectport.Hash("done-2")), ResultReason: "provider_rejected", Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.CompleteTagCommand(tx, completion) }); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, commandID).Scan(&state); err != nil || state != "partial" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	var resultReason string
	if err = native.QueryRow(ctx, `SELECT COALESCE(result_reason,'') FROM customer_tag_command_lines WHERE effect_ref='eer_2'`).Scan(&resultReason); err != nil || resultReason != "provider_rejected" {
		t.Fatalf("safe result reason=%q err=%v", resultReason, err)
	}
	var history []customerport.TagCommandResult
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		history, readErr = store.ListTagCommands(tx, 1, 10)
		return readErr
	}); err != nil || len(history) != 1 || history[0].Lines[0].ResultReason != "provider_rejected" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	// Rejected-only commands never masquerade as executed.
	var rejectedID int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		var e error
		rejectedID, e = store.CreateTagCommand(tx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "rejected-command", IdempotencyKey: "rejected-command", OccurredAt: time.Now()}, [32]byte{3})
		if e != nil {
			return e
		}
		if _, e = store.CreateRejectedTagCommandLine(tx, rejectedID, customerport.TagCommandTarget{CustomerID: 2, StaffID: 1, AddTagIDs: []int64{1}}, "target_unavailable"); e != nil {
			return e
		}
		return store.SetTagCommandState(tx, rejectedID, "rejected")
	}); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT state FROM customer_tag_commands WHERE id=$1`, rejectedID).Scan(&state); err != nil || state != "rejected" {
		t.Fatalf("rejected state=%q err=%v", state, err)
	}
}

type tagCommandIntegrationGate struct{}

func (tagCommandIntegrationGate) FreezeTagCommandTarget(_ context.Context, target customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	target.StaffID = 1
	return customerport.FrozenTagCommandTarget{TagCommandTarget: target, BindingDigest: string(effectport.Hash("tag-command-integration-binding")), TargetDigest: string(effectport.Hash("tag-command-integration-target"))}, nil
}

type tagCommandFailingOutbox struct{}

func (tagCommandFailingOutbox) Append(context.Context, platformoutbox.Event) (platformoutbox.Event, error) {
	return platformoutbox.Event{}, errors.New("outbox rejected")
}

func TestTagCommandPostgreSQLAcceptanceAtomicConcurrentReplay(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-atomic','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active')`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewTagCommandService(uow, TagCommandPostgreSQL{}, effects, tagCommandIntegrationGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	command := customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "atomic-replay-key", IdempotencyKey: "atomic-replay-key", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: []int64{1}}}}
	results := make(chan customerport.TagCommandResult, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			result, submitErr := service.SubmitTagCommand(ctx, command)
			if submitErr != nil {
				failures <- submitErr
				return
			}
			results <- result
		}()
	}
	var first customerport.TagCommandResult
	for range 2 {
		select {
		case submitErr := <-failures:
			t.Fatalf("concurrent command=%v", submitErr)
		case result := <-results:
			if first.ID == 0 {
				first = result
			} else if result.ID != first.ID || len(result.Lines) != 1 {
				t.Fatalf("replay result=%+v first=%+v", result, first)
			}
		}
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 1, "external_effects": 1, "external_effect_operation_receipts": 2, "river_job": 1, "audit_events": 1, "outbox_events": 1} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	// A post-acceptance append failure rolls every business and EER fact back;
	// no compensating provider path is used.
	failing, err := customerapp.NewTagCommandService(uow, TagCommandPostgreSQL{}, effects, tagCommandIntegrationGate{}, platformaudit.NewPostgreSQLStore(), tagCommandFailingOutbox{})
	if err != nil {
		t.Fatal(err)
	}
	failed := command
	failed.SourceRef, failed.IdempotencyKey = "atomic-rollback-key", "atomic-rollback-key"
	if _, err = failing.SubmitTagCommand(ctx, failed); err == nil {
		t.Fatal("outbox failure must abort acceptance")
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 1, "external_effects": 1, "river_job": 1} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("rollback %s=%d want=%d err=%v", table, got, want, err)
		}
	}
}

func TestTagCommandPostgreSQLBatchOver100QueuesIndependentRiverEffects(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := tagCommandPGPool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-batch','$argon2id$test','Tag batch admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE SELECT value,'active' FROM generate_series(1,101) value`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewTagCommandService(uow, TagCommandPostgreSQL{}, effects, tagCommandIntegrationGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]customerport.TagCommandTarget, 0, 101)
	for customerID := 1; customerID <= 101; customerID++ {
		targets = append(targets, customerport.TagCommandTarget{CustomerID: customerdomain.CustomerID(customerID), AddTagIDs: []int64{1}})
	}
	result, err := service.SubmitTagCommand(ctx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "batch-over-100", IdempotencyKey: "batch-over-100", OccurredAt: time.Now(), Targets: targets})
	if err != nil || result.ID < 1 || len(result.Lines) != 101 || result.State != "queued" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 101, "external_effects": 101, "river_job": 101} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	// River holds one stable customer effect per job. Restarting between jobs can
	// never merge two customer mutations or cause a batch-wide resend.
	var duplicateArgs int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM (SELECT args->>'effect_id' effect_id,count(*) FROM river_job GROUP BY args->>'effect_id' HAVING count(*) > 1) duplicates`).Scan(&duplicateArgs); err != nil || duplicateArgs != 0 {
		t.Fatalf("duplicate river effects=%d err=%v", duplicateArgs, err)
	}
}
