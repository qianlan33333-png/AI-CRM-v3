package main

import (
	"context"
	"errors"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	segmentbootstrap "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/bootstrap"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type testUoW struct{ err error }

func (u testUoW) Within(ctx context.Context, fn func(context.Context) error) error {
	if u.err != nil {
		return u.err
	}
	return fn(ctx)
}

type refreshReaderStub struct {
	runs map[int64]segmentdomain.RefreshRun
	err  error
}

func (s refreshReaderStub) GetRefresh(_ context.Context, id int64) (segmentdomain.RefreshRun, error) {
	if s.err != nil {
		return segmentdomain.RefreshRun{}, s.err
	}
	return s.runs[id], nil
}

func TestWaitForPublishedRefreshesUpdatesDeploymentReport(t *testing.T) {
	report := segmentbootstrap.Report{Packages: []segmentbootstrap.PackageReport{
		{Refresh: &segmentbootstrap.RefreshReport{RunID: 10, State: segmentdomain.RefreshQueued}},
		{SkippedReason: "operator_archived"},
	}}
	err := waitForPublishedRefreshes(context.Background(), &report, refreshReaderStub{runs: map[int64]segmentdomain.RefreshRun{
		10: {ID: 10, State: segmentdomain.RefreshPublished},
	}}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Packages[0].Refresh.State; got != segmentdomain.RefreshPublished {
		t.Fatalf("state=%s", got)
	}
}

func TestWaitForPublishedRefreshesFailsClosed(t *testing.T) {
	report := segmentbootstrap.Report{Packages: []segmentbootstrap.PackageReport{{Refresh: &segmentbootstrap.RefreshReport{RunID: 11, State: segmentdomain.RefreshQueued}}}}
	err := waitForPublishedRefreshes(context.Background(), &report, refreshReaderStub{runs: map[int64]segmentdomain.RefreshRun{
		11: {ID: 11, State: segmentdomain.RefreshFailed, ErrorCode: "persistence_conflict"},
	}}, time.Millisecond)
	if err == nil {
		t.Fatal("expected failed refresh to fail deployment bootstrap")
	}
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
