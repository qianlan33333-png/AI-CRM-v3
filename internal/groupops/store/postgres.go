// Package store owns the PostgreSQL facts for Group Ops.  It is deliberately
// scoped to local plans, staff references, opaque group references, and
// immutable execution projections; it never reads customer or identity data.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrInvalid  = errors.New("invalid Group Ops persistence request")
	ErrNotFound = groupopsapp.ErrNotFound
	ErrConflict = groupopsapp.ErrConflict
)

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

func (r *Repository) List(ctx context.Context, limit, offset int32) ([]groupopsport.PlanListItem, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT p.id,p.name,p.status,p.revision,p.created_by,p.updated_by,p.created_at,p.updated_at,
		       count(e.id) FILTER (WHERE e.state IN ('accepted','provider_accepted','outcome_unknown'))
		FROM group_ops_plans p
		LEFT JOIN group_ops_executions e ON e.plan_id=p.id
		GROUP BY p.id
		ORDER BY p.updated_at DESC,p.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]groupopsport.PlanListItem, 0)
	for rows.Next() {
		var item groupopsport.PlanListItem
		if err = rows.Scan(&item.ID, &item.Name, &item.Status, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.QueueCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Count(ctx context.Context) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_plans`).Scan(&count)
	return count, err
}

func (r *Repository) Get(ctx context.Context, id int64) (groupopsport.Detail, error) {
	return r.get(ctx, id, false)
}

func (r *Repository) Lock(ctx context.Context, id int64) (groupopsport.Detail, error) {
	return r.get(ctx, id, true)
}

func (r *Repository) get(ctx context.Context, id int64, lock bool) (groupopsport.Detail, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Detail{}, err
	}
	query := `SELECT id,name,status,revision,created_by,updated_by,created_at,updated_at FROM group_ops_plans WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var detail groupopsport.Detail
	err = tx.QueryRow(ctx, query, id).Scan(&detail.Plan.ID, &detail.Plan.Name, &detail.Plan.Status, &detail.Plan.Revision, &detail.Plan.CreatedBy, &detail.Plan.UpdatedBy, &detail.Plan.CreatedAt, &detail.Plan.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupopsport.Detail{}, ErrNotFound
	}
	if err != nil {
		return groupopsport.Detail{}, err
	}

	memberRows, err := tx.Query(ctx, `SELECT staff_id FROM group_ops_plan_members WHERE plan_id=$1 ORDER BY staff_id`, id)
	if err != nil {
		return groupopsport.Detail{}, err
	}
	detail.Members = make([]groupopsport.Member, 0)
	for memberRows.Next() {
		var item groupopsport.Member
		if err = memberRows.Scan(&item.StaffID); err != nil {
			memberRows.Close()
			return groupopsport.Detail{}, err
		}
		detail.Members = append(detail.Members, item)
	}
	if err = memberRows.Err(); err != nil {
		memberRows.Close()
		return groupopsport.Detail{}, err
	}
	memberRows.Close()

	// Application validation compares opaque references using Go's bytewise
	// string order. Database locale collation can place lower-case references
	// before upper-case ones, so force the same deterministic order here.
	assetRows, err := tx.Query(ctx, `SELECT id,asset_reference FROM group_ops_plan_group_assets WHERE plan_id=$1 ORDER BY asset_reference COLLATE "C",id`, id)
	if err != nil {
		return groupopsport.Detail{}, err
	}
	detail.GroupAssets = make([]groupopsport.GroupAsset, 0)
	for assetRows.Next() {
		var item groupopsport.GroupAsset
		if err = assetRows.Scan(&item.ID, &item.AssetRef); err != nil {
			assetRows.Close()
			return groupopsport.Detail{}, err
		}
		detail.GroupAssets = append(detail.GroupAssets, item)
	}
	if err = assetRows.Err(); err != nil {
		assetRows.Close()
		return groupopsport.Detail{}, err
	}
	assetRows.Close()

	nodeRows, err := tx.Query(ctx, `SELECT id,position,kind,message_text,delay_minutes,material_reference,material_plan FROM group_ops_plan_nodes WHERE plan_id=$1 ORDER BY position,id`, id)
	if err != nil {
		return groupopsport.Detail{}, err
	}
	detail.Nodes = make([]groupopsport.Node, 0)
	for nodeRows.Next() {
		var item groupopsport.Node
		var raw []byte
		if err = nodeRows.Scan(&item.ID, &item.Position, &item.Kind, &item.MessageText, &item.DelayMinutes, &item.MaterialRef, &raw); err != nil {
			nodeRows.Close()
			return groupopsport.Detail{}, err
		}
		if json.Unmarshal(raw, &item.MaterialPlan) != nil {
			nodeRows.Close()
			return groupopsport.Detail{}, ErrInvalid
		}
		if item.MaterialPlan.References == nil {
			item.MaterialPlan.References = []groupopsport.MaterialReference{}
		}
		detail.Nodes = append(detail.Nodes, item)
	}
	if err = nodeRows.Err(); err != nil {
		nodeRows.Close()
		return groupopsport.Detail{}, err
	}
	nodeRows.Close()

	var reference string
	err = tx.QueryRow(ctx, `SELECT reference FROM group_ops_plan_webhook_descriptors WHERE plan_id=$1`, id).Scan(&reference)
	if errors.Is(err, pgx.ErrNoRows) {
		reference = ""
	} else if err != nil {
		return groupopsport.Detail{}, err
	}
	detail.WebhookDescriptor = groupopsapp.WebhookDescriptor(reference)
	detail.Safety = groupopsport.LocalSafety()
	return detail, nil
}

func (r *Repository) Create(ctx context.Context, plan groupopsport.Plan) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, plan.Name, plan.Status, plan.Revision, plan.CreatedBy, plan.UpdatedBy, plan.CreatedAt, plan.UpdatedAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_webhook_descriptors(plan_id,reference) VALUES($1,'')`, id)
	return id, err
}

func (r *Repository) Save(ctx context.Context, detail groupopsport.Detail) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if detail.Plan.ID < 1 {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE group_ops_plans SET name=$2,status=$3,revision=$4,updated_by=$5,updated_at=$6 WHERE id=$1`, detail.Plan.ID, detail.Plan.Name, detail.Plan.Status, detail.Plan.Revision, detail.Plan.UpdatedBy, detail.Plan.UpdatedAt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM group_ops_plan_members WHERE plan_id=$1`, detail.Plan.ID); err != nil {
		return err
	}
	for _, member := range detail.Members {
		if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, detail.Plan.ID, member.StaffID); err != nil {
			return err
		}
	}
	if err = reconcileAssets(ctx, tx, detail.Plan.ID, detail.GroupAssets); err != nil {
		return err
	}
	if err = reconcileNodes(ctx, tx, detail.Plan.ID, detail.Nodes); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_webhook_descriptors(plan_id,reference) VALUES($1,$2) ON CONFLICT(plan_id) DO UPDATE SET reference=EXCLUDED.reference`, detail.Plan.ID, detail.WebhookDescriptor.Reference)
	return err
}

