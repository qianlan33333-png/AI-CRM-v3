package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// providerDisabledGroupOpsDirectory is a concrete id-dev source adapter. It
// makes the missing real directory provider explicit at composition time;
// local directory reads and opaque owner selection still use Group Ops' own
// projection, while refresh never fabricates a provider snapshot.
type providerDisabledGroupOpsDirectory struct{}

func (providerDisabledGroupOpsDirectory) ListOwnedGroups(context.Context, int64, int32) (groupopsport.GroupDirectorySnapshot, error) {
	return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
}

func (providerDisabledGroupOpsDirectory) RefreshOperationMembers(context.Context, int32) ([]groupopsport.OperationMember, error) {
	return nil, groupopsapp.ErrProviderDisabled
}

// providerDisabledGroupOpsEvidence is deliberately non-nil in Composition.
// It refuses to turn an HTTP digest into delivery evidence until an approved
// owner-side Provider receipt verifier is installed.
type providerDisabledGroupOpsEvidence struct{}

func (providerDisabledGroupOpsEvidence) VerifyReconciliationEvidence(context.Context, groupopsport.ReconciliationEvidence) (groupopsport.ReconciliationEvidenceResult, error) {
	return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
}

var _ groupopsport.GroupDirectorySource = providerDisabledGroupOpsDirectory{}
var _ groupopsport.ReconciliationEvidenceVerifier = providerDisabledGroupOpsEvidence{}

// wecomGroupOpsEvidence performs its provider pagination outside every UoW,
// then returns only a digest-bound observation. A msgid alone is task
// acceptance; delivery is true only for a matching sender/chat result status.
type wecomGroupOpsEvidence struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	receipts groupopsport.GroupMessageReceiptReader
	reader   wecomport.GroupMessageTaskReader
}

