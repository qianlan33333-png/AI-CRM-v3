package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	groupopshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/http"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

type protocolReplayStub struct {
	created bool
	err     error
	calls   int
}

func (s *protocolReplayStub) ClaimWebhookReplay(_ context.Context, _, _ string, _, _ [sha256.Size]byte, _ time.Time) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.created, nil
}

func TestGroupOpsProtocolAuthenticatorClaimsDigestAndDerivesOpaqueKey(t *testing.T) {
	key := strings.Repeat("k", 32)
	now := time.Unix(1_788_000_000, 0).UTC()
	timestamp := "1788000000"
	event := "evt-20260903-000001"
	body := []byte(`{"nodes":[{"kind":"message"}]}`)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n"))
	_, _ = mac.Write(body)
	request := httptest.NewRequest("POST", "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(string(body)))
	request.Header.Set(groupopsport.WebhookClientIDHeader, groupopsport.WebhookClientID)
	request.Header.Set(groupopsport.WebhookTimestampHeader, timestamp)
	request.Header.Set(groupopsport.WebhookNonceHeader, event)
	request.Header.Set(groupopsport.WebhookSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	replay := &protocolReplayStub{created: true}
	authenticator := &groupOpsProtocolAuthenticator{key: []byte(key), replay: replay, now: func() time.Time { return now }}

	derived, err := authenticator.AuthenticateGroupOpsWebhook(context.Background(), request, "plan-hook", body)
	if err != nil {
		t.Fatalf("authenticate err=%v", err)
	}
	digest := sha256.Sum256([]byte(event))
	want := "webhook-" + hex.EncodeToString(digest[:])
	if derived != want || len(derived) > 128 || replay.calls != 1 {
		t.Fatalf("derived=%q want=%q replay_calls=%d", derived, want, replay.calls)
	}
}

func TestGroupOpsProtocolAuthenticatorFailsClosedForSignatureReplayAndUnavailableStore(t *testing.T) {
	key := strings.Repeat("k", 32)
	now := time.Unix(1_788_000_000, 0).UTC()
	body := []byte(`{"ok":true}`)
	newRequest := func() *http.Request {
		request := httptest.NewRequest("POST", "/api/automation/group-ops/webhooks/plan-hook", strings.NewReader(string(body)))
		request.Header.Set(groupopsport.WebhookClientIDHeader, groupopsport.WebhookClientID)
		request.Header.Set(groupopsport.WebhookTimestampHeader, "1788000000")
		request.Header.Set(groupopsport.WebhookNonceHeader, "evt-20260903-000002")
		request.Header.Set(groupopsport.WebhookSignatureHeader, "sha256="+strings.Repeat("0", sha256.Size*2))
		return request
	}

	t.Run("invalid signature does not claim replay", func(t *testing.T) {
		replay := &protocolReplayStub{created: true}
		authenticator := &groupOpsProtocolAuthenticator{key: []byte(key), replay: replay, now: func() time.Time { return now }}
		_, err := authenticator.AuthenticateGroupOpsWebhook(context.Background(), newRequest(), "plan-hook", body)
		if err == nil || errors.Is(err, groupopshttp.ErrProtocolUnavailable) || replay.calls != 0 {
			t.Fatalf("err=%v replay_calls=%d", err, replay.calls)
		}
	})

	t.Run("replay is rejected", func(t *testing.T) {
		request := newRequest()
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write([]byte("1788000000\nevt-20260903-000002\n"))
		_, _ = mac.Write(body)
		request.Header.Set(groupopsport.WebhookSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
		replay := &protocolReplayStub{created: false}
		authenticator := &groupOpsProtocolAuthenticator{key: []byte(key), replay: replay, now: func() time.Time { return now }}
		_, err := authenticator.AuthenticateGroupOpsWebhook(context.Background(), request, "plan-hook", body)
		if err == nil || errors.Is(err, groupopshttp.ErrProtocolUnavailable) || replay.calls != 1 {
			t.Fatalf("err=%v replay_calls=%d", err, replay.calls)
		}
	})

	t.Run("replay store outage is unavailable", func(t *testing.T) {
		request := newRequest()
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write([]byte("1788000000\nevt-20260903-000002\n"))
		_, _ = mac.Write(body)
		request.Header.Set(groupopsport.WebhookSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
		replay := &protocolReplayStub{err: errors.New("database unavailable")}
		authenticator := &groupOpsProtocolAuthenticator{key: []byte(key), replay: replay, now: func() time.Time { return now }}
		_, err := authenticator.AuthenticateGroupOpsWebhook(context.Background(), request, "plan-hook", body)
		if !errors.Is(err, groupopshttp.ErrProtocolUnavailable) || replay.calls != 1 {
			t.Fatalf("err=%v replay_calls=%d", err, replay.calls)
		}
	})

	t.Run("short key is unavailable", func(t *testing.T) {
		authenticator := &groupOpsProtocolAuthenticator{key: []byte("too-short"), replay: &protocolReplayStub{created: true}, now: func() time.Time { return now }}
		_, err := authenticator.AuthenticateGroupOpsWebhook(context.Background(), newRequest(), "plan-hook", body)
		if !errors.Is(err, groupopshttp.ErrProtocolUnavailable) {
			t.Fatalf("err=%v", err)
		}
	})
}