func reconcileAssets(ctx context.Context, tx pgx.Tx, planID int64, desired []groupopsport.GroupAsset) error {
	rows, err := tx.Query(ctx, `SELECT id FROM group_ops_plan_group_assets WHERE plan_id=$1`, planID)
	if err != nil {
		return err
	}
	existing := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = false
	}
	rows.Close()
	for _, item := range desired {
		if item.ID > 0 {
			if _, err = tx.Exec(ctx, `UPDATE group_ops_plan_group_assets SET asset_reference=$3 WHERE id=$1 AND plan_id=$2`, item.ID, planID, item.AssetRef); err != nil {
				return err
			}
			existing[item.ID] = true
		} else {
			if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_group_assets(plan_id,asset_reference) VALUES($1,$2)`, planID, item.AssetRef); err != nil {
				return err
			}
		}
	}
	for id, kept := range existing {
		if !kept {
			if _, err = tx.Exec(ctx, `DELETE FROM group_ops_plan_group_assets WHERE id=$1 AND plan_id=$2`, id, planID); err != nil {
				return err
			}
		}
	}
	return nil
}

func reconcileNodes(ctx context.Context, tx pgx.Tx, planID int64, desired []groupopsport.Node) error {
	rows, err := tx.Query(ctx, `SELECT id FROM group_ops_plan_nodes WHERE plan_id=$1`, planID)
	if err != nil {
		return err
	}
	existing := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = false
	}
	rows.Close()
	for _, item := range desired {
		raw, marshalErr := json.Marshal(item.MaterialPlan)
		if marshalErr != nil {
			return marshalErr
		}
		if item.ID > 0 {
			if _, err = tx.Exec(ctx, `UPDATE group_ops_plan_nodes SET position=$3,kind=$4,message_text=$5,delay_minutes=$6,material_reference=$7,material_plan=$8 WHERE id=$1 AND plan_id=$2`, item.ID, planID, item.Position, item.Kind, item.MessageText, item.DelayMinutes, item.MaterialRef, raw); err != nil {
				return err
			}
			existing[item.ID] = true
		} else {
			if _, err = tx.Exec(ctx, `INSERT INTO group_ops_plan_nodes(plan_id,position,kind,message_text,delay_minutes,material_reference,material_plan) VALUES($1,$2,$3,$4,$5,$6,$7)`, planID, item.Position, item.Kind, item.MessageText, item.DelayMinutes, item.MaterialRef, raw); err != nil {
				return err
			}
		}
	}
	for id, kept := range existing {
		if !kept {
			if _, err = tx.Exec(ctx, `DELETE FROM group_ops_plan_nodes WHERE id=$1 AND plan_id=$2`, id, planID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repository) Reserve(ctx context.Context, operation string, reservation groupopsapp.Reservation) (groupopsapp.Receipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsapp.Receipt{}, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_operation_receipts(operation,actor_scope,key_digest,payload_digest,state,created_at) VALUES($1,$2,$3,$4,'in_progress',$5) ON CONFLICT(operation,actor_scope,key_digest) DO NOTHING RETURNING id`, operation, reservation.ActorScope, reservation.KeyDigest[:], reservation.PayloadDigest[:], reservation.CreatedAt).Scan(&id)
	if err == nil {
		return groupopsapp.Receipt{ID: id, Operation: operation, ActorScope: reservation.ActorScope, KeyDigest: reservation.KeyDigest, PayloadDigest: reservation.PayloadDigest, State: "in_progress"}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return groupopsapp.Receipt{}, false, err
	}
	var receipt groupopsapp.Receipt
	var key, payload []byte
	var createdAt, completedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot,created_at,completed_at FROM group_ops_operation_receipts WHERE operation=$1 AND actor_scope=$2 AND key_digest=$3`, operation, reservation.ActorScope, reservation.KeyDigest[:]).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &key, &payload, &receipt.State, &receipt.ResultSnapshot, &createdAt, &completedAt)
	if err != nil {
		return groupopsapp.Receipt{}, false, err
	}
	if len(key) != sha256.Size || len(payload) != sha256.Size {
		return groupopsapp.Receipt{}, false, ErrInvalid
	}
	copy(receipt.KeyDigest[:], key)
	copy(receipt.PayloadDigest[:], payload)
	return receipt, false, nil
}

func (r *Repository) Complete(ctx context.Context, id int64, snapshot json.RawMessage, now time.Time) (groupopsapp.Receipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsapp.Receipt{}, err
	}
	var receipt groupopsapp.Receipt
	var key, payload []byte
	var createdAt, completedAt *time.Time
	err = tx.QueryRow(ctx, `UPDATE group_ops_operation_receipts SET state='completed',result_snapshot=$2,completed_at=$3 WHERE id=$1 AND state='in_progress' RETURNING id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot,created_at,completed_at`, id, snapshot, now).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &key, &payload, &receipt.State, &receipt.ResultSnapshot, &createdAt, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id,operation,actor_scope,key_digest,payload_digest,state,result_snapshot,created_at,completed_at FROM group_ops_operation_receipts WHERE id=$1`, id).Scan(&receipt.ID, &receipt.Operation, &receipt.ActorScope, &key, &payload, &receipt.State, &receipt.ResultSnapshot, &createdAt, &completedAt)
	}
	if err != nil {
		return groupopsapp.Receipt{}, err
	}
	if len(key) != sha256.Size || len(payload) != sha256.Size {
		return groupopsapp.Receipt{}, ErrInvalid
	}
	copy(receipt.KeyDigest[:], key)
	copy(receipt.PayloadDigest[:], payload)
	return receipt, nil
}

