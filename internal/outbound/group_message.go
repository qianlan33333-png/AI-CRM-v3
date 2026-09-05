package outbound

import (
	"context"
	"errors"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

var ErrGroupMessageProviderDisabled = errors.New("Group Ops message provider is disabled")

// GroupMessageProvider is the composition-time carrier for the bounded Group
// Ops outbound adapter. Its disabled mode is deterministic and makes no
// network call. The preparation writer is a stable Media port reserved for an
// explicitly enabled, approved Provider adapter; disabled mode can never use
// it to manufacture a preparation receipt.
type GroupMessageProvider struct {
	enabled           bool
	preparationWriter mediaport.GroupOpsMaterialPreparationWriter
}

type GroupMessageProviderConfig struct {
	Enabled           bool
	PreparationWriter mediaport.GroupOpsMaterialPreparationWriter
}

func NewGroupMessageProvider(config GroupMessageProviderConfig) (*GroupMessageProvider, error) {
	return &GroupMessageProvider{enabled: config.Enabled, preparationWriter: config.PreparationWriter}, nil
}

// RecordPreparedMaterials is the only preparation write seam exposed to a
// future approved adapter. It is intentionally unavailable in the current
// provider-disabled composition and delegates persistence to Media ownership.
func (p *GroupMessageProvider) RecordPreparedMaterials(ctx context.Context, command mediaport.GroupOpsMaterialPreparationCommand) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	if p == nil || !p.enabled || p.preparationWriter == nil {
		return mediaport.GroupOpsMaterialPreparationReceipt{}, ErrGroupMessageProviderDisabled
	}
	return p.preparationWriter.RecordPreparedGroupOpsMaterials(ctx, command)
}

func (p *GroupMessageProvider) Execute(_ context.Context, envelope effectport.Envelope, _ effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Kind != effectport.KindGroupMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", "invalid-envelope")}, nil
	}
	if !p.enabled {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", string(envelope.Fingerprint())), CallAttempted: false, RealExternalCallExecuted: false}, nil
	}
	// No live Group Message protocol adapter is registered in this repository;
	// an accidentally enabled carrier still fails closed rather than claiming a
	// Provider call or delivery.
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-not-configured", string(envelope.Fingerprint())), CallAttempted: false, RealExternalCallExecuted: false}, nil
}

// DisabledGroupMessageProvider is the deterministic default adapter for the
// Group Ops outbound kind. It makes no network call and returns a valid
// receipt digest so the EER worker records an auditable final_failed outcome.
type DisabledGroupMessageProvider struct{}

func NewDisabledGroupMessageProvider() *DisabledGroupMessageProvider {
	return &DisabledGroupMessageProvider{}
}

func (p *DisabledGroupMessageProvider) Execute(_ context.Context, envelope effectport.Envelope, _ effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Kind != effectport.KindGroupMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", "invalid-envelope")}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", string(envelope.Fingerprint())), CallAttempted: false, RealExternalCallExecuted: false}, nil
}

// GroupMessageCompletionProjector is implemented by the Group Ops store. It
// is intentionally expressed as a local projection method so outbound never
// imports Group Ops app/store/http/worker packages.
type GroupMessageCompletionProjector interface {
	CompleteEffect(context.Context, string, groupopsport.ExecutionState, bool, bool, string, int32, time.Time) error
}

type GroupMessageCompletionSink struct {
	projector GroupMessageCompletionProjector
}

func NewGroupMessageCompletionSink(projector GroupMessageCompletionProjector) (*GroupMessageCompletionSink, error) {
	if projector == nil {
		return nil, errors.New("Group Ops completion projector is required")
	}
	return &GroupMessageCompletionSink{projector: projector}, nil
}

func (s *GroupMessageCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || s.projector == nil || envelope.Kind != effectport.KindGroupMessage || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid Group Ops completion")
	}
	state := groupopsport.ExecutionOutcomeUnknown
	providerAccepted := false
	deliveryProven := false
	switch result.Completion {
	case effectport.StateExecuted:
		state = groupopsport.ExecutionProviderAccepted
		providerAccepted = result.CallAttempted && result.RealExternalCallExecuted
	case effectport.StateFinalFailed:
		state = groupopsport.ExecutionFinalFailed
	case effectport.StateUnknown:
		state = groupopsport.ExecutionOutcomeUnknown
	case effectport.StateRetryable:
		state = groupopsport.ExecutionOutcomeUnknown
	default:
		return errors.New("invalid Group Ops completion state")
	}
	return s.projector.CompleteEffect(ctx, effectRef, state, providerAccepted, deliveryProven, string(result.ReceiptDigest), attempt.Number, time.Now().UTC())
}

// CompletionRouter keeps EER's single completion-sink slot while routing
// owner-specific projections by opaque envelope kind.
type CompletionRouter struct {
	tag        *TagCatalogCompletionSink
	group      *GroupMessageCompletionSink
	channel    *ChannelAssetCompletionSink
	entrant    *ChannelEntrantCompletionSink
	link       *ChannelLinkCompletionSink
	private    *PrivateMessageCompletionSink
	automation effectport.CompletionSink
	sidebar    effectport.CompletionSink
	survey     effectport.CompletionSink
}

