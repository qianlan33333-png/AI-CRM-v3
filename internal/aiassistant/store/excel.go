package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platform "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) ExcelPlan(ctx context.Context, key string) (ai.PlanID, error) {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "excel-import:"+key); err != nil {
		return 0, err
	}
	var id ai.PlanID
	err = tx.QueryRow(ctx, `SELECT plan_id FROM ai_assistant_excel_imports WHERE batch_key=$1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func (r *Repository) BindExcelPlan(ctx context.Context, key, digest string, id ai.PlanID) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_excel_imports(batch_key,plan_id,file_digest) VALUES($1,$2,$3)`, key, id, digest)
	return err
}
func (r *Repository) LoadDeferredTarget(ctx context.Context, ref string) (ai.DeferredTarget, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 4 || parts[0] != "aiassistant" {
		return ai.DeferredTarget{}, ErrInvalid
	}
	ids := make([]int64, 3)
	for i := range ids {
		n, e := strconv.ParseInt(parts[i+1], 10, 64)
		if e != nil || n < 1 {
			return ai.DeferredTarget{}, ErrInvalid
		}
		ids[i] = n
	}
	var t *ai.DeferredTarget
	err := r.pool.QueryRow(ctx, `SELECT deferred_target FROM ai_assistant_plan_recipients WHERE plan_id=$1 AND id=$2 AND current_content_version_id=$3 AND review_state='approved' AND execution_state<>'not_accepted'`, ids[0], ids[1], ids[2]).Scan(&t)
	if err != nil || t == nil || !t.Valid() {
		return ai.DeferredTarget{}, ErrInvalid
	}
	return *t, nil
}
func (r *Repository) ExcelPlans(ctx context.Context) ([]ai.PlanID, error) {
	rows, err := r.pool.Query(ctx, `SELECT plan_id FROM ai_assistant_excel_imports ORDER BY plan_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ai.PlanID{}
	for rows.Next() {
		var id ai.PlanID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (r *Repository) SaveExcelSnapshot(ctx context.Context, id ai.PlanID, key string) error {
	tx, err := platform.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE ai_assistant_excel_imports SET approved_snapshot=$2 WHERE plan_id=$1 AND approved_snapshot=''`, id, key)
	return err
}
func (r *Repository) ExcelApproval(ctx context.Context, id ai.PlanID) (string, time.Time, error) {
	var key string
	var at time.Time
	err := r.pool.QueryRow(ctx, `SELECT i.approved_snapshot,COALESCE(min(d.occurred_at),p.created_at) FROM ai_assistant_excel_imports i JOIN ai_assistant_plans p ON p.id=i.plan_id LEFT JOIN ai_assistant_review_decisions d ON d.plan_id=p.id AND d.recipient_id IS NULL AND d.decision='approved' WHERE i.plan_id=$1 GROUP BY i.approved_snapshot,p.created_at`, id).Scan(&key, &at)
	return key, at, err
}

func (r *Repository) RecordExcelDelivery(ctx context.Context, recipient ai.Recipient, v outbound.PrivateMessageDelivery) error {
	if recipient.DeferredTarget == nil || v.Status == nil || *v.Status == 0 {
		return ErrInvalid
	}
	return r.uow.Within(ctx, func(tx context.Context) error {
		items, err := r.ListEffectBindings(tx, recipient.PlanID)
		if err != nil {
			return err
		}
		for _, binding := range items {
			if binding.RecipientID == recipient.ID {
				if binding.DeliveryProven {
					return nil
				}
				state := ai.ExecutionFinalFailed
				proof := false
				if *v.Status == 1 {
					if v.SentAt == nil {
						return ErrInvalid
					}
					state = ai.ExecutionDeliveryProven
					proof = true
				}
				receipt := effect.Hash("excel.delivery", v.MessageID, v.SenderUserID, v.ExternalUserID, strconv.Itoa(*v.Status))
				return r.CompleteExternalEffect(tx, binding.EffectID, state, true, proof, receipt, binding.AttemptCount, binding.Generation, binding.Fence, time.Now().UTC())
			}
		}
		return ErrNotFound
	})
}
