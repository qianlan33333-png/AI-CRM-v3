package app

// This file contains the Group Ops runtime/application seam.  It accepts
// immutable local plan snapshots and hands opaque group-message intents to the
// External Effects port.  It deliberately does not resolve customers,
// audiences, OneID identities, or Provider credentials.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrProviderDisabled = errors.New("group ops provider is disabled")
	ErrRuntimeInvalid   = errors.New("invalid Group Ops runtime command")
)

// RuntimeService coordinates local run/execution facts with the stable EER
// transaction accepter.  A nil directory source is intentional: reads and
// refreshes fail closed instead of inventing a group or sender.
type RuntimeService struct {
	uow        platformport.UnitOfWork
	plans      Store
	runtime    groupopsport.RuntimeStore
	effects    effectport.TransactionalAccepter
	staff      groupopsport.EligibleStaffReader
	directory  groupopsport.GroupDirectorySource
	senders    groupopsport.ExecutionSenderResolver
	materials  groupopsport.MaterialSnapshotResolver
	evidence   groupopsport.ReconciliationEvidenceVerifier
	reconciler groupopsport.ExternalReconciler
	now        func() time.Time
	// dispatchEnabled means the local runtime may accept an EER intent. It is
	// never used as evidence that a Provider call or delivery occurred.
	dispatchEnabled bool
}

func NewRuntimeService(uow platformport.UnitOfWork, plans Store, runtime groupopsport.RuntimeStore, effects effectport.TransactionalAccepter, staff groupopsport.EligibleStaffReader, directory groupopsport.GroupDirectorySource, senders groupopsport.ExecutionSenderResolver, evidence groupopsport.ReconciliationEvidenceVerifier, reconciler groupopsport.ExternalReconciler, materials ...groupopsport.MaterialSnapshotResolver) *RuntimeService {
	var materialResolver groupopsport.MaterialSnapshotResolver
	if len(materials) > 0 {
		materialResolver = materials[0]
	}
	return &RuntimeService{uow: uow, plans: plans, runtime: runtime, effects: effects, staff: staff, directory: directory, senders: senders, materials: materialResolver, evidence: evidence, reconciler: reconciler, now: time.Now}
}

func (s *RuntimeService) SetDispatchEnabled(enabled bool) {
	if s != nil {
		s.dispatchEnabled = enabled
	}
}

func (s *RuntimeService) safety() groupopsport.RuntimeSafety {
	if s != nil && s.dispatchEnabled {
		return groupopsport.DispatchEnabledRuntimeSafety()
	}
	return groupopsport.DisabledRuntimeSafety()
}

func (s *RuntimeService) ready() bool {
	return s != nil && s.uow != nil && s.plans != nil && s.runtime != nil && s.effects != nil && s.senders != nil
}

func (s *RuntimeService) nowUTC() time.Time {
	if s == nil || s.now == nil {
		return time.Time{}
	}
	return s.now().UTC()
}

func (s *RuntimeService) PreviewRunDue(ctx context.Context, planID int64) (groupopsport.RunDuePreview, error) {
	if s == nil || s.uow == nil || s.plans == nil || s.runtime == nil || planID < 1 {
		return groupopsport.RunDuePreview{}, ErrRuntimeInvalid
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.RunDuePreview{}, ErrUnavailable
	}
	var result groupopsport.RunDuePreview
	err := s.uow.Within(ctx, func(tx context.Context) error {
		detail, err := s.plans.Get(tx, planID)
		if err != nil {
			return err
		}
		result = groupopsport.RunDuePreview{PlanID: planID, PlanStatus: detail.Plan.Status, SnapshotRevision: detail.Plan.Revision, EvaluatedAt: now, Blockers: []string{}, RuntimeSafety: s.safety()}
		validation := contentValidation(detail)
		if detail.Plan.Status != groupopsport.PlanActive {
			result.Blockers = append(result.Blockers, "plan_not_active")
		}
		result.Blockers = append(result.Blockers, validation.IssueCodes...)
		result.Blockers = append(result.Blockers, s.materialBlockers(tx, detail, now)...)
		if len(result.Blockers) == 0 {
			keys, keyErr := s.runtime.ListExecutionKeys(tx, planID, detail.Plan.Revision)
			if keyErr != nil {
				return keyErr
			}
			existing := make(map[string]struct{}, len(keys))
			for _, key := range keys {
				existing[executionKeyString(key.NodeID, key.TargetReference)] = struct{}{}
			}
			result.DueExecutionCount = int32(countMessageDrafts(detail, existing))
			result.NextDueAt = nextMessageDue(detail, now)
		}
		return nil
	})
	if err != nil {
		return groupopsport.RunDuePreview{}, classify(err)
	}
	return result, nil
}