func NewCompletionRouterWithChannels(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, channel *ChannelAssetCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && channel == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, channel: channel}, nil
}

func NewCompletionRouterWithChannelEntrants(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, channel *ChannelAssetCompletionSink, entrant *ChannelEntrantCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && channel == nil && entrant == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, channel: channel, entrant: entrant}, nil
}
func NewCompletionRouterWithAllChannels(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, channel *ChannelAssetCompletionSink, entrant *ChannelEntrantCompletionSink, link *ChannelLinkCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && channel == nil && entrant == nil && link == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, channel: channel, entrant: entrant, link: link}, nil
}

type PrivateMessageCompletionSink struct {
	outbound PrivateMessageCompletionProjector
	ai       aiassistantport.EffectCompletionProjector
}

func NewPrivateMessageCompletionSink(outbound PrivateMessageCompletionProjector, ai aiassistantport.EffectCompletionProjector) (*PrivateMessageCompletionSink, error) {
	if outbound == nil || ai == nil {
		return nil, errors.New("private message completion projectors are required")
	}
	return &PrivateMessageCompletionSink{outbound: outbound, ai: ai}, nil
}

func (s *PrivateMessageCompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if s == nil || envelope.Kind != effectport.KindOutboundMessage || !effectport.ValidDigest(result.ReceiptDigest) {
		return errors.New("invalid private message completion")
	}
	state := aiassistantport.ExecutionOutcomeUnknown
	providerAccepted := false
	deliveryProven := false
	switch result.Completion {
	case effectport.StateExecuted:
		state = aiassistantport.ExecutionProviderAccepted
		providerAccepted = result.CallAttempted && result.RealExternalCallExecuted
	case effectport.StateFinalFailed:
		state = aiassistantport.ExecutionFinalFailed
	case effectport.StateUnknown:
		state = aiassistantport.ExecutionOutcomeUnknown
	case effectport.StateRetryable:
		state = aiassistantport.ExecutionRetryableFailed
	case effectport.StateReconciled:
		state = aiassistantport.ExecutionReconciled
	default:
		return errors.New("invalid private message completion state")
	}
	if err := s.outbound.CompletePrivateMessage(ctx, effectRef, string(state), time.Now().UTC()); err != nil {
		return err
	}
	return s.ai.CompleteExternalEffect(ctx, effectRef, state, providerAccepted, deliveryProven, result.ReceiptDigest, attempt.Number, attempt.Generation, attempt.Fence, time.Now().UTC())
}

func NewCompletionRouterWithPrivate(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, private *PrivateMessageCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && private == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, private: private}, nil
}

func (r *CompletionRouter) WithPrivateMessage(private *PrivateMessageCompletionSink) *CompletionRouter {
	if r != nil {
		r.private = private
	}
	return r
}

func (r *CompletionRouter) WithAutomationMessage(message effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.automation = message
	}
	return r
}

func (r *CompletionRouter) WithSidebarJSSDK(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.sidebar = sink
	}
	return r
}

func (r *CompletionRouter) WithSurveyCompletion(sink effectport.CompletionSink) *CompletionRouter {
	if r != nil {
		r.survey = sink
	}
	return r
}

func NewCompletionRouter(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group}, nil
}

func NewCompletionRouterWithMessage(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink, message effectport.CompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil && message == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group, automation: message}, nil
}

func (r *CompletionRouter) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if r == nil {
		return errors.New("completion router is unavailable")
	}
	switch envelope.Kind {
	case effectport.KindAutomationMessage:
		if r.automation == nil {
			return errors.New("message completion sink is unavailable")
		}
		return r.automation.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindWeComTagCatalog:
		if r.tag == nil {
			return errors.New("tag completion sink is unavailable")
		}
		return r.tag.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindGroupMessage:
		if r.group == nil {
			return errors.New("Group Ops completion sink is unavailable")
		}
		return r.group.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindChannelAsset:
		if r.channel == nil {
			return errors.New("channel asset completion sink is unavailable")
		}
		return r.channel.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindChannelWelcome, effectport.KindChannelEntryTag:
		if r.entrant == nil {
			return errors.New("channel entrant completion sink is unavailable")
		}
		return r.entrant.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindChannelLink:
		if r.link == nil {
			return errors.New("channel link completion sink is unavailable")
		}
		return r.link.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindOutboundMessage:
		if r.private == nil {
			return errors.New("private message completion sink is unavailable")
		}
		return r.private.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindSidebarJSSDKSend:
		if r.sidebar == nil {
			return errors.New("sidebar JSSDK completion sink is unavailable")
		}
		return r.sidebar.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	case effectport.KindSurveyCompletion:
		if r.survey == nil {
			return errors.New("survey completion sink is unavailable")
		}
		return r.survey.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	default:
		return errors.New("unsupported completion kind")
	}
}

var _ effectport.ProviderAdapter = (*DisabledGroupMessageProvider)(nil)
var _ effectport.ProviderAdapter = (*GroupMessageProvider)(nil)
var _ effectport.CompletionSink = (*GroupMessageCompletionSink)(nil)
var _ effectport.CompletionSink = (*CompletionRouter)(nil)
