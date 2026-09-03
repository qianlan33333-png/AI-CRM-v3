package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// automationMediaReader is a test double for Media's stable port. The
// production composition injects Media's repository; Automation never imports
// its concrete store, including from tests.
type automationMediaReader struct{ enabled map[int64]bool }

func (r automationMediaReader) ImageExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}
func (r automationMediaReader) AttachmentExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}
func (r automationMediaReader) MiniProgramExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}
func (r automationMediaReader) GroupInviteExists(_ context.Context, id int64) (bool, error) {
	return r.enabled[id], nil
}

func TestPostgreSQLAgentFixedContentPublishActivatePauseJourney(t *testing.T) {
	native, cleanup := automationIntegrationPool(t)
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
	automationRepository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaReader := automationMediaReader{enabled: map[int64]bool{11: true, 12: true, 13: true, 14: true}}
	service := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepository, mediaReader, mediaReader, mediaReader, mediaReader, automationRepository)

	created, err := service.Create(ctx, automationport.CreateCommand{Actor: 7, IdempotencyKey: "automation-pg-create-0001", Agent: automationport.Agent{
		AgentName: "固定欢迎话术", AgentCode: "fixed_welcome", AutomationType: automationport.AutomationTypeFixedScript,
		Status: automationport.AgentStatusPaused, DraftRolePrompt: "保持准确", DraftTaskPrompt: "使用已发布内容",
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The page's high-entropy host suggestion is intentionally not a database
	// reservation. The create command remains the sole concurrency authority:
	// PostgreSQL rejects a duplicate code and rolls back its receipt/audit/outbox.
	if _, err = service.Create(ctx, automationport.CreateCommand{Actor: 7, IdempotencyKey: "automation-pg-create-0002", Agent: automationport.Agent{
		AgentName: "重复编码", AgentCode: created.AgentCode, AutomationType: automationport.AutomationTypeAgent,
		Status: automationport.AgentStatusPaused,
	}}); !errors.Is(err, automationapp.ErrAgentConflict) {
		t.Fatalf("duplicate agent code err=%v", err)
	}
	var conflictAgents, conflictReceipts, conflictAudits, conflictOutbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM automation_agents),(SELECT count(*) FROM automation_operation_receipts),(SELECT count(*) FROM automation_audit_events),(SELECT count(*) FROM automation_outbox)`).Scan(&conflictAgents, &conflictReceipts, &conflictAudits, &conflictOutbox); err != nil {
		t.Fatal(err)
	}
	if conflictAgents != 1 || conflictReceipts != 1 || conflictAudits != 1 || conflictOutbox != 1 {
		t.Fatalf("duplicate create was not fully rolled back agents=%d receipts=%d audits=%d outbox=%d", conflictAgents, conflictReceipts, conflictAudits, conflictOutbox)
	}
	content := automationport.FixedContentPackage{ContentText: "欢迎加入", ImageLibraryIDs: []int64{11}, AttachmentLibraryIDs: []int64{12}, MiniprogramLibraryIDs: []int64{13}, GroupInviteLibraryIDs: []int64{14}}
	saved, err := service.SaveFixedContent(ctx, automationport.FixedContentCommand{ID: created.ID, ContentPackage: content, Actor: 7, IdempotencyKey: "automation-pg-content-01"})
	if err != nil || saved.DraftVersion != 2 || saved.PublishedVersion != 1 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if _, err = service.SetStatus(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-active-01"}, automationport.AgentStatusActive); !errors.Is(err, automationapp.ErrAgentConflict) {
		t.Fatalf("activation before publish err=%v", err)
	}
	published, err := service.Publish(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-publish-01"})
	if err != nil || published.DraftVersion != published.PublishedVersion {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	active, err := service.SetStatus(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-active-02"}, automationport.AgentStatusActive)
	if err != nil || active.Status != automationport.AgentStatusActive || !active.ExecutionEnabled {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	paused, err := service.SetStatus(ctx, automationport.MutationCommand{ID: created.ID, Actor: 7, IdempotencyKey: "automation-pg-pause-0001"}, automationport.AgentStatusPaused)
	if err != nil || paused.Status != automationport.AgentStatusPaused || paused.ExecutionEnabled {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	replayed, err := service.SaveFixedContent(ctx, automationport.FixedContentCommand{ID: created.ID, ContentPackage: content, Actor: 7, IdempotencyKey: "automation-pg-content-01"})
	if err != nil || replayed.ID != saved.ID || replayed.DraftVersion != saved.DraftVersion {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if _, err = service.SaveFixedContent(ctx, automationport.FixedContentCommand{ID: created.ID, ContentPackage: automationport.FixedContentPackage{ContentText: "drift", ImageLibraryIDs: []int64{11}}, Actor: 7, IdempotencyKey: "automation-pg-content-01"}); !errors.Is(err, automationapp.ErrAgentConflict) {
		t.Fatalf("payload drift err=%v", err)
	}
	got, err := service.Get(ctx, created.ID)
	if err != nil || len(got.FixedContentPackage.ImageLibraryIDs) != 1 || got.FixedContentPackage.ImageLibraryIDs[0] != 11 || len(got.FixedContentPackage.AttachmentLibraryIDs) != 1 || got.FixedContentPackage.AttachmentLibraryIDs[0] != 12 || len(got.FixedContentPackage.MiniprogramLibraryIDs) != 1 || got.FixedContentPackage.MiniprogramLibraryIDs[0] != 13 || len(got.FixedContentPackage.GroupInviteLibraryIDs) != 1 || got.FixedContentPackage.GroupInviteLibraryIDs[0] != 14 {
		t.Fatalf("read=%+v err=%v", got, err)
	}
	var agents, receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM automation_agents),(SELECT count(*) FROM automation_operation_receipts),(SELECT count(*) FROM automation_audit_events),(SELECT count(*) FROM automation_outbox)`).Scan(&agents, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if agents != 1 || receipts != 5 || audits != 5 || outbox != 5 {
		t.Fatalf("atomic persisted facts agents=%d receipts=%d audits=%d outbox=%d", agents, receipts, audits, outbox)
	}
	if _, err = native.Exec(ctx, `UPDATE automation_audit_events SET operation=operation`); err == nil {
		t.Fatal("automation audit unexpectedly mutable")
	}
}

func automationIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping automation PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_automation_test_" + hex.EncodeToString(random[:])
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
		t.Fatal("locate test")
	}
	for _, name := range []string{"0013_automation_agents.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
