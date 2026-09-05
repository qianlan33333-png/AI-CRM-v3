package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	archivestore "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	wecom "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// TestMessageArchivePostgreSQLInboxJourney exercises the composition seam with
// real PostgreSQL Inbox and Archive owner stores. Its reader is deliberately a
// deterministic decrypted fixture: no SDK credentials, network pull or real
// message content are involved in the database/restart proof.
func TestMessageArchivePostgreSQLInboxJourney(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	applyMessageArchiveJourneyMigrations(t, ctx, native)
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}

	var staffID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('archive-journey','$argon2id$journey','Archive Journey','archive-staff',true) RETURNING id`).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:wx-archive-journey", Value: "wm_unresolved", Source: "wecom.message_archive"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &archiveJourneyReader{fact: fact}
	inbox, err := webhook.NewService(webhook.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	archive := archiveapp.Service{Enabled: true, ReadEnabled: true, CorpScope: "wecom-corp:wx-archive-journey", Reader: reader, Identity: archiveJourneyIdentity{}, Lineage: archiveJourneyLineage{}, Staff: archiveJourneyStaff{id: staffID}, Store: archivestore.NewPostgreSQL(), UOW: uow, PageLimit: 100, PageBudget: 1, Now: func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) }}
	processor := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: uow, Archive: archive, Now: func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) }}
	key, err := idempotency.Parse("wecom:message-archive:journey-0001")
	if err != nil {
		t.Fatal(err)
	}
	notification, _ := json.Marshal(map[string]any{"corp_id": "wx-archive-journey", "event": "msgaudit_notify", "received_at": "2026-09-05T00:00:00Z"})
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, ingestErr := inbox.Ingest(tx, webhook.Ingest{Provider: "wecom.message_archive", IdempotencyKey: key, Payload: notification, MaxAttempts: 1})
		return ingestErr
	}); err != nil {
		t.Fatal(err)
	}

	processed, err := processor.ProcessOnce(ctx, "archive-journey-one", 1)
	if err != nil || processed != 1 {
		t.Fatalf("first worker processed=%d err=%v", processed, err)
	}
	var cursor int64
	var inboxStatus string
	var attempts, maxAttempts int
	if err = native.QueryRow(ctx, `SELECT last_seq FROM message_archive_sync_state WHERE corp_scope='wecom-corp:wx-archive-journey'`).Scan(&cursor); err != nil || cursor != 2 {
		t.Fatalf("first cursor=%d err=%v", cursor, err)
	}
	if err = native.QueryRow(ctx, `SELECT status,attempt_count,max_attempts FROM webhook_inbox WHERE idempotency_key=$1`, key).Scan(&inboxStatus, &attempts, &maxAttempts); err != nil || inboxStatus != "retryable" || attempts != 1 || maxAttempts != 2 {
		t.Fatalf("continuation status=%s attempts=%d/%d err=%v", inboxStatus, attempts, maxAttempts, err)
	}

	// A new one-shot worker instance is the restart boundary. It reads the
	// committed cursor and only asks the fixture for seq=2, then marks Inbox done.
	restarted := wecom.ArchiveInboxProcessor{Enabled: true, Inbox: inbox, UOW: uow, Archive: archive, Now: processor.Now}
	processed, err = restarted.ProcessOnce(ctx, "archive-journey-restart", 1)
	if err != nil || processed != 1 || len(reader.cursors) != 2 || reader.cursors[0] != 0 || reader.cursors[1] != 2 {
		t.Fatalf("restart processed=%d cursors=%v err=%v", processed, reader.cursors, err)
	}
	var messages, unresolved, unsupported int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM message_archive_messages),(SELECT count(*) FROM message_archive_participants WHERE resolution_status='not_found'),(SELECT count(*) FROM message_archive_messages WHERE provider_payload IS NOT NULL)`).Scan(&messages, &unresolved, &unsupported); err != nil || messages != 2 || unresolved != 2 || unsupported != 1 {
		t.Fatalf("archive messages=%d unresolved=%d unsupported=%d err=%v", messages, unresolved, unsupported, err)
	}
	if err = native.QueryRow(ctx, `SELECT status,attempt_count,max_attempts FROM webhook_inbox WHERE idempotency_key=$1`, key).Scan(&inboxStatus, &attempts, &maxAttempts); err != nil || inboxStatus != "processed" || attempts != 2 || maxAttempts != 2 {
		t.Fatalf("completed status=%s attempts=%d/%d err=%v", inboxStatus, attempts, maxAttempts, err)
	}

	// Replayed notification stays processed, triggers no zero-cursor pull and
	// creates neither messages nor a second receipt.
	if err = uow.Within(ctx, func(tx context.Context) error {
		replay, ingestErr := inbox.Ingest(tx, webhook.Ingest{Provider: "wecom.message_archive", IdempotencyKey: key, Payload: notification, MaxAttempts: 1})
		if ingestErr != nil {
			return ingestErr
		}
		if !replay.Replay {
			return errArchiveJourneyReplay
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	processed, err = restarted.ProcessOnce(ctx, "archive-journey-replay", 1)
	if err != nil || processed != 0 || len(reader.cursors) != 2 {
		t.Fatalf("replay processed=%d cursors=%v err=%v", processed, reader.cursors, err)
	}
}

var errArchiveJourneyReplay = archiveJourneyError("expected Inbox replay")

type archiveJourneyError string

func (e archiveJourneyError) Error() string { return string(e) }

type archiveJourneyReader struct {
	fact    identitydomain.VerifiedFact
	cursors []uint64
}

func (reader *archiveJourneyReader) ArchiveHealth(context.Context) (wecomport.ArchiveHealth, error) {
	return wecomport.ArchiveHealth{}, nil
}
func (reader *archiveJourneyReader) GetChatData(_ context.Context, cursor uint64, _ uint32) ([]wecomport.EncryptedArchiveRecord, error) {
	reader.cursors = append(reader.cursors, cursor)
	if cursor != 0 {
		return nil, nil
	}
	return []wecomport.EncryptedArchiveRecord{{Seq: 1, MsgID: "journey-text"}, {Seq: 2, MsgID: "journey-unknown"}}, nil
}
func (reader *archiveJourneyReader) DecryptArchiveData(context.Context, []wecomport.EncryptedArchiveRecord) ([]wecomport.PlainArchiveRecord, error) {
	return []wecomport.PlainArchiveRecord{
		{Seq: 1, MsgID: "journey-text", Payload: json.RawMessage(`{"msgid":"journey-text","from":"archive-staff","tolist":["wm_unresolved"],"msgtype":"text","msgtime":1788336000,"text":{"content":"fixture text"}}`), ExternalIdentities: []wecomport.TrustedArchiveExternalIdentity{{Value: "wm_unresolved", Fact: reader.fact}}},
		{Seq: 2, MsgID: "journey-unknown", Payload: json.RawMessage(`{"msgid":"journey-unknown","from":"archive-staff","tolist":["wm_unresolved"],"msgtype":"mixed_future","msgtime":1788336060,"mixed_future":{"fixture":true}}`), ExternalIdentities: []wecomport.TrustedArchiveExternalIdentity{{Value: "wm_unresolved", Fact: reader.fact}}},
	}, nil
}
func (reader *archiveJourneyReader) GetArchiveMedia(context.Context, wecomport.ArchiveMediaRequest) (wecomport.ArchiveMediaChunk, error) {
	return wecomport.ArchiveMediaChunk{}, archiveapp.ErrProviderPage
}

type archiveJourneyIdentity struct{}

func (archiveJourneyIdentity) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}

type archiveJourneyLineage struct{}

func (archiveJourneyLineage) CanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return []customerdomain.CustomerID{1}, nil
}

type archiveJourneyStaff struct{ id int64 }

func (staff archiveJourneyStaff) UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error) {
	return accessdomain.User{ID: staff.id}, nil
}

func applyMessageArchiveJourneyMigrations(t *testing.T, ctx context.Context, native *pgxpool.Pool) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate message archive journey")
	}
	base := filepath.Join(filepath.Dir(source), "..", "..", "migrations")
	for _, name := range []string{"0071_message_archive_core.sql", "0072_message_archive_migration_receipts.sql"} {
		sql, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = native.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

var _ archiveport.CustomerMessageReader = archiveapp.Service{}
