package app

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

func TestMachineRequestRateLimiterPersistsCredentialAndMachineWindows(t *testing.T) {
	repository := newMemoryRepository()
	now := testNow
	limiter, err := NewMachineRequestRateLimiter(repository, testUOW{}, MachineRequestRateLimitConfig{
		Window:                    time.Minute,
		MaxClientCredentialChecks: 2,
		MaxMachineRequests:        1,
		Now:                       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	source := netip.MustParseAddr("203.0.113.20")
	for attempt := 0; attempt < 2; attempt++ {
		if err = limiter.AllowClientCredentials(context.Background(), "client-a", source); err != nil {
			t.Fatalf("credential attempt %d: %v", attempt, err)
		}
	}
	if err = limiter.AllowClientCredentials(context.Background(), "client-a", source); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("credential limit error=%v", err)
	}
	if err = limiter.AllowClientCredentials(context.Background(), "client-a", netip.MustParseAddr("203.0.113.21")); err != nil {
		t.Fatalf("different source should have its own credential bucket: %v", err)
	}

	principal := domain.MachinePrincipal{ClientRecord: 9, ClientID: "client-a"}
	if err = limiter.AllowMachineRequest(context.Background(), principal, source); err != nil {
		t.Fatalf("machine request: %v", err)
	}
	if err = limiter.AllowMachineRequest(context.Background(), principal, source); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("machine request limit error=%v", err)
	}

	now = now.Add(time.Minute)
	if err = limiter.AllowMachineRequest(context.Background(), principal, source); err != nil {
		t.Fatalf("next window request: %v", err)
	}
}
