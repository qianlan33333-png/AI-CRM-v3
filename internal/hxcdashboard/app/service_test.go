package app

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type testIdentity struct{ seen []identityport.ScopedUnionID }

func (resolver *testIdentity) ResolveHXCUnionIDs(_ context.Context, refs []identityport.ScopedUnionID) ([]identityport.ScopedUnionIDResult, error) {
	resolver.seen = append(resolver.seen, refs...)
	out := make([]identityport.ScopedUnionIDResult, 0, len(refs))
	for _, ref := range refs {
		out = append(out, identityport.ScopedUnionIDResult{Position: ref.Position, Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(77)})
	}
	return out, nil
}

func TestProjectUsesOnlyScopedUnionIDAndDetectsDuplicateAccountConflict(t *testing.T) {
	resolver := &testIdentity{}
	service := Service{Scope: "wechat-open-platform:platform-1", SubjectKey: []byte("01234567890123456789012345678901"), Identity: resolver, UOW: testUOW{}}
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	projection, err := service.project(context.Background(), hxcport.Snapshot{AsOf: now, Rows: []domain.SourceRow{{HXCUserID: "a", UnionID: "u-a", SubscriptionTier: "pro", SubscriptionExpiresAt: &future, LastUsedAt: &now}, {HXCUserID: "b", UnionID: "u-b", SubscriptionTier: "pro", SubscriptionExpiresAt: &future}, {HXCUserID: "c", SubscriptionTier: "free", SourceUpdatedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolver.seen) != 2 || resolver.seen[0].Scope != "wechat-open-platform:platform-1" {
		t.Fatalf("unexpected identity input: %#v", resolver.seen)
	}
	if projection.Counts.Total != 3 || projection.Counts.ActiveUsed != 1 || projection.Counts.ActiveUnused != 1 || projection.Counts.RegisteredNoActiveMembership != 1 {
		t.Fatalf("bad funnel counts: %#v", projection.Counts)
	}
	if projection.Counts.Conflict != 2 || projection.Counts.Unmatched != 1 || projection.Counts.Matched != 0 {
		t.Fatalf("bad identity counts: %#v", projection.Counts)
	}
	for _, row := range projection.Rows {
		if row.HXCUserID != "" || row.UnionID != "" {
			t.Fatal("raw HXC identity survived projection")
		}
	}
}
