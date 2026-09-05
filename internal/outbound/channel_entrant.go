package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type ChannelEntrantProvider struct {
	reader        channelport.PublishedEntrantActionReader
	uow           platformport.UnitOfWork
	grants        wecomport.WelcomeGrantRedeemer
	relationships wecomport.CurrentExternalContactReader
	tags          tagport.ProviderTagBindingReader
	writer        wecomport.EntrantActionWriter
	Now           func() time.Time
}

func NewChannelEntrantProvider(reader channelport.PublishedEntrantActionReader, uow platformport.UnitOfWork, grants wecomport.WelcomeGrantRedeemer, relationships wecomport.CurrentExternalContactReader, tags tagport.ProviderTagBindingReader, writer wecomport.EntrantActionWriter) *ChannelEntrantProvider {
	return &ChannelEntrantProvider{reader: reader, uow: uow, grants: grants, relationships: relationships, tags: tags, writer: writer}
}

func (provider *ChannelEntrantProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || provider.reader == nil || provider.uow == nil || provider.writer == nil || !envelope.Valid() || (envelope.Kind != effectport.KindChannelWelcome && envelope.Kind != effectport.KindChannelEntryTag) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("channel.entrant.invalid")}, nil
	}
	action, err := provider.reader.ReadPublishedEntrantAction(ctx, string(envelope.SourceRefDigest))
	if err != nil {
		if envelope.Kind == effectport.KindChannelWelcome {
			return welcomeAdapterResult(effectport.StateRetryable, effectport.Hash("channel.entrant.read-unavailable", string(envelope.Fingerprint())), "provider_unavailable", false), nil
		}
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: effectport.Hash("channel.entrant.read-unavailable", string(envelope.Fingerprint()))}, nil
	}
	if envelope.Kind == effectport.KindChannelWelcome {
		if action.Kind != "welcome" || provider.grants == nil {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.not-configured"), "welcome_not_configured", false), nil
		}
		// Pre-0066 entrant records did not carry a first receipt deadline. Their
		// ten-minute encrypted grant retention is not permission to send, so they
		// are a durable no-send rather than a fresh execution window.
		if action.SendDeadlineAt.IsZero() {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.deadline-missing-not-attempted", action.EffectRef), "deadline_missing", false), nil
		}
		if !provider.now().Before(action.SendDeadlineAt) {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.expired-not-attempted", action.EffectRef), "deadline_expired", false), nil
		}
		attachments, materialErr := welcomeAttachments(action.WelcomeMaterialSnapshot)
		if materialErr != nil {
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.material-invalid", action.EffectRef), "material_invalid", false), nil
		}
		var welcomeCode string
		err = provider.uow.Within(ctx, func(tx context.Context) error {
			var redeemErr error
			welcomeCode, redeemErr = provider.grants.Redeem(tx, action.WelcomeGrantRef, action.EffectRef)
			return redeemErr
		})
		if err != nil {
			if errors.Is(err, wecomport.ErrWelcomeGrantExpired) {
				return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.grant-expired-not-attempted", action.EffectRef), "grant_expired", false), nil
			}
			return welcomeAdapterResult(effectport.StateRetryable, effectport.Hash("channel.welcome.grant-unavailable-not-attempted", action.EffectRef, strconv.Itoa(int(attempt.Number))), "provider_unavailable", false), nil
		}
		if !provider.now().Before(action.SendDeadlineAt) {
			welcomeCode = ""
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.expired-not-attempted", action.EffectRef), "deadline_expired", false), nil
		}
		// Keep the Provider HTTP budget within the frozen business deadline. A
		// writer that honors context therefore cannot start a normal timeout
		// after the welcome window has already elapsed.
		callContext, cancel := ctx, func() {}
		callContext, cancel = context.WithDeadline(ctx, action.SendDeadlineAt)
		if callContext.Err() != nil {
			cancel()
			welcomeCode = ""
			return welcomeAdapterResult(effectport.StateFinalFailed, effectport.Hash("channel.welcome.expired-not-attempted", action.EffectRef), "deadline_expired", false), nil
		}
		err = provider.writer.SendWelcomeMessage(callContext, welcomeCode, action.WelcomeMessage, attachments)
		cancel()
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
		attempted := wecomport.ProviderCallAttempted(err)
		state := effectport.StateRetryable
		if attempted {
			state = effectport.StateUnknown
		}
		if envelope.Kind == effectport.KindChannelWelcome {
			reason := "provider_unavailable"
			if attempted {
				reason = "outcome_unknown"
			}
			return welcomeAdapterResult(state, effectport.Hash("channel.entrant.provider-error", action.EffectRef, strconv.Itoa(int(attempt.Number))), reason, attempted), err
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash("channel.entrant.provider-error", action.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: attempted, RealExternalCallExecuted: attempted}, err
	}
	if envelope.Kind == effectport.KindChannelWelcome {
		return welcomeAdapterResult(effectport.StateExecuted, effectport.Hash("channel.entrant.executed", action.EffectRef, strconv.Itoa(int(attempt.Number))), "sent", true), nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("channel.entrant.executed", action.EffectRef, strconv.Itoa(int(attempt.Number))), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (provider *ChannelEntrantProvider) now() time.Time {
	if provider != nil && provider.Now != nil {
		return provider.Now().UTC()
	}
	return time.Now().UTC()
}

const channelWelcomeOutcomeArtifactKind = "channel.welcome.outcome.v1"

// welcomeAdapterResult carries only a closed safe reason into Channel's
// completion receipt. It never includes a decrypted welcome code, raw
// callback body, or Provider response.
func welcomeAdapterResult(state effectport.State, receipt effectport.Digest, reason string, attempted bool) effectport.AdapterResult {
	payload := []byte(`{"reason":"` + reason + `"}`)
	artifact := effectport.ResultArtifact{Kind: channelWelcomeOutcomeArtifactKind, Payload: payload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(payload))
	return effectport.AdapterResult{Completion: state, ReceiptDigest: receipt, CallAttempted: attempted, RealExternalCallExecuted: attempted, Artifact: artifact}
}

func welcomeAttachments(raw json.RawMessage) ([]wecomport.WelcomeAttachment, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var snapshot mediaport.GroupOpsMaterialSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || mediaport.ValidateGroupOpsMaterialSnapshot(snapshot) != nil {
		return nil, errors.New("invalid channel welcome material snapshot")
	}
	result := make([]wecomport.WelcomeAttachment, len(snapshot.Attachments))
	for index, item := range snapshot.Attachments {
		result[index] = wecomport.WelcomeAttachment{MsgType: item.MsgType, MediaID: item.MediaID, AppID: item.AppID, PagePath: item.PagePath, Title: item.Title, URL: item.URL, Description: item.Description, PicURL: item.PicURL}
	}
	return result, nil
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
	completion := channelport.EntrantActionCompletion{EffectRef: effectRef, State: string(result.Completion), ResultDigest: string(result.ReceiptDigest), Attempt: attempt.Number, CompletedAt: time.Now().UTC()}
	if envelope.Kind == effectport.KindChannelWelcome {
		completion.ResultReason = channelWelcomeCompletionReason(result)
	}
	return sink.writer.CompleteEntrantAction(ctx, completion)
}

func channelWelcomeCompletionReason(result effectport.AdapterResult) string {
	if result.Artifact.Valid() && result.Artifact.Kind == channelWelcomeOutcomeArtifactKind {
		var artifact struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(result.Artifact.Payload, &artifact) == nil && validChannelWelcomeResultReason(artifact.Reason) {
			return artifact.Reason
		}
	}
	switch result.Completion {
	case effectport.StateExecuted:
		return "sent"
	case effectport.StateUnknown:
		return "outcome_unknown"
	case effectport.StateRetryable:
		return "provider_unavailable"
	default:
		return "final_failed"
	}
}

func validChannelWelcomeResultReason(reason string) bool {
	switch reason {
	case "welcome_not_configured", "deadline_missing", "deadline_expired", "grant_expired", "material_invalid", "provider_unavailable", "outcome_unknown", "sent", "final_failed":
		return true
	default:
		return false
	}
}

var _ effectport.ProviderAdapter = (*ChannelEntrantProvider)(nil)
var _ effectport.CompletionSink = (*ChannelEntrantCompletionSink)(nil)
