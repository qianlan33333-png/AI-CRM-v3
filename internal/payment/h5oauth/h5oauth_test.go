package h5oauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
)

type uowStub struct{}

func (uowStub) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type stateStoreStub struct {
	states map[[32]byte]State
}

func (s *stateStoreStub) Create(_ context.Context, digest [32]byte, state State, _ time.Time) error {
	if s.states == nil {
		s.states = map[[32]byte]State{}
	}
	s.states[digest] = state
	return nil
}
func (s *stateStoreStub) Consume(_ context.Context, digest [32]byte, _ time.Time) (State, error) {
	state, ok := s.states[digest]
	if !ok {
		return State{}, ErrInvalid
	}
	delete(s.states, digest)
	return state, nil
}

type issuerStub struct{ fact identitydomain.VerifiedFact }

func (i *issuerStub) IssueTrusted(_ context.Context, command paymentsession.IssueCommand) (paymentsession.Issued, error) {
	i.fact = command.Fact
	return paymentsession.Issued{Token: "pays_h5_session_token_00000001", ExpiresAt: time.Now().Add(time.Minute), Channel: paymentdomain.ChannelH5Official}, nil
}

type providerStub struct{ fact identitydomain.VerifiedFact }

func (providerStub) Enabled() bool { return true }
func (providerStub) AuthorizationURL(state string) string {
	return "https://open.weixin.qq.com/connect/oauth2/authorize?state=" + url.QueryEscape(state)
}
func (stub providerStub) Exchange(context.Context, string) (identitydomain.VerifiedFact, error) {
	return stub.fact, nil
}

func TestH5OAuthStateIsBoundExpiresAndCannotReplay(t *testing.T) {
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-oa", Value: "oa-openid", Source: "test.provider"})
	if err != nil {
		t.Fatal(err)
	}
	store, issuer := &stateStoreStub{}, &issuerStub{}
	service, err := NewService(uowStub{}, store, providerStub{fact: fact}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC) }
	if _, err = service.Start(context.Background(), "https://evil.example/pay/course-7"); err == nil {
		t.Fatal("accepted open redirect")
	}
	authorization, err := service.Start(context.Background(), "/pay/course-7")
	if err != nil || !strings.HasPrefix(authorization, "https://open.weixin.qq.com/") {
		t.Fatalf("authorization=%q err=%v", authorization, err)
	}
	parsed, _ := url.Parse(authorization)
	state := parsed.Query().Get("state")
	issued, redirect, err := service.Complete(context.Background(), state, "trusted-code")
	if err != nil || redirect != "/pay/course-7" || issued.Channel != paymentdomain.ChannelH5Official || issuer.fact.Reference().Kind != identitydomain.KindOAOpenID || issuer.fact.Reference().Scope != "wechat-app:wx-oa" {
		t.Fatalf("issued=%+v redirect=%q fact=%+v err=%v", issued, redirect, issuer.fact.Reference(), err)
	}
	if _, _, err = service.Complete(context.Background(), state, "trusted-code"); err == nil {
		t.Fatal("OAuth state replay succeeded")
	}
	legacyAuthorization, err := service.Start(context.Background(), "/pay/7")
	if err != nil {
		t.Fatalf("legacy numeric payment path err=%v", err)
	}
	legacyURL, _ := url.Parse(legacyAuthorization)
	legacyState := legacyURL.Query().Get("state")
	_, legacyRedirect, err := service.Complete(context.Background(), legacyState, "trusted-code")
	if err != nil || legacyRedirect != "/pay/7" {
		t.Fatalf("legacy redirect=%q err=%v", legacyRedirect, err)
	}
	for _, path := range []string{"/s/term-31", "/s/term-31/pay", "/c/coupon-2026"} {
		authorization, startErr := service.Start(context.Background(), path)
		if startErr != nil {
			t.Fatalf("path=%q err=%v", path, startErr)
		}
		parsed, _ := url.Parse(authorization)
		_, redirect, completeErr := service.Complete(context.Background(), parsed.Query().Get("state"), "trusted-code")
		if completeErr != nil || redirect != path {
			t.Fatalf("path=%q redirect=%q err=%v", path, redirect, completeErr)
		}
	}
	for _, path := range []string{"https://evil.example/pay/course-7", "//evil.example/pay/course-7", "/pay/", "/pay/course/7", "/pay/course?x=1", "/pay/%", "/pay/course%2F7", "/pay/course%5C7", "/s/", "/s/course/other", "/s/course/pay/other", "/s/course%2Fother", "/s/course%23fragment", "/c/short", "/c/CAPITAL-2026", "/c/coupon%2F2026"} {
		if _, err := service.Start(context.Background(), path); !errors.Is(err, ErrInvalid) {
			t.Fatalf("path=%q err=%v", path, err)
		}
	}
}
