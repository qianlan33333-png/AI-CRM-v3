package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestWelcomeAttachmentsRequireValidatedFrozenMediaSnapshot(t *testing.T) {
	raw, err := json.Marshal(mediaport.GroupOpsMaterialSnapshot{SchemaVersion: 2, NodeKind: "message", Attachments: []mediaport.GroupOpsProviderReadyAttachment{
		{MsgType: "image", MediaID: "media-1"},
		{MsgType: "miniprogram", MediaID: "thumb-1", AppID: "wx-app", PagePath: "pages/home", Title: "课程"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := welcomeAttachments(raw)
	if err != nil || len(items) != 2 || items[1].AppID != "wx-app" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err = welcomeAttachments(json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":""}]}`)); err == nil {
		t.Fatal("invalid provider material snapshot was accepted")
	}
}

func TestChannelWelcomeDeadlineAndGrantExpiryNeverCallProvider(t *testing.T) {
	now := time.Now().UTC()
	for _, item := range []struct {
		name     string
		deadline time.Time
		grantErr error
		reason   string
	}{
		{name: "deadline missing on legacy welcome", reason: "deadline_missing"},
		{name: "deadline elapsed before redemption", deadline: now, reason: "deadline_expired"},
		{name: "known grant expiry", deadline: now.Add(time.Second), grantErr: wecomport.ErrWelcomeGrantExpired, reason: "grant_expired"},
	} {
		t.Run(item.name, func(t *testing.T) {
			grants := &welcomeGrantRedeemerStub{code: "grant-code", err: item.grantErr}
			writer := &welcomeWriterStub{}
			provider := NewChannelEntrantProvider(channelWelcomeReaderStub{action: welcomeTestAction(item.deadline)}, directChannelWelcomeUOW{}, grants, nil, nil, writer)
			provider.Now = func() time.Time { return now }
			result, err := provider.Execute(context.Background(), welcomeTestEnvelope(), effectport.Attempt{Number: 1})
			if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || writer.calls != 0 || !result.Artifact.Valid() || channelWelcomeCompletionReason(result) != item.reason {
				t.Fatalf("result=%+v err=%v grants=%d calls=%d", result, err, grants.calls, writer.calls)
			}
		})
	}
}

func TestChannelWelcomeRechecksDeadlineAndBoundsProviderContext(t *testing.T) {
	now := time.Now().UTC()
	deadline := now.Add(500 * time.Millisecond)
	grants := &welcomeGrantRedeemerStub{code: "grant-code"}
	writer := &welcomeWriterStub{}
	provider := NewChannelEntrantProvider(channelWelcomeReaderStub{action: welcomeTestAction(deadline)}, directChannelWelcomeUOW{}, grants, nil, nil, writer)
	calls := 0
	provider.Now = func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return deadline
	}
	result, err := provider.Execute(context.Background(), welcomeTestEnvelope(), effectport.Attempt{Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || grants.calls != 1 || writer.calls != 0 {
		t.Fatalf("result=%+v err=%v grants=%d calls=%d", result, err, grants.calls, writer.calls)
	}

	provider.Now = func() time.Time { return now }
	result, err = provider.Execute(context.Background(), welcomeTestEnvelope(), effectport.Attempt{Number: 2})
	if err != nil || result.Completion != effectport.StateExecuted || writer.calls != 1 || writer.deadline.IsZero() || !writer.deadline.Equal(deadline) {
		t.Fatalf("result=%+v err=%v calls=%d context-deadline=%s", result, err, writer.calls, writer.deadline)
	}
}

func TestChannelWelcomeProviderUnknownKeepsSingleEffect(t *testing.T) {
	now := time.Now().UTC()
	grants := &welcomeGrantRedeemerStub{code: "grant-code"}
	writer := &welcomeWriterStub{err: wecomport.WrapProviderWriteError(errors.New("timeout"), true)}
	provider := NewChannelEntrantProvider(channelWelcomeReaderStub{action: welcomeTestAction(now.Add(time.Second))}, directChannelWelcomeUOW{}, grants, nil, nil, writer)
	provider.Now = func() time.Time { return now }
	envelope := welcomeTestEnvelope()
	result, err := provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1})
	if err == nil || result.Completion != effectport.StateUnknown || !result.CallAttempted || !result.RealExternalCallExecuted || writer.calls != 1 || channelWelcomeCompletionReason(result) != "outcome_unknown" {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, writer.calls)
	}
	if envelope.SourceRefDigest != welcomeTestEnvelope().SourceRefDigest || envelope.TargetRefDigest != welcomeTestEnvelope().TargetRefDigest {
		t.Fatal("unknown provider result changed the stable welcome effect identity")
	}
}

type channelWelcomeReaderStub struct {
	action channelport.PublishedEntrantAction
}

func (stub channelWelcomeReaderStub) ReadPublishedEntrantAction(context.Context, string) (channelport.PublishedEntrantAction, error) {
	return stub.action, nil
}

type directChannelWelcomeUOW struct{}

var _ platformport.UnitOfWork = directChannelWelcomeUOW{}

func (directChannelWelcomeUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type welcomeGrantRedeemerStub struct {
	code  string
	err   error
	calls int
}

func (stub *welcomeGrantRedeemerStub) Redeem(context.Context, string, string) (string, error) {
	stub.calls++
	return stub.code, stub.err
}

type welcomeWriterStub struct {
	calls    int
	err      error
	deadline time.Time
}

func (stub *welcomeWriterStub) SendWelcomeMessage(ctx context.Context, _ string, _ string, _ []wecomport.WelcomeAttachment) error {
	stub.calls++
	if deadline, ok := ctx.Deadline(); ok {
		stub.deadline = deadline
	}
	return stub.err
}

func (stub *welcomeWriterStub) AddContactTag(context.Context, string, string, string) error {
	return nil
}

func welcomeTestAction(deadline time.Time) channelport.PublishedEntrantAction {
	return channelport.PublishedEntrantAction{Kind: "welcome", EffectRef: "eer_1", WelcomeGrantRef: "wgrant_1", WelcomeMessage: "hello", SendDeadlineAt: deadline}
}

func welcomeTestEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindChannelWelcome, SourceRefDigest: effectport.Hash("test", "source"), TargetRefDigest: effectport.Hash("test", "target"), PayloadDigest: effectport.Hash("test", "payload"), PolicyVersionHash: effectport.Hash("test", "policy")}
}