func (s *RuntimeService) RunDue(ctx context.Context, command groupopsport.RunDueCommand) (groupopsport.RunSummary, error) {
	if command.ActorID < 1 {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}
	return s.AcceptPlan(ctx, groupopsport.AcceptPlanCommand{PlanID: command.PlanID, Trigger: groupopsport.RunTriggerDue, AcceptedBy: "admin:" + strconv.FormatInt(command.ActorID, 10), IdempotencyKey: command.IdempotencyKey})
}

func (s *RuntimeService) AcceptBroadcast(ctx context.Context, planID, actorID int64, key string) (groupopsport.RunSummary, error) {
	if actorID < 1 {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}
	return s.AcceptPlan(ctx, groupopsport.AcceptPlanCommand{PlanID: planID, Trigger: groupopsport.RunTriggerBroadcast, AcceptedBy: "admin:" + strconv.FormatInt(actorID, 10), IdempotencyKey: key})
}

func (s *RuntimeService) AcceptPlan(ctx context.Context, command groupopsport.AcceptPlanCommand) (groupopsport.RunSummary, error) {
	if !s.ready() || command.PlanID < 1 || !validRuntimeKey(command.IdempotencyKey) || command.AcceptedBy == "" || len(command.AcceptedBy) > 140 {
		return groupopsport.RunSummary{}, invalidOrUnavailableRuntime(s)
	}
	if !s.dispatchEnabled {
		return groupopsport.RunSummary{}, ErrProviderDisabled
	}
	if command.Trigger != groupopsport.RunTriggerDue && command.Trigger != groupopsport.RunTriggerBroadcast && command.Trigger != groupopsport.RunTriggerWebhook {
		return groupopsport.RunSummary{}, ErrRuntimeInvalid
	}
	now := s.nowUTC()
	if now.IsZero() {
		return groupopsport.RunSummary{}, ErrUnavailable
	}
	var summary groupopsport.RunSummary
	err := s.uow.Within(ctx, func(tx context.Context) error {
		detail, err := s.plans.Lock(tx, command.PlanID)
		if err != nil {
			return err
		}
		if groupopsdomain.ValidateDetail(detail) != nil {
			return ErrUnavailable
		}
		validation := contentValidation(detail)
		if detail.Plan.Status != groupopsport.PlanActive || !validation.Valid {
			return ErrStateConflict
		}
		sourceKey := sha256.Sum256([]byte(strings.Join([]string{"group-ops.run.v1", strconv.FormatInt(command.PlanID, 10), strconv.FormatInt(detail.Plan.Revision, 10), string(command.Trigger), command.IdempotencyKey}, "\x00")))
		run, err := s.runtime.ReserveRun(tx, groupopsport.RunReservation{PlanID: command.PlanID, Trigger: command.Trigger, SourceKeyDigest: sourceKey, PlanRevision: detail.Plan.Revision, ScheduledFor: now, AcceptedAt: now, AcceptedBy: command.AcceptedBy})
		if err != nil {
			return err
		}
		keys, err := s.runtime.ListExecutionKeys(tx, command.PlanID, detail.Plan.Revision)
		if err != nil {
			return err
		}
		existing := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			existing[executionKeyString(key.NodeID, key.TargetReference)] = struct{}{}
		}
		drafts, err := s.buildDrafts(tx, detail, run, existing, now)
		if err != nil {
			return err
		}
		if _, err = s.runtime.CreateExecutionIntents(tx, drafts); err != nil {
			return err
		}
		initial, intentErr := s.runtime.InitialExecutionIntents(tx, run.ID)
		if intentErr != nil {
			return intentErr
		}
		for _, draft := range initial {
			projection, receipt, acceptErr := s.effects.AcceptAndQueueWithin(tx, groupOpsEffectAcceptCommand(draft, command.IdempotencyKey))
			if acceptErr != nil {
				return acceptErr
			}
			if projection.ID == "" || projection.QueueJobID < 1 || receipt.ID == "" || receipt.QueueReceiptID == "" {
				return ErrUnavailable
			}
			draft.ExternalEffectID = projection.ID
			if _, err = s.runtime.InsertExecution(tx, draft); err != nil {
				return err
			}
			if err = s.runtime.BindAcceptedExecutionIntent(tx, draft.IntentID, projection.ID); err != nil {
				return err
			}
		}
		summary, err = s.runtime.ReadRunSummary(tx, run.ID)
		if err != nil {
			return err
		}
		summary.RuntimeSafety = s.safety()
		return nil
	})
	if err != nil {
		return groupopsport.RunSummary{}, classify(err)
	}
	return summary, nil
}

