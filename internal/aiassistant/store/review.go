package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ListPlans(ctx context.Context, query aiassistantport.PlanListQuery, cursor aiassistantapp.Cursor) ([]aiassistantport.Plan, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,name,source_kind,source_digest,state,version,target_count,pending_count,approved_count,rejected_count,ineligible_count,needs_attention_count,created_by,created_at,updated_at
		FROM ai_assistant_plans
		WHERE ($1::text='' OR name ILIKE '%'||$1||'%') AND ($2::text='' OR state=$2)
		AND ($3::timestamptz IS NULL OR (updated_at,id)<($3,$4))
		ORDER BY updated_at DESC,id DESC LIMIT $5`, strings.TrimSpace(query.Keyword), query.State, nullableTime(cursor.UpdatedAt), cursor.ID, query.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]aiassistantport.Plan, 0, query.Limit+1)
	for rows.Next() {
		var plan aiassistantport.Plan
		var digest []byte
		if err = rows.Scan(&plan.ID, &plan.Name, &plan.SourceKind, &digest, &plan.State, &plan.Version, &plan.TargetCount, &plan.PendingCount, &plan.ApprovedCount, &plan.RejectedCount, &plan.IneligibleCount, &plan.NeedsAttentionCount, &plan.CreatedBy, &plan.CreatedAt, &plan.UpdatedAt); err != nil {
			return nil, err
		}
		plan.SourceDigest, err = digestFromBytes(digest)
		if err != nil {
			return nil, err
		}
		items = append(items, plan)
	}
	return items, rows.Err()
}

func (r *Repository) ListApprovalRecipients(ctx context.Context, planID aiassistantport.PlanID, lock bool) ([]aiassistantport.Recipient, []aiassistantport.ContentVersion, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	query := `SELECT r.id,r.plan_id,r.customer_id,r.staff_id,r.review_state,r.execution_state,r.version,r.current_content_version_id,r.updated_at,
		c.id,c.recipient_id,c.version,c.content_digest,c.content_payload,c.created_at
		FROM ai_assistant_plan_recipients r JOIN ai_assistant_content_versions c ON c.id=r.current_content_version_id
		WHERE r.plan_id=$1 AND r.review_state IN ('pending_review','approved') ORDER BY r.id`
	if lock {
		query += ` FOR UPDATE OF r`
	}
	rows, err := tx.Query(ctx, query, planID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var recipients []aiassistantport.Recipient
	var contents []aiassistantport.ContentVersion
	for rows.Next() {
		var recipient aiassistantport.Recipient
		var content aiassistantport.ContentVersion
		var digest, payload []byte
		if err = rows.Scan(&recipient.ID, &recipient.PlanID, &recipient.CustomerID, &recipient.StaffID, &recipient.ReviewState, &recipient.ExecutionState, &recipient.Version, &recipient.ContentVersionID, &recipient.UpdatedAt,
			&content.ID, &content.RecipientID, &content.Version, &digest, &payload, &content.CreatedAt); err != nil {
			return nil, nil, err
		}
		content.Digest, err = digestFromBytes(digest)
		if err != nil || json.Unmarshal(payload, &content.Blocks) != nil {
			return nil, nil, ErrInvalid
		}
		recipients = append(recipients, recipient)
		contents = append(contents, content)
	}
	return recipients, contents, rows.Err()
}

func (r *Repository) SavePlanApproval(ctx context.Context, plan aiassistantport.Plan, recipients []aiassistantport.Recipient, contents []aiassistantport.ContentVersion, intents []outboundport.PrivateMessageIntentResult, actor int64, idempotencyDigest [32]byte, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if len(recipients) == 0 || len(recipients) != len(contents) || len(recipients) != len(intents) {
		return ErrInvalid
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_assistant_plans SET state='dispatching',version=$2,pending_count=0,approved_count=$3,updated_at=$4 WHERE id=$1 AND version=$5`, plan.ID, plan.Version, plan.ApprovedCount, now.UTC(), plan.Version-1)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrConflict
	}
	for i, recipient := range recipients {
		if intents[i].IntentID < 1 || intents[i].EffectID == "" {
			return ErrInvalid
		}
		tag, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET review_state='approved',execution_state='queued',version=version+1,updated_at=$3 WHERE id=$1 AND plan_id=$2 AND execution_state='not_accepted'`, recipient.ID, plan.ID, now.UTC())
		if err != nil || tag.RowsAffected() != 1 {
			return ErrConflict
		}
		payload, _ := digestBytes(contents[i].Digest)
		_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_effect_bindings(recipient_id,outbound_intent_id,external_effect_id,payload_digest,state,generation,created_at,updated_at) VALUES($1,$2,$3,$4,'queued',1,$5,$5)`, recipient.ID, intents[i].IntentID, intents[i].EffectID, payload, now.UTC())
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_review_decisions(plan_id,recipient_id,decision,reason,actor_id,aggregate_version,idempotency_digest,occurred_at) VALUES($1,NULL,'approved','',$2,$3,$4,$5)`, plan.ID, actor, plan.Version, idempotencyDigest[:], now.UTC())
	return err
}

func (r *Repository) ListEffectBindings(ctx context.Context, planID aiassistantport.PlanID) ([]aiassistantport.EffectBinding, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT b.recipient_id,b.external_effect_id,b.state,b.generation,b.fence,b.attempt_count,b.provider_accepted,b.delivery_proven,b.updated_at FROM ai_assistant_effect_bindings b JOIN ai_assistant_plan_recipients r ON r.id=b.recipient_id WHERE r.plan_id=$1 ORDER BY b.recipient_id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []aiassistantport.EffectBinding{}
	for rows.Next() {
		var item aiassistantport.EffectBinding
		if err = rows.Scan(&item.RecipientID, &item.EffectID, &item.State, &item.Generation, &item.Fence, &item.AttemptCount, &item.ProviderAccepted, &item.DeliveryProven, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetRecipientByEffect(ctx context.Context, effectID string) (aiassistantport.Recipient, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.Recipient{}, err
	}
	var out aiassistantport.Recipient
	err = tx.QueryRow(ctx, `SELECT r.id,r.plan_id,r.customer_id,r.staff_id,r.review_state,r.execution_state,r.version,r.current_content_version_id,b.external_effect_id,r.updated_at FROM ai_assistant_plan_recipients r JOIN ai_assistant_effect_bindings b ON b.recipient_id=r.id WHERE b.external_effect_id=$1`, effectID).Scan(&out.ID, &out.PlanID, &out.CustomerID, &out.StaffID, &out.ReviewState, &out.ExecutionState, &out.Version, &out.ContentVersionID, &out.EffectID, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return aiassistantport.Recipient{}, ErrNotFound
	}
	return out, err
}

func (r *Repository) CompleteExternalEffect(ctx context.Context, effectID string, state aiassistantport.ExecutionState, providerAccepted, deliveryProven bool, receipt effectport.Digest, attempts int32, generation, fence int64, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	raw, err := digestBytes(receipt)
	if err != nil {
		return err
	}
	var recipientID aiassistantport.RecipientID
	var planID aiassistantport.PlanID
	err = tx.QueryRow(ctx, `UPDATE ai_assistant_effect_bindings b SET state=$2,generation=$3,fence=$4,attempt_count=$5,provider_accepted=$6,delivery_proven=$7,provider_receipt_digest=$8,updated_at=$9 FROM ai_assistant_plan_recipients r WHERE b.external_effect_id=$1 AND r.id=b.recipient_id RETURNING b.recipient_id,r.plan_id`, effectID, state, generation, fence, attempts, providerAccepted, deliveryProven, raw, now.UTC()).Scan(&recipientID, &planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET execution_state=$2,version=version+1,updated_at=$3 WHERE id=$1`, recipientID, state, now.UTC()); err != nil {
		return err
	}
	var total, terminal, failed, attention int
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE execution_state IN ('provider_accepted','delivery_proven','reconciled','final_failed')),count(*) FILTER(WHERE execution_state='final_failed'),count(*) FILTER(WHERE execution_state IN ('outcome_unknown','retryable_failed')) FROM ai_assistant_plan_recipients WHERE plan_id=$1 AND review_state='approved'`, planID).Scan(&total, &terminal, &failed, &attention); err != nil {
		return err
	}
	planState := aiassistantport.PlanDispatching
	if attention > 0 {
		planState = aiassistantport.PlanNeedsAttention
	} else if total > 0 && terminal == total {
		if failed > 0 {
			planState = aiassistantport.PlanCompletedWithFailures
		} else {
			planState = aiassistantport.PlanCompleted
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ai_assistant_plans SET state=$2,needs_attention_count=$3,version=version+1,updated_at=$4 WHERE id=$1`, planID, planState, attention, now.UTC())
	return err
}

