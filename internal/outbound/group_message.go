package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
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
	executions        groupopsport.DispatchExecutionReader
	materials         groupopsport.MaterialReadinessVerifier
	writer            wecomport.GroupMessageSender
}

type GroupMessageProviderConfig struct {
	Enabled           bool
	PreparationWriter mediaport.GroupOpsMaterialPreparationWriter
	Executions        groupopsport.DispatchExecutionReader
	Materials         groupopsport.MaterialReadinessVerifier
	Writer            wecomport.GroupMessageSender
}

func NewGroupMessageProvider(config GroupMessageProviderConfig) (*GroupMessageProvider, error) {
	return &GroupMessageProvider{enabled: config.Enabled, preparationWriter: config.PreparationWriter, executions: config.Executions, materials: config.Materials, writer: config.Writer}, nil
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

func (p *GroupMessageProvider) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if p == nil || envelope.Kind != effectport.KindGroupMessage || !envelope.Valid() {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", "invalid-envelope")}, nil
	}
	if !p.enabled {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("group-ops-provider-disabled", string(envelope.Fingerprint())), CallAttempted: false, RealExternalCallExecuted: false}, nil
	}
	base := effectport.Hash("group-ops.provider.v1", string(envelope.Fingerprint()), attempt.EffectID, strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10))
	if p.executions == nil || p.materials == nil || p.writer == nil || attempt.EffectID == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "not-configured")}, nil
	}
	execution, err := p.executions.LoadDispatchExecution(ctx, attempt.EffectID)
	if err != nil || execution.ExternalEffectID != attempt.EffectID || execution.State != groupopsport.ExecutionAccepted || execution.DeliveryProven ||
		execution.SourceRefDigest != string(envelope.SourceRefDigest) || execution.TargetRefDigest != string(envelope.TargetRefDigest) || execution.PayloadDigest != string(envelope.PayloadDigest) || execution.PolicyVersionHash != string(envelope.PolicyVersionHash) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "dispatch-unavailable")}, nil
	}
	if err = p.materials.VerifyMaterialReady(ctx, execution.MaterialSnapshot, execution.MaterialSourceSnapshot, execution.MaterialSourceDigest, time.Now().UTC()); err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "material-not-ready"), CallAttempted: false, RealExternalCallExecuted: false}, nil
	}
	request, err := groupMessageRequest(execution)
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "snapshot-invalid")}, nil
	}
	receipt, attempted, err := p.writer.SendGroupMessage(ctx, request)
	if err != nil {
		state := effectport.StateRetryable
		if attempted {
			state = effectport.StateUnknown
			if rejected, ok := err.(wecomport.GroupMessageSendError); ok && !rejected.OutcomeUnknown() {
				state = effectport.StateFinalFailed
			}
		}
		return effectport.AdapterResult{Completion: state, ReceiptDigest: effectport.Hash(string(base), "provider-error"), CallAttempted: attempted, RealExternalCallExecuted: attempted}, nil
	}
	if !attempted || strings.TrimSpace(receipt.MessageID) == "" {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash(string(base), "provider-rejected")}, nil
	}
	artifactPayload, artifactErr := json.Marshal(struct {
		ExecutionID string `json:"execution_id"`
		EffectID    string `json:"effect_id"`
		MessageID   string `json:"msgid"`
		Sender      string `json:"sender"`
		ChatID      string `json:"chat_id"`
	}{strconv.FormatInt(execution.ExecutionID, 10), attempt.EffectID, receipt.MessageID, request.SenderUserID, request.ChatIDs[0]})
	if artifactErr != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: effectport.Hash(string(base), "receipt-artifact-unavailable"), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	artifact := effectport.ResultArtifact{Kind: "group-ops.wecom-task.v1", Payload: artifactPayload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash(string(base), "provider-accepted", receipt.MessageID), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

func canonicalGroupMessageJSON(raw []byte) ([]byte, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid Group Ops snapshot JSON")
	}
	return json.Marshal(value)
}

func emptyGroupMessageMaterial(raw []byte) bool {
	var value struct {
		SchemaVersion int               `json:"schema_version"`
		References    []json.RawMessage `json:"references"`
	}
	return json.Unmarshal(raw, &value) == nil && value.SchemaVersion == 1 && len(value.References) == 0
}

