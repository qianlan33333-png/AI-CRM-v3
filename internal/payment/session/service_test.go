package session

import (
	"context"
	"errors"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type provisionerStub struct {
	result identityport.ProvisionResult
	calls  int
}

func (stub *provisionerStub) ProvisionVerifiedIdentity(_ context.Context, command identityport.ProvisionCommand) (identityport.ProvisionResult, error) {
	stub.calls++
	if !command.Fact.Valid() {
		return identityport.ProvisionResult{}, errors.New("unverified")
	}
	return stub.result, nil
}

type memoryStore struct{ records map[[32]byte]Record }

func (store *memoryStore) Insert(_ context.Context, record Record) (Record, error) {
	if store.records == nil {
		store.records = make(map[[32]byte]Record)
	}
	record.ID = int64(len(store.records) + 1)
	store.records[record.TokenDigest] = record
	return record, nil
}

func (store *memoryStore) Consume(_ context.Context, digest [32]byte, now time.Time) (Record, error) {
	record, ok := store.records[digest]
	if !ok || record.ConsumedAt != nil || !record.ExpiresAt.After(now) {
		return Record{}, ErrExpired
	}
	record.ConsumedAt = &now
	store.records[digest] = record
	return record, nil
}

func (store *memoryStore) Lookup(_ context.Context, digest [32]byte, now time.Time) (Record, error) {
	record, ok := store.records[digest]
	if !ok || !record.ExpiresAt.After(now) {
		return Record{}, ErrExpired
	}
	return record, nil
}

func verifiedFact(t *testing.T) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:wx-test", Value: "openid-test", Source: "wechat-oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func TestTrustedSessionUsesOneIDAndIsSingleUse(t *testing.T) {
	provisioner := &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}
	store := &memoryStore{}
	service, err := NewService(testUOW{}, provisioner, store, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	issued, err := service.IssueTrusted(context.Background(), IssueCommand{Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0001"})
	if err != nil || issued.Token == "" || issued.PayerIdentityID != 8 || issued.PayerCustomerID != 21 || issued.BeneficiaryCustomerID != 21 || provisioner.calls != 1 {
		t.Fatalf("issued=%+v calls=%d err=%v", issued, provisioner.calls, err)
	}
	record, err := service.Consume(context.Background(), issued.Token)
	if err != nil || record.PayerIdentityID != 8 || record.ConsumedAt == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err = service.Consume(context.Background(), issued.Token); !errors.Is(err, ErrExpired) {
		t.Fatalf("replay err=%v", err)
	}
	actor, err := service.LookupWithin(context.Background(), issued.Token, now)
	if err != nil || actor.PayerIdentityID != 8 || actor.PayerCustomerID != 21 {
		t.Fatalf("lookup actor=%+v err=%v", actor, err)
	}
}

func TestTrustedSessionRejectsUnverifiedOrUnauthorizedBeneficiary(t *testing.T) {
	provisioner := &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}
	service, _ := NewService(testUOW{}, provisioner, &memoryStore{}, 5*time.Minute)
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{BeneficiaryCustomerID: 42, Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0002"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("beneficiary err=%v", err)
	}
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{BeneficiaryCustomerID: 42, AdminAssisted: true, Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0003"}); err != nil {
		t.Fatalf("admin-assisted err=%v", err)
	}
	if _, err := service.IssueTrusted(context.Background(), IssueCommand{IdempotencyKey: "oauth-callback-0004"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unverified err=%v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	service, _ := NewService(testUOW{}, &provisionerStub{result: identityport.ProvisionResult{IdentityID: 8, CustomerID: 21}}, &memoryStore{}, time.Minute)
	now := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	issued, err := service.IssueTrusted(context.Background(), IssueCommand{Fact: verifiedFact(t), IdempotencyKey: "oauth-callback-0005"})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(time.Minute) }
	if _, err = service.Consume(context.Background(), issued.Token); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry err=%v", err)
	}
}
