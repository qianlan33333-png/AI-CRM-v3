// Package port contains stable Outbound contracts. Business domains submit
// immutable intents here and never call a WeCom provider directly.
package port

import (
	"context"
	"errors"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var ErrInvalidPrivateMessageIntent = errors.New("invalid private message intent")

type PrivateMessageIntentCommand struct {
	SourceReference  string
	CustomerID       customerdomain.CustomerID
	StaffID          int64
	PayloadReference string
	SourceDigest     effectport.Digest
	TargetDigest     effectport.Digest
	PayloadDigest    effectport.Digest
	PolicyHash       effectport.Digest
	ReceiptKey       effectport.Digest
}

func (c PrivateMessageIntentCommand) Valid() bool {
	return strings.TrimSpace(c.SourceReference) != "" && len(c.SourceReference) <= 200 &&
		c.CustomerID > 0 && c.StaffID > 0 && strings.TrimSpace(c.PayloadReference) != "" && len(c.PayloadReference) <= 200 &&
		effectport.ValidDigest(c.SourceDigest) && effectport.ValidDigest(c.TargetDigest) &&
		effectport.ValidDigest(c.PayloadDigest) && effectport.ValidDigest(c.PolicyHash) && effectport.ValidDigest(c.ReceiptKey)
}

type PrivateMessageIntentResult struct {
	IntentID int64
	EffectID string
	Replayed bool
}

// PrivateMessageIntentWriter must participate in the caller's PostgreSQL UoW.
// Implementations persist the Outbound-owned payload reference and call the
// External Effects TransactionalAccepter without starting another transaction.
type PrivateMessageIntentWriter interface {
	WritePrivateMessageIntentWithin(context.Context, PrivateMessageIntentCommand) (PrivateMessageIntentResult, error)
}

// Provider-side contracts keep the WeCom adapter behind the stable Outbound
// boundary. Raw channel identifiers exist only in memory after effect
// acceptance and are never part of an AI Assistant DTO or table.
type PrivateMessageIntent struct {
	CustomerID       customerdomain.CustomerID
	StaffID          int64
	PayloadReference string
	PayloadDigest    effectport.Digest
}
type PrivateMessageTarget struct{ ExternalUserID, StaffUserID string }
type PrivateMessageAttachment struct {
	Kind                                                                  string
	Content                                                               []byte
	FileName, MediaType, AppID, PagePath, Title, URL, Description, PicURL string
}
type PrivateMessagePayload struct {
	Text        string
	Attachments []PrivateMessageAttachment
}
type PrivateMessageProviderReceipt struct{ MessageID string }

type PrivateMessageIntentReader interface {
	PrivateMessageIntentForEnvelope(context.Context, effectport.Envelope) (PrivateMessageIntent, error)
}
type PrivateMessageTargetResolver interface {
	ResolvePrivateMessageTarget(context.Context, customerdomain.CustomerID, int64) (PrivateMessageTarget, error)
}
type PrivateMessagePayloadReader interface {
	LoadPrivateMessagePayload(context.Context, string, effectport.Digest) (PrivateMessagePayload, error)
}
type PrivateMessageSender interface {
	SendPrivateMessage(context.Context, PrivateMessageTarget, PrivateMessagePayload) (PrivateMessageProviderReceipt, bool, error)
}
type PrivateMessageSendError interface {
	error
	OutcomeUnknown() bool
}