func groupMessageRequest(execution groupopsport.DispatchExecution) (wecomport.GroupMessageRequest, error) {
	if execution.ExecutionID < 1 || execution.TargetReference == "" || strings.TrimSpace(execution.SenderUserID) != execution.SenderUserID || execution.SenderUserID == "" || !effectport.ValidDigest(effectport.Digest(execution.ContentDigest)) || !effectport.ValidDigest(effectport.Digest(execution.MaterialDigest)) {
		return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops dispatch execution")
	}
	var content struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		MessageText   string `json:"message_text"`
	}
	if json.Unmarshal(execution.ContentSnapshot, &content) != nil || content.SchemaVersion != 1 || content.Kind != "message" || strings.TrimSpace(content.MessageText) != content.MessageText {
		return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops content snapshot")
	}
	canonicalContent, canonicalErr := canonicalGroupMessageJSON(execution.ContentSnapshot)
	if canonicalErr != nil || string(effectport.Hash("group-ops.content.snapshot.v1", string(canonicalContent))) != execution.ContentDigest {
		return wecomport.GroupMessageRequest{}, errors.New("Group Ops content digest mismatch")
	}
	canonicalMaterial, canonicalErr := canonicalGroupMessageJSON(execution.MaterialSnapshot)
	if canonicalErr != nil || string(effectport.Hash("group-ops.material.snapshot.v1", string(canonicalMaterial))) != execution.MaterialDigest {
		return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops material snapshot")
	}
	attachments := []wecomport.GroupMessageAttachment{}
	if !emptyGroupMessageMaterial(canonicalMaterial) {
		var materials mediaport.GroupOpsMaterialSnapshot
		if json.Unmarshal(canonicalMaterial, &materials) != nil || mediaport.ValidateGroupOpsMaterialSnapshot(materials) != nil {
			return wecomport.GroupMessageRequest{}, errors.New("invalid Group Ops material snapshot")
		}
		attachments = make([]wecomport.GroupMessageAttachment, len(materials.Attachments))
		for index, attachment := range materials.Attachments {
			attachments[index] = wecomport.GroupMessageAttachment{MsgType: attachment.MsgType, MediaID: attachment.MediaID, AppID: attachment.AppID, PagePath: attachment.PagePath, Title: attachment.Title, URL: attachment.URL, Description: attachment.Description, PicURL: attachment.PicURL}
		}
	}
	if content.MessageText == "" && len(attachments) == 0 {
		return wecomport.GroupMessageRequest{}, errors.New("Group Ops message is empty")
	}
	return wecomport.GroupMessageRequest{SenderUserID: execution.SenderUserID, ChatIDs: []string{execution.TargetReference}, Text: content.MessageText, Attachments: attachments}, nil
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
	projector     GroupMessageCompletionProjector
	receipts      groupopsport.GroupMessageReceiptWriter
	continuations groupopsport.ExecutionContinuationEnqueuer
}

func NewGroupMessageCompletionSink(projector GroupMessageCompletionProjector, receipts ...groupopsport.GroupMessageReceiptWriter) (*GroupMessageCompletionSink, error) {
	if projector == nil || len(receipts) > 1 {
		return nil, errors.New("Group Ops completion projector is required")
	}
	sink := &GroupMessageCompletionSink{projector: projector}
	if len(receipts) == 1 {
		sink.receipts = receipts[0]
	}
	return sink, nil
}

func (s *GroupMessageCompletionSink) WithContinuation(enqueuer groupopsport.ExecutionContinuationEnqueuer) *GroupMessageCompletionSink {
	if s != nil {
		s.continuations = enqueuer
	}
	return s
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
	if result.Completion == effectport.StateExecuted {
		if s.receipts == nil {
			return errors.New("Group Ops task receipt writer is required")
		}
		var artifact struct {
			ExecutionID string `json:"execution_id"`
			EffectID    string `json:"effect_id"`
			MessageID   string `json:"msgid"`
			Sender      string `json:"sender"`
			ChatID      string `json:"chat_id"`
		}
		if result.Artifact.Kind != "group-ops.wecom-task.v1" || !result.Artifact.Valid() || json.Unmarshal(result.Artifact.Payload, &artifact) != nil || artifact.EffectID != effectRef || artifact.MessageID == "" || artifact.Sender == "" || artifact.ChatID == "" {
			return errors.New("invalid Group Ops task receipt artifact")
		}
		executionID, parseErr := strconv.ParseInt(artifact.ExecutionID, 10, 64)
		if parseErr != nil || executionID < 1 {
			return errors.New("invalid Group Ops task receipt execution")
		}
		if err := s.receipts.RecordGroupMessageTask(ctx, groupopsport.GroupMessageReceipt{ExecutionID: executionID, ExternalEffectID: effectRef, MessageID: artifact.MessageID, SenderUserID: artifact.Sender, ChatID: artifact.ChatID, TaskEvidenceDigest: string(result.Artifact.Digest)}); err != nil {
			return err
		}
	}
	if err := s.projector.CompleteEffect(ctx, effectRef, state, providerAccepted, deliveryProven, string(result.ReceiptDigest), attempt.Number, time.Now().UTC()); err != nil {
		return err
	}
	if result.Completion == effectport.StateExecuted && s.continuations != nil {
		return s.continuations.EnqueueGroupOpsContinuationWithin(ctx, effectRef)
	}
	return nil
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
