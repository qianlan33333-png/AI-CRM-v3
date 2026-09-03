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
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

var (
	ErrInvalid  = errors.New("invalid tag catalog persistence command")
	ErrNotFound = tagport.ErrNotFound
	ErrConflict = tagport.ErrConflict
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "tag.catalog.group.order:"+strconv.FormatInt(groupID, 10)); err != nil {
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
	err = tx.QueryRow(ctx, `UPDATE tag_catalog_tags t SET tag_name=$2,version=t.version+1,updated_at=clock_timestamp() FROM tag_groups g WHERE t.group_id=g.id AND t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL RETURNING t.id,t.group_id,g.group_name,t.tag_name,t.sort_order`, id, name).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
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
	err = tx.QueryRow(ctx, `UPDATE tag_catalog_tags t SET archived_at=clock_timestamp(),version=t.version+1,updated_at=clock_timestamp() FROM tag_groups g WHERE t.group_id=g.id AND t.id=$1 AND t.archived_at IS NULL AND g.archived_at IS NULL RETURNING t.id,t.group_id,g.group_name,t.tag_name,t.sort_order`, id).Scan(&v.ID, &v.GroupID, &v.GroupName, &v.Name, &v.SortOrder)
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
	rows, err := tx.Query(ctx, `SELECT t.id,t.group_id FROM tag_catalog_tags t JOIN tag_groups g ON g.id=t.group_id WHERE t.archived_at IS NULL AND g.archived_at IS NULL ORDER BY g.sort_order,g.id,t.sort_order,t.id FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := []int64{}
	groupByID := map[int64]int64{}
	for rows.Next() {
		var id, groupID int64
		if err = rows.Scan(&id, &groupID); err != nil {
			return nil, err
		}
		current = append(current, id)
		groupByID[id] = groupID
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !domain.SameIDSet(current, ids) {
		return nil, ErrConflict
	}
	// The frozen catalog order is group-major. A reorder may permute tags only
	// within their current group; accepting an arbitrary global permutation
	// would silently claim an order ListTags can never project.
	for index, id := range ids {
		if groupByID[id] != groupByID[current[index]] {
			return nil, ErrConflict
		}
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
func (r *Repository) CompleteProviderSync(ctx context.Context, observation tagport.SyncCompletion) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	if observation.State == "" {
		observation.State = tagport.SyncExecuted
	}
	if observation.EffectID < 1 || observation.Generation < 1 || !validSyncCompletionState(observation.State) {
		return ErrInvalid
	}
	if observation.State != tagport.SyncExecuted {
		var receiptID, actor int64
		updateErr := tx.QueryRow(ctx, `UPDATE tag_sync_receipts SET state=$2,completed_at=CASE WHEN $2 IN ('final_failed','cancelled','reconciled') THEN clock_timestamp() ELSE NULL END WHERE effect_id=$1 AND state IN ('reserved','queued','outcome_unknown','retryable_failed') RETURNING id,actor_admin_user_id`, observation.EffectID, observation.State).Scan(&receiptID, &actor)
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return ErrConflict
		}
		if updateErr != nil {
			return updateErr
		}
		payload, marshalErr := json.Marshal(map[string]any{"actor": actor, "receipt_id": receiptID, "effect_id": observation.EffectID, "generation": observation.Generation, "state": observation.State})
		if marshalErr != nil {
			return marshalErr
		}
		_, updateErr = r.Append(ctx, tagport.Event{Type: "tag.catalog_sync_state_changed", Payload: payload, OccurredAt: time.Now().UTC(), IdempotencyKey: "tag-catalog-sync-state:" + strconv.FormatInt(observation.EffectID, 10) + ":" + strconv.FormatInt(observation.Generation, 10) + ":" + string(observation.State)})
		return updateErr
	}
	if !validProviderObservation(observation) {
		return ErrInvalid
	}
	var receiptID, actor int64
	err = tx.QueryRow(ctx, `SELECT id,actor_admin_user_id FROM tag_sync_receipts WHERE effect_id=$1 FOR UPDATE`, observation.EffectID).Scan(&receiptID, &actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var existingDigest string
	var existingSnapshot []byte
	err = tx.QueryRow(ctx, `SELECT artifact_digest,snapshot::text FROM tag_provider_observations WHERE effect_id=$1 AND generation=$2 FOR UPDATE`, observation.EffectID, observation.Generation).Scan(&existingDigest, &existingSnapshot)
	if err == nil {
		if existingDigest == observation.ArtifactDigest && canonicalProviderBytes(existingSnapshot) == string(observation.Snapshot) {
			var state string
			if scanErr := tx.QueryRow(ctx, `SELECT state FROM tag_sync_receipts WHERE id=$1`, receiptID).Scan(&state); scanErr != nil {
				return scanErr
			}
			if state == string(tagport.SyncReceiptExecuted) {
				return nil
			}
			return ErrConflict
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tag_provider_observations(effect_id,generation,artifact_digest,snapshot) VALUES($1,$2,$3,$4::jsonb)`, observation.EffectID, observation.Generation, observation.ArtifactDigest, observation.Snapshot); err != nil {
		return err
	}
	var snapshot providerSnapshot
	if err = json.Unmarshal(observation.Snapshot, &snapshot); err != nil || snapshot.Groups == nil {
		return ErrInvalid
	}
	groupCount, tagCount, err := r.projectProviderSnapshot(ctx, tx, *snapshot.Groups)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE tag_sync_receipts SET state='executed',group_count=$2,tag_count=$3,completed_at=clock_timestamp() WHERE id=$1 AND state IN ('reserved','queued','outcome_unknown','retryable_failed')`, receiptID, groupCount, tagCount)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	payload, err := json.Marshal(map[string]any{"actor": actor, "receipt_id": receiptID, "effect_id": observation.EffectID, "generation": observation.Generation, "artifact_digest": observation.ArtifactDigest, "group_count": groupCount, "tag_count": tagCount, "state": tagport.SyncExecuted})
	if err != nil {
		return err
	}
	_, err = r.Append(ctx, tagport.Event{Type: "tag.catalog_sync_completed", Payload: payload, OccurredAt: time.Now().UTC(), IdempotencyKey: "tag-catalog-sync-completed:" + strconv.FormatInt(observation.EffectID, 10) + ":" + strconv.FormatInt(observation.Generation, 10)})
	return err
}

func validSyncCompletionState(state tagport.SyncState) bool {
	switch state {
	case tagport.SyncQueued, tagport.SyncExecuted, tagport.SyncOutcomeUnknown, tagport.SyncRetryableFailed, tagport.SyncFinalFailed, tagport.SyncCancelled, tagport.SyncReconciled:
		return true
	default:
		return false
	}
}

func (r *Repository) projectProviderSnapshot(ctx context.Context, tx pgx.Tx, groups []providerGroup) (int, int, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tag.catalog.provider.projection'))`); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE tag_groups,tag_catalog_tags,tag_references,tag_provider_group_bindings,tag_provider_tag_bindings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `WITH boundary AS (SELECT COALESCE(max(sort_order),-1)+1 AS base FROM tag_groups WHERE archived_at IS NULL), ranked AS (SELECT id,(SELECT base FROM boundary)+row_number() OVER (ORDER BY sort_order,id)-1 AS next_order FROM tag_groups WHERE archived_at IS NULL) UPDATE tag_groups item SET sort_order=ranked.next_order::integer FROM ranked WHERE item.id=ranked.id`); err != nil {
		return 0, 0, err
	}
	groupBindings := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT provider_group_id,group_id FROM tag_provider_group_bindings`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var providerID string
		var id int64
		if err = rows.Scan(&providerID, &id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		groupBindings[providerID] = id
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	seenGroups := map[string]struct{}{}
	for order, group := range groups {
		seenGroups[group.ID] = struct{}{}
		id := groupBindings[group.ID]
		if id == 0 {
			if err = tx.QueryRow(ctx, `INSERT INTO tag_groups(group_name,sort_order) VALUES($1,$2) RETURNING id`, group.Name, order).Scan(&id); err != nil {
				return 0, 0, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO tag_provider_group_bindings(provider_group_id,group_id) VALUES($1,$2)`, group.ID, id); err != nil {
				return 0, 0, err
			}
			groupBindings[group.ID] = id
		} else if _, err = tx.Exec(ctx, `UPDATE tag_groups SET group_name=$2,sort_order=$3,archived_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, group.Name, order); err != nil {
			return 0, 0, err
		}
	}
	rows, err = tx.Query(ctx, `SELECT item.id FROM tag_groups item LEFT JOIN tag_provider_group_bindings binding ON binding.group_id=item.id WHERE item.archived_at IS NULL AND binding.group_id IS NULL ORDER BY item.sort_order,item.id`)
	if err != nil {
		return 0, 0, err
	}
	localGroups := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		localGroups = append(localGroups, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	for index, id := range localGroups {
		if _, err = tx.Exec(ctx, `UPDATE tag_groups SET sort_order=$2,updated_at=clock_timestamp() WHERE id=$1`, id, len(groups)+index); err != nil {
			return 0, 0, err
		}
	}
	if _, err = tx.Exec(ctx, `WITH boundary AS (SELECT group_id,COALESCE(max(sort_order),-1)+1 AS base FROM tag_catalog_tags WHERE archived_at IS NULL GROUP BY group_id), ranked AS (SELECT item.id,boundary.base+row_number() OVER (PARTITION BY item.group_id ORDER BY item.sort_order,item.id)-1 AS next_order FROM tag_catalog_tags item JOIN boundary USING(group_id) WHERE item.archived_at IS NULL) UPDATE tag_catalog_tags item SET sort_order=ranked.next_order::integer FROM ranked WHERE item.id=ranked.id`); err != nil {
		return 0, 0, err
	}
	tagBindings := map[string]int64{}
	rows, err = tx.Query(ctx, `SELECT provider_tag_id,tag_id FROM tag_provider_tag_bindings`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var providerID string
		var id int64
		if err = rows.Scan(&providerID, &id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		tagBindings[providerID] = id
	}
	rows.Close()
	seenTags := map[string]struct{}{}
	providerCountByGroup := map[int64]int{}
	tagCount := 0
	for _, group := range groups {
		groupID := groupBindings[group.ID]
		for order, tag := range group.Tags {
			tagCount++
			seenTags[tag.ID] = struct{}{}
			id := tagBindings[tag.ID]
			if id == 0 {
				if err = tx.QueryRow(ctx, `INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES($1,$2,$3) RETURNING id`, groupID, tag.Name, order).Scan(&id); err != nil {
					return 0, 0, err
				}
				if _, err = tx.Exec(ctx, `INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES($1,$2)`, tag.ID, id); err != nil {
					return 0, 0, err
				}
				tagBindings[tag.ID] = id
			} else if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET group_id=$2,tag_name=$3,sort_order=$4,archived_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, groupID, tag.Name, order); err != nil {
				return 0, 0, err
			}
		}
		providerCountByGroup[groupID] = len(group.Tags)
	}
	for providerID, id := range tagBindings {
		if _, ok := seenTags[providerID]; ok {
			continue
		}
		var references int64
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM tag_references WHERE resource_kind='tag' AND resource_id=$1`, id).Scan(&references); err != nil {
			return 0, 0, err
		}
		if references != 0 {
			return 0, 0, ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET archived_at=COALESCE(archived_at,clock_timestamp()),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
			return 0, 0, err
		}
	}
	rows, err = tx.Query(ctx, `SELECT item.id,item.group_id FROM tag_catalog_tags item LEFT JOIN tag_provider_tag_bindings binding ON binding.tag_id=item.id WHERE item.archived_at IS NULL AND binding.tag_id IS NULL ORDER BY item.group_id,item.sort_order,item.id`)
	if err != nil {
		return 0, 0, err
	}
	type localTag struct{ id, groupID int64 }
	localTags := []localTag{}
	for rows.Next() {
		var id, groupID int64
		if err = rows.Scan(&id, &groupID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		localTags = append(localTags, localTag{id: id, groupID: groupID})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	localTagOffset := map[int64]int{}
	for _, tag := range localTags {
		order := providerCountByGroup[tag.groupID] + localTagOffset[tag.groupID]
		localTagOffset[tag.groupID]++
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET sort_order=$2,updated_at=clock_timestamp() WHERE id=$1`, tag.id, order); err != nil {
			return 0, 0, err
		}
	}
	for providerID, id := range groupBindings {
		if _, ok := seenGroups[providerID]; ok {
			continue
		}
		var references, localChildren int64
		if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM tag_references WHERE resource_kind='group' AND resource_id=$1)+(SELECT count(*) FROM tag_references reference JOIN tag_catalog_tags child ON child.id=reference.resource_id WHERE reference.resource_kind='tag' AND child.group_id=$1), (SELECT count(*) FROM tag_catalog_tags child LEFT JOIN tag_provider_tag_bindings binding ON binding.tag_id=child.id WHERE child.group_id=$1 AND child.archived_at IS NULL AND binding.tag_id IS NULL)`, id).Scan(&references, &localChildren); err != nil {
			return 0, 0, err
		}
		if references != 0 || localChildren != 0 {
			return 0, 0, ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_catalog_tags SET archived_at=COALESCE(archived_at,clock_timestamp()),version=version+1,updated_at=clock_timestamp() WHERE group_id=$1`, id); err != nil {
			return 0, 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE tag_groups SET archived_at=COALESCE(archived_at,clock_timestamp()),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
			return 0, 0, err
		}
	}
	var activeTags int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM tag_catalog_tags item JOIN tag_groups parent ON parent.id=item.group_id WHERE item.archived_at IS NULL AND parent.archived_at IS NULL`).Scan(&activeTags); err != nil {
		return 0, 0, err
	}
	if activeTags > 1000 {
		return 0, 0, ErrConflict
	}
	return len(groups), tagCount, nil
}

type providerSnapshot struct {
	Groups *[]providerGroup `json:"groups"`
}
type providerGroup struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Order int32         `json:"order"`
	Tags  []providerTag `json:"tags"`
}
type providerTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int32  `json:"order"`
}