func (s *RuntimeService) AcceptWebhook(ctx context.Context, webhookReference, key string) (groupopsport.RunSummary, error) {
	if !s.ready() || !validRuntimeKey(key) || !validOpaqueReference(webhookReference) {
		return groupopsport.RunSummary{}, invalidOrUnavailableRuntime(s)
	}
	var command groupopsport.AcceptPlanCommand
	err := s.uow.Within(ctx, func(tx context.Context) error {
		planID, err := s.runtime.FindPlanByWebhookReference(tx, webhookReference)
		if err != nil {
			return err
		}
		command = groupopsport.AcceptPlanCommand{PlanID: planID, Trigger: groupopsport.RunTriggerWebhook, AcceptedBy: "webhook:" + webhookReference, IdempotencyKey: key}
		return nil
	})
	if err != nil {
		return groupopsport.RunSummary{}, classify(err)
	}
	// The lookup transaction is read-only. AcceptPlan obtains the plan lock and
	// creates the run/EER intents atomically in its own Unit of Work.
	return s.AcceptPlan(ctx, command)
}

func groupOpsEffectAcceptCommand(draft groupopsport.ExecutionDraft, key string) effectport.AcceptCommand {
	return effectport.AcceptCommand{ReceiptKey: effectport.Hash("group-ops.accept.v1", strconv.FormatInt(draft.PlanID, 10), strconv.FormatInt(draft.RunID, 10), strconv.FormatInt(draft.NodeID, 10), draft.TargetReference, key), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindGroupMessage, SourceRefDigest: effectport.Hash("group-ops.run", strconv.FormatInt(draft.RunID, 10)), TargetRefDigest: effectport.Hash("group-ops.target", draft.TargetReference), PayloadDigest: effectport.Hash("group-ops.payload", draft.ContentDigest, draft.MaterialDigest, draft.SenderUserID), PolicyVersionHash: effectport.Hash("group-ops.policy", "v1")}, ScheduledAt: draft.ScheduledFor}
}

