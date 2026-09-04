package app

import (
	"context"
	"errors"
	"testing"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type unusedStore struct{ Store }
type unusedUOW struct{}

func (unusedUOW) Within(context.Context, func(context.Context) error) error {
	panic("unexpected transaction")
}

var _ platformport.UnitOfWork = unusedUOW{}

func TestActivationFailsClosedBeforePersistenceOrProviderWork(t *testing.T) {
	service := NewService(unusedUOW{}, unusedStore{})
	_, err := service.TransitionPackage(context.Background(), VersionCommand{ID: 1, ExpectedVersion: 1, Actor: 1, IdempotencyKey: "1234567890abcdef"}, segmentdomain.Active)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("activation=%v", err)
	}
}

func TestMutationRequiresStrongIdempotencyKey(t *testing.T) {
	service := NewService(unusedUOW{}, unusedStore{})
	_, err := service.CreateGroup(context.Background(), GroupCommand{Name: "新客", Actor: 1, IdempotencyKey: "short"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("short idempotency key=%v", err)
	}
}
