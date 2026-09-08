package app

import (
	"context"
	"fmt"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
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
