package app_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type ownerHandoffPGResolver struct {
	candidate customerport.OwnerHandoffCandidate
}

func (r ownerHandoffPGResolver) ResolveOwnerHandoffCandidates(_ context.Context, _ customerport.OwnerHandoffMode, _, _ int64, _ string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	if len(ids) != 1 || ids[0] != r.candidate.CustomerID {
		return nil, customer.ErrOwnerHandoffConflict
	}
	return []customerport.OwnerHandoffCandidate{r.candidate}, nil
}

func TestPostgreSQLOwnerHandoffLocalOnlyPreviewConfirmIsAtomic(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffAppPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var source, target int64
	var customerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('source-a','$argon2id$fixture','Source','source-a',false) RETURNING id`).Scan(&source); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('target-b','$argon2id$fixture','Target','target-b',true) RETURNING id`).Scan(&target)
	}); err != nil {
		t.Fatal(err)
	}
	candidate := customerport.OwnerHandoffCandidate{CustomerID: customerID, RelationshipDigest: [32]byte{1}, State: "ready"}
	service, err := customerapp.NewOwnerHandoffService(uow, customer.NewPostgreSQLOwnerHandoffStore(), accessstore.NewPostgreSQL(), ownerHandoffPGResolver{candidate: candidate}, mustOwnerHandoffAudit(t), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: source, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: source, TargetStaffID: target, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{customerID}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "preview-owner-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: source, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-owner-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Lines) != 1 || batch.Lines[0].State != "local_updated" {
		t.Fatalf("batch=%+v", batch)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		owner, found, e := customer.NewPostgreSQLOwnerHandoffStore().LocalOwner(txctx, customerID, false)
		if e != nil {
			return e
		}
		if !found || owner.StaffID != target || owner.Source != "owner_handoff_local_only" {
			t.Fatalf("owner=%+v found=%t", owner, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ownerHandoffAppPool(t *testing.T, ctx context.Context, databaseURL string) (*platformpostgres.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_owner_handoff_app_" + hex.EncodeToString(raw)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0006_wecom_callback_channel_acquisition.sql", "0007_media.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0092_customer_owner_handoff.sql"} {
		body, e := os.ReadFile(filepath.Join(root, "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = native.Exec(ctx, string(body)); e != nil {
			t.Fatalf("apply %s: %v", name, e)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	}
}

func mustOwnerHandoffAudit(t *testing.T) *platformaudit.Service {
	t.Helper()
	value, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type failingOwnerHandoffAudit struct{}

func (failingOwnerHandoffAudit) Append(context.Context, platformaudit.Event) (platformaudit.Event, error) {
	return platformaudit.Event{}, errors.New("audit unavailable")
}

func TestPostgreSQLOwnerHandoffLocalOnlyRollsBackWhenAuditFails(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffAppPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var source, target int64
	var customerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('source-x','$argon2id$fixture','Source','source-x') RETURNING id`).Scan(&source); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('target-y','$argon2id$fixture','Target','target-y') RETURNING id`).Scan(&target)
	}); err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewOwnerHandoffService(uow, customer.NewPostgreSQLOwnerHandoffStore(), accessstore.NewPostgreSQL(), ownerHandoffPGResolver{candidate: customerport.OwnerHandoffCandidate{CustomerID: customerID, RelationshipDigest: [32]byte{9}, State: "ready"}}, failingOwnerHandoffAudit{}, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: source, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: source, TargetStaffID: target, CorpScope: "wecom-corp:fixture", CustomerIDs: []customerdomain.CustomerID{customerID}, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "preview-owner-failure"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: source, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "confirm-owner-failure"}); err == nil {
		t.Fatal("expected audit failure")
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, e := platformpostgres.RequireTransaction(txctx)
		if e != nil {
			return e
		}
		var owners, batches, outbox int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM customer_local_owners WHERE customer_id=$1`, customerID).Scan(&owners); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM customer_owner_handoff_batches`).Scan(&batches); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, fmt.Sprintf("%d", customerID)).Scan(&outbox); e != nil {
			return e
		}
		if owners != 0 || batches != 0 || outbox != 0 {
			t.Fatalf("rollback leaked owners=%d batches=%d outbox=%d", owners, batches, outbox)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