func (adapter wecomGroupOpsEvidence) VerifyReconciliationEvidence(ctx context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.ReconciliationEvidenceResult, error) {
	if adapter.uow == nil || adapter.receipts == nil || adapter.reader == nil {
		return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
	}
	var receipt groupopsport.GroupMessageReceipt
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		receipt, found, readErr = adapter.receipts.FindGroupMessageReceipt(tx, input)
		return readErr
	})
	if err != nil || !found {
		if err != nil {
			return groupopsport.ReconciliationEvidenceResult{}, err
		}
		return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
	}
	cursor := ""
	seen := map[string]struct{}{}
	for {
		page, readErr := adapter.reader.GetGroupMessageSendResult(ctx, receipt.MessageID, receipt.SenderUserID, cursor, 100)
		if readErr != nil {
			return groupopsport.ReconciliationEvidenceResult{}, readErr
		}
		for _, item := range page.Items {
			if item.SenderUserID != receipt.SenderUserID || item.ChatID != receipt.ChatID {
				continue
			}
			digest := string(effectport.Hash("group-ops.wecom-delivery.v1", receipt.MessageID, item.SenderUserID, item.ChatID, strconv.Itoa(item.Status)))
			if digest != input.EvidenceDigest {
				return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrConflict
			}
			return groupopsport.ReconciliationEvidenceResult{DeliveryProven: item.Status == 1, EvidenceDigest: digest}, nil
		}
		if page.NextCursor == "" {
			break
		}
		if _, duplicate := seen[page.NextCursor]; duplicate {
			return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrConflict
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return groupopsport.ReconciliationEvidenceResult{}, groupopsapp.ErrProviderDisabled
}

func (adapter wecomGroupOpsEvidence) ReadProviderDelivery(ctx context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.GroupMessageReceipt, bool, error) {
	if adapter.uow == nil || adapter.receipts == nil || adapter.reader == nil || input.ExecutionID < 1 || input.ExternalEffectID == "" {
		return groupopsport.GroupMessageReceipt{}, false, groupopsapp.ErrProviderDisabled
	}
	var receipt groupopsport.GroupMessageReceipt
	var found bool
	if err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var err error
		receipt, found, err = adapter.receipts.FindGroupMessageReceipt(tx, input)
		return err
	}); err != nil || !found {
		return groupopsport.GroupMessageReceipt{}, false, err
	}
	cursor, seen := "", map[string]struct{}{}
	for {
		page, err := adapter.reader.GetGroupMessageSendResult(ctx, receipt.MessageID, receipt.SenderUserID, cursor, 100)
		if err != nil {
			return groupopsport.GroupMessageReceipt{}, false, err
		}
		for _, item := range page.Items {
			if item.SenderUserID == receipt.SenderUserID && item.ChatID == receipt.ChatID {
				status := item.Status
				receipt.DeliveryStatus = &status
				receipt.DeliveryEvidenceDigest = string(effectport.Hash("group-ops.wecom-delivery.v1", receipt.MessageID, item.SenderUserID, item.ChatID, strconv.Itoa(item.Status)))
				return receipt, true, nil
			}
		}
		if page.NextCursor == "" {
			return groupopsport.GroupMessageReceipt{}, false, nil
		}
		if _, duplicate := seen[page.NextCursor]; duplicate {
			return groupopsport.GroupMessageReceipt{}, false, groupopsapp.ErrConflict
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}

var _ groupopsport.ReconciliationEvidenceVerifier = wecomGroupOpsEvidence{}
var _ groupopsport.ProviderDeliveryReader = wecomGroupOpsEvidence{}

// wecomGroupOpsDirectory performs every provider read before RuntimeService
// opens its persistence transaction. A full snapshot is required before a
// replacement is allowed, so failed pagination cannot erase the prior group
// projection.
type wecomGroupOpsDirectory struct {
	enabled bool
	groups  wecomport.GroupChatReader
	staffs  wecomport.DirectoryProvider
	staff   groupOpsStaffAdapter
	now     func() time.Time
}

func (adapter *wecomGroupOpsDirectory) ListOwnedGroups(ctx context.Context, ownerID int64, _ int32) (groupopsport.GroupDirectorySnapshot, error) {
	if adapter == nil || !adapter.enabled || adapter.groups == nil || adapter.staff.access == nil || ownerID < 1 {
		return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
	}
	owner, err := adapter.staff.access.UserByID(ctx, ownerID, false)
	if err != nil || !owner.Active || !validGroupOpsSenderID(owner.WeComUserID) {
		return groupopsport.GroupDirectorySnapshot{}, groupopsapp.ErrProviderDisabled
	}
	cursor := ""
	seenCursor := map[string]struct{}{}
	seenChat := map[string]struct{}{}
	items := make([]groupopsport.GroupDirectoryItem, 0)
	now := time.Now().UTC()
	if adapter.now != nil {
		now = adapter.now().UTC()
	}
	for {
		page, pageErr := adapter.groups.ListGroupChats(ctx, owner.WeComUserID, cursor, 100)
		if pageErr != nil {
			return groupopsport.GroupDirectorySnapshot{}, pageErr
		}
		for _, summary := range page.Items {
			if summary.Status != 0 {
				continue
			}
			if _, duplicate := seenChat[summary.ChatID]; duplicate {
				return groupopsport.GroupDirectorySnapshot{}, errors.New("WeCom group directory returned duplicate chat")
			}
			seenChat[summary.ChatID] = struct{}{}
			detail, detailErr := adapter.groups.GetGroupChat(ctx, summary.ChatID)
			if detailErr != nil || detail.ChatID != summary.ChatID || detail.OwnerUserID != owner.WeComUserID {
				return groupopsport.GroupDirectorySnapshot{}, errors.New("WeCom group directory detail is incomplete")
			}
			items = append(items, groupopsport.GroupDirectoryItem{ChatReference: detail.ChatID, OwnerStaffID: ownerID, DisplayName: detail.Name, MemberCount: int32(detail.MemberCount), RefreshedAt: now})
		}
		if page.NextCursor == "" {
			break
		}
		if _, repeated := seenCursor[page.NextCursor]; repeated {
			return groupopsport.GroupDirectorySnapshot{}, errors.New("WeCom group directory cursor repeated")
		}
		seenCursor[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ChatReference < items[j].ChatReference })
	return groupopsport.GroupDirectorySnapshot{Items: items, Complete: true}, nil
}

func (adapter *wecomGroupOpsDirectory) RefreshOperationMembers(ctx context.Context, pageSize int32) ([]groupopsport.OperationMember, error) {
	if adapter == nil || !adapter.enabled || adapter.staffs == nil || adapter.staff.access == nil || !adapter.staffs.DirectoryReady() {
		return nil, groupopsapp.ErrProviderDisabled
	}
	providerIDs, err := adapter.staffs.ListContactStaff(ctx)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(providerIDs))
	for _, id := range providerIDs {
		if validGroupOpsSenderID(id) {
			allowed[id] = struct{}{}
		}
	}
	local, err := adapter.staff.ListEligibleStaff(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]groupopsport.OperationMember, 0, len(local))
	for _, item := range local {
		if _, found := allowed[item.SenderUserID]; found {
			items = append(items, item)
		}
	}
	if len(items) > int(pageSize) {
		return nil, errors.New("WeCom operation-member snapshot exceeds requested page")
	}
	return items, nil
}

