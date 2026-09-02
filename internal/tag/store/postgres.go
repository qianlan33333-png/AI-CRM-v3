// Package store owns PostgreSQL persistence for the local WeCom tag catalog.
// It deliberately contains no customer identity, external_userid, credential,
// or provider-call data.  Sync records are durable local intent/receipt facts;
// outbound execution is coordinated outside this owner.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

var (
	ErrInvalid  = errors.New("invalid tag catalog persistence command")
	ErrNotFound = errors.New("tag catalog persistence item not found")
	ErrConflict = errors.New("tag catalog persistence conflict")
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
func (r *Repository) Within(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.uow == nil || fn == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, fn)
}
func transaction(ctx context.Context) (pgx.Tx, error) {
	return platformpostgres.RequireTransaction(ctx)
}

func (r *Repository) ListGroups(ctx context.Context) ([]domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,group_name,sort_order FROM tag_groups WHERE archived_at IS NULL ORDER BY sort_order,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Group{}
	for rows.Next() {
		var v domain.Group
		if err = rows.Scan(&v.ID, &v.Name, &v.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) ListTags(ctx context.Context) ([]domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.group_id,g.group_name,t.tag_name,t.sort_order FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id WHERE t.archived_at IS NULL AND g.archived_at IS NULL ORDER BY g.sort_order,g.id,t.sort_order,t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Tag{}
	for rows.Next() {
		var v domain.Tag
		if err = rows.Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) GetGroup(ctx context.Context, id int64) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `SELECT id,group_name,sort_order FROM tag_groups WHERE id=$1 AND archived_at IS NULL`, id).Scan(&v.ID, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) GetGroupIncludingArchived(ctx context.Context, id int64) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `SELECT id,group_name,sort_order FROM tag_groups WHERE id=$1`, id).Scan(&v.ID, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) GetTag(ctx context.Context, id int64) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `SELECT t.id,t.group_id,g.group_name,t.tag_name,t.sort_order FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id WHERE t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) GetTagIncludingArchived(ctx context.Context, id int64) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `SELECT t.id,t.group_id,g.group_name,t.tag_name,t.sort_order FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id WHERE t.id=$1`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) CreateGroup(ctx context.Context, name string) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tag.catalog.groups.order'))`); err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `INSERT INTO tag_groups(group_name,sort_order) SELECT $1,COALESCE(max(sort_order)+1,0) FROM tag_groups WHERE archived_at IS NULL RETURNING id,group_name,sort_order`, name).Scan(&v.ID, &v.Name, &v.SortOrder)
	return v, err
}
func (r *Repository) CreateTag(ctx context.Context, groupID int64, name string) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tag.catalog.group.order:' || $1::text))`, groupID); err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `WITH parent AS (SELECT id,group_name FROM tag_groups WHERE id=$1 AND archived_at IS NULL FOR KEY SHARE), inserted AS (INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) SELECT id,$2,COALESCE((SELECT max(sort_order)+1 FROM tag_catalog_tags WHERE group_id=$1 AND archived_at IS NULL),0) FROM parent RETURNING id,group_id,tag_name,sort_order) SELECT i.id,i.group_id,p.group_name,i.tag_name,i.sort_order FROM inserted i JOIN parent p ON p.id=i.group_id`, groupID, name).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) UpdateGroup(ctx context.Context, id int64, name string) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `UPDATE tag_groups SET group_name=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND archived_at IS NULL RETURNING id,group_name,sort_order`, id, name).Scan(&v.ID, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) ArchiveGroup(ctx context.Context, id int64) (domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Group{}, err
	}
	var v domain.Group
	err = tx.QueryRow(ctx, `UPDATE tag_groups SET archived_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND archived_at IS NULL RETURNING id,group_name,sort_order`, id).Scan(&v.ID, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET archived_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE group_id=$1 AND archived_at IS NULL`, id); err != nil {
		return domain.Group{}, err
	}
	return v, nil
}
func (r *Repository) UpdateTag(ctx context.Context, id int64, name string) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `UPDATE tag_catalog_tags t SET tag_name=$2,version=version+1,updated_at=clock_timestamp() FROM tag_groups g WHERE t.group_id=g.id AND t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL RETURNING t.id,t.group_id,g.group_name,t.tag_name,t.sort_order`, id, name).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) ArchiveTag(ctx context.Context, id int64) (domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return domain.Tag{}, err
	}
	var v domain.Tag
	err = tx.QueryRow(ctx, `UPDATE tag_catalog_tags t SET archived_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() FROM tag_groups g WHERE t.group_id=g.id AND t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL RETURNING t.id,t.group_id,g.group_name,t.tag_name,t.sort_order`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Tag{}, ErrNotFound
	}
	return v, err
}
func (r *Repository) ReorderGroups(ctx context.Context, ids []int64) ([]domain.Group, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM tag_groups WHERE archived_at IS NULL FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		current = append(current, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(current, ids) {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE tag_groups SET sort_order=sort_order+(SELECT COALESCE(max(sort_order),0)+count(*)+1 FROM tag_groups WHERE archived_at IS NULL),version=version+1,updated_at=clock_timestamp() WHERE archived_at IS NULL`); err != nil {
		return nil, err
	}
	for i, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE tag_groups SET sort_order=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, i); err != nil {
			return nil, err
		}
	}
	return r.ListGroups(ctx)
}
func (r *Repository) ReorderTags(ctx context.Context, ids []int64) ([]domain.Tag, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM tag_catalog_tags WHERE archived_at IS NULL FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		current = append(current, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(current, ids) {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET sort_order=sort_order+(SELECT COALESCE(max(sort_order),0)+count(*)+1 FROM tag_catalog_tags WHERE archived_at IS NULL),version=version+1,updated_at=clock_timestamp() WHERE archived_at IS NULL`); err != nil {
		return nil, err
	}
	for i, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET sort_order=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, i); err != nil {
			return nil, err
		}
	}
	return r.ListTags(ctx)
}
func keyDigest(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }
func (r *Repository) ReserveMutation(ctx context.Context, in tagport.MutationReceiptReservation) (tagport.MutationReceipt, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.MutationReceipt{}, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tag_operation_receipts(operation,actor_admin_user_id,idempotency_key_digest,payload_digest,state) VALUES($1,$2,$3,$4,'in_progress') ON CONFLICT(operation,actor_admin_user_id,idempotency_key_digest) DO NOTHING RETURNING id`, in.Operation, in.Actor, keyDigest(in.IdempotencyKey), in.PayloadDigest).Scan(&id)
	if err == nil {
		return tagport.MutationReceipt{ID: id, Operation: in.Operation, Actor: in.Actor, IdempotencyKey: in.IdempotencyKey, PayloadDigest: append([]byte(nil), in.PayloadDigest...), State: tagport.MutationInProgress}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tagport.MutationReceipt{}, false, err
	}
	var payload []byte
	var state string
	var results []int64
	err = tx.QueryRow(ctx, `SELECT id,payload_digest,state,result_ids FROM tag_operation_receipts WHERE operation=$1 AND actor_admin_user_id=$2 AND idempotency_key_digest=$3`, in.Operation, in.Actor, keyDigest(in.IdempotencyKey)).Scan(&id, &payload, &state, &results)
	if err != nil {
		return tagport.MutationReceipt{}, false, err
	}
	return tagport.MutationReceipt{ID: id, Operation: in.Operation, Actor: in.Actor, IdempotencyKey: in.IdempotencyKey, PayloadDigest: payload, State: tagport.MutationReceiptState(state), ResultIDs: results}, false, nil
}
func (r *Repository) CompleteMutation(ctx context.Context, id int64, ids []int64, _ time.Time) (tagport.MutationReceipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.MutationReceipt{}, err
	}
	var state string
	err = tx.QueryRow(ctx, `UPDATE tag_operation_receipts SET state='completed',result_ids=$2,completed_at=clock_timestamp() WHERE id=$1 AND state='in_progress' RETURNING state`, id, ids).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.MutationReceipt{}, ErrConflict
	}
	return tagport.MutationReceipt{ID: id, State: tagport.MutationReceiptState(state), ResultIDs: append([]int64(nil), ids...)}, err
}
func (r *Repository) Append(ctx context.Context, event tagport.Event) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var actor int64
	var payload map[string]any
	_ = json.Unmarshal(event.Payload, &payload)
	if value, ok := payload["actor"].(float64); ok {
		actor = int64(value)
	}
	if actor < 1 {
		return 0, ErrInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tag_audit_events(event_type,actor_admin_user_id,payload,occurred_at) VALUES($1,$2,$3::jsonb,$4) RETURNING id`, event.Type, actor, event.Payload, event.OccurredAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO tag_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES($1,'tag_catalog',$2,$3::jsonb)`, event.Type, id, event.Payload)
	return id, err
}
func (r *Repository) TagReferences(ctx context.Context, id int64) (int64, error) {
	return r.referenceCount(ctx, "tag", id)
}
func (r *Repository) GroupReferences(ctx context.Context, id int64) (int64, error) {
	return r.referenceCount(ctx, "group", id)
}
func (r *Repository) referenceCount(ctx context.Context, kind string, id int64) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	var n int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM tag_references WHERE resource_kind=$1 AND resource_id=$2`, kind, id).Scan(&n)
	return n, err
}
func (r *Repository) ReserveSync(ctx context.Context, c tagport.SyncCommand) (tagport.SyncReceipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.SyncReceipt{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO tag_sync_receipts(actor_admin_user_id,idempotency_key_digest,trace_id,sync_kind,state) VALUES($1,$2,$3,$4,'reserved') ON CONFLICT(actor_admin_user_id,idempotency_key_digest) DO NOTHING RETURNING id`, c.Actor, keyDigest(c.IdempotencyKey), c.TraceID, c.Kind).Scan(&id)
	if err == nil {
		return tagport.SyncReceipt{ID: id, Command: c, State: tagport.SyncReserved}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tagport.SyncReceipt{}, err
	}
	var trace, kind, state, effectRef, effectState, acceptReceipt, queueReceipt string
	var eventID, jobID, effectID int64
	err = tx.QueryRow(ctx, `SELECT id,trace_id,sync_kind,state,event_id,queue_job_id,effect_id,effect_ref,effect_state,accept_receipt_id,queue_receipt_id FROM tag_sync_receipts WHERE actor_admin_user_id=$1 AND idempotency_key_digest=$2`, c.Actor, keyDigest(c.IdempotencyKey)).Scan(&id, &trace, &kind, &state, &eventID, &jobID, &effectID, &effectRef, &effectState, &acceptReceipt, &queueReceipt)
	if err != nil {
		return tagport.SyncReceipt{}, err
	}
	if trace != c.TraceID || kind != string(c.Kind) {
		return tagport.SyncReceipt{}, ErrConflict
	}
	return tagport.SyncReceipt{ID: id, Command: c, State: tagport.SyncReceiptState(state), EventID: eventID, Effect: tagport.SyncEffectReceipt{QueueJobID: jobID, EffectID: effectID, EffectRef: effectRef, EffectState: effectState, AcceptReceiptID: acceptReceipt, QueueReceiptID: queueReceipt}}, nil
}
func (r *Repository) AcceptSync(ctx context.Context, id, eventID int64, effect tagport.SyncEffectReceipt) (tagport.SyncReceipt, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.SyncReceipt{}, err
	}
	var actor int64
	var trace, kind, state string
	err = tx.QueryRow(ctx, `UPDATE tag_sync_receipts SET event_id=$2,queue_job_id=$3,effect_id=$4,effect_ref=$5,effect_state=$6,accept_receipt_id=$7,queue_receipt_id=$8,state='queued',accepted_at=clock_timestamp() WHERE id=$1 AND state='reserved' RETURNING actor_admin_user_id,trace_id,sync_kind,state`, id, eventID, effect.QueueJobID, effect.EffectID, effect.EffectRef, effect.EffectState, effect.AcceptReceiptID, effect.QueueReceiptID).Scan(&actor, &trace, &kind, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.SyncReceipt{}, ErrConflict
	}
	return tagport.SyncReceipt{ID: id, Command: tagport.SyncCommand{Actor: actor, TraceID: trace, Kind: tagport.SyncKind(kind)}, State: tagport.SyncReceiptState(state), EventID: eventID, Effect: effect}, err
}
func (r *Repository) ReadExecutionStatus(ctx context.Context) (tagport.ExecutionStatus, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.ExecutionStatus{}, err
	}
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	if err != nil {
		return tagport.ExecutionStatus{}, err
	}
	return tagport.ExecutionStatus{Payload: json.RawMessage(`{"mode":"provider_execution_unavailable","accepted":true,"queued":true,"attempted":false,"executed":false,"outcome_unknown":false,"reconciled":false,"real_external_call_executed":false,"sync_executed":false}`), ObservedAt: now.UTC()}, nil
}
func Digest(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
