package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLAccessIntegration(t *testing.T) {
	databaseURL := environmentValue("AICRM_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping access PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "access_test_" + hex.EncodeToString(random)
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET search_path TO `+pgx.Identifier{schema}.Sanitize())
		return err
	}
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	}()

	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, source, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "migrations", "0003_access.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := accessstore.NewPostgreSQL()
	passwordHash, err := (credential.PasswordHasher{}).Hash("integration-password")
	if err != nil {
		t.Fatal(err)
	}
	input := domain.User{Username: "bootstrap", PasswordHash: passwordHash, DisplayName: "Bootstrap",
		Active: true, Roles: []domain.Role{domain.RoleSuperAdmin}}
	if _, err = repository.CreateUser(ctx, input); !errors.Is(err, platformpostgres.ErrTransactionNeeded) {
		t.Fatalf("write without transaction error = %v", err)
	}
	var first domain.User
	if err = unit.Within(ctx, func(txContext context.Context) error {
		var createErr error
		var created bool
		first, created, createErr = repository.BootstrapUser(txContext, input)
		if createErr == nil && !created {
			t.Error("first bootstrap was not created")
		}
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = unit.Within(ctx, func(txContext context.Context) error {
		again, created, bootstrapErr := repository.BootstrapUser(txContext, input)
		if bootstrapErr == nil && (created || again.ID != first.ID) {
			t.Errorf("idempotent bootstrap created=%v id=%d first=%d", created, again.ID, first.ID)
		}
		return bootstrapErr
	}); err != nil {
		t.Fatal(err)
	}
}

func environmentValue(key string) string {
	prefix := key + "="
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
