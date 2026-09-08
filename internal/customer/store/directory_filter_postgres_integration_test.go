package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestDirectoryFilterPredicatesKeepPageAndTotalInLockstepPostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	native, cleanup := directoryFilterPool(t, ctx, url)
	defer cleanup()
	if _, err = native.Exec(ctx, `
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,oneid_label,phone_masked,phone_assurance,activation_status,last_synced_at,updated_at) VALUES
		(1,'active','owner-nine-first','','CID-1','','','active',NULL,'2026-09-08T10:00:00Z'),
		(2,'active','owner-nine-tagged','','CID-2','','','active',NULL,'2026-09-08T09:00:00Z'),
		(3,'active','other-owner-tagged','','CID-3','','','active',NULL,'2026-09-08T08:00:00Z');
		INSERT INTO customer_local_owners(customer_id,staff_id) VALUES(1,9),(2,9),(3,8)`); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := PostgreSQL{}
	var ownerIDs []customerdomain.CustomerID
	var page customerapp.PageData
	err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		ownerIDs, readErr = repository.CustomerIDsForOwner(tx, 9, 100)
		if readErr != nil {
			return readErr
		}
		page, readErr = repository.List(tx, customerapp.Query{
			Limit:     2,
			Watermark: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
			Filters: customerapp.Filters{
				OwnerCustomerIDs: ownerIDs,
				TagCustomerIDs:   []customerdomain.CustomerID{2, 3},
			},
		})
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerIDs) != 2 || ownerIDs[0] != 1 || ownerIDs[1] != 2 {
		t.Fatalf("ownerIDs=%v", ownerIDs)
	}
	if page.Count != 1 || page.TotalIsEstimate || len(page.Items) != 1 || page.Items[0].CustomerID != 2 || page.Items[0].OwnerStaffID == nil || *page.Items[0].OwnerStaffID != 9 {
		t.Fatalf("page=%+v", page)
	}
}

func directoryFilterPool(t *testing.T, ctx context.Context, url string) (*pgxpool.Pool, func()) {
	t.Helper()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "directory_filter_" + hex.EncodeToString(random)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `
		CREATE TABLE customer_directory_projection (
			customer_id BIGINT PRIMARY KEY, customer_status TEXT NOT NULL, display_name TEXT NOT NULL,
			avatar_url TEXT NOT NULL, oneid_label TEXT NOT NULL, phone_masked TEXT NOT NULL,
			phone_assurance TEXT NULL, activation_status TEXT NOT NULL, last_synced_at TIMESTAMPTZ NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE customer_local_owners (customer_id BIGINT PRIMARY KEY, staff_id BIGINT NOT NULL);
	`); err != nil {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	return native, func() {
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
