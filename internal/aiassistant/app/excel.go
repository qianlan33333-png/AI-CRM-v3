package app

import (
	"context"
	"encoding/json"
	"fmt"
	domain "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/domain"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"time"
)

type ExcelImportStore interface {
	ExcelPlan(context.Context, string) (ai.PlanID, error)
	BindExcelPlan(context.Context, string, string, ai.PlanID) error
}

// CreateExcelPlan is a dedicated trusted import edge; ordinary intake cannot
// opt out of canonical identity validation by supplying deferred_target.
func (s *Service) CreateExcelPlan(ctx context.Context, batchKey string, command ai.CreatePlanCommand) (ai.CreatePlanResult, error) {
	var result ai.CreatePlanResult
	store, ok := s.store.(ExcelImportStore)
	if !ok || batchKey == "" || len(batchKey) > 128 || !command.Valid() || command.SourceKind != "excel_batch" {
		return result, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range command.Recipients {
		if r.DeferredTarget == nil || !r.DeferredTarget.Valid() || len(r.Content) != 2 || r.Content[0].Kind != ai.ContentText || r.Content[1].ExcelCard == nil {
			return result, ErrInvalid
		}
		for _, b := range r.Content {
			if !b.Valid() {
				return result, ErrInvalid
			}
		}
		// One user per file prevents ambiguous distinct-user reporting and accidental double sends.
		if seen[r.DeferredTarget.UnionID] {
			return result, fmt.Errorf("%w: duplicate recipient", ErrInvalid)
		}
		seen[r.DeferredTarget.UnionID] = true
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		id, err := store.ExcelPlan(tx, batchKey)
		if err != nil {
			return err
		}
		if id > 0 {
			result.Plan, err = s.store.GetPlan(tx, id, false)
			result.Replayed = true
			return err
		}
		command.IdempotencyKey = "excel-import-" + batchKey
		if command.OccurredAt.IsZero() {
			command.OccurredAt = time.Now().UTC()
		}
		if err = s.createWithin(tx, command, command.Recipients, &result); err != nil {
			return err
		}
		return store.BindExcelPlan(tx, batchKey, string(command.SourceDigest), result.Plan.ID)
	})
	return result, classify(err)
}

// ApplyExcelCover freezes one uploaded cover for every row in one PostgreSQL
// UoW. The immutable bytes are stored before this call; an interrupted upload
// may leave an unreferenced blob, never a partially updated review batch.
func (s *Service) ApplyExcelCover(ctx context.Context, actor ai.Actor, id ai.PlanID, version int64, key string, cover effect.Digest) (ai.Plan, error) {
	var result ai.Plan
	repo, ok := s.store.(interface {
		ExcelCoverRecipients(context.Context, ai.PlanID) ([]ai.Recipient, []ai.ContentVersion, error)
	})
	if !ok || !actor.Valid() || id < 1 || version < 1 || !validKey(key) || !effect.ValidDigest(cover) {
		return result, ErrInvalid
	}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		input := struct {
			Actor   ai.Actor
			ID      ai.PlanID
			Version int64
			Cover   effect.Digest
		}{actor, id, version, cover}
		receipt, owned, err := s.store.Reserve(tx, reservation("excel_cover", actor, key, digestJSON(input), s.nowUTC()))
		if err != nil {
			return err
		}
		plan, err := s.store.GetPlan(tx, id, true)
		if err != nil {
			return err
		}
		if !owned {
			result = plan
			return nil
		}
		if plan.SourceKind != "excel_batch" || plan.Version != version || (plan.State != ai.PlanPendingReview && plan.State != ai.PlanPartiallyApproved) {
			return ErrConflict
		}
		rows, contents, err := repo.ExcelCoverRecipients(tx, id)
		if err != nil {
			return err
		}
		for i, r := range rows {
			if r.DeferredTarget == nil || len(contents[i].Blocks) != 2 || contents[i].Blocks[1].ExcelCard == nil {
				return ErrInvalid
			}
			blocks := contents[i].Blocks
			card := *blocks[1].ExcelCard
			card.CoverDigest = cover
			blocks[1].ExcelCard = &card
			payload, digest, err := domain.FreezeContent(blocks)
			if err != nil {
				return ErrInvalid
			}
			_, content, err := s.store.UpdateContent(tx, id, r.ID, r.Version, payload, digest, actor.ID, s.nowUTC())
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]any{"plan_id": id, "recipient_id": r.ID, "content_version_id": content.ID, "content_digest": content.Digest, "reason": "batch_cover_updated"})
			if err = s.store.AppendEvent(tx, ai.Event{Type: ai.EventContentUpdated, AggregateID: id, RecipientID: r.ID, ActorID: actor.ID, IdempotencyKey: fmt.Sprintf("%s:%d", key, r.ID), Payload: body, OccurredAt: s.nowUTC()}); err != nil {
				return err
			}
		}
		result, err = s.store.GetPlan(tx, id, false)
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"plan_id": id})
		_, err = s.store.Complete(tx, receipt.ID, body, s.nowUTC())
		return err
	})
	return result, classify(err)
}