func (s *RuntimeService) buildDrafts(tx context.Context, detail groupopsport.Detail, run groupopsport.Run, existing map[string]struct{}, now time.Time) ([]groupopsport.ExecutionDraft, error) {
	assets := append([]groupopsport.GroupAsset{}, detail.GroupAssets...)
	sort.SliceStable(assets, func(i, j int) bool { return assets[i].AssetRef < assets[j].AssetRef })
	drafts := make([]groupopsport.ExecutionDraft, 0)
	delay := time.Duration(0)
	for _, node := range detail.Nodes {
		if node.Kind == groupopsport.NodeDelay {
			delay += time.Duration(node.DelayMinutes) * time.Minute
			continue
		}
		if node.MaterialRef != "" {
			return nil, ErrStateConflict
		}
		contentRaw, err := json.Marshal(struct {
			SchemaVersion int32  `json:"schema_version"`
			NodeID        int64  `json:"node_id"`
			Position      int32  `json:"position"`
			Kind          string `json:"kind"`
			MessageText   string `json:"message_text,omitempty"`
		}{1, node.ID, node.Position, string(node.Kind), node.MessageText})
		if err != nil {
			return nil, ErrRuntimeInvalid
		}
		materialRaw, materialDigest, err := s.resolveMaterialSnapshot(tx, node.MaterialPlan, now.Add(delay))
		if err != nil {
			// Material is owned by Media and must be frozen before an EER
			// intent exists. A missing/changed source is an unavailable
			// dependency, not permission to manufacture a kind/id digest.
			return nil, ErrUnavailable
		}
		contentDigest := string(effectport.Hash("group-ops.content.snapshot.v1", string(contentRaw)))
		for _, asset := range assets {
			key := executionKeyString(node.ID, asset.AssetRef)
			if _, ok := existing[key]; ok {
				continue
			}
			sender, found, err := s.senders.ResolveExecutionSender(tx, asset.AssetRef)
			if err != nil {
				return nil, ErrUnavailable
			}
			if !found || sender == "" {
				// A plan member is an editing scope, not a sender fallback. An
				// owner-less/unknown group must never be guessed.
				return nil, ErrUnavailable
			}
			keyDigest := sha256.Sum256([]byte(strings.Join([]string{"group-ops.execution.v1", strconv.FormatInt(run.ID, 10), strconv.FormatInt(node.ID, 10), asset.AssetRef, strconv.FormatInt(run.PlanRevision, 10)}, "\x00")))
			drafts = append(drafts, groupopsport.ExecutionDraft{RunID: run.ID, PlanID: run.PlanID, PlanRevision: run.PlanRevision, NodeID: node.ID, NodePosition: node.Position, TargetReference: asset.AssetRef, SenderUserID: sender, TargetDigest: string(effectport.Hash("group-ops.target", asset.AssetRef)), ContentSnapshot: contentRaw, ContentDigest: contentDigest, MaterialSnapshot: materialRaw, MaterialDigest: materialDigest, ExecutionKeyDigest: keyDigest, ScheduledFor: now.Add(delay), CreatedAt: now})
		}
	}
	if len(drafts) == 0 && len(existing) == 0 {
		return nil, ErrStateConflict
	}
	return drafts, nil
}

func (s *RuntimeService) resolveMaterialSnapshot(ctx context.Context, plan groupopsport.MaterialPlan, requiredThrough time.Time) (json.RawMessage, string, error) {
	if len(plan.References) == 0 {
		raw := json.RawMessage(`{"schema_version":1,"references":[]}`)
		return raw, string(effectport.Hash("group-ops.material.snapshot.v1", string(raw))), nil
	}
	if s == nil || s.materials == nil {
		return nil, "", ErrUnavailable
	}
	raw, digest, err := s.materials.ResolveMaterialSnapshot(ctx, plan, requiredThrough)
	if err != nil || !validMaterialSnapshotResult(raw, digest) {
		return nil, "", ErrUnavailable
	}
	return append(json.RawMessage(nil), raw...), digest, nil
}

func validMaterialSnapshotResult(raw json.RawMessage, digest string) bool {
	if len(raw) == 0 || !json.Valid(raw) || !effectport.ValidDigest(effectport.Digest(digest)) {
		return false
	}
	return digest == string(effectport.Hash("group-ops.material.snapshot.v1", string(raw)))
}

func (s *RuntimeService) materialBlockers(ctx context.Context, detail groupopsport.Detail, now time.Time) []string {
	blockers := make([]string, 0)
	seen := make(map[string]struct{})
	delay := time.Duration(0)
	for _, node := range detail.Nodes {
		if node.Kind == groupopsport.NodeDelay {
			delay += time.Duration(node.DelayMinutes) * time.Minute
			continue
		}
		if len(node.MaterialPlan.References) == 0 {
			continue
		}
		_, _, err := s.resolveMaterialSnapshot(ctx, node.MaterialPlan, now.Add(delay))
		if err == nil {
			continue
		}
		const code = "material_snapshot_unavailable"
		if _, ok := seen[code]; !ok {
			seen[code] = struct{}{}
			blockers = append(blockers, code)
		}
	}
	return blockers
}