var _ groupopsport.GroupDirectorySource = (*wecomGroupOpsDirectory)(nil)

// groupOpsDispatchReader rechecks the current target owner/sender under the
// Group Ops UoW immediately before outbound crosses the provider boundary.
// A paused plan, changed group binding, or ineligible sender is therefore a
// deterministic pre-dispatch failure, not a send attempt.
type groupOpsDispatchReader struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	execution groupopsport.DispatchExecutionReader
	senders   groupopsport.ExecutionSenderResolver
}

func (adapter groupOpsDispatchReader) LoadDispatchExecution(ctx context.Context, effectID string) (groupopsport.DispatchExecution, error) {
	if adapter.uow == nil || adapter.execution == nil || adapter.senders == nil || effectID == "" {
		return groupopsport.DispatchExecution{}, errors.New("Group Ops dispatch reader is unavailable")
	}
	var execution groupopsport.DispatchExecution
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		value, loadErr := adapter.execution.LoadDispatchExecution(tx, effectID)
		if loadErr != nil {
			return loadErr
		}
		sender, found, senderErr := adapter.senders.ResolveExecutionSender(tx, value.TargetReference)
		if senderErr != nil || !found || sender != value.SenderUserID {
			return errors.New("Group Ops sender eligibility changed")
		}
		execution = value
		return nil
	})
	return execution, err
}

var _ groupopsport.DispatchExecutionReader = groupOpsDispatchReader{}

// groupOpsExternalReconciler is the only Composition Root bridge from the
// Group Ops domain to EER control. It keeps the EER operation and the Group Ops
// execution projection inside the caller's transaction; Group Ops never
// imports EER store/app/http/worker packages.
type groupOpsExternalReconciler struct {
	repository *externaleffects.Repository
}

func (adapter groupOpsExternalReconciler) ReconcileExternalEffect(ctx context.Context, command groupopsport.ExternalReconcileCommand) error {
	if adapter.repository == nil || command.ActorID < 1 || command.EffectID == "" || command.Generation < 1 || command.Fence < 1 || command.LeaseExpiresAt.IsZero() || !externaleffects.ValidDigest(externaleffects.Digest(command.EvidenceDigest)) {
		return errors.New("Group Ops external reconciliation is unavailable")
	}
	// The HTTP idempotency key is intentionally not used as an EER digest. The
	// adapter derives a stable opaque receipt key so raw protocol/user keys do
	// not cross the EER boundary or enter structured logs.
	receiptKey := externaleffects.Hash("group-ops.execution-reconcile", command.EffectID, command.ReceiptKey, command.EvidenceDigest)
	return adapter.repository.ReconcileWithin(ctx, externaleffects.ControlCommand{
		EffectID:         command.EffectID,
		ReceiptKey:       receiptKey,
		EvidenceDigest:   externaleffects.Digest(command.EvidenceDigest),
		ActorAdminUserID: command.ActorID,
		Generation:       command.Generation,
		Fence:            command.Fence,
		LeaseExpiresAt:   command.LeaseExpiresAt,
	})
}

