package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type RunEffectReconcileCommand struct {
	RunID, Actor, Generation, Fence int64
	EffectID, IdempotencyKey        string
	LeaseExpiresAt                  time.Time
	EvidenceDigest, Resolution      string
}

type RunConfirmCommand struct {
	PackageID             int64
	PackageVersion        int64
	SnapshotID            int64
	AgentID               int64
	AgentPublishedVersion int64
	PreviewDigest         string
	Actor                 int64
	IdempotencyKey        string
}

func (s *RuntimeService) CreateBroadcastPreview(ctx context.Context, packageID, actor int64) (automationdomain.RunPreview, error) {
	if s == nil || packageID < 1 || actor < 1 {
		return automationdomain.RunPreview{}, ErrRuntimeInvalid
	}
	configuration, err := s.audiences.AudienceExecutionConfiguration(ctx, segmentport.PackageID(packageID))
	if err != nil {
		return automationdomain.RunPreview{}, ErrRuntimeUnavailable
	}
	if !configuration.Ready || configuration.Snapshot.ID < 1 || len(configuration.SenderStaffIDs) < 1 {
		return automationdomain.RunPreview{}, ErrRuntimeNotReady
	}
	now := s.now().UTC()
	digestInput, _ := json.Marshal([]any{configuration.PackageID, configuration.PackageVersion, configuration.Snapshot.ID, configuration.ConfigurationVersionID, configuration.AgentID, configuration.AgentPublishedVersion, configuration.BindingVersion, configuration.SenderSetVersion, configuration.Snapshot.MemberCount, now.UnixNano()})
	preview := automationdomain.RunPreview{PackageID: packageID, PackageVersion: configuration.PackageVersion, SnapshotID: int64(configuration.Snapshot.ID), ConfigurationVersionID: int64(configuration.ConfigurationVersionID), AgentID: configuration.AgentID, AgentPublishedVersion: configuration.AgentPublishedVersion, BindingVersion: configuration.BindingVersion, SenderSetVersion: configuration.SenderSetVersion, TargetCount: configuration.Snapshot.MemberCount, PreviewDigest: sha256.Sum256(digestInput), CreatedBy: actor, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		preview, e = s.store.CreatePreview(tx, preview)
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]any{"preview_id": preview.ID, "package_id": packageID, "snapshot_id": preview.SnapshotID, "target_count": preview.TargetCount})
		return s.store.AppendRuntimeFact(tx, runtimeFact("preview", preview.ID, "create", "automation.run.previewed.v1", actor, hex.EncodeToString(preview.PreviewDigest[:]), now, payload))
	})
	return preview, runtimeClassify(err)
}
func (s *RuntimeService) ConfirmRun(ctx context.Context, c RunConfirmCommand) (automationdomain.RuntimeRun, error) {
	if s == nil || s.messages == nil || c.PackageID < 1 || c.PackageVersion < 1 || c.SnapshotID < 1 || c.AgentID < 1 || c.AgentPublishedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	rawDigest, err := hex.DecodeString(c.PreviewDigest)
	if err != nil || len(rawDigest) != 32 {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	var digest [32]byte
	copy(digest[:], rawDigest)
	var preview automationdomain.RunPreview
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		preview, e = s.store.PreviewByDigest(tx, digest)
		return e
	})
	if err != nil {
		return automationdomain.RuntimeRun{}, runtimeClassify(err)
	}
	now := s.now().UTC()
	if !now.Before(preview.ExpiresAt) || preview.PackageID != c.PackageID || preview.PackageVersion != c.PackageVersion || preview.SnapshotID != c.SnapshotID || preview.AgentID != c.AgentID || preview.AgentPublishedVersion != c.AgentPublishedVersion {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	configuration, err := s.audiences.AudienceExecutionConfiguration(ctx, segmentport.PackageID(c.PackageID))
	if err != nil || !configuration.Ready {
		return automationdomain.RuntimeRun{}, ErrRuntimeNotReady
	}
	if configuration.PackageVersion != preview.PackageVersion || int64(configuration.Snapshot.ID) != preview.SnapshotID || configuration.AgentID != preview.AgentID || configuration.AgentPublishedVersion != preview.AgentPublishedVersion || configuration.BindingVersion != preview.BindingVersion || configuration.SenderSetVersion != preview.SenderSetVersion {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	members := []segmentport.Member{}
	cursor := ""
	for {
		page, e := s.snapshots.Members(ctx, segmentport.SnapshotID(preview.SnapshotID), cursor, 1000)
		if e != nil {
			return automationdomain.RuntimeRun{}, ErrRuntimeUnavailable
		}
		members = append(members, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if int64(len(members)) != preview.TargetCount || len(members) == 0 {
		return automationdomain.RuntimeRun{}, ErrRuntimeConflict
	}
	recipients := make([]automationdomain.RuntimeRecipient, len(members))
	for i, item := range members {
		recipients[i] = automationdomain.RuntimeRecipient{CustomerID: int64(item.CustomerID), SenderStaffID: configuration.SenderStaffIDs[i%len(configuration.SenderStaffIDs)], State: automationport.RecipientAccepted}
	}
	payload, _ := json.Marshal(c)
	var run automationdomain.RuntimeRun
	err = s.runtimeMutation(ctx, "confirm_run", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		run = automationdomain.RuntimeRun{PackageID: c.PackageID, PackageVersion: c.PackageVersion, SnapshotID: c.SnapshotID, AgentID: c.AgentID, AgentPublishedVersion: c.AgentPublishedVersion, BindingVersion: preview.BindingVersion, SenderSetVersion: preview.SenderSetVersion, PreviewDigest: digest, State: automationport.RunExecuting, TargetCount: int64(len(recipients)), CreatedBy: c.Actor, CreatedAt: now, UpdatedAt: now}
		created, createdRecipients, e := s.store.CreateRun(tx, run, recipients)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		policyDigest := sha256.Sum256([]byte("automation.manual-run.policy.v1"))
		for _, recipient := range createdRecipients {
			sourceDigest := sha256.Sum256([]byte(fmt.Sprintf("automation-run:%d:recipient:%d", created.ID, recipient.ID)))
			targetDigest := sha256.Sum256([]byte(fmt.Sprintf("customer:%d", recipient.CustomerID)))
			acceptance, acceptErr := s.messages.AcceptMessageWithin(tx, outboundport.MessageIntent{SourceKind: "automation_run", SourceID: created.ID, RunRecipientID: recipient.ID, CustomerID: customerdomain.CustomerID(recipient.CustomerID), SenderStaffID: recipient.SenderStaffID, AgentID: created.AgentID, AgentPublishedVersion: created.AgentPublishedVersion, ContentReference: fmt.Sprintf("automation-agent:%d:published:%d", created.AgentID, created.AgentPublishedVersion), SourceDigest: sourceDigest, TargetDigest: targetDigest, PayloadDigest: configuration.ContentDigest, PolicyDigest: policyDigest, ReceiptKey: fmt.Sprintf("automation-run-%d-recipient-%d", created.ID, recipient.ID)})
			if acceptErr != nil {
				return created, RuntimeFact{}, acceptErr
			}
			if bindErr := s.store.BindRecipientEffect(tx, recipient.ID, acceptance.EffectID, now); bindErr != nil {
				return created, RuntimeFact{}, bindErr
			}
		}
		return created, runtimeFact("run", created.ID, "confirm", "automation.run.queued.v1", c.Actor, c.IdempotencyKey, now), nil
	}, &run)
	return run, runtimeClassify(err)
}
func PreviewDigestString(p automationdomain.RunPreview) string {
	return hex.EncodeToString(p.PreviewDigest[:])
}
func (s *RuntimeService) ListRuns(ctx context.Context, cursor int64, limit int) ([]automationdomain.RuntimeRun, string, error) {
	if cursor < 0 || limit < 1 || limit > 100 {
		return nil, "", ErrRuntimeInvalid
	}
	var out []automationdomain.RuntimeRun
	var next string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, next, e = s.store.ListRuns(tx, cursor, limit)
		return e
	})
	return out, next, runtimeClassify(err)
}
func (s *RuntimeService) Run(ctx context.Context, id int64) (automationdomain.RuntimeRun, error) {
	if id < 1 {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	var out automationdomain.RuntimeRun
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.Run(tx, id); return e })
	return out, runtimeClassify(err)
}
func (s *RuntimeService) RunRecipients(ctx context.Context, id, cursor int64, limit int) ([]automationdomain.RuntimeRecipient, string, error) {
	if id < 1 || cursor < 0 || limit < 1 || limit > 100 {
		return nil, "", ErrRuntimeInvalid
	}
	var out []automationdomain.RuntimeRecipient
	var next string
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, next, e = s.store.RunRecipients(tx, id, cursor, limit)
		return e
	})
	return out, next, runtimeClassify(err)
}