var _ aiassistantport.EffectCompletionProjector = (*Repository)(nil)

func (r *Repository) ListRecipients(ctx context.Context, query aiassistantport.RecipientPageQuery, afterID int64) ([]aiassistantport.Recipient, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT r.id,r.plan_id,r.customer_id,r.staff_id,r.review_state,r.execution_state,r.version,r.current_content_version_id,COALESCE(b.external_effect_id,''),r.updated_at
		FROM ai_assistant_plan_recipients r LEFT JOIN ai_assistant_effect_bindings b ON b.recipient_id=r.id
		WHERE r.plan_id=$1 AND ($2::text='' OR r.review_state=$2) AND r.id>$3 ORDER BY r.id LIMIT $4`, query.PlanID, query.State, afterID, query.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]aiassistantport.Recipient, 0, query.Limit+1)
	for rows.Next() {
		var recipient aiassistantport.Recipient
		if err = rows.Scan(&recipient.ID, &recipient.PlanID, &recipient.CustomerID, &recipient.StaffID, &recipient.ReviewState, &recipient.ExecutionState, &recipient.Version, &recipient.ContentVersionID, &recipient.EffectID, &recipient.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, recipient)
	}
	return items, rows.Err()
}

func (r *Repository) GetRecipient(ctx context.Context, planID aiassistantport.PlanID, recipientID aiassistantport.RecipientID, lock bool) (aiassistantport.Recipient, aiassistantport.ContentVersion, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	query := `SELECT r.id,r.plan_id,r.customer_id,r.staff_id,r.review_state,r.execution_state,r.version,r.current_content_version_id,COALESCE(b.external_effect_id,''),r.updated_at
		FROM ai_assistant_plan_recipients r LEFT JOIN ai_assistant_effect_bindings b ON b.recipient_id=r.id WHERE r.plan_id=$1 AND r.id=$2`
	if lock {
		query += ` FOR UPDATE OF r`
	}
	var recipient aiassistantport.Recipient
	err = tx.QueryRow(ctx, query, planID, recipientID).Scan(&recipient.ID, &recipient.PlanID, &recipient.CustomerID, &recipient.StaffID, &recipient.ReviewState, &recipient.ExecutionState, &recipient.Version, &recipient.ContentVersionID, &recipient.EffectID, &recipient.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrNotFound
	}
	if err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	content, err := contentVersion(ctx, tx, recipient.ContentVersionID)
	return recipient, content, err
}

func contentVersion(ctx context.Context, tx pgx.Tx, id aiassistantport.ContentVersionID) (aiassistantport.ContentVersion, error) {
	var content aiassistantport.ContentVersion
	var digest, payload []byte
	err := tx.QueryRow(ctx, `SELECT id,recipient_id,version,content_digest,content_payload,created_at FROM ai_assistant_content_versions WHERE id=$1`, id).Scan(&content.ID, &content.RecipientID, &content.Version, &digest, &payload, &content.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return aiassistantport.ContentVersion{}, ErrNotFound
	}
	if err != nil {
		return aiassistantport.ContentVersion{}, err
	}
	content.Digest, err = digestFromBytes(digest)
	if err != nil || json.Unmarshal(payload, &content.Blocks) != nil {
		return aiassistantport.ContentVersion{}, ErrInvalid
	}
	return content, nil
}

func (r *Repository) UpdateContent(ctx context.Context, planID aiassistantport.PlanID, recipientID aiassistantport.RecipientID, expectedVersion int64, payload []byte, digest effectport.Digest, actor int64, now time.Time) (aiassistantport.Recipient, aiassistantport.ContentVersion, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	var state aiassistantport.PlanState
	if err = tx.QueryRow(ctx, `SELECT state FROM ai_assistant_plans WHERE id=$1 FOR UPDATE`, planID).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrNotFound
	}
	if err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	if state != aiassistantport.PlanPendingReview && state != aiassistantport.PlanPartiallyApproved {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrConflict
	}
	recipient, _, err := r.GetRecipient(ctx, planID, recipientID, true)
	if err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	if recipient.Version != expectedVersion || recipient.ReviewState != aiassistantport.ReviewPending || recipient.ExecutionState != aiassistantport.ExecutionNotAccepted {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrConflict
	}
	var content aiassistantport.ContentVersion
	raw, err := digestBytes(digest)
	if err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO ai_assistant_content_versions(recipient_id,version,content_digest,content_payload,created_by,created_at)
		VALUES($1,(SELECT COALESCE(max(version),0)+1 FROM ai_assistant_content_versions WHERE recipient_id=$1),$2,$3::jsonb,$4,$5)
		RETURNING id,recipient_id,version,content_digest,content_payload,created_at`, recipientID, raw, payload, actor, now.UTC()).Scan(&content.ID, &content.RecipientID, &content.Version, &raw, &payload, &content.CreatedAt)
	if err != nil {
		if unique(err) {
			return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrConflict
		}
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, err
	}
	content.Digest = digest
	if err = json.Unmarshal(payload, &content.Blocks); err != nil {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrInvalid
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET current_content_version_id=$3,version=version+1,updated_at=$4 WHERE id=$1 AND plan_id=$2 AND version=$5`, recipientID, planID, content.ID, now.UTC(), expectedVersion)
	if err != nil || tag.RowsAffected() != 1 {
		return aiassistantport.Recipient{}, aiassistantport.ContentVersion{}, ErrConflict
	}
	recipient.ContentVersionID, recipient.Version, recipient.UpdatedAt = content.ID, expectedVersion+1, now.UTC()
	return recipient, content, nil
}

func (r *Repository) SaveRecipientReview(ctx context.Context, plan aiassistantport.Plan, recipient aiassistantport.Recipient, previous aiassistantport.ReviewState, reason string, actor int64, idempotencyDigest [32]byte, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_assistant_plans SET state=$2,version=$3,pending_count=$4,approved_count=$5,rejected_count=$6,ineligible_count=$7,needs_attention_count=$8,updated_at=$9 WHERE id=$1 AND version=$10`, plan.ID, plan.State, plan.Version, plan.PendingCount, plan.ApprovedCount, plan.RejectedCount, plan.IneligibleCount, plan.NeedsAttentionCount, now.UTC(), plan.Version-1)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrConflict
	}
	tag, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET review_state=$3,version=$4,updated_at=$5 WHERE id=$1 AND plan_id=$2 AND version=$6 AND review_state=$7`, recipient.ID, plan.ID, recipient.ReviewState, recipient.Version, now.UTC(), recipient.Version-1, previous)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_review_decisions(plan_id,recipient_id,decision,reason,actor_id,aggregate_version,idempotency_digest,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, plan.ID, recipient.ID, recipient.ReviewState, reason, actor, plan.Version, idempotencyDigest[:], now.UTC())
	return err
}

func (r *Repository) SavePlanRejection(ctx context.Context, plan aiassistantport.Plan, reason string, actor int64, idempotencyDigest [32]byte, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_assistant_plans SET state=$2,version=$3,pending_count=0,approved_count=0,rejected_count=$4,rejected_reason=$5,updated_at=$6 WHERE id=$1 AND version=$7`, plan.ID, plan.State, plan.Version, plan.RejectedCount, reason, now.UTC(), plan.Version-1)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_assistant_plan_recipients SET review_state='rejected',version=version+1,updated_at=$2 WHERE plan_id=$1 AND review_state IN ('pending_review','approved')`, plan.ID, now.UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_assistant_review_decisions(plan_id,recipient_id,decision,reason,actor_id,aggregate_version,idempotency_digest,occurred_at) VALUES($1,NULL,'rejected',$2,$3,$4,$5,$6)`, plan.ID, reason, actor, plan.Version, idempotencyDigest[:], now.UTC())
	return err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