var _ groupopsport.ExternalReconciler = groupOpsExternalReconciler{}

// groupOpsMaterialAdapter is a narrow Composition Root adapter. Media owns
// both ports and therefore owns source locking/preparation; Group Ops receives
// only the frozen JSON snapshot and its digest. In particular, this adapter
// does not derive a digest from a kind/id pair or reopen a mutable package.
type groupOpsMaterialAdapter struct {
	capturer mediaport.GroupOpsMaterialSourceCapturer
	freezer  mediaport.GroupOpsMaterialSnapshotFreezer
}

// groupOpsMaterialReadinessAdapter repeats Media's capture/read boundary
// immediately before a write. It never prepares or uploads material: changed
// source content, a changed receipt digest, or an expired ReadyUntil fails
// before the outbound adapter crosses the Provider boundary.
type groupOpsMaterialReadinessAdapter struct {
	uow interface {
		Within(context.Context, func(context.Context) error) error
	}
	capturer mediaport.GroupOpsMaterialSourceCapturer
	freezer  mediaport.GroupOpsMaterialSnapshotFreezer
}

func (adapter groupOpsMaterialReadinessAdapter) VerifyMaterialReady(ctx context.Context, snapshotRaw, factsRaw json.RawMessage, factsDigest string, now time.Time) error {
	canonicalSnapshot, snapshotErr := canonicalGroupOpsJSON(snapshotRaw)
	canonicalFacts, factsCanonicalErr := canonicalGroupOpsJSON(factsRaw)
	if adapter.uow == nil || adapter.capturer == nil || adapter.freezer == nil || snapshotErr != nil || factsCanonicalErr != nil || !effectport.ValidDigest(effectport.Digest(factsDigest)) || factsDigest != string(effectport.Hash("group-ops.material.intent.v1", string(canonicalFacts))) || now.IsZero() {
		return errors.New("Group Ops material readiness unavailable")
	}
	// A text-only message has no Media-owned source to recapture or provider
	// preparation to re-read. RuntimeService persists this canonical empty
	// intent itself, so accepting it here preserves the same frozen-facts
	// boundary without inventing a Media record merely to send text.
	if emptyGroupOpsMaterialIntent(canonicalSnapshot, canonicalFacts) {
		return nil
	}
	var facts struct {
		SchemaVersion int                                      `json:"schema_version"`
		Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
		Preparations  []groupopsmaterial.PreparedMaterial      `json:"preparations"`
	}
	if json.Unmarshal(canonicalFacts, &facts) != nil || facts.SchemaVersion != 1 || mediaport.ValidateGroupOpsMaterialSourceSnapshot(facts.Sources) != nil {
		return errors.New("invalid frozen Group Ops material facts")
	}
	plan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(facts.Sources.References))}
	for i, source := range facts.Sources.References {
		plan.References[i] = source.Reference
	}
	return adapter.uow.Within(ctx, func(tx context.Context) error {
		current, err := adapter.capturer.CaptureGroupOpsMaterialSources(tx, plan)
		if err != nil {
			return err
		}
		currentRaw, err := json.Marshal(current)
		frozenRaw, frozenErr := json.Marshal(facts.Sources)
		if err != nil || frozenErr != nil || string(currentRaw) != string(frozenRaw) {
			return errors.New("Group Ops material source changed")
		}
		withFacts, ok := adapter.freezer.(interface {
			FreezeGroupOpsMaterialWithFacts(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) (mediaport.GroupOpsMaterialSnapshot, []groupopsmaterial.PreparedMaterial, error)
		})
		if !ok {
			return errors.New("Group Ops material preparation facts unavailable")
		}
		snapshot, prepared, err := withFacts.FreezeGroupOpsMaterialWithFacts(tx, current, now.UTC())
		if err != nil {
			return err
		}
		actualSnapshot, marshalErr := json.Marshal(snapshot)
		actualFacts, factsErr := json.Marshal(struct {
			SchemaVersion int                                      `json:"schema_version"`
			Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
			Preparations  []groupopsmaterial.PreparedMaterial      `json:"preparations"`
		}{1, current, prepared})
		actualSnapshot, marshalErr = canonicalGroupOpsJSON(actualSnapshot)
		actualFacts, factsErr = canonicalGroupOpsJSON(actualFacts)
		if marshalErr != nil || factsErr != nil || string(actualSnapshot) != string(canonicalSnapshot) || string(actualFacts) != string(canonicalFacts) {
			return errors.New("Group Ops material preparation changed or expired")
		}
		return nil
	})
}