func (s *RuntimeService) EffectReconciliationCandidate(ctx context.Context, runID int64, effectID string) (effectport.ReconciliationCandidate, error) {
	if s == nil || s.effects == nil || runID < 1 || effectID == "" {
		return effectport.ReconciliationCandidate{}, ErrRuntimeInvalid
	}
	var recipient automationdomain.RuntimeRecipient
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		recipient, e = s.store.RecipientForEffect(tx, runID, effectID)
		return e
	})
	if err != nil {
		return effectport.ReconciliationCandidate{}, runtimeClassify(err)
	}
	if recipient.State != automationport.RecipientOutcomeUnknown {
		return effectport.ReconciliationCandidate{}, ErrRuntimeConflict
	}
	candidate, err := s.effects.ReconciliationCandidate(ctx, effectID)
	if err != nil {
		return effectport.ReconciliationCandidate{}, runtimeClassify(err)
	}
	if candidate.Owner != effectport.OwnerOutbound || candidate.Kind != effectport.KindOutboundMessage || candidate.State != effectport.StateUnknown {
		return effectport.ReconciliationCandidate{}, ErrRuntimeConflict
	}
	return candidate, nil
}

func (s *RuntimeService) ReconcileRunEffect(ctx context.Context, command RunEffectReconcileCommand) (automationdomain.RunReconciliation, error) {
	if s == nil || s.effects == nil || command.RunID < 1 || command.Actor < 1 || command.Generation < 1 || command.Fence < 1 || command.EffectID == "" || command.LeaseExpiresAt.IsZero() || !validRuntimeMutation(command.Actor, command.IdempotencyKey) || !validReconciliationResolution(command.Resolution) {
		return automationdomain.RunReconciliation{}, ErrRuntimeInvalid
	}
	rawEvidence, err := hex.DecodeString(strings.ToLower(command.EvidenceDigest))
	if err != nil || len(rawEvidence) != sha256.Size || hex.EncodeToString(rawEvidence) != command.EvidenceDigest {
		return automationdomain.RunReconciliation{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	if now.Before(command.LeaseExpiresAt.UTC()) {
		return automationdomain.RunReconciliation{}, ErrRuntimeConflict
	}
	candidate, err := s.EffectReconciliationCandidate(ctx, command.RunID, command.EffectID)
	if err != nil {
		return automationdomain.RunReconciliation{}, err
	}
	if candidate.Generation != command.Generation || candidate.Fence != command.Fence || !candidate.LeaseExpiresAt.Equal(command.LeaseExpiresAt.UTC()) {
		return automationdomain.RunReconciliation{}, ErrRuntimeConflict
	}
	var evidence [32]byte
	copy(evidence[:], rawEvidence)
	payload, _ := json.Marshal(command)
	var output automationdomain.RunReconciliation
	err = s.runtimeMutation(ctx, "reconcile_effect", command.Actor, command.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		recipient, e := s.store.RecipientForEffect(tx, command.RunID, command.EffectID)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		if recipient.State != automationport.RecipientOutcomeUnknown {
			return output, RuntimeFact{}, ErrRuntimeConflict
		}
		output, e = s.store.CreateRunReconciliation(tx, automationdomain.RunReconciliation{RunID: command.RunID, RecipientID: recipient.ID, EffectID: command.EffectID, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt.UTC(), EvidenceDigest: evidence, Resolution: command.Resolution, ActorID: command.Actor, ReceiptDigest: sha256.Sum256([]byte(command.IdempotencyKey)), CreatedAt: now})
		if e != nil {
			return output, RuntimeFact{}, e
		}
		projection, e := s.effects.ReconcileEffectWithin(tx, effectport.ReconcileCommand{EffectID: command.EffectID, ReceiptKey: effectport.Hash("automation.effect.reconcile", command.EffectID, command.IdempotencyKey), EvidenceDigest: effectport.Digest("sha256:" + command.EvidenceDigest), ActorAdminUserID: command.Actor, Generation: command.Generation, Fence: command.Fence, LeaseExpiresAt: command.LeaseExpiresAt.UTC()})
		if e != nil {
			return output, RuntimeFact{}, e
		}
		if projection.State != effectport.StateReconciled {
			return output, RuntimeFact{}, ErrRuntimeConflict
		}
		factPayload, _ := json.Marshal(map[string]any{"reconciliation_id": output.ID, "run_id": output.RunID, "recipient_id": output.RecipientID, "effect_id": output.EffectID, "generation": output.Generation, "fence": output.Fence, "resolution": output.Resolution})
		return output, runtimeFact("recipient", recipient.ID, "reconcile", "automation.recipient.reconciled.v1", command.Actor, command.IdempotencyKey, now, factPayload), nil
	}, &output)
	return output, runtimeClassify(err)
}

func validReconciliationResolution(value string) bool {
	return value == "provider_accepted" || value == "delivery_proven" || value == "final_failed"
}
func (s *RuntimeService) CancelRun(ctx context.Context, id, actor int64, key string) (automationdomain.RuntimeRun, error) {
	if id < 1 || !validRuntimeMutation(actor, key) {
		return automationdomain.RuntimeRun{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(map[string]any{"run_id": id})
	var out automationdomain.RuntimeRun
	err := s.runtimeMutation(ctx, "cancel_run", actor, key, payload, func(tx context.Context) (any, RuntimeFact, error) {
		var e error
		out, e = s.store.CancelRun(tx, id, now)
		return out, runtimeFact("run", id, "cancel", "automation.run.cancelled.v1", actor, key, now), e
	}, &out)
	return out, runtimeClassify(err)
}
func validateCountEquation(run automationdomain.RuntimeRun, recipients int) error {
	if run.TargetCount != int64(recipients)+run.SkippedCount {
		return fmt.Errorf("run count equation: %w", ErrRuntimeConflict)
	}
	return nil
}
