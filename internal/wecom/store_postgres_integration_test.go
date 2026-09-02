package wecom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLWeComStoresIntegration(t *testing.T) {
	pool, cleanup := wecomIntegrationPool(t)
	defer cleanup()
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("oauth state nonce and transaction boundary", func(t *testing.T) {
		store := NewPostgreSQLOAuthStateStore()
		now := time.Now().UTC()
		stateDigest := sha256.Sum256([]byte("state"))
		nonceDigest := sha256.Sum256([]byte("nonce"))
		state := OAuthState{Purpose: OAuthSidebar, Redirect: "/sidebar", ExpiresAt: now.Add(time.Minute)}
		if err := store.Create(ctx, state, stateDigest, nonceDigest); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("transaction boundary error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Create(txContext, state, stateDigest, nonceDigest)
		}); err != nil {
			t.Fatal(err)
		}
		wrongNonce := sha256.Sum256([]byte("wrong nonce"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, wrongNonce, now)
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("wrong nonce error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			consumed, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, nonceDigest, now)
			if consumeErr == nil && consumed.Redirect != "/sidebar" {
				return errors.New("unexpected consumed redirect")
			}
			return consumeErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthSidebar, stateDigest, nonceDigest, now)
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("state replay error=%v", err)
		}
		expiredState := sha256.Sum256([]byte("expired state"))
		expiredNonce := sha256.Sum256([]byte("expired nonce"))
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Create(txContext, OAuthState{Purpose: OAuthAdmin, Redirect: "/admin", ExpiresAt: now.Add(time.Second)}, expiredState, expiredNonce)
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			_, consumeErr := store.Consume(txContext, OAuthAdmin, expiredState, expiredNonce, now.Add(2*time.Second))
			return consumeErr
		}); !errors.Is(err, ErrInvalidOAuth) {
			t.Fatalf("expired state error=%v", err)
		}
	})

	t.Run("follow relationship active then terminated", func(t *testing.T) {
		var customerID int64
		if err := pool.Native().QueryRow(ctx, `INSERT INTO customers (status) VALUES ('active') RETURNING id`).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		store := NewPostgreSQLFollowRelationshipStore()
		relationship := FollowRelationship{CorpID: "wx-corp", EmployeeID: "employee", CustomerID: customerdomain.CustomerID(customerID), Active: true}
		if err := store.Upsert(ctx, relationship); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
			t.Fatalf("relationship transaction boundary error=%v", err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Upsert(txContext, relationship)
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			active, activeErr := store.IsActive(txContext, relationship.CorpID, relationship.EmployeeID, relationship.CustomerID)
			if activeErr == nil && !active {
				return errors.New("relationship should be active")
			}
			return activeErr
		}); err != nil {
			t.Fatal(err)
		}
		relationship.Active = false
		if err := unit.Within(ctx, func(txContext context.Context) error {
			return store.Upsert(txContext, relationship)
		}); err != nil {
			t.Fatal(err)
		}
		if err := unit.Within(ctx, func(txContext context.Context) error {
			active, activeErr := store.IsActive(txContext, relationship.CorpID, relationship.EmployeeID, relationship.CustomerID)
			if activeErr == nil && active {
				return errors.New("relationship should be inactive")
			}
			return activeErr
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func wecomIntegrationPool(t *testing.T) (*platformpostgres.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("open PostgreSQL integration database")
	}
	if err = admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal("ping PostgreSQL integration database")
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_wecom_test_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal("create PostgreSQL integration schema")
	}
	testConfig := config.Copy()
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal("open isolated PostgreSQL integration schema")
	}
	for _, path := range wecomMigrationPaths(t) {
		sql, readErr := os.ReadFile(path)
		if readErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
			admin.Close()
			t.Fatal("apply WeCom integration migration")
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func wecomMigrationPaths(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate WeCom integration test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return []string{
		filepath.Join(root, "migrations", "0001_platform.sql"),
		filepath.Join(root, "migrations", "0002_identity.sql"),
		filepath.Join(root, "migrations", "0004_wecom.sql"),
	}
}
