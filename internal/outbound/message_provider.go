package outbound

import (
	"context"

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
	payloads   outboundport.FrozenAutomationMessagePayloadReader
	writer     PrivateMessageSender
}
type MessageProviderConfig struct {
	Enabled    bool
	CorpScope  string
	Executions outboundport.MessageExecutionReader
	Identities identityport.OutboundIdentityReader
	Staff      accessport.OutboundStaffIdentityReader
	Content    automationport.OutboundPublishedContentReader
	Payloads   outboundport.FrozenAutomationMessagePayloadReader
	Writer     PrivateMessageSender
}

func NewMessageProvider(c MessageProviderConfig) (*MessageProvider, error) {
	if c.Executions == nil || c.Identities == nil || c.Staff == nil || c.Content == nil || c.Payloads == nil || c.Writer == nil || c.CorpScope == "" {
		return nil, ErrInvalidMessageIntent
	}
	return &MessageProvider{c.Enabled, c.CorpScope, c.Executions, c.Identities, c.Staff, c.Content, c.Payloads, c.Writer}, nil
}
func (p *MessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Kind != effectport.KindAutomationMessage || !envelope.Valid() {
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
	payload := PrivateMessagePayload{}
	if execution.ContentSnapshot != nil {
		payload, err = p.payloads.LoadFrozenAutomationMessagePayload(ctx, execution.ContentSnapshot, execution.ContentSnapshotDigest)
		if err != nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.frozen-content-unavailable", string(envelope.Fingerprint()))}, nil
		}
	} else {
		// 0089 did not exist for historical intents. Only their already-supported
		// pure-text package can be read through the old path; never rebuild a
		// material-bearing intent from mutable current Media.
		content, contentFound, contentErr := p.content.OutboundPublishedContent(ctx, automationport.AgentID(execution.AgentID), execution.AgentPublishedVersion)
		if contentErr != nil {
			return effectport.AdapterResult{Completion: effectport.StateRetryable}, contentErr
		}
		if !contentFound || content.ContentDigest != execution.PayloadDigest {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.content-drift", string(envelope.Fingerprint()))}, nil
		}
		if content.Content.ContentText == "" || len(content.Content.ImageLibraryIDs) > 0 || len(content.Content.MiniprogramLibraryIDs) > 0 || len(content.Content.AttachmentLibraryIDs) > 0 || len(content.Content.GroupInviteLibraryIDs) > 0 || content.Content.DynamicMiniprogramCard != nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.legacy-material-unrecoverable", string(envelope.Fingerprint()))}, nil
		}
		payload.Text = content.Content.ContentText
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
	receipt, attempted, err := p.writer.SendPrivateMessage(ctx, PrivateMessageTarget{ExternalUserID: identity.Value, StaffUserID: sender}, payload)
	if err != nil {
		// The sender distinguishes a preflight rejection (invalid payload,
		// attachment limit, or provider permission failure) from a request whose
		// outcome is genuinely unknown. Do not turn deterministic rejections into
		// retries or unknown effects merely because this adapter was attempted.
		state := effectport.StateRetryable
		if failure, ok := err.(PrivateMessageSendError); ok {
			state = effectport.StateFinalFailed
			if attempted && failure.OutcomeUnknown() {
				state = effectport.StateUnknown
			}
			// The typed sender error is already a complete Provider outcome. The
			// effect kernel treats a returned error as retryable/unknown before it
			// examines AdapterResult, so keep this classification observable.
			return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("outbound.message.provider-error", string(envelope.Fingerprint())), CallAttempted: attempted, RealExternalCallExecuted: attempted}, nil
		} else if attempted {
			state = effectport.StateUnknown
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("outbound.message.provider-error", string(envelope.Fingerprint())), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if !attempted || receipt.MessageID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("outbound.message.provider-rejected", string(envelope.Fingerprint()))}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("outbound.message.provider-receipt", receipt.MessageID, string(envelope.Fingerprint())), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

var _ effectport.ProviderAdapter = (*MessageProvider)(nil)
