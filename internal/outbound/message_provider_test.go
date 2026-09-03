package outbound

import (
	"context"
	"errors"
	"testing"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type executionStub struct{ value outboundport.MessageExecution }

func (s executionStub) MessageExecution(context.Context, string) (outboundport.MessageExecution, bool, error) {
	return s.value, true, nil
}

type identityStub struct{ found bool }

func (s identityStub) VerifiedOutboundIdentity(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (identityport.OutboundIdentity, bool, error) {
	return identityport.OutboundIdentity{IdentityID: 1, CustomerID: 2, Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:c", Value: "external-secret"}, s.found, nil
}

type staffStub struct{}

func (staffStub) OutboundProviderStaffID(context.Context, accessport.StaffID) (string, bool, error) {
	return "sender-secret", true, nil
}

type contentStub struct {
	value automationport.OutboundPublishedContent
}

func (s contentStub) OutboundPublishedContent(context.Context, automationport.AgentID, int64) (automationport.OutboundPublishedContent, bool, error) {
	return s.value, true, nil
}

type writerStub struct {
	calls int
	err   error
}

func (w *writerStub) SendExternalContactText(_ context.Context, sender, external, content string) (string, error) {
	w.calls++
	if sender != "sender-secret" || external != "external-secret" || content != "hello" {
		return "", errors.New("unexpected provider payload")
	}
	return "provider-receipt", w.err
}

type crossedError struct{}

func (crossedError) Error() string               { return "uncertain" }
func (crossedError) ProviderCallAttempted() bool { return true }
func messageEnvelope() effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindOutboundMessage, SourceRefDigest: effectport.Hash("s"), TargetRefDigest: effectport.Hash("t"), PayloadDigest: effectport.Hash("p"), PolicyVersionHash: effectport.Hash("v")}
}
func messageProviderFixture(t *testing.T, enabled bool, identityFound bool, writer *writerStub) *MessageProvider {
	t.Helper()
	digest := [32]byte{1}
	execution := outboundport.MessageExecution{MessageIntentID: 1, RunRecipientID: 1, CustomerID: 2, SenderStaffID: 3, AgentID: 4, AgentPublishedVersion: 5, PayloadDigest: digest}
	content := automationport.OutboundPublishedContent{AgentID: 4, PublishedVersion: 5, ContentDigest: digest, Content: automationport.FixedContentPackage{ContentText: "hello"}}
	p, err := NewMessageProvider(MessageProviderConfig{Enabled: enabled, CorpScope: "wecom-corp:c", Executions: executionStub{execution}, Identities: identityStub{identityFound}, Staff: staffStub{}, Content: contentStub{content}, Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestMessageProviderDisabledNeverCallsProvider(t *testing.T) {
	w := &writerStub{}
	result, err := messageProviderFixture(t, false, true, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || w.calls != 0 || result.Completion != effectport.StateFinalFailed || result.CallAttempted {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, w.calls)
	}
}
func TestMessageProviderAcceptedIsNotDeliveryProof(t *testing.T) {
	w := &writerStub{}
	result, err := messageProviderFixture(t, true, true, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || w.calls != 1 || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
func TestMessageProviderPostCallErrorIsOutcomeUnknown(t *testing.T) {
	w := &writerStub{err: crossedError{}}
	result, err := messageProviderFixture(t, true, true, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err == nil || !result.CallAttempted || result.Completion != effectport.StateUnknown {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
func TestMessageProviderMissingIdentityFailsWithoutCall(t *testing.T) {
	w := &writerStub{}
	result, err := messageProviderFixture(t, true, false, w).Execute(context.Background(), messageEnvelope(), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || w.calls != 0 || result.Completion != effectport.StateFinalFailed || result.CallAttempted {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
