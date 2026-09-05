package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

func TestPostgreSQLAudienceFirstClicksKeepsFirstResolvedAttribution(t *testing.T) {
	native, cleanup := radarIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	var customerID, identityID int64
	if err := native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(ctx, `
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:test','radar-audience-customer','verified','test-fixture',1,$2)
		RETURNING id`, customerID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	insertRadar := func(code string) int64 {
		t.Helper()
		var radarID int64
		if err := native.QueryRow(ctx, `
			INSERT INTO radar_links(public_code,name,title,content_type,destination_url,auth_policy,status,created_by,updated_by,created_at,updated_at)
			VALUES($1,'Audience radar','Audience radar','link','https://example.test/radar','unionid_required','enabled',1,1,$2,$2)
			RETURNING id`, code, now).Scan(&radarID); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Exec(ctx, `INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at) VALUES($1,1,'{}',1,$2)`, radarID, now); err != nil {
			t.Fatal(err)
		}
		return radarID
	}
	radarOneID := insertRadar("rd_1234567890abcdef")
	radarTwoID := insertRadar("rd_fedcba0987654321")

	nextDigest := byte(1)
	insertSession := func(radarID int64, attribution string) int64 {
		t.Helper()
		digest := make([]byte, 32)
		digest[0] = nextDigest
		nextDigest++
		var sessionID int64
		if attribution == "resolved" {
			evidence := make([]byte, 32)
			evidence[0] = nextDigest
			nextDigest++
			if err := native.QueryRow(ctx, `
				INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at)
				VALUES($1,$2,1,$3,$4,'resolved',$5,$6,$7) RETURNING id`,
				digest, radarID, identityID, customerID, evidence, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
				t.Fatal(err)
			}
			return sessionID
		}
		if err := native.QueryRow(ctx, `
			INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,attribution_status,expires_at,created_at)
			VALUES($1,$2,1,$3,$4,$5) RETURNING id`, digest, radarID, attribution, now.Add(time.Hour), now).Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		return sessionID
	}
	insertEvent := func(radarID, sessionID int64, attribution, stage string, occurredAt time.Time) {
		t.Helper()
		keyDigest, payloadDigest := make([]byte, 32), make([]byte, 32)
		keyDigest[0], payloadDigest[0] = nextDigest, nextDigest+1
		receiptID := "audience-radar-receipt-" + hex.EncodeToString([]byte{nextDigest})
		nextDigest += 2
		var identity, customer any
		if attribution == "resolved" {
			identity, customer = identityID, customerID
		}
		if _, err := native.Exec(ctx, `
			INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at)
			VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			receiptID, radarID, sessionID, stage, attribution, identity, customer, keyDigest, payloadDigest, occurredAt, now); err != nil {
			t.Fatal(err)
		}
	}

	firstResolved := insertSession(radarOneID, "resolved")
	insertEvent(radarOneID, firstResolved, "resolved", "oauth_verified", now.Add(-48*time.Hour))
	insertEvent(radarOneID, firstResolved, "resolved", "content_opened", now.Add(-24*time.Hour))
	secondRadar := insertSession(radarTwoID, "resolved")
	insertEvent(radarTwoID, secondRadar, "resolved", "identity_resolved", now.Add(-36*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "anonymous"), "anonymous", "content_opened", now.Add(-60*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "pending"), "pending", "oauth_verified", now.Add(-60*time.Hour))
	insertEvent(radarOneID, insertSession(radarOneID, "conflict"), "conflict", "content_opened", now.Add(-60*time.Hour))
	insertEvent(radarTwoID, insertSession(radarTwoID, "resolved"), "resolved", "oauth_verified", now.Add(time.Hour))

	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	var facts []radarport.AudienceFirstClick
	if err := uow.Within(ctx, func(txCtx context.Context) error {
		var readErr error
		facts, readErr = NewPostgres().AudienceFirstClicks(txCtx, now)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts=%+v", facts)
	}
	if int64(facts[0].CustomerID) != customerID || facts[0].RadarID != radarOneID || !facts[0].FirstClickedAt.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("first radar fact=%+v", facts[0])
	}
	if int64(facts[1].CustomerID) != customerID || facts[1].RadarID != radarTwoID || !facts[1].FirstClickedAt.Equal(now.Add(-36*time.Hour)) {
		t.Fatalf("second radar fact=%+v", facts[1])
	}
}

func radarIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Radar PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_radar_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	identifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Radar integration test")
	}
	for _, migrationName := range []string{"0002_identity.sql", "0050_radar_core.sql", "0051_radar_sessions_events.sql", "0052_radar_legacy_import.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(migration)); execErr != nil {
			t.Fatalf("apply %s: %v", migrationName, execErr)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(cleanupCtx)
	}
}