// validProviderObservation repeats the successful snapshot schema at the
// owned persistence boundary. The sink is intentionally not trusted: both
// digest and canonical snapshot bytes must be independently reproducible here.
func validProviderObservation(observation tagport.SyncCompletion) bool {
	if len(observation.Snapshot) == 0 || len(observation.Snapshot) > 256<<10 || len(observation.ArtifactDigest) != 71 || !strings.HasPrefix(observation.ArtifactDigest, "sha256:") {
		return false
	}
	var snapshot providerSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(observation.Snapshot)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(&struct{}{}) != io.EOF || snapshot.Groups == nil || len(*snapshot.Groups) > 1000 {
		return false
	}
	tags := map[string]struct{}{}
	groups := map[string]struct{}{}
	count := 0
	for _, group := range *snapshot.Groups {
		if !providerID(group.ID) || !providerName(group.Name) {
			return false
		}
		if _, exists := groups[group.ID]; exists {
			return false
		}
		groups[group.ID] = struct{}{}
		for _, tag := range group.Tags {
			count++
			if count > 10000 || !providerID(tag.ID) || !providerName(tag.Name) {
				return false
			}
			if _, exists := tags[tag.ID]; exists {
				return false
			}
			tags[tag.ID] = struct{}{}
		}
	}
	canonical, err := canonicalProviderSnapshot(snapshot)
	if err != nil || string(canonical) != string(observation.Snapshot) {
		return false
	}
	return observation.ArtifactDigest == providerArtifactDigest(canonical)
}