func (r *Repository) Append(ctx context.Context, event groupopsport.Event) (groupopsport.EventID, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var body map[string]any
	if json.Unmarshal(event.Payload, &body) != nil {
		return 0, ErrInvalid
	}
	actor, ok := body["actor"].(float64)
	if !ok || int64(actor) < 1 || event.Type != groupopsport.EvGroupOpsPlanUpdated || len(event.IdempotencyKey) < 8 {
		return 0, ErrInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_audit_events(event_type,idempotency_key,actor_admin_user_id,payload,occurred_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, event.Type, event.IdempotencyKey, int64(actor), event.Payload, event.OccurredAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	var aggregateID int64
	if value, ok := body["plan_id"].(float64); ok {
		aggregateID = int64(value)
	}
	if aggregateID < 1 {
		return 0, ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO group_ops_outbox(event_type,aggregate_id,payload,idempotency_key) VALUES($1,$2,$3,$4)`, event.Type, aggregateID, event.Payload, event.IdempotencyKey)
	return groupopsport.EventID(id), err
}

// ResolveExecutionOwner reads only the owner projection of an opaque group
// target. The Composition Root combines this local result with the Access
// staff port to verify the active sender ID; this store never reads admin_users.
func (r *Repository) ResolveExecutionOwner(ctx context.Context, target string) (int64, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var owner int64
	err = tx.QueryRow(ctx, `SELECT owner_staff_id FROM group_ops_directory_groups WHERE chat_reference=$1`, target).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return owner, err == nil, err
}

func (r *Repository) ListExecutionKeys(ctx context.Context, planID, revision int64) ([]groupopsport.ExecutionKey, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT node_id,target_reference FROM group_ops_executions WHERE plan_id=$1 AND plan_revision=$2`, planID, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]groupopsport.ExecutionKey, 0)
	for rows.Next() {
		var item groupopsport.ExecutionKey
		if err = rows.Scan(&item.NodeID, &item.TargetReference); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ReserveRun(ctx context.Context, reservation groupopsport.RunReservation) (groupopsport.Run, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Run{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_runs(plan_id,trigger_kind,source_key_digest,plan_revision,scheduled_for,accepted_at,accepted_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(plan_id,trigger_kind,source_key_digest) DO NOTHING RETURNING id`, reservation.PlanID, reservation.Trigger, reservation.SourceKeyDigest[:], reservation.PlanRevision, reservation.ScheduledFor, reservation.AcceptedAt, reservation.AcceptedBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id,plan_id,trigger_kind,plan_revision,scheduled_for,accepted_at,accepted_by FROM group_ops_runs WHERE plan_id=$1 AND trigger_kind=$2 AND source_key_digest=$3`, reservation.PlanID, reservation.Trigger, reservation.SourceKeyDigest[:]).Scan(&id, &reservation.PlanID, &reservation.Trigger, &reservation.PlanRevision, &reservation.ScheduledFor, &reservation.AcceptedAt, &reservation.AcceptedBy)
	}
	if err != nil {
		return groupopsport.Run{}, err
	}
	return groupopsport.Run{ID: id, PlanID: reservation.PlanID, Trigger: reservation.Trigger, PlanRevision: reservation.PlanRevision, ScheduledFor: reservation.ScheduledFor.UTC(), AcceptedAt: reservation.AcceptedAt.UTC(), AcceptedBy: reservation.AcceptedBy}, nil
}

func parseEffectID(value string) (int64, error) {
	if !strings.HasPrefix(value, "eer_") {
		return 0, ErrInvalid
	}
	numeric, err := strconv.ParseInt(strings.TrimPrefix(value, "eer_"), 10, 64)
	if err != nil || numeric < 1 {
		return 0, ErrInvalid
	}
	return numeric, nil
}

func (r *Repository) InsertExecution(ctx context.Context, draft groupopsport.ExecutionDraft) (groupopsport.Execution, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	effectID, err := parseEffectID(draft.ExternalEffectID)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_executions(run_id,plan_id,node_id,plan_revision,node_position,target_reference,sender_userid_snapshot,target_digest,content_snapshot,content_digest,material_snapshot,material_digest,execution_key_digest,external_effect_id,state,scheduled_for,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'accepted',$15,$16,$16) RETURNING id`, draft.RunID, draft.PlanID, draft.NodeID, draft.PlanRevision, draft.NodePosition, draft.TargetReference, draft.SenderUserID, draft.TargetDigest, draft.ContentSnapshot, draft.ContentDigest, draft.MaterialSnapshot, draft.MaterialDigest, draft.ExecutionKeyDigest[:], effectID, draft.ScheduledFor, draft.CreatedAt).Scan(&id)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	return r.getExecution(ctx, tx, id)
}

func (r *Repository) GetExecution(ctx context.Context, id int64) (groupopsport.Execution, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	return r.getExecution(ctx, tx, id)
}

// LoadDispatchExecution returns the owner-owned immutable payload only while
// the accepted plan, its version, and the precise group binding remain valid.
// Outbound calls this through the stable Group Ops port before crossing the
// Provider boundary; it never exposes a mutable plan or a provider response.
func (r *Repository) LoadDispatchExecution(ctx context.Context, effectRef string) (groupopsport.DispatchExecution, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.DispatchExecution{}, err
	}
	effectID, err := parseEffectID(effectRef)
	if err != nil {
		return groupopsport.DispatchExecution{}, err
	}
	var item groupopsport.DispatchExecution
	var runID int64
	err = tx.QueryRow(ctx, `
SELECT e.id,e.run_id,e.state,e.delivery_proven,e.target_reference,e.sender_userid_snapshot,
       e.content_snapshot,e.content_digest,e.material_snapshot,e.material_digest,e.target_digest
FROM group_ops_executions e
JOIN group_ops_plans p ON p.id=e.plan_id
JOIN group_ops_plan_group_assets a ON a.plan_id=e.plan_id AND a.asset_reference=e.target_reference
JOIN group_ops_directory_groups g ON g.chat_reference=e.target_reference
WHERE e.external_effect_id=$1
  AND e.state='accepted'
  AND p.status='active'
  AND p.revision=e.plan_revision`, effectID).Scan(
		&item.ExecutionID, &runID, &item.State, &item.DeliveryProven, &item.TargetReference, &item.SenderUserID,
		&item.ContentSnapshot, &item.ContentDigest, &item.MaterialSnapshot, &item.MaterialDigest, &item.TargetRefDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupopsport.DispatchExecution{}, ErrNotFound
	}
	if err != nil {
		return groupopsport.DispatchExecution{}, err
	}
	item.ExternalEffectID = effectRef
	item.SourceRefDigest = string(effectport.Hash("group-ops.run", strconv.FormatInt(runID, 10)))
	item.PayloadDigest = string(effectport.Hash("group-ops.payload", item.ContentDigest, item.MaterialDigest, item.SenderUserID))
	item.PolicyVersionHash = string(effectport.Hash("group-ops.policy", "v1"))
	return item, nil
}

func (r *Repository) getExecution(ctx context.Context, tx pgx.Tx, id int64) (groupopsport.Execution, error) {
	var e groupopsport.Execution
	var effectID int64
	var receipt, evidence *string
	err := tx.QueryRow(ctx, `SELECT id,run_id,plan_id,plan_revision,node_id,node_position,target_reference,target_digest,content_digest,material_digest,external_effect_id,state,provider_accepted,delivery_proven,provider_receipt_digest,reconciliation_evidence_digest,attempt_count,scheduled_for,created_at,updated_at FROM group_ops_executions WHERE id=$1`, id).Scan(&e.ID, &e.RunID, &e.PlanID, &e.PlanRevision, &e.NodeID, &e.NodePosition, &e.TargetReference, &e.TargetDigest, &e.ContentDigest, &e.MaterialDigest, &effectID, &e.State, &e.ProviderAccepted, &e.DeliveryProven, &receipt, &evidence, &e.AttemptCount, &e.ScheduledFor, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupopsport.Execution{}, ErrNotFound
	}
	if err != nil {
		return groupopsport.Execution{}, err
	}
	e.ExternalEffectID = "eer_" + strconv.FormatInt(effectID, 10)
	e.ProviderReceiptPresent = receipt != nil && *receipt != ""
	e.ReconciliationEvidencePresent = evidence != nil && *evidence != ""
	return e, nil
}

func (r *Repository) ListExecutions(ctx context.Context, planID int64, limit, offset int32) ([]groupopsport.Execution, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_executions WHERE plan_id=$1`, planID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM group_ops_executions WHERE plan_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, planID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	items := make([]groupopsport.Execution, 0, len(ids))
	for _, id := range ids {
		item, getErr := r.getExecution(ctx, tx, id)
		if getErr != nil {
			return nil, 0, getErr
		}
		items = append(items, item)
	}
	return items, total, nil
}

func (r *Repository) ReadRunSummary(ctx context.Context, runID int64) (groupopsport.RunSummary, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.RunSummary{}, err
	}
	var summary groupopsport.RunSummary
	err = tx.QueryRow(ctx, `SELECT id,plan_id,trigger_kind,plan_revision,scheduled_for,accepted_at,accepted_by FROM group_ops_runs WHERE id=$1`, runID).Scan(&summary.Run.ID, &summary.Run.PlanID, &summary.Run.Trigger, &summary.Run.PlanRevision, &summary.Run.ScheduledFor, &summary.Run.AcceptedAt, &summary.Run.AcceptedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupopsport.RunSummary{}, ErrNotFound
	}
	if err != nil {
		return groupopsport.RunSummary{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM group_ops_executions WHERE run_id=$1 ORDER BY node_position,target_reference,id`, runID)
	if err != nil {
		return groupopsport.RunSummary{}, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return groupopsport.RunSummary{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return groupopsport.RunSummary{}, err
	}
	rows.Close()
	summary.Executions = make([]groupopsport.Execution, 0)
	summary.PendingIntents = make([]groupopsport.ExecutionIntent, 0)
	for _, id := range ids {
		e, getErr := r.getExecution(ctx, tx, id)
		if getErr != nil {
			return groupopsport.RunSummary{}, getErr
		}
		summary.Executions = append(summary.Executions, e)
		switch e.State {
		case groupopsport.ExecutionAccepted:
			summary.Accepted++
		case groupopsport.ExecutionProviderAccepted:
			summary.ProviderAccepted++
		case groupopsport.ExecutionDeliveryProven:
			summary.DeliveryProven++
		case groupopsport.ExecutionOutcomeUnknown:
			summary.OutcomeUnknown++
		case groupopsport.ExecutionReconciled:
			summary.Reconciled++
		case groupopsport.ExecutionFinalFailed:
			summary.FinalFailed++
		}
	}
	intentRows, intentErr := tx.Query(ctx, `SELECT id,node_id,node_position,target_reference,scheduled_for,state,external_effect_id FROM group_ops_execution_intents WHERE run_id=$1 AND state <> 'accepted' ORDER BY target_reference,node_position,id`, runID)
	if intentErr != nil {
		return groupopsport.RunSummary{}, intentErr
	}
	defer intentRows.Close()
	for intentRows.Next() {
		var item groupopsport.ExecutionIntent
		var effectID *int64
		if intentErr = intentRows.Scan(&item.ID, &item.NodeID, &item.NodePosition, &item.TargetReference, &item.ScheduledFor, &item.State, &effectID); intentErr != nil {
			return groupopsport.RunSummary{}, intentErr
		}
		if effectID != nil {
			item.ExternalEffectID = "eer_" + strconv.FormatInt(*effectID, 10)
		}
		summary.PendingIntents = append(summary.PendingIntents, item)
	}
	if intentErr = intentRows.Err(); intentErr != nil {
		return groupopsport.RunSummary{}, intentErr
	}
	return summary, nil
}

func (r *Repository) RecordExecutionOutcome(ctx context.Context, id int64, state groupopsport.ExecutionState, providerAccepted, deliveryProven bool, receipt string, attempts int32, now time.Time) (groupopsport.Execution, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE group_ops_executions SET state=$2,provider_accepted=$3,delivery_proven=$4,provider_receipt_digest=NULLIF($5,''),attempt_count=$6,updated_at=$7 WHERE id=$1`, id, state, providerAccepted, deliveryProven, receipt, attempts, now)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	if result.RowsAffected() != 1 {
		return groupopsport.Execution{}, ErrNotFound
	}
	return r.getExecution(ctx, tx, id)
}

func (r *Repository) ReconcileExecution(ctx context.Context, id int64, evidence string, deliveryProven bool, now time.Time) (groupopsport.Execution, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE group_ops_executions SET state='reconciled',delivery_proven=$2,provider_accepted=CASE WHEN $2 THEN TRUE ELSE provider_accepted END,reconciliation_evidence_digest=$3,updated_at=$4 WHERE id=$1 AND state='outcome_unknown'`, id, deliveryProven, evidence, now)
	if err != nil {
		return groupopsport.Execution{}, err
	}
	if result.RowsAffected() != 1 {
		return groupopsport.Execution{}, ErrConflict
	}
	return r.getExecution(ctx, tx, id)
}

func (r *Repository) FindPlanByWebhookReference(ctx context.Context, reference string) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `SELECT plan_id FROM group_ops_plan_webhook_descriptors WHERE reference=$1 AND reference<>''`, reference).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func (r *Repository) ListDirectoryGroups(ctx context.Context, owner int64, limit, offset int32) ([]groupopsport.GroupDirectoryItem, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if owner > 0 {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_directory_groups WHERE owner_staff_id=$1`, owner).Scan(&total)
	} else {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_directory_groups`).Scan(&total)
	}
	if err != nil {
		return nil, 0, err
	}
	query := `SELECT chat_reference,owner_staff_id,display_name,member_count,refreshed_at FROM group_ops_directory_groups ORDER BY refreshed_at DESC,chat_reference LIMIT $1 OFFSET $2`
	args := []any{limit, offset}
	if owner > 0 {
		query = `SELECT chat_reference,owner_staff_id,display_name,member_count,refreshed_at FROM group_ops_directory_groups WHERE owner_staff_id=$1 ORDER BY refreshed_at DESC,chat_reference LIMIT $2 OFFSET $3`
		args = []any{owner, limit, offset}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]groupopsport.GroupDirectoryItem, 0)
	for rows.Next() {
		var item groupopsport.GroupDirectoryItem
		if err = rows.Scan(&item.ChatReference, &item.OwnerStaffID, &item.DisplayName, &item.MemberCount, &item.RefreshedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *Repository) ReplaceDirectoryGroups(ctx context.Context, owner int64, items []groupopsport.GroupDirectoryItem, now time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	refs := make([]string, 0, len(items))
	for _, item := range items {
		if item.OwnerStaffID != owner {
			return ErrInvalid
		}
		digest := groupDirectoryDigest(item)
		if _, err = tx.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(chat_reference) DO UPDATE SET owner_staff_id=EXCLUDED.owner_staff_id,display_name=EXCLUDED.display_name,member_count=EXCLUDED.member_count,source_digest=EXCLUDED.source_digest,refreshed_at=EXCLUDED.refreshed_at`, item.ChatReference, owner, item.DisplayName, item.MemberCount, digest, now); err != nil {
			return err
		}
		refs = append(refs, item.ChatReference)
	}
	if len(refs) == 0 {
		_, err = tx.Exec(ctx, `DELETE FROM group_ops_directory_groups WHERE owner_staff_id=$1`, owner)
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM group_ops_directory_groups WHERE owner_staff_id=$1 AND NOT (chat_reference = ANY($2::text[]))`, owner, refs)
	return err
}

func groupDirectoryDigest(item groupopsport.GroupDirectoryItem) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{item.ChatReference, strconv.FormatInt(item.OwnerStaffID, 10), item.DisplayName, strconv.Itoa(int(item.MemberCount))}, "\x00")))
	return "sha256:" + fmt.Sprintf("%x", sum[:])
}

func (r *Repository) RecordDirectoryRefresh(ctx context.Context, kind string, actor, owner int64, key [sha256.Size]byte, snapshot string, count int32, providerRead bool, now time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO group_ops_directory_refresh_receipts(refresh_kind,actor_id,owner_staff_id,key_digest,snapshot_digest,item_count,provider_read_executed,refreshed_at) VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8) ON CONFLICT(refresh_kind,actor_id,key_digest) DO NOTHING RETURNING id`, kind, actor, owner, key[:], snapshot, count, providerRead, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var oldOwner *int64
		var oldKey []byte
		var oldSnapshot string
		var oldCount int32
		var oldProvider bool
		err = tx.QueryRow(ctx, `SELECT id,owner_staff_id,key_digest,snapshot_digest,item_count,provider_read_executed FROM group_ops_directory_refresh_receipts WHERE refresh_kind=$1 AND actor_id=$2 AND key_digest=$3`, kind, actor, key[:]).Scan(&id, &oldOwner, &oldKey, &oldSnapshot, &oldCount, &oldProvider)
		if err == nil && (oldOwner == nil && owner != 0 || oldOwner != nil && (owner == 0 || *oldOwner != owner) || oldSnapshot != snapshot || oldCount != count || oldProvider != providerRead) {
			return ErrConflict
		}
	}
	return err
}