func (s *RuntimeService) ListExecutions(ctx context.Context, planID int64, limit, offset int32) (groupopsport.ExecutionPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || planID < 1 || limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumOffset {
		return groupopsport.ExecutionPage{}, invalidOrUnavailableRuntime(s)
	}
	var items []groupopsport.Execution
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, total, err = s.runtime.ListExecutions(tx, planID, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.ExecutionPage{}, classify(err)
	}
	if items == nil {
		items = []groupopsport.Execution{}
	}
	return groupopsport.ExecutionPage{Items: items, Total: total, Limit: limit, Offset: offset, HasMore: int64(offset)+int64(len(items)) < total, RuntimeSafety: s.safety()}, nil
}

func (s *RuntimeService) ProjectExecutionOutcome(ctx context.Context, command groupopsport.ExecutionOutcomeCommand) (groupopsport.Execution, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.ExecutionID < 1 || command.AttemptCount < 0 || !validExecutionState(command.State) || command.DeliveryProven && !command.ProviderAccepted {
		return groupopsport.Execution{}, ErrRuntimeInvalid
	}
	now := s.nowUTC()
	var result groupopsport.Execution
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = s.runtime.RecordExecutionOutcome(tx, command.ExecutionID, command.State, command.ProviderAccepted, command.DeliveryProven, command.ProviderReceiptDigest, command.AttemptCount, now)
		return err
	})
	if err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	return result, nil
}

func (s *RuntimeService) ManualReconcile(ctx context.Context, command groupopsport.ManualReconcileCommand) (groupopsport.Execution, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.ExecutionID < 1 || command.ActorID < 1 || !validRuntimeKey(command.IdempotencyKey) || !effectport.ValidDigest(effectport.Digest(command.EvidenceDigest)) || command.Generation < 1 || command.Fence < 1 || command.LeaseExpiresAt.IsZero() {
		return groupopsport.Execution{}, ErrRuntimeInvalid
	}
	if s.evidence == nil || s.reconciler == nil {
		return groupopsport.Execution{}, ErrProviderDisabled
	}
	now := s.nowUTC()
	if now.Before(command.LeaseExpiresAt) {
		return groupopsport.Execution{}, ErrConflict
	}
	var result groupopsport.Execution
	err := s.uow.Within(ctx, func(tx context.Context) error {
		existing, err := s.runtime.GetExecution(tx, command.ExecutionID)
		if err != nil {
			return err
		}
		if existing.State != groupopsport.ExecutionOutcomeUnknown || existing.ExternalEffectID == "" {
			return ErrStateConflict
		}
		verified, err := s.evidence.VerifyReconciliationEvidence(tx, groupopsport.ReconciliationEvidence{ExecutionID: existing.ID, ExternalEffectID: existing.ExternalEffectID, EvidenceDigest: command.EvidenceDigest})
		if err != nil || !effectport.ValidDigest(effectport.Digest(verified.EvidenceDigest)) || verified.EvidenceDigest != command.EvidenceDigest {
			return ErrConflict
		}
		if err = s.reconciler.ReconcileExternalEffect(tx, groupopsport.ExternalReconcileCommand{EffectID: existing.ExternalEffectID, ReceiptKey: command.IdempotencyKey, EvidenceDigest: command.EvidenceDigest, ActorID: command.ActorID, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt}); err != nil {
			return err
		}
		result, err = s.runtime.ReconcileExecution(tx, existing.ID, command.EvidenceDigest, verified.DeliveryProven, now)
		return err
	})
	if err != nil {
		return groupopsport.Execution{}, classify(err)
	}
	return result, nil
}

