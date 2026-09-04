package main

import (
	"context"
	"errors"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type testUoW struct{ err error }

func (u testUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	if u.err != nil {
		return u.err
	}
	return fn(ctx)
}

type usersStub struct {
	users []accessdomain.User
	err   error
}

func (s usersStub) ListUsers(context.Context) ([]accessdomain.User, error) { return s.users, s.err }

func TestBootstrapActorPrefersActiveSuperAdmin(t *testing.T) {
	id, err := bootstrapActor(context.Background(), testUoW{}, usersStub{users: []accessdomain.User{
		{ID: 1, Active: true, Roles: []accessdomain.Role{accessdomain.RoleAdmin}},
		{ID: 2, Active: false, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
		{ID: 3, Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}},
	}})
	if err != nil || id != 3 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}

func TestBootstrapActorFailsClosed(t *testing.T) {
	if _, err := bootstrapActor(context.Background(), testUoW{}, usersStub{users: []accessdomain.User{{ID: 1, Active: true, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}}); err == nil {
		t.Fatal("expected missing administrator error")
	}
	want := errors.New("read failed")
	if _, err := bootstrapActor(context.Background(), testUoW{err: want}, usersStub{}); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