func emptyGroupOpsMaterialIntent(snapshotRaw, factsRaw []byte) bool {
	var snapshot struct {
		SchemaVersion int               `json:"schema_version"`
		References    []json.RawMessage `json:"references"`
	}
	var facts struct {
		SchemaVersion int `json:"schema_version"`
		Sources       struct {
			SchemaVersion int               `json:"schema_version"`
			References    []json.RawMessage `json:"references"`
		} `json:"sources"`
		Preparations []json.RawMessage `json:"preparations"`
	}
	return json.Unmarshal(snapshotRaw, &snapshot) == nil && snapshot.SchemaVersion == 1 && snapshot.References != nil && len(snapshot.References) == 0 &&
		json.Unmarshal(factsRaw, &facts) == nil && facts.SchemaVersion == 1 && facts.Sources.SchemaVersion == 1 && facts.Sources.References != nil && len(facts.Sources.References) == 0 && facts.Preparations != nil && len(facts.Preparations) == 0
}

func canonicalGroupOpsJSON(raw []byte) ([]byte, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid Group Ops material JSON")
	}
	return json.Marshal(value)
}

var _ groupopsport.MaterialReadinessVerifier = groupOpsMaterialReadinessAdapter{}

func (adapter groupOpsMaterialAdapter) ResolveMaterialSnapshot(ctx context.Context, plan groupopsport.MaterialPlan, requiredThrough time.Time) (json.RawMessage, string, error) {
	raw, digest, _, _, err := adapter.ResolveMaterialIntentSnapshot(ctx, plan, requiredThrough)
	if err != nil {
		// Retain the narrow legacy resolver contract for consumers that do not
		// persist a Group Ops intent. Runtime dispatch uses the richer method
		// above and fails closed when receipt facts are unavailable.
		mediaPlan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(plan.References))}
		for index, reference := range plan.References {
			mediaPlan.References[index] = mediaport.GroupOpsMaterialReference{Kind: reference.Kind, ID: reference.ID}
		}
		if adapter.capturer == nil || adapter.freezer == nil || mediaport.ValidateGroupOpsMaterialPlan(mediaPlan) != nil {
			return nil, "", err
		}
		sources, captureErr := adapter.capturer.CaptureGroupOpsMaterialSources(ctx, mediaPlan)
		if captureErr != nil {
			return nil, "", captureErr
		}
		snapshot, freezeErr := adapter.freezer.FreezeGroupOpsMaterial(ctx, sources, requiredThrough)
		if freezeErr != nil || mediaport.ValidateGroupOpsMaterialSnapshot(snapshot) != nil {
			return nil, "", err
		}
		raw, marshalErr := json.Marshal(snapshot)
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		return raw, string(effectport.Hash("group-ops.material.snapshot.v1", string(raw))), nil
	}
	return raw, digest, err
}

