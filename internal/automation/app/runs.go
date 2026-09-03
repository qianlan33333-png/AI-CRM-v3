package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

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
	if s == nil || c.PackageID < 1 || c.PackageVersion < 1 || c.SnapshotID < 1 || c.AgentID < 1 || c.AgentPublishedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
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
		run = automationdomain.RuntimeRun{PackageID: c.PackageID, PackageVersion: c.PackageVersion, SnapshotID: c.SnapshotID, AgentID: c.AgentID, AgentPublishedVersion: c.AgentPublishedVersion, BindingVersion: preview.BindingVersion, SenderSetVersion: preview.SenderSetVersion, PreviewDigest: digest, State: automationport.RunReady, TargetCount: int64(len(recipients)), CreatedBy: c.Actor, CreatedAt: now, UpdatedAt: now}
		created, e := s.store.CreateRun(tx, run, recipients)
		return created, runtimeFact("run", created.ID, "confirm", "automation.run.ready.v1", c.Actor, c.IdempotencyKey, now), e
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
