package outbound

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type messageAcceptanceStub struct{ next int64 }

func (s *messageAcceptanceStub) AcceptAndQueueWithin(_ context.Context, _ effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	s.next++
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", s.next), QueueJobID: s.next}, effectport.Receipt{QueueReceiptID: fmt.Sprintf("queue-%d", s.next)}, nil
}

type messageCompletionStub struct{}

func (messageCompletionStub) ProjectMessageCompletion(context.Context, outboundport.MessageCompletion) error {
	return nil
}

func TestPostgreSQLMessageContentSnapshotShapeAndLegacyReadback(t *testing.T) {
	ctx := context.Background()
	native, cleanup := outboundMessageSnapshotDatabase(t, ctx)
	defer cleanup()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewMessageService(native, uow, &messageAcceptanceStub{}, messageCompletionStub{})
	if err != nil {
		t.Fatal(err)
	}

	legacy := messageIntentFixture(1, nil, [32]byte{})
	var legacyAcceptance outboundport.MessageAcceptance
	if err = uow.Within(ctx, func(tx context.Context) error {
		var acceptErr error
		legacyAcceptance, acceptErr = service.AcceptMessageWithin(tx, legacy)
		return acceptErr
	}); err != nil {
		t.Fatalf("accept legacy pure-text intent: %v", err)
	}
	var snapshotIsNull, digestIsNull bool
	if err = native.QueryRow(ctx, `SELECT content_snapshot IS NULL,content_snapshot_digest IS NULL FROM outbound_message_intents WHERE id=$1`, legacyAcceptance.MessageIntentID).Scan(&snapshotIsNull, &digestIsNull); err != nil || !snapshotIsNull || !digestIsNull {
		t.Fatalf("legacy snapshot shape snapshot_null=%t digest_null=%t err=%v", snapshotIsNull, digestIsNull, err)
	}
	legacyExecution, found, err := service.MessageExecution(ctx, string(effectport.Hash("legacy-message")))
	if err != nil || found || legacyExecution.MessageIntentID != 0 {
		t.Fatalf("unexpected unrelated legacy read execution=%+v found=%t err=%v", legacyExecution, found, err)
	}
	if _, err = native.Exec(ctx, `UPDATE outbound_message_intents SET envelope_fingerprint=$2,state='queued' WHERE id=$1`, legacyAcceptance.MessageIntentID, effectport.Hash("legacy-message")); err != nil {
		t.Fatal(err)
	}
	legacyExecution, found, err = service.MessageExecution(ctx, string(effectport.Hash("legacy-message")))
	if err != nil || !found || legacyExecution.ContentSnapshot != nil || legacyExecution.ContentSnapshotDigest != ([32]byte{}) {
		t.Fatalf("legacy execution=%+v found=%t err=%v", legacyExecution, found, err)
	}

	raw := json.RawMessage(`{"text":"frozen"}`)
	digest := sha256.Sum256(raw)
	withSnapshot := messageIntentFixture(2, raw, digest)
	var snapshotAcceptance outboundport.MessageAcceptance
	if err = uow.Within(ctx, func(tx context.Context) error {
		var acceptErr error
		snapshotAcceptance, acceptErr = service.AcceptMessageWithin(tx, withSnapshot)
		return acceptErr
	}); err != nil {
		t.Fatalf("accept frozen snapshot: %v", err)
	}
	fingerprint := string(effectport.Hash("snapshot-message"))
	if _, err = native.Exec(ctx, `UPDATE outbound_message_intents SET envelope_fingerprint=$2,state='queued' WHERE id=$1`, snapshotAcceptance.MessageIntentID, fingerprint); err != nil {
		t.Fatal(err)
	}
	execution, found, err := service.MessageExecution(ctx, fingerprint)
	if err != nil || !found || string(execution.ContentSnapshot) != string(raw) || execution.ContentSnapshotDigest != digest {
		t.Fatalf("frozen execution=%+v found=%t err=%v", execution, found, err)
	}

	if err = uow.Within(ctx, func(tx context.Context) error {
		_, acceptErr := service.AcceptMessageWithin(tx, messageIntentFixture(3, json.RawMessage(`null`), sha256.Sum256([]byte("null"))))
		return acceptErr
	}); !errors.Is(err, ErrInvalidMessageIntent) {
		t.Fatalf("JSON null acceptance err=%v, want invalid intent", err)
	}
	for _, invalid := range []struct {
		name     string
		snapshot any
		digest   any
	}{
		{name: "json null", snapshot: []byte(`null`), digest: make([]byte, 32)},
		{name: "short digest", snapshot: []byte(`{}`), digest: []byte{1}},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			err := insertSnapshotShapeRow(ctx, native, invalid.snapshot, invalid.digest, int64(100+len(invalid.name)))
			if err == nil || pgErrorCode(err) != "23514" {
				t.Fatalf("shape err=%v code=%s", err, pgErrorCode(err))
			}
		})
	}
}

func messageIntentFixture(id int64, snapshot json.RawMessage, snapshotDigest [32]byte) outboundport.MessageIntent {
	value := byte(id)
	return outboundport.MessageIntent{SourceKind: "automation_enrollment", SourceID: id, RunRecipientID: id, CustomerID: 1, SenderStaffID: 1, AgentID: 1, AgentPublishedVersion: 1, ContentReference: "automation-agent:1:published:1", SourceDigest: [32]byte{value}, TargetDigest: [32]byte{value}, PayloadDigest: [32]byte{value}, ContentSnapshot: snapshot, ContentSnapshotDigest: snapshotDigest, PolicyDigest: [32]byte{value}, ReceiptKey: fmt.Sprintf("message-snapshot-receipt-%02d", id)}
}

func insertSnapshotShapeRow(ctx context.Context, pool *pgxpool.Pool, snapshot, digest any, id int64) error {
	value := byte(id)
	_, err := pool.Exec(ctx, `INSERT INTO outbound_message_intents(source_kind,source_id,run_recipient_id,customer_id,sender_staff_id,agent_id,agent_published_version,content_reference,source_digest,target_digest,payload_digest,content_snapshot,content_snapshot_digest,policy_digest,receipt_key_digest,intent_digest,envelope_fingerprint,state,created_at,updated_at) VALUES('automation_enrollment',$1,$1,1,1,1,1,'automation-agent:1:published:1',$2,$2,$2,$3::jsonb,$4::bytea,$2,$2,$2,$5,'queued',clock_timestamp(),clock_timestamp())`, id, []byte{value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value, value}, snapshot, digest, effectport.Hash("shape", fmt.Sprint(id)))
	return err
}

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func outboundMessageSnapshotDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Outbound PostgreSQL snapshot test")
	}
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "outbound_snapshot_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		native.Close()
		admin.Close()
		t.Fatal("locate outbound snapshot integration test")
	}
	for _, name := range []string{"0001_platform.sql", "0005_external_effects.sql", "0044_outbound_automation_messages.sql", "0089_outbound_message_content_snapshots.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