func (adapter groupOpsMaterialAdapter) ResolveMaterialIntentSnapshot(ctx context.Context, plan groupopsport.MaterialPlan, requiredThrough time.Time) (json.RawMessage, string, json.RawMessage, string, error) {
	if adapter.capturer == nil || adapter.freezer == nil || ctx == nil || requiredThrough.IsZero() {
		return nil, "", nil, "", errors.New("Group Ops material ports are unavailable")
	}
	mediaPlan := mediaport.GroupOpsMaterialPlan{References: make([]mediaport.GroupOpsMaterialReference, len(plan.References))}
	for index, reference := range plan.References {
		mediaPlan.References[index] = mediaport.GroupOpsMaterialReference{Kind: reference.Kind, ID: reference.ID}
	}
	if err := mediaport.ValidateGroupOpsMaterialPlan(mediaPlan); err != nil {
		return nil, "", nil, "", err
	}
	sources, err := adapter.capturer.CaptureGroupOpsMaterialSources(ctx, mediaPlan)
	if err != nil {
		return nil, "", nil, "", err
	}
	withFacts, ok := adapter.freezer.(interface {
		FreezeGroupOpsMaterialWithFacts(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) (mediaport.GroupOpsMaterialSnapshot, []groupopsmaterial.PreparedMaterial, error)
	})
	if !ok {
		return nil, "", nil, "", errors.New("Group Ops material preparation facts are unavailable")
	}
	snapshot, prepared, err := withFacts.FreezeGroupOpsMaterialWithFacts(ctx, sources, requiredThrough)
	if err != nil {
		return nil, "", nil, "", err
	}
	if err = mediaport.ValidateGroupOpsMaterialSnapshot(snapshot); err != nil {
		return nil, "", nil, "", err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", nil, "", err
	}
	raw, err = canonicalGroupOpsJSON(raw)
	if err != nil {
		return nil, "", nil, "", err
	}
	facts := struct {
		SchemaVersion int                                      `json:"schema_version"`
		Sources       mediaport.GroupOpsMaterialSourceSnapshot `json:"sources"`
		Preparations  []groupopsmaterial.PreparedMaterial      `json:"preparations"`
	}{SchemaVersion: 1, Sources: sources, Preparations: prepared}
	factsRaw, err := json.Marshal(facts)
	if err != nil {
		return nil, "", nil, "", err
	}
	factsRaw, err = canonicalGroupOpsJSON(factsRaw)
	if err != nil {
		return nil, "", nil, "", err
	}
	return raw, string(effectport.Hash("group-ops.material.snapshot.v1", string(raw))), factsRaw, string(effectport.Hash("group-ops.material.intent.v1", string(factsRaw))), nil
}

var _ groupopsport.MaterialSnapshotResolver = groupOpsMaterialAdapter{}
var _ groupopsport.MaterialIntentSnapshotResolver = groupOpsMaterialAdapter{}

func newGroupOpsMaterialAdapter(capturer mediaport.GroupOpsMaterialSourceCapturer, freezer mediaport.GroupOpsMaterialSnapshotFreezer) (groupopsport.MaterialSnapshotResolver, error) {
	if capturer == nil || freezer == nil {
		return nil, errors.New("Media Group Ops material ports are unavailable")
	}
	return groupOpsMaterialAdapter{capturer: capturer, freezer: freezer}, nil
}

// mediaPreparedPlanReader is the Composition Root adapter from Media's
// transaction-bound preparation port to the freezer's provider-neutral typed
// reader. Group invite links are already provider-ready facts from the real
// Media capture and therefore do not need a receipt/lease row. Image,
// attachment, and miniprogram items must come from Media's persisted
// preparation receipt with an unexpired lease; this adapter never derives a
// media ID or digest from kind/id.
type mediaPreparedPlanReader struct {
	reader mediaport.GroupOpsMaterialPreparationReader
}