func canonicalProviderBytes(raw []byte) string {
	var snapshot providerSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	canonical, err := canonicalProviderSnapshot(snapshot)
	if err != nil {
		return ""
	}
	return string(canonical)
}

func canonicalProviderSnapshot(snapshot providerSnapshot) ([]byte, error) {
	if snapshot.Groups == nil {
		return nil, errors.New("provider snapshot groups must be explicit")
	}
	sort.SliceStable(*snapshot.Groups, func(i, j int) bool {
		if (*snapshot.Groups)[i].Order == (*snapshot.Groups)[j].Order {
			return (*snapshot.Groups)[i].ID < (*snapshot.Groups)[j].ID
		}
		return (*snapshot.Groups)[i].Order < (*snapshot.Groups)[j].Order
	})
	for i := range *snapshot.Groups {
		if (*snapshot.Groups)[i].Tags == nil {
			(*snapshot.Groups)[i].Tags = []providerTag{}
		}
		sort.SliceStable((*snapshot.Groups)[i].Tags, func(a, b int) bool {
			if (*snapshot.Groups)[i].Tags[a].Order == (*snapshot.Groups)[i].Tags[b].Order {
				return (*snapshot.Groups)[i].Tags[a].ID < (*snapshot.Groups)[i].Tags[b].ID
			}
			return (*snapshot.Groups)[i].Tags[a].Order < (*snapshot.Groups)[i].Tags[b].Order
		})
	}
	return json.Marshal(snapshot)
}