// ClaimWebhookReplay persists only digest facts. It is intentionally a small
// standalone transaction because signature verification occurs before the
// runtime's plan transaction; the durable run source key still provides the
// second idempotency fence for acceptance.
func (r *Repository) ClaimWebhookReplay(ctx context.Context, clientID, resource string, eventDigest, payloadDigest [sha256.Size]byte, now time.Time) (bool, error) {
	if clientID != groupopsport.WebhookClientID || !validOpaqueStore(resource) || now.IsZero() || isZeroDigest(eventDigest) || isZeroDigest(payloadDigest) {
		return false, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `INSERT INTO group_ops_protocol_replays(client_id,resource_reference,event_id_digest,payload_digest,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(client_id,event_id_digest) DO NOTHING`, clientID, resource, eventDigest[:], payloadDigest[:], now)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 1 {
		return true, tx.Commit(ctx)
	}
	var oldPayload []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest FROM group_ops_protocol_replays WHERE client_id=$1 AND event_id_digest=$2`, clientID, eventDigest[:]).Scan(&oldPayload)
	if err != nil {
		return false, err
	}
	if len(oldPayload) != sha256.Size || !equalDigest(oldPayload, payloadDigest) {
		return false, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func isZeroDigest(value [sha256.Size]byte) bool { return value == [sha256.Size]byte{} }

func validOpaqueStore(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}

func equalDigest(value []byte, expected [sha256.Size]byte) bool {
	if len(value) != sha256.Size {
		return false
	}
	var difference byte
	for index := range expected {
		difference |= value[index] ^ expected[index]
	}
	return difference == 0
}

// RecordGroupMessageTask records only validated task acceptance evidence. It
// is invoked from EER's completion transaction, after the provider network
// call has returned. A failure rolls that transaction back, leaving EER's
// already-attempted fact for its no-resend unknown recovery path.
func (r *Repository) RecordGroupMessageTask(ctx context.Context, task groupopsport.GroupMessageReceipt) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if task.ExecutionID < 1 || !effectport.ValidDigest(effectport.Digest(task.TaskEvidenceDigest)) || task.MessageID == "" || !validOpaqueStore(task.SenderUserID) || !validOpaqueStore(task.ChatID) {
		return ErrInvalid
	}
	effectID, err := parseEffectID(task.ExternalEffectID)
	if err != nil {
		return err
	}
	var storedExecution, storedEffect int64
	var sender, chat string
	err = tx.QueryRow(ctx, `SELECT id,external_effect_id,sender_userid_snapshot,target_reference FROM group_ops_executions WHERE id=$1 FOR UPDATE`, task.ExecutionID).Scan(&storedExecution, &storedEffect, &sender, &chat)
	if err != nil {
		return err
	}
	if storedExecution != task.ExecutionID || storedEffect != effectID || sender != task.SenderUserID || chat != task.ChatID {
		return ErrConflict
	}
	result, err := tx.Exec(ctx, `INSERT INTO group_ops_group_message_tasks(execution_id,external_effect_id,msgid,sender_userid_snapshot,chat_reference,task_evidence_digest,accepted_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()) ON CONFLICT(external_effect_id) DO UPDATE SET msgid=EXCLUDED.msgid,sender_userid_snapshot=EXCLUDED.sender_userid_snapshot,chat_reference=EXCLUDED.chat_reference,task_evidence_digest=EXCLUDED.task_evidence_digest WHERE group_ops_group_message_tasks.execution_id=EXCLUDED.execution_id AND group_ops_group_message_tasks.msgid=EXCLUDED.msgid AND group_ops_group_message_tasks.sender_userid_snapshot=EXCLUDED.sender_userid_snapshot AND group_ops_group_message_tasks.chat_reference=EXCLUDED.chat_reference AND group_ops_group_message_tasks.task_evidence_digest=EXCLUDED.task_evidence_digest`, task.ExecutionID, effectID, task.MessageID, task.SenderUserID, task.ChatID, task.TaskEvidenceDigest)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// FindGroupMessageReceipt returns the owner-held WeCom task reference only
// when the reconciliation request names the same execution/effect pair. This
// is deliberately separate from EER: a msgid proves task acceptance, not
// member delivery.
func (r *Repository) FindGroupMessageReceipt(ctx context.Context, evidence groupopsport.ReconciliationEvidence) (groupopsport.GroupMessageReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.GroupMessageReceipt{}, false, err
	}
	if evidence.ExecutionID < 1 {
		return groupopsport.GroupMessageReceipt{}, false, ErrInvalid
	}
	effectID, err := parseEffectID(evidence.ExternalEffectID)
	if err != nil {
		return groupopsport.GroupMessageReceipt{}, false, err
	}
	var task groupopsport.GroupMessageReceipt
	var status pgtype.Int4
	var deliveryDigest pgtype.Text
	err = tx.QueryRow(ctx, `SELECT execution_id,external_effect_id,msgid,sender_userid_snapshot,chat_reference,task_evidence_digest,delivery_status,delivery_evidence_digest FROM group_ops_group_message_tasks WHERE execution_id=$1 AND external_effect_id=$2`, evidence.ExecutionID, effectID).Scan(&task.ExecutionID, new(int64), &task.MessageID, &task.SenderUserID, &task.ChatID, &task.TaskEvidenceDigest, &status, &deliveryDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupopsport.GroupMessageReceipt{}, false, nil
	}
	if err != nil {
		return groupopsport.GroupMessageReceipt{}, false, err
	}
	task.ExternalEffectID = evidence.ExternalEffectID
	if status.Valid {
		value := int(status.Int32)
		task.DeliveryStatus = &value
	}
	if deliveryDigest.Valid {
		task.DeliveryEvidenceDigest = deliveryDigest.String
	}
	return task, true, nil
}

func (r *Repository) RecordGroupMessageDelivery(ctx context.Context, task groupopsport.GroupMessageReceipt, evidenceDigest string) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if task.ExecutionID < 1 || task.DeliveryStatus == nil || !effectport.ValidDigest(effectport.Digest(evidenceDigest)) {
		return ErrInvalid
	}
	effectID, err := parseEffectID(task.ExternalEffectID)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE group_ops_group_message_tasks SET delivery_status=$3,delivery_evidence_digest=$4,delivery_checked_at=clock_timestamp() WHERE execution_id=$1 AND external_effect_id=$2 AND msgid=$5 AND sender_userid_snapshot=$6 AND chat_reference=$7`, task.ExecutionID, effectID, *task.DeliveryStatus, evidenceDigest, task.MessageID, task.SenderUserID, task.ChatID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// CompleteEffect is called by the External Effects completion sink with a
// transaction-bound context. It maps only the typed EER outcome into the
// local execution projection and never performs a provider call.
func (r *Repository) CompleteEffect(ctx context.Context, effectRef string, result groupopsport.ExecutionState, providerAccepted, deliveryProven bool, receipt string, attempts int32, now time.Time) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	effectID, err := parseEffectID(effectRef)
	if err != nil {
		return err
	}
	var executionID int64
	err = tx.QueryRow(ctx, `SELECT id FROM group_ops_executions WHERE external_effect_id=$1`, effectID).Scan(&executionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	updated, err := tx.Exec(ctx, `UPDATE group_ops_executions SET state=$2,provider_accepted=$3,delivery_proven=$4,provider_receipt_digest=NULLIF($5,''),attempt_count=$6,updated_at=$7 WHERE id=$1 AND state NOT IN ('delivery_proven','reconciled','final_failed')`, executionID, result, providerAccepted, deliveryProven, receipt, attempts, now)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// The following readers expose only sealed, v3-owned historical facts. They
// intentionally do not join current plan, staff, customer, identity, runtime
// or Provider tables. A fresh v3 database legitimately returns empty pages;
// it never fabricates donor history rows.
func (r *Repository) ListHistoricalPlans(ctx context.Context, limit, offset int32) ([]groupopsport.HistoricalPlan, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT plan_id,name,status,revision,created_by,updated_by,created_at,updated_at,source_plan_id,source_code,plan_type,original_status,owner_staff_id,archived_at FROM group_ops_v1_history_plans ORDER BY plan_id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]groupopsport.HistoricalPlan, 0)
	for rows.Next() {
		item, scanErr := scanHistoricalPlan(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_v1_history_plans`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *Repository) ListHistoricalDirectory(ctx context.Context, limit, offset int32) ([]groupopsport.HistoricalDirectory, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id,source_kind,source_id,chat_reference,display_name,owner_staff_id,owner_name,member_count,internal_member_count,external_member_count,original_status,recorded_at FROM group_ops_v1_history_directory ORDER BY id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]groupopsport.HistoricalDirectory, 0)
	for rows.Next() {
		var item groupopsport.HistoricalDirectory
		var sourceID, ownerID pgtype.Int8
		var displayName, ownerName pgtype.Text
		var memberCount, internalCount, externalCount pgtype.Int4
		var recordedAt pgtype.Timestamptz
		if err = rows.Scan(&item.ID, &item.SourceKind, &sourceID, &item.ChatReference, &displayName, &ownerID, &ownerName, &memberCount, &internalCount, &externalCount, &item.OriginalStatus, &recordedAt); err != nil {
			return nil, 0, err
		}
		if !recordedAt.Valid {
			return nil, 0, ErrInvalid
		}
		item.SourceID = nullableInt64(sourceID)
		item.DisplayName = nullableText(displayName)
		item.OwnerStaffID = nullableInt64(ownerID)
		item.OwnerName = nullableText(ownerName)
		item.MemberCount = nullableInt32(memberCount)
		item.InternalMemberCount = nullableInt32(internalCount)
		item.ExternalMemberCount = nullableInt32(externalCount)
		item.RecordedAt = recordedAt.Time
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_v1_history_directory`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *Repository) ListHistoricalGroups(ctx context.Context, planID int64, limit, offset int32) ([]groupopsport.HistoricalGroup, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id,source_group_id,source_plan_id,plan_id,chat_reference,display_name,owner_staff_id,internal_member_count,external_member_count,original_status,created_at,removed_at FROM group_ops_v1_history_groups WHERE plan_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, planID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]groupopsport.HistoricalGroup, 0)
	for rows.Next() {
		var item groupopsport.HistoricalGroup
		var ownerID pgtype.Int8
		var createdAt, removedAt pgtype.Timestamptz
		if err = rows.Scan(&item.ID, &item.SourceGroupID, &item.SourcePlanID, &item.PlanID, &item.ChatReference, &item.DisplayName, &ownerID, &item.InternalMemberCount, &item.ExternalMemberCount, &item.OriginalStatus, &createdAt, &removedAt); err != nil {
			return nil, 0, err
		}
		if !createdAt.Valid {
			return nil, 0, ErrInvalid
		}
		item.OwnerStaffID, item.RemovedAt, item.CreatedAt = nullableInt64(ownerID), nullableTime(removedAt), createdAt.Time
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_v1_history_groups WHERE plan_id=$1`, planID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *Repository) ListHistoricalNodes(ctx context.Context, planID int64, limit, offset int32) ([]groupopsport.HistoricalNode, int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id,source_node_id,source_plan_id,plan_id,day_index,trigger_time,sort_order,original_status,content_package,created_at,updated_at FROM group_ops_v1_history_nodes WHERE plan_id=$1 ORDER BY sort_order,id LIMIT $2 OFFSET $3`, planID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]groupopsport.HistoricalNode, 0)
	for rows.Next() {
		var item groupopsport.HistoricalNode
		var raw []byte
		var createdAt, updatedAt pgtype.Timestamptz
		if err = rows.Scan(&item.ID, &item.SourceNodeID, &item.SourcePlanID, &item.PlanID, &item.DayIndex, &item.TriggerTime, &item.SortOrder, &item.OriginalStatus, &raw, &createdAt, &updatedAt); err != nil {
			return nil, 0, err
		}
		if !createdAt.Valid || !updatedAt.Valid || !json.Valid(raw) {
			return nil, 0, ErrInvalid
		}
		var content map[string]json.RawMessage
		if json.Unmarshal(raw, &content) != nil || content == nil {
			return nil, 0, ErrInvalid
		}
		item.ContentPackage = append(json.RawMessage(nil), raw...)
		item.CreatedAt, item.UpdatedAt = createdAt.Time, updatedAt.Time
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM group_ops_v1_history_nodes WHERE plan_id=$1`, planID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanHistoricalPlan(rows pgx.Rows) (groupopsport.HistoricalPlan, error) {
	var item groupopsport.HistoricalPlan
	var ownerID pgtype.Int8
	var archivedAt pgtype.Timestamptz
	if err := rows.Scan(&item.Plan.ID, &item.Plan.Name, &item.Plan.Status, &item.Plan.Revision, &item.Plan.CreatedBy, &item.Plan.UpdatedBy, &item.Plan.CreatedAt, &item.Plan.UpdatedAt, &item.SourcePlanID, &item.SourceCode, &item.PlanType, &item.OriginalStatus, &ownerID, &archivedAt); err != nil {
		return groupopsport.HistoricalPlan{}, err
	}
	item.OwnerStaffID, item.ArchivedAt = nullableInt64(ownerID), nullableTime(archivedAt)
	return item, nil
}

func nullableInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
func nullableInt32(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	result := value.Int32
	return &result
}
func nullableText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

var _ groupopsport.ExecutionTargetOwnerResolver = (*Repository)(nil)
var _ groupopsport.GroupMessageReceiptReader = (*Repository)(nil)
var _ groupopsport.RuntimeStore = (*Repository)(nil)
var _ groupopsport.WebhookReplayStore = (*Repository)(nil)
var _ groupopsapp.Store = (*Repository)(nil)
var _ groupopsport.HistoricalReader = (*Repository)(nil)

// Keep pgconn in the package's dependency graph for callers that want to
// classify unique-key races without importing a concrete database elsewhere.
func IsUnique(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

var _ = groupopsdomain.ValidatePlan

// CreateExecutionIntents persists every frozen node before any provider effect
// is accepted. The first message per group is immediately eligible; later
// nodes wait for the preceding message task acceptance.
func (r *Repository) CreateExecutionIntents(ctx context.Context, drafts []groupopsport.ExecutionDraft) ([]groupopsport.ExecutionIntent, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	ordered := append([]groupopsport.ExecutionDraft(nil), drafts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TargetReference == ordered[j].TargetReference {
			return ordered[i].NodePosition < ordered[j].NodePosition
		}
		return ordered[i].TargetReference < ordered[j].TargetReference
	})
	previous := map[string]int64{}
	items := make([]groupopsport.ExecutionIntent, 0, len(ordered))
	for _, d := range ordered {
		state := "waiting"
		var predecessor any
		if previous[d.TargetReference] == 0 {
			state = "ready_to_accept"
			predecessor = nil
		} else {
			predecessor = previous[d.TargetReference]
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO group_ops_execution_intents(run_id,plan_id,node_id,plan_revision,node_position,target_reference,sender_userid_snapshot,target_digest,content_snapshot,content_digest,material_snapshot,material_digest,execution_key_digest,predecessor_intent_id,state,scheduled_for,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17) ON CONFLICT(run_id,node_id,target_reference) DO UPDATE SET id=group_ops_execution_intents.id RETURNING id`, d.RunID, d.PlanID, d.NodeID, d.PlanRevision, d.NodePosition, d.TargetReference, d.SenderUserID, d.TargetDigest, d.ContentSnapshot, d.ContentDigest, d.MaterialSnapshot, d.MaterialDigest, d.ExecutionKeyDigest[:], predecessor, state, d.ScheduledFor, d.CreatedAt).Scan(&id)
		if err != nil {
			return nil, err
		}
		previous[d.TargetReference] = id
		items = append(items, groupopsport.ExecutionIntent{ID: id, NodeID: d.NodeID, NodePosition: d.NodePosition, TargetReference: d.TargetReference, ScheduledFor: d.ScheduledFor, State: groupopsport.ExecutionIntentState(state)})
	}
	return items, nil
}

