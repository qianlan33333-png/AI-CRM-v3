package outbound

import (
	"context"
	"errors"
	"time"

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
	tag     *TagCatalogCompletionSink
	group   *GroupMessageCompletionSink
	channel *ChannelAssetCompletionSink
	entrant *ChannelEntrantCompletionSink
	link    *ChannelLinkCompletionSink
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

func NewCompletionRouter(tag *TagCatalogCompletionSink, group *GroupMessageCompletionSink) (*CompletionRouter, error) {
	if tag == nil && group == nil {
		return nil, errors.New("at least one completion sink is required")
	}
	return &CompletionRouter{tag: tag, group: group}, nil
}

func (r *CompletionRouter) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if r == nil {
		return errors.New("completion router is unavailable")
	}
	switch envelope.Kind {
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
	default:
		return errors.New("unsupported completion kind")
	}
}

var _ effectport.ProviderAdapter = (*DisabledGroupMessageProvider)(nil)
var _ effectport.ProviderAdapter = (*GroupMessageProvider)(nil)
var _ effectport.CompletionSink = (*GroupMessageCompletionSink)(nil)
var _ effectport.CompletionSink = (*CompletionRouter)(nil)
