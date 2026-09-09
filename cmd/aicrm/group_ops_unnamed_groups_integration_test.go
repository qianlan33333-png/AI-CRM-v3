package main

import (
	"context"
	"strings"
	"testing"
	"time"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestGroupOpsDirectoryPersistsMixedNamedAndUnnamedGroups(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	store, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	var owner int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('unnamed-groups','$argon2id$test','Directory Test','unnamed-owner',true) RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items := []groupopsport.GroupDirectoryItem{
		{ChatReference: "named-chat", OwnerStaffID: owner, DisplayName: "真实群名", MemberCount: 2, RefreshedAt: now},
		{ChatReference: "unnamed-chat", OwnerStaffID: owner, DisplayName: "", MemberCount: 1, RefreshedAt: now},
	}
	if err = uow.Within(ctx, func(tx context.Context) error { return store.ReplaceDirectoryGroups(tx, owner, items, now) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		rows, total, readErr := store.ListDirectoryGroups(tx, owner, 100, 0)
		if readErr != nil {
			return readErr
		}
		if total != 2 || len(rows) != 2 || rows[0].DisplayName != "真实群名" || rows[1].DisplayName != "" {
			t.Fatalf("directory names were lost or fabricated: %+v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The migration relaxes absence only, not the existing length constraint.
	items[1].DisplayName = strings.Repeat("a", 129)
	if err = uow.Within(ctx, func(tx context.Context) error { return store.ReplaceDirectoryGroups(tx, owner, items, now) }); err == nil {
		t.Fatal("invalid long name accepted")
	}
}