func (r *Repository) InitialExecutionIntents(ctx context.Context, runID int64) ([]groupopsport.ExecutionDraft, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,run_id,plan_id,plan_revision,node_id,node_position,target_reference,sender_userid_snapshot,target_digest,content_snapshot,content_digest,material_snapshot,material_digest,execution_key_digest,scheduled_for,created_at FROM group_ops_execution_intents WHERE run_id=$1 AND predecessor_intent_id IS NULL AND state='ready_to_accept' ORDER BY target_reference,node_position,id FOR UPDATE`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIntentDrafts(rows)
}

func (r *Repository) ClaimNextExecutionIntent(ctx context.Context, effectRef string) (groupopsport.ExecutionDraft, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return groupopsport.ExecutionDraft{}, false, err
	}
	effectID, err := parseEffectID(effectRef)
	if err != nil {
		return groupopsport.ExecutionDraft{}, false, err
	}
	rows, err := tx.Query(ctx, `SELECT child.id,child.run_id,child.plan_id,child.plan_revision,child.node_id,child.node_position,child.target_reference,child.sender_userid_snapshot,child.target_digest,child.content_snapshot,child.content_digest,child.material_snapshot,child.material_digest,child.execution_key_digest,child.scheduled_for,child.created_at FROM group_ops_execution_intents parent JOIN group_ops_executions execution ON execution.external_effect_id=$1 JOIN group_ops_execution_intents child ON child.predecessor_intent_id=parent.id WHERE parent.run_id=execution.run_id AND parent.target_reference=execution.target_reference AND parent.node_id=execution.node_id AND parent.state='accepted' AND execution.state='provider_accepted' AND child.state='waiting' ORDER BY child.node_position,child.id FOR UPDATE OF child`, effectID)
	if err != nil {
		return groupopsport.ExecutionDraft{}, false, err
	}
	defer rows.Close()
	items, err := scanIntentDrafts(rows)
	if err != nil {
		return groupopsport.ExecutionDraft{}, false, err
	}
	if len(items) == 0 {
		return groupopsport.ExecutionDraft{}, false, nil
	}
	if len(items) != 1 {
		return groupopsport.ExecutionDraft{}, false, ErrConflict
	}
	updated, updateErr := tx.Exec(ctx, `UPDATE group_ops_execution_intents SET state='ready_to_accept',updated_at=clock_timestamp() WHERE id=$1 AND state='waiting'`, items[0].IntentID)
	if updateErr != nil {
		return groupopsport.ExecutionDraft{}, false, updateErr
	}
	if updated.RowsAffected() != 1 {
		return groupopsport.ExecutionDraft{}, false, ErrConflict
	}
	return items[0], true, nil
}

func (r *Repository) BindAcceptedExecutionIntent(ctx context.Context, intentID int64, effectRef string) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	effectID, err := parseEffectID(effectRef)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE group_ops_execution_intents SET state='accepted',external_effect_id=$2,updated_at=clock_timestamp() WHERE id=$1 AND state='ready_to_accept'`, intentID, effectID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) HaltExecutionIntent(ctx context.Context, intentID int64) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE group_ops_execution_intents SET state='halted',updated_at=clock_timestamp() WHERE id=$1 AND state='ready_to_accept'`, intentID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func scanIntentDrafts(rows pgx.Rows) ([]groupopsport.ExecutionDraft, error) {
	items := []groupopsport.ExecutionDraft{}
	for rows.Next() {
		var d groupopsport.ExecutionDraft
		var key []byte
		if err := rows.Scan(&d.IntentID, &d.RunID, &d.PlanID, &d.PlanRevision, &d.NodeID, &d.NodePosition, &d.TargetReference, &d.SenderUserID, &d.TargetDigest, &d.ContentSnapshot, &d.ContentDigest, &d.MaterialSnapshot, &d.MaterialDigest, &key, &d.ScheduledFor, &d.CreatedAt); err != nil {
			return nil, err
		}
		if len(key) != sha256.Size {
			return nil, ErrInvalid
		}
		copy(d.ExecutionKeyDigest[:], key)
		items = append(items, d)
	}
	return items, rows.Err()
}
