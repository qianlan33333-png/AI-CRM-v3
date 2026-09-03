package outbound

import (
	"context"
	"errors"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type ExternalContactTextWriter interface {
	SendExternalContactText(context.Context, string, string, string) (string, error)
}
type MessageProvider struct {
	enabled    bool
	corpScope  string
	executions outboundport.MessageExecutionReader
	identities identityport.OutboundIdentityReader
	staff      accessport.OutboundStaffIdentityReader
	content    automationport.OutboundPublishedContentReader
	writer     ExternalContactTextWriter
}
type MessageProviderConfig struct {
	Enabled    bool
	CorpScope  string
	Executions outboundport.MessageExecutionReader
	Identities identityport.OutboundIdentityReader
	Staff      accessport.OutboundStaffIdentityReader
	Content    automationport.OutboundPublishedContentReader
	Writer     ExternalContactTextWriter
}

func NewMessageProvider(c MessageProviderConfig) (*MessageProvider, error) {
	if c.Executions == nil || c.Identities == nil || c.Staff == nil || c.Content == nil || c.Writer == nil || c.CorpScope == "" {
		return nil, ErrInvalidMessageIntent
	}
	return &MessageProvider{c.Enabled, c.CorpScope, c.Executions, c.Identities, c.Staff, c.Content, c.Writer}, nil
}
func (p *MessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Kind != effectport.KindOutboundMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.invalid")}, nil
	}
	if !p.enabled {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.provider-disabled", string(envelope.Fingerprint()))}, nil
	}
	execution, found, err := p.executions.MessageExecution(ctx, string(envelope.Fingerprint()))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.intent-missing", string(envelope.Fingerprint()))}, nil
	}
	content, found, err := p.content.OutboundPublishedContent(ctx, automationport.AgentID(execution.AgentID), execution.AgentPublishedVersion)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found || content.ContentDigest != execution.PayloadDigest {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.content-drift", string(envelope.Fingerprint()))}, nil
	}
	if content.Content.ContentText == "" || len(content.Content.ImageLibraryIDs) > 0 || len(content.Content.MiniprogramLibraryIDs) > 0 || len(content.Content.AttachmentLibraryIDs) > 0 || len(content.Content.GroupInviteLibraryIDs) > 0 || content.Content.DynamicMiniprogramCard != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.material-unsupported", string(envelope.Fingerprint()))}, nil
	}
	identity, found, err := p.identities.VerifiedOutboundIdentity(ctx, execution.CustomerID, identitydomain.KindWeComExternalUserID, p.corpScope)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.identity-unavailable", string(envelope.Fingerprint()))}, nil
	}
	sender, found, err := p.staff.OutboundProviderStaffID(ctx, accessport.StaffID(execution.SenderStaffID))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable}, err
	}
	if !found {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.sender-unavailable", string(envelope.Fingerprint()))}, nil
	}
	receipt, err := p.writer.SendExternalContactText(ctx, sender, identity.Value, content.Content.ContentText)
	if err != nil {
		attempted := providerCallAttempted(err)
		return effectport.AdapterResult{Completion: map[bool]effectport.State{true: effectport.StateUnknown, false: effectport.StateRetryable}[attempted], CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("outbound.message.provider-receipt", receipt, string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true}, nil
}
func providerCallAttempted(err error) bool {
	var v interface{ ProviderCallAttempted() bool }
	return errors.As(err, &v) && v.ProviderCallAttempted()
}

var _ effectport.ProviderAdapter = (*MessageProvider)(nil)