func (s *RuntimeService) ListOperationMembers(ctx context.Context, pageSize int32) (groupopsport.OperationMemberPage, error) {
	if s == nil || s.uow == nil || s.staff == nil || pageSize < 1 || pageSize > 100 {
		return groupopsport.OperationMemberPage{}, invalidOrUnavailableRuntime(s)
	}
	reader, ok := s.staff.(groupopsport.EligibleStaffReader)
	if !ok {
		return groupopsport.OperationMemberPage{}, ErrUnavailable
	}
	var items []groupopsport.OperationMember
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, err = reader.ListEligibleStaff(tx)
		return err
	})
	if err != nil {
		return groupopsport.OperationMemberPage{}, classify(err)
	}
	if len(items) > int(pageSize) {
		items = items[:pageSize]
	}
	if items == nil {
		items = []groupopsport.OperationMember{}
	}
	return groupopsport.OperationMemberPage{Scope: "group_ops", Items: items, PageSize: pageSize, RuntimeSafety: s.safety()}, nil
}

func (s *RuntimeService) RefreshOperationMembers(ctx context.Context, command groupopsport.OperationMemberRefreshCommand) (groupopsport.OperationMemberPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.ActorID < 1 || !validRuntimeKey(command.IdempotencyKey) || command.PageSize < 1 || command.PageSize > 100 {
		return groupopsport.OperationMemberPage{}, invalidOrUnavailableRuntime(s)
	}
	if s.directory == nil {
		// The local Access-backed GET remains available. Refresh is a
		// provider/source read and must identify its disabled dependency as
		// a deterministic 503 rather than a malformed client command.
		return groupopsport.OperationMemberPage{}, ErrProviderDisabled
	}
	// Provider reads are deliberately outside the UoW. A failed or partial read
	// therefore cannot hold a database transaction or replace the prior local
	// projection.
	items, err := s.directory.RefreshOperationMembers(ctx, command.PageSize)
	if err != nil || items == nil || len(items) > int(command.PageSize) {
		if err != nil {
			return groupopsport.OperationMemberPage{}, classify(err)
		}
		return groupopsport.OperationMemberPage{}, ErrConflict
	}
	now := s.nowUTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		raw, err := json.Marshal(items)
		if err != nil {
			return err
		}
		return s.runtime.RecordDirectoryRefresh(tx, "operation_members", command.ActorID, 0, sha256.Sum256([]byte(command.IdempotencyKey)), string(effectport.Hash("group-ops.operation-members.snapshot", string(raw))), int32(len(items)), true, now)
	})
	if err != nil {
		return groupopsport.OperationMemberPage{}, classify(err)
	}
	return groupopsport.OperationMemberPage{Scope: "group_ops", Items: items, PageSize: command.PageSize, RuntimeSafety: s.safety()}, nil
}

func (s *RuntimeService) ListGroups(ctx context.Context, owner int64, limit, offset int32) (groupopsport.GroupDirectoryPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || owner < 0 || limit < 1 || limit > 200 || offset < 0 || offset > MaximumOffset {
		return groupopsport.GroupDirectoryPage{}, invalidOrUnavailableRuntime(s)
	}
	var items []groupopsport.GroupDirectoryItem
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		items, total, err = s.runtime.ListDirectoryGroups(tx, owner, limit, offset)
		return err
	})
	if err != nil {
		return groupopsport.GroupDirectoryPage{}, classify(err)
	}
	if items == nil {
		items = []groupopsport.GroupDirectoryItem{}
	}
	return groupopsport.GroupDirectoryPage{Items: items, Total: total, Limit: limit, Offset: offset, HasMore: int64(offset)+int64(len(items)) < total, RuntimeSafety: s.safety()}, nil
}

