package customer

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
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOwnerHandoffDoesNotOverwriteOwnerAddedAfterPreview(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	var customerID customerdomain.CustomerID
	var firstStaff, secondStaff int64
	if err = uow.Within(ctx, func(tx context.Context) error {
		native, transactionErr := platformpostgres.RequireTransaction(tx)
		if transactionErr != nil {
			return transactionErr
		}
		var insertErr error
		if insertErr = native.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); insertErr != nil {
			return insertErr
		}
		if insertErr = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('owner-a','$argon2id$fixture','Owner A','owner-a') RETURNING id`).Scan(&firstStaff); insertErr != nil {
			return insertErr
		}
		return native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('owner-b','$argon2id$fixture','Owner B','owner-b') RETURNING id`).Scan(&secondStaff)
	}); err != nil {
		t.Fatal(err)
	}

	store := NewPostgreSQLOwnerHandoffStore()
	at := time.Date(2026, 9, 6, 2, 0, 0, 0, time.UTC)
	// A preview that saw no customer_local_owners row freezes expected version
	// zero.  Another accepted command wins first; this confirmation must not
	// turn zero into a wildcard and replace that owner.
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, assignErr := store.AssignLocalOwner(tx, customerID, firstStaff, 0, "owner_handoff_local_only", at)
		return assignErr
	}); err != nil {
		t.Fatalf("first assignment: %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, assignErr := store.AssignLocalOwner(tx, customerID, secondStaff, 0, "owner_handoff_local_only", at.Add(time.Second))
		return assignErr
	}); !errors.Is(err, ErrOwnerHandoffConflict) {
		t.Fatalf("expected frozen-empty conflict, got %v", err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		owner, found, readErr := store.LocalOwner(tx, customerID, false)
		if readErr != nil {
			return readErr
		}
		if !found || owner.StaffID != firstStaff || owner.Version != 1 || owner.Source != "owner_handoff_local_only" {
			t.Fatalf("owner was overwritten: %+v found=%t", owner, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err = uow.Within(ctx, func(tx context.Context) error {
		owner, assignErr := store.AssignLocalOwner(tx, customerID, secondStaff, 1, "owner_handoff_wecom_then_crm", at.Add(2*time.Second))
		if assignErr != nil {
			return assignErr
		}
		if owner.StaffID != secondStaff || owner.Version != 2 || owner.Source != "owner_handoff_wecom_then_crm" {
			t.Fatalf("cas update=%+v", owner)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ownerHandoffPool(t *testing.T, ctx context.Context, databaseURL string) (*platformpostgres.Pool, func()) {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_owner_handoff_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0092_customer_owner_handoff.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			native.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(raw)); execErr != nil {
			native.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func TestPostgreSQLOwnerHandoffExecutionUsesFrozenCiphertextAndFourDigests(t *testing.T) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping owner-handoff PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, cleanup := ownerHandoffPool(t, ctx, databaseURL)
	defer cleanup()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cipher, err := NewOwnerHandoffCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	const batchID, effectID = "owner-batch-001", "eer_9001"
	var sourceStaff, targetStaff, customerID int64
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, transactionErr := platformpostgres.RequireTransaction(txctx)
		if transactionErr != nil {
			return transactionErr
		}
		if transactionErr = tx.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); transactionErr != nil {
			return transactionErr
		}
		if transactionErr = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('source-user','$argon2id$fixture','Source','source-user') RETURNING id`).Scan(&sourceStaff); transactionErr != nil {
			return transactionErr
		}
		if transactionErr = tx.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('target-user','$argon2id$fixture','Target','target-user') RETURNING id`).Scan(&targetStaff); transactionErr != nil {
			return transactionErr
		}
		digest := make([]byte, 32)
		for index := range digest {
			digest[index] = byte(index + 1)
		}
		if _, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES('preview-001',$1,'wecom_then_crm',$1,$2,'wecom-corp:fixture',$3,'CONFIRM',clock_timestamp()+interval '30 minutes')`, sourceStaff, targetStaff, digest); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES($1,'preview-001',$2,'owner-handoff-fixture-key',$3,'wecom_then_crm',$2,$4,'wecom-corp:fixture','accepted')`, batchID, sourceStaff, digest, targetStaff); transactionErr != nil {
			return transactionErr
		}
		sourceCipher, cipherErr := cipher.Seal(batchID, 1, "source_userid", "source-user")
		if cipherErr != nil {
			return cipherErr
		}
		targetCipher, cipherErr := cipher.Seal(batchID, 1, "target_userid", "target-user")
		if cipherErr != nil {
			return cipherErr
		}
		externalCipher, cipherErr := cipher.Seal(batchID, 1, "external_userid", "external-1")
		if cipherErr != nil {
			return cipherErr
		}
		sourceDigest := ownerHandoffSnapshotDigest("source-userid", "source-user")
		targetDigest := ownerHandoffSnapshotDigest("target-userid", "target-user")
		payloadDigest := ownerHandoffSnapshotDigest("transfer-payload", "external-1", "")
		policyDigest := ownerHandoffSnapshotDigest("policy", "wecom_then_crm", "wecom-corp:fixture", int64Text(sourceStaff), int64Text(targetStaff))
		_, transactionErr = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,effect_id,state) VALUES($1,1,$2,'wecom_then_crm',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'queued')`, batchID, customerID, sourceStaff, targetStaff, digest, sourceCipher, targetCipher, externalCipher, sourceDigest[:], targetDigest[:], digest, payloadDigest[:], policyDigest[:], effectID)
		return transactionErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		execution, readErr := store.ReadOwnerHandoffExecution(txctx, effectID)
		if readErr != nil {
			return readErr
		}
		if execution.EffectID != effectID || execution.SourceUserID != "source-user" || execution.TargetUserID != "target-user" || execution.ExternalUserID != "external-1" || execution.WelcomeMessage != "" || execution.SourceDigest != snapshotDigestText("source-userid", "source-user") {
			t.Fatalf("execution=%+v", execution)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, transactionErr := platformpostgres.RequireTransaction(txctx)
		if transactionErr != nil {
			return transactionErr
		}
		_, transactionErr = tx.Exec(ctx, `UPDATE customer_owner_handoff_lines SET target_userid_digest=decode(repeat('00',32),'hex') WHERE effect_id=$1`, effectID)
		return transactionErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		_, readErr := store.ReadOwnerHandoffExecution(txctx, effectID)
		return readErr
	}); !errors.Is(err, ErrOwnerHandoffConflict) {
		t.Fatalf("tampered digest read err=%v", err)
	}
}

func int64Text(value int64) string {
	return fmt.Sprintf("%d", value)
}

func snapshotDigestText(label string, values ...string) string {
	digest := ownerHandoffSnapshotDigest(label, values...)
	return effectDigestString(digest[:])
}