func (adapter mediaPreparedPlanReader) ReadPreparedGroupOpsPlan(ctx context.Context, sources mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time) (groupopsmaterial.PreparedPlan, error) {
	if ctx == nil || requiredThrough.IsZero() || mediaport.ValidateGroupOpsMaterialSourceSnapshot(sources) != nil {
		return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
	}
	requiresReceipt := false
	for _, source := range sources.References {
		if source.Reference.Kind != "group_invite" {
			requiresReceipt = true
			break
		}
	}
	var items []mediaport.GroupOpsMaterialPreparation
	if requiresReceipt {
		if adapter.reader == nil {
			return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
		}
		var err error
		items, err = adapter.reader.ReadPreparedGroupOpsMaterials(ctx, sources, requiredThrough)
		if err != nil {
			return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
		}
	} else {
		items = make([]mediaport.GroupOpsMaterialPreparation, 0, len(sources.References))
		for _, source := range sources.References {
			items = append(items, mediaport.GroupOpsMaterialPreparation{Reference: source.Reference, SourceDigest: source.SourceDigest, Attachment: source.ProviderFields})
		}
	}
	if mediaport.ValidateGroupOpsMaterialPreparations(sources, items, requiredThrough) != nil {
		return groupopsmaterial.PreparedPlan{}, groupopsmaterial.ErrUnavailable
	}
	prepared := make([]groupopsmaterial.PreparedMaterial, len(items))
	for index, item := range items {
		prepared[index] = groupopsmaterial.PreparedMaterial{Reference: item.Reference, SourceDigest: item.SourceDigest, ReceiptDigest: item.ReceiptDigest, ReadyUntil: item.ReadyUntil, Attachment: item.Attachment}
	}
	return groupopsmaterial.PreparedPlan{Items: prepared}, nil
}

var _ groupopsmaterial.PreparedPlanReader = mediaPreparedPlanReader{}

// groupOpsStaffAdapter is the Composition Root's only bridge from Group Ops
// to staff records. Group Ops stores only an opaque owner_staff_id and never
// reaches across to admin_users; Access remains the owner of staff identity,
// active state, and the verified WeCom sender identifier.
type groupOpsStaffAdapter struct {
	access accessport.Repository
	owners groupopsport.ExecutionTargetOwnerResolver
}

func (adapter groupOpsStaffAdapter) IsActiveStaff(ctx context.Context, staffID int64) (bool, error) {
	if adapter.access == nil || staffID < 1 {
		return false, errors.New("Group Ops staff access is unavailable")
	}
	user, err := adapter.access.UserByID(ctx, staffID, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user.Active, nil
}

func (adapter groupOpsStaffAdapter) ListEligibleStaff(ctx context.Context) ([]groupopsport.OperationMember, error) {
	if adapter.access == nil {
		return nil, errors.New("Group Ops staff access is unavailable")
	}
	users, err := adapter.access.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]groupopsport.OperationMember, 0, len(users))
	for _, user := range users {
		if !user.Active || user.ID < 1 || user.WeComUserID == "" {
			continue
		}
		if !validGroupOpsSenderID(user.WeComUserID) {
			// A malformed legacy binding is never exposed as a selectable
			// sender. It remains an Access-owned repair item instead.
			continue
		}
		items = append(items, groupopsport.OperationMember{StaffID: user.ID, SenderUserID: user.WeComUserID, DisplayName: user.DisplayName})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].StaffID < items[j].StaffID })
	return items, nil
}

func (adapter groupOpsStaffAdapter) ResolveExecutionSender(ctx context.Context, target string) (string, bool, error) {
	if adapter.access == nil || adapter.owners == nil || target == "" {
		return "", false, errors.New("Group Ops sender access is unavailable")
	}
	owner, found, err := adapter.owners.ResolveExecutionOwner(ctx, target)
	if err != nil || !found || owner < 1 {
		return "", false, err
	}
	user, err := adapter.access.UserByID(ctx, owner, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !user.Active || user.WeComUserID == "" {
		return "", false, nil
	}
	if !validGroupOpsSenderID(user.WeComUserID) {
		return "", false, nil
	}
	return user.WeComUserID, true, nil
}

func validGroupOpsSenderID(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r) {
			continue
		}
		return false
	}
	return true
}

var _ groupopsport.ActiveStaffReader = groupOpsStaffAdapter{}
var _ groupopsport.EligibleStaffReader = groupOpsStaffAdapter{}
var _ groupopsport.ExecutionSenderResolver = groupOpsStaffAdapter{}