func (s *RuntimeService) RefreshGroups(ctx context.Context, command groupopsport.GroupRefreshCommand) (groupopsport.GroupDirectoryPage, error) {
	if s == nil || s.uow == nil || s.runtime == nil || command.OwnerStaffID < 1 || command.ActorID < 1 || command.Limit < 1 || command.Limit > 200 || !validRuntimeKey(command.IdempotencyKey) {
		return groupopsport.GroupDirectoryPage{}, invalidOrUnavailableRuntime(s)
	}
	if s.directory == nil {
		// No real directory source is wired in the id-dev composition. Do
		// not delete/replace the local projection or guess an owner.
		return groupopsport.GroupDirectoryPage{}, ErrProviderDisabled
	}
	// The full provider snapshot is fetched before the persistence UoW. Only a
	// complete snapshot may replace the current directory, which prevents a
	// paging or transport failure from deleting existing group bindings.
	snapshot, readErr := s.directory.ListOwnedGroups(ctx, command.OwnerStaffID, command.Limit)
	if readErr != nil {
		return groupopsport.GroupDirectoryPage{}, classify(readErr)
	}
	if !snapshot.Complete {
		return groupopsport.GroupDirectoryPage{}, ErrConflict
	}
	now := s.nowUTC()
	items := append([]groupopsport.GroupDirectoryItem(nil), snapshot.Items...)
	err := s.uow.Within(ctx, func(tx context.Context) error {
		for _, item := range snapshot.Items {
			if item.OwnerStaffID != command.OwnerStaffID || !validOpaqueReference(item.ChatReference) || item.MemberCount < 0 {
				return ErrConflict
			}
		}
		raw, err := json.Marshal(snapshot.Items)
		if err != nil {
			return err
		}
		if err = s.runtime.ReplaceDirectoryGroups(tx, command.OwnerStaffID, snapshot.Items, now); err != nil {
			return err
		}
		if err = s.runtime.RecordDirectoryRefresh(tx, "groups", command.ActorID, command.OwnerStaffID, sha256.Sum256([]byte(command.IdempotencyKey)), string(effectport.Hash("group-ops.groups.snapshot", string(raw))), int32(len(snapshot.Items)), true, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return groupopsport.GroupDirectoryPage{}, classify(err)
	}
	pageItems := items
	if len(pageItems) > int(command.Limit) {
		pageItems = pageItems[:command.Limit]
	}
	return groupopsport.GroupDirectoryPage{Items: pageItems, Total: int64(len(items)), Limit: command.Limit, Offset: 0, HasMore: len(pageItems) < len(items), RuntimeSafety: s.safety()}, nil
}

func countMessageDrafts(detail groupopsport.Detail, existing map[string]struct{}) int {
	count := 0
	for _, node := range detail.Nodes {
		if node.Kind != groupopsport.NodeMessage {
			continue
		}
		for _, asset := range detail.GroupAssets {
			if _, ok := existing[executionKeyString(node.ID, asset.AssetRef)]; !ok {
				count++
			}
		}
	}
	return count
}

func nextMessageDue(detail groupopsport.Detail, now time.Time) *time.Time {
	delay := time.Duration(0)
	for _, node := range detail.Nodes {
		if node.Kind == groupopsport.NodeDelay {
			delay += time.Duration(node.DelayMinutes) * time.Minute
			continue
		}
		value := now.Add(delay)
		return &value
	}
	return nil
}

func executionKeyString(nodeID int64, target string) string {
	return strconv.FormatInt(nodeID, 10) + "\x00" + target
}

func validRuntimeKey(value string) bool {
	return value != "" && len(value) >= 16 && len(value) <= 128 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func validOpaqueReference(value string) bool {
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

func validExecutionState(value groupopsport.ExecutionState) bool {
	switch value {
	case groupopsport.ExecutionAccepted, groupopsport.ExecutionProviderAccepted, groupopsport.ExecutionDeliveryProven, groupopsport.ExecutionOutcomeUnknown, groupopsport.ExecutionReconciled, groupopsport.ExecutionFinalFailed:
		return true
	default:
		return false
	}
}

func invalidOrUnavailableRuntime(s *RuntimeService) error {
	if s == nil || !s.ready() {
		return ErrUnavailable
	}
	return ErrRuntimeInvalid
}

var _ groupopsport.ExecutionOutcomeProjector = (*RuntimeService)(nil)
