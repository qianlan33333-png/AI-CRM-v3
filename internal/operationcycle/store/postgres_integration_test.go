package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// The cycle domain is local-only: this Journey verifies report persistence,
// receipt replay/drift, runner lease and terminal evidence on a real PG16
// schema. No identity, recipient or Provider table is used by the commands.
func TestPostgreSQLOperationCycleReportToTerminalActionJourney(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	service := operationapp.NewService(uow, NewRepository(), NewEventJournal(), NewEventJournal())
	snapshot := map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "weekly.review", "run_key": "weekly.review.001",
		"revision": 1, "strategy_version": 1, "status": "active", "title": "每周复盘",
		"name": "每周复盘", "cron": "每周一 09:00", "dot": "#2EA121", "action": "开始复盘",
		"steps": []any{map[string]any{"label": "复盘", "color": "#2EA121", "dim": false}},
	}
	report := operationapp.ReportCommand{Snapshot: snapshot, IdempotencyKey: "operation-cycle-report-key-0001", ReporterID: "cycle-runner", ClientID: "cycle-runner-v3"}
	if _, err = service.Report(ctx, report); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err = service.Report(ctx, report); err != nil {
		t.Fatalf("report replay: %v", err)
	}
	drift := operationapp.ReportCommand{Snapshot: map[string]any{"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "weekly.review", "run_key": "weekly.review.002"}, IdempotencyKey: report.IdempotencyKey, ReporterID: report.ReporterID, ClientID: report.ClientID}
	if _, err = service.Report(ctx, drift); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("report payload drift=%v, want conflict", err)
	}

	strategies, err := service.ListStrategies(ctx, 10, 0)
	if err != nil || len(strategies["items"].([]map[string]any)) != 1 {
		t.Fatalf("persisted strategies=%#v err=%v", strategies, err)
	}
	if _, err = service.Heartbeat(ctx, operationapp.RunnerHeartbeatCommand{RunnerID: "cycle-runner", PrincipalID: "operation-cycle-service", ConnectorVersion: "v1", CodexVersion: "v1", AppServerProtocol: "v1", CompatibilityStatus: "ready", BindingKeys: []string{"weekly.review"}}); err != nil {
		t.Fatalf("runner heartbeat: %v", err)
	}
	start := operationapp.StartCommand{StrategyKey: "weekly.review", RunKey: "weekly.review.001", ActionKey: "review", IdempotencyKey: "operation-cycle-start-key-0001", ActorID: "7"}
	queued, err := service.Start(ctx, start)
	if err != nil || queued["status"] != "queued" {
		t.Fatalf("queue action=%#v err=%v", queued, err)
	}
	if replayed, replayErr := service.Start(ctx, start); replayErr != nil || replayed["request_id"] != queued["request_id"] {
		t.Fatalf("action replay=%#v err=%v", replayed, replayErr)
	}
	start.RunKey = "weekly.review.002"
	if _, err = service.Start(ctx, start); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("action payload drift=%v, want conflict", err)
	}
	claimed, err := service.Claim(ctx, "cycle-runner", "operation-cycle-service")
	if err != nil || claimed["claimed"] != true {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	requestID, lease := claimed["request_id"].(string), claimed["lease_token"].(string)
	for _, event := range []operationapp.ActionEventCommand{
		{RequestID: requestID, EventID: "cycle-thread-bound-0001", EventType: "thread_bound", LeaseToken: lease, ThreadID: "thread-1"},
		{RequestID: requestID, EventID: "cycle-turn-started-0001", EventType: "turn_started", LeaseToken: lease, ThreadID: "thread-1", TurnID: "turn-1"},
		{RequestID: requestID, EventID: "cycle-completed-0001", EventType: "completed", LeaseToken: lease, Result: map[string]any{"outcome": "outcome_unknown"}},
	} {
		if _, err = service.RecordActionEvent(ctx, event); err != nil {
			t.Fatalf("record %s: %v", event.EventType, err)
		}
	}
	result, err := service.GetActionResult(ctx, requestID)
	if err != nil || result["status"] != "completed" {
		t.Fatalf("terminal action=%#v err=%v", result, err)
	}
	var reports, actions, events, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM operation_cycle_report_receipts),
		(SELECT count(*) FROM operation_cycle_action_requests),
		(SELECT count(*) FROM operation_cycle_action_request_events),
		(SELECT count(*) FROM audit_events WHERE resource_type='operation_cycle'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_type='operation_cycle')`).Scan(&reports, &actions, &events, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if reports != 1 || actions != 1 || events != 3 || audits != 7 || outbox != 7 {
		t.Fatalf("local facts reports/actions/events/audits/outbox=%d/%d/%d/%d/%d", reports, actions, events, audits, outbox)
	}
}

// The admin Journey is entirely local: typed strategy definition changes,
// immutable versions, receipts, audit and outbox commit in one PostgreSQL UoW.
// A newly constructed service proves that a browser refresh reads persisted
// state rather than an in-process projection.
func TestPostgreSQLOperationCycleAdminJourneyPersistsImmutableHistory(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	newService := func() *operationapp.Service {
		return operationapp.NewService(uow, NewRepository(), NewEventJournal(), NewEventJournal())
	}
	service := newService()
	definition := operationapp.StrategyDefinition{
		Schedule: "每周一 09:00", IndicatorColor: "#2EA121", PrimaryAction: "start_review",
		Stages: []operationapp.StrategyStage{{Key: "prepare", Label: "准备", Color: "#3370FF", State: "completed"}, {Key: "retro", Label: "复盘", Color: "#2EA121", State: "current"}},
	}
	create := operationapp.CreateStrategyCommand{StrategyKey: "admin.weekly.review", Title: "每周复盘", Definition: definition, IdempotencyKey: "admin-create-weekly-review", ActorID: "7"}
	created, err := service.CreateStrategy(ctx, create)
	if err != nil || created["status"] != "draft" || created["version"] != int32(1) {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	replayed, err := service.CreateStrategy(ctx, create)
	if err != nil || replayed["strategy_key"] != create.StrategyKey {
		t.Fatalf("create replay=%#v err=%v", replayed, err)
	}
	drift := create
	drift.Title = "漂移标题"
	if _, err = service.CreateStrategy(ctx, drift); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("create payload drift=%v, want conflict", err)
	}
	definition.Schedule = "每周二 10:00"
	updated, err := service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 1, Title: "每周复盘 v2", Definition: definition, IdempotencyKey: "admin-update-weekly-review", ActorID: "7"})
	if err != nil || updated["version"] != int32(2) {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	if _, err = service.UpdateStrategy(ctx, operationapp.UpdateStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 1, Title: "陈旧更新", Definition: definition, IdempotencyKey: "admin-stale-update", ActorID: "7"}); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("stale CAS=%v, want conflict", err)
	}
	active, err := service.TransitionStrategy(ctx, operationapp.TransitionStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 2, Status: "active", IdempotencyKey: "admin-activate-weekly-review", ActorID: "7"})
	if err != nil || active["version"] != int32(3) {
		t.Fatalf("activate=%#v err=%v", active, err)
	}
	paused, err := service.TransitionStrategy(ctx, operationapp.TransitionStrategyCommand{StrategyKey: create.StrategyKey, ExpectedVersion: 3, Status: "paused", IdempotencyKey: "admin-pause-weekly-review", ActorID: "7"})
	if err != nil || paused["version"] != int32(4) {
		t.Fatalf("pause=%#v err=%v", paused, err)
	}
	concurrent := []operationapp.UpdateStrategyCommand{
		{StrategyKey: create.StrategyKey, ExpectedVersion: 4, Title: "并发更新 A", Definition: definition, IdempotencyKey: "admin-concurrent-a", ActorID: "7"},
		{StrategyKey: create.StrategyKey, ExpectedVersion: 4, Title: "并发更新 B", Definition: definition, IdempotencyKey: "admin-concurrent-b", ActorID: "7"},
	}
	results := make(chan error, len(concurrent))
	for _, command := range concurrent {
		command := command
		go func() {
			_, updateErr := service.UpdateStrategy(ctx, command)
			results <- updateErr
		}()
	}
	successes, conflicts := 0, 0
	for range concurrent {
		switch updateErr := <-results; {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, operationapp.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent CAS error=%v", updateErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS successes/conflicts=%d/%d, want 1/1", successes, conflicts)
	}

	refreshed := newService()
	persisted, err := refreshed.GetStrategy(ctx, create.StrategyKey)
	if err != nil || persisted["status"] != "paused" || persisted["version"] != int32(5) {
		t.Fatalf("refreshed strategy=%#v err=%v", persisted, err)
	}
	history, err := refreshed.ListStrategyVersions(ctx, create.StrategyKey, 10, 0)
	if err != nil {
		t.Fatalf("list immutable strategy history: %v", err)
	}
	versions, ok := history["items"].([]map[string]any)
	if !ok || len(versions) != 5 || versions[0]["version"] != int32(5) || versions[4]["version"] != int32(1) {
		t.Fatalf("immutable strategy history=%#v err=%v", history, err)
	}
	var receipts, strategyVersions, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM operation_cycle_admin_receipts),
		(SELECT count(*) FROM operation_cycle_strategy_versions WHERE strategy_key=$1),
		(SELECT count(*) FROM audit_events WHERE resource_type='operation_cycle'),
		(SELECT count(*) FROM outbox_events WHERE aggregate_type='operation_cycle')`, create.StrategyKey).Scan(&receipts, &strategyVersions, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if receipts != 5 || strategyVersions != 5 || audits != 5 || outbox != 5 {
		t.Fatalf("receipts/versions/audits/outbox=%d/%d/%d/%d, want 5/5/5/5", receipts, strategyVersions, audits, outbox)
	}
	if _, err = native.Exec(ctx, `UPDATE operation_cycle_strategy_versions SET title='mutated' WHERE strategy_key=$1 AND version=1`, create.StrategyKey); err == nil {
		t.Fatal("immutable strategy version accepted direct mutation")
	}
}