func providerID(value string) bool {
	return providerRequiredText(value, 128)
}
func providerName(value string) bool {
	return providerRequiredText(value, 200)
}
func providerRequiredText(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func providerArtifactDigest(payload []byte) string {
	sum := sha256.Sum256([]byte("external-effect.artifact.v1\x00wecom.tag_catalog.snapshot.v1\x00" + string(payload)))
	return "sha256:" + hex.EncodeToString(sum[:])
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "tag_sync_receipts_single_active" {
			return tagport.SyncReceipt{}, tagport.ErrSyncInProgress
		}
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

func (r *Repository) LatestSync(ctx context.Context) (tagport.SyncStatus, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return tagport.SyncStatus{}, err
	}
	var status tagport.SyncStatus
	var state string
	err = tx.QueryRow(ctx, `SELECT id,effect_ref,state,group_count,tag_count,accepted_at,completed_at FROM tag_sync_receipts ORDER BY id DESC LIMIT 1`).Scan(&status.ReceiptID, &status.EffectID, &state, &status.GroupCount, &status.TagCount, &status.AcceptedAt, &status.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tagport.SyncStatus{State: tagport.SyncIdle}, nil
	}
	if err != nil {
		return tagport.SyncStatus{}, err
	}
	status.State = tagport.SyncState(state)
	switch status.State {
	case tagport.SyncQueued, tagport.SyncOutcomeUnknown, tagport.SyncRetryableFailed:
		status.Active = true
	}
	return status, nil
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
