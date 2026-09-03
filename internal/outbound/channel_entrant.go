package outbound

import (
	"context"
	"errors"
	"strconv"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ChannelEntrantProvider struct {
	reader        channelport.PublishedEntrantActionReader
	uow           platformport.UnitOfWork
	grants        wecom.WelcomeGrantRedeemer
	relationships wecomport.CurrentExternalContactReader
	tags          tagport.ProviderTagBindingReader
	writer        wecomport.EntrantActionWriter
}

func NewChannelEntrantProvider(reader channelport.PublishedEntrantActionReader, uow platformport.UnitOfWork, grants wecom.WelcomeGrantRedeemer, relationships wecomport.CurrentExternalContactReader, tags tagport.ProviderTagBindingReader, writer wecomport.EntrantActionWriter) *ChannelEntrantProvider {
	return &ChannelEntrantProvider{reader: reader, uow: uow, grants: grants, relationships: relationships, tags: tags, writer: writer}
}

func (provider *ChannelEntrantProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || provider.reader == nil || provider.uow == nil || provider.writer == nil || !envelope.Valid() || (envelope.Kind != effectport.KindChannelWelcome && envelope.Kind != effectport.KindChannelEntryTag) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.entrant.invalid")}, nil
	}
	action, err := provider.reader.ReadPublishedEntrantAction(ctx, string(envelope.SourceRefDigest))
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entrant.read-unavailable", string(envelope.Fingerprint()))}, nil
	}
	if envelope.Kind == effectport.KindChannelWelcome {
		if action.Kind != "welcome" || provider.grants == nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.welcome.not-configured")}, nil
		}
		var welcomeCode string
		err = provider.uow.Within(ctx, func(tx context.Context) error {
			var redeemErr error
			welcomeCode, redeemErr = provider.grants.Redeem(tx, action.WelcomeGrantRef, action.EffectRef)
			return redeemErr
		})
		if err != nil {
			return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("channel.welcome.grant-unavailable", action.EffectRef, strconv.Itoa(int(attempt.Number)))}, nil
		}
		err = provider.writer.SendWelcomeMessage(ctx, welcomeCode, action.WelcomeMessage)
		welcomeCode = ""
	} else {
		if action.Kind != "entry_tag" || provider.relationships == nil || provider.tags == nil {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.entry-tag.not-configured")}, nil
		}
		contact, readErr := provider.relationships.CurrentExternalContact(ctx, customerdomain.CustomerID(action.CustomerID), action.StaffID)
		if readErr != nil {
			return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entry-tag.relationship-unavailable", action.EffectRef)}, nil
		}
		providerTagID, found, readErr := provider.tags.ProviderTagID(ctx, action.LocalTagID)
		if readErr != nil || !found {
			return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entry-tag.binding-unavailable", action.EffectRef)}, nil
		}
		err = provider.writer.AddContactTag(ctx, contact.EmployeeUserID, contact.ExternalUserID, providerTagID)
	}
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash("channel.entrant.provider-unknown", action.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true}, err
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("channel.entrant.executed", action.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

type ChannelEntrantCompletionSink struct {
	writer channelport.EntrantActionCompletionWriter
}

func NewChannelEntrantCompletionSink(writer channelport.EntrantActionCompletionWriter) (*ChannelEntrantCompletionSink, error) {
	if writer == nil {
		return nil, errors.New("channel entrant completion writer is required")
	}
	return &ChannelEntrantCompletionSink{writer: writer}, nil
}

func (sink *ChannelEntrantCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || sink.writer == nil || (envelope.Kind != effectport.KindChannelWelcome && envelope.Kind != effectport.KindChannelEntryTag) || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid channel entrant completion")
	}
	return sink.writer.CompleteEntrantAction(ctx, channelport.EntrantActionCompletion{EffectRef: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number, CompletedAt: time.Now().UTC()})
}

var _ effectport.ProviderAdapter = (*ChannelEntrantProvider)(nil)
var _ effectport.CompletionSink = (*ChannelEntrantCompletionSink)(nil)