func TestPostgreSQLOperationCycleRunHistoryRejectsDestructiveOverwrite(t *testing.T) {
	native, cleanup := operationCycleIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	service := operationapp.NewService(uow, NewRepository(), NewEventJournal(), NewEventJournal())
	base := map[string]any{
		"schema_version": "operation_cycle_snapshot.v1", "strategy_key": "runner.weekly.review", "run_key": "runner.weekly.review.001",
		"revision": 1, "strategy_version": 1, "status": "active", "title": "运行周期", "name": "运行周期", "cron": "每周一", "dot": "#2EA121", "action": "查看进度",
		"steps": []any{map[string]any{"label": "复盘", "color": "#2EA121", "dim": false}},
	}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: base, IdempotencyKey: "history-report-1", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatal(err)
	}
	overwrite := make(map[string]any, len(base))
	for key, value := range base {
		overwrite[key] = value
	}
	overwrite["title"] = "相同版本被覆盖"
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: overwrite, IdempotencyKey: "history-report-overwrite", ReporterID: "cycle-runner", ClientID: "v3-runner"}); !errors.Is(err, operationapp.ErrConflict) {
		t.Fatalf("destructive overwrite=%v, want conflict", err)
	}
	second := make(map[string]any, len(base))
	for key, value := range base {
		second[key] = value
	}
	second["revision"] = 2
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: second, IdempotencyKey: "history-report-2", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatalf("revision 2: %v", err)
	}
	if _, err = service.Report(ctx, operationapp.ReportCommand{Snapshot: base, IdempotencyKey: "history-report-old-replay", ReporterID: "cycle-runner", ClientID: "v3-runner"}); err != nil {
		t.Fatalf("older immutable report replay: %v", err)
	}
	history, err := service.ListRunVersions(ctx, "runner.weekly.review.001", 10, 0)
	if err != nil {
		t.Fatalf("list immutable run history: %v", err)
	}
	versions, ok := history["items"].([]map[string]any)
	if !ok || len(versions) != 2 || versions[0]["snapshot_revision"] != int32(2) || versions[1]["snapshot_revision"] != int32(1) {
		t.Fatalf("immutable run history=%#v err=%v", history, err)
	}
	persisted, err := service.GetRun(ctx, "runner.weekly.review.001")
	if err != nil || persisted["snapshot_revision"] != int32(2) {
		t.Fatalf("latest run projection=%#v err=%v", persisted, err)
	}
	strategy, err := service.GetStrategy(ctx, "runner.weekly.review")
	strategySnapshot, _ := strategy["snapshot"].(map[string]any)
	if err != nil || strategySnapshot["revision"] != float64(2) {
		t.Fatalf("latest strategy projection regressed=%#v err=%v", strategy, err)
	}
}

func operationCycleIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping operation-cycle PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_operation_cycle_test_" + hex.EncodeToString(raw)
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate operation-cycle integration test")
	}
	migrations := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	entries, err := os.ReadDir(migrations)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() > "0023_operation_cycle_admin_history.sql" {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		sql, readErr := os.ReadFile(filepath.Join(migrations, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}
