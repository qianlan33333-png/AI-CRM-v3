package externaleffects

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river"
)

// Repository owns only the opaque external-effects tables. It never writes a
// customer, recipient, provider credential, or provider response.
type Repository struct {
	pool  *pgxpool.Pool
	river *river.Client[pgx.Tx]
	now   func() time.Time
}

func NewRepository(pool *pgxpool.Pool, client *river.Client[pgx.Tx]) (*Repository, error) {
	if pool == nil || client == nil {
		return nil, platformjobqueue.ErrUnavailable
	}
	return &Repository{pool: pool, river: client, now: time.Now}, nil
}

func effectID(id int64) string { return "eer_" + strconv.FormatInt(id, 10) }
func parseEffectID(value string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(value, "eer_%d", &id); err != nil || id < 1 {
		return 0, ErrInvalid
	}
	return id, nil
}
func projection(id int64, owner Owner, kind Kind, state State, attempts int32, generation int64, updated time.Time) Projection {
	return Projection{ID: effectID(id), Owner: owner, Kind: kind, State: state, AttemptCount: attempts, Generation: generation, UpdatedAt: updated.UTC()}
}

func (r *Repository) AcceptAndQueue(ctx context.Context, command AcceptCommand) (Projection, Receipt, error) {
	if r == nil || r.pool == nil || !command.Valid() {
		return Projection{}, Receipt{}, ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	defer tx.Rollback(ctx)
	var id int64
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT id,owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE envelope_fingerprint=$1 FOR UPDATE`, command.Envelope.Fingerprint()).Scan(&id, &owner, &kind, &state, &attempts, &generation, &updated)
	if err == nil {
		var rid int64
		var digest, receiptState string
		var completed time.Time
		receiptErr := tx.QueryRow(ctx, `SELECT id,command_digest,state,completed_at FROM external_effect_operation_receipts WHERE operation='accept' AND effect_id=$1 AND receipt_key_digest=$2`, id, command.ReceiptKey).Scan(&rid, &digest, &receiptState, &completed)
		if receiptErr != nil || Digest(digest) != command.Digest() {
			return Projection{}, Receipt{}, ErrPayloadMismatch
		}
		return commitProjection(tx, projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated), Receipt{ID: "eerop_" + strconv.FormatInt(rid, 10), EffectID: effectID(id), CommandDigest: Digest(digest), State: State(receiptState), CompletedAt: completed})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO external_effects (owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES ($1,$2,$3,$4,$5,$6,$7,'accepted') RETURNING id,created_at`, command.Envelope.Owner, command.Envelope.Kind, command.Envelope.SourceRefDigest, command.Envelope.TargetRefDigest, command.Envelope.PayloadDigest, command.Envelope.PolicyVersionHash, command.Envelope.Fingerprint()).Scan(&id, &updated)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state) VALUES ('accept',$1,$2,$3,'accepted')`, id, command.ReceiptKey, command.Digest()); err != nil {
		return Projection{}, Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES ($1,1)`, id); err != nil {
		return Projection{}, Receipt{}, err
	}
	inserted, err := platformjobqueue.InsertTx(ctx, r.river, tx, EffectJobArgs{EffectID: id, Generation: 1})
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	queueKey := Hash("queue", effectID(id))
	queueDigest := Hash("queue", effectID(id), strconv.FormatInt(inserted.Job.ID, 10))
	if _, err = tx.Exec(ctx, `UPDATE external_effects SET state='queued',updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return Projection{}, Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_jobs(effect_id,generation,river_job_id,queue,args_digest,scheduled_at) VALUES ($1,1,$2,'outbound',$3,clock_timestamp())`, id, inserted.Job.ID, Hash("river-args", effectID(id), "1")); err != nil {
		return Projection{}, Receipt{}, err
	}
	var receiptID int64
	var completed time.Time
	err = tx.QueryRow(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state) VALUES ('queue',$1,$2,$3,'queued') RETURNING id,completed_at`, id, queueKey, queueDigest).Scan(&receiptID, &completed)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	var final time.Time
	if err = tx.QueryRow(ctx, `SELECT updated_at FROM external_effects WHERE id=$1`, id).Scan(&final); err != nil {
		return Projection{}, Receipt{}, err
	}
	return commitProjection(tx, projection(id, command.Envelope.Owner, command.Envelope.Kind, StateQueued, 0, 1, final), Receipt{ID: "eerop_" + strconv.FormatInt(receiptID, 10), EffectID: effectID(id), CommandDigest: queueDigest, State: StateQueued, CompletedAt: completed})
}

func commitProjection(tx pgx.Tx, p Projection, receipt Receipt) (Projection, Receipt, error) {
	if err := tx.Commit(context.Background()); err != nil {
		return Projection{}, Receipt{}, err
	}
	return p, receipt, nil
}

func (r *Repository) List(ctx context.Context, limit int) ([]Projection, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `SELECT id,owner,kind,state,attempt_count,generation,updated_at FROM external_effects ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Projection{}
	for rows.Next() {
		var id int64
		var owner, kind, state string
		var attempts int32
		var generation int64
		var updated time.Time
		if err = rows.Scan(&id, &owner, &kind, &state, &attempts, &generation, &updated); err != nil {
			return nil, err
		}
		out = append(out, projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated))
	}
	return out, rows.Err()
}
func (r *Repository) Get(ctx context.Context, id string) (Projection, error) {
	numeric, err := parseEffectID(id)
	if err != nil {
		return Projection{}, err
	}
	var owner, kind, state string
	var attempts int32
	var generation int64
	var updated time.Time
	err = r.pool.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,updated_at FROM external_effects WHERE id=$1`, numeric).Scan(&owner, &kind, &state, &attempts, &generation, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, ErrNotFound
	}
	return projection(numeric, Owner(owner), Kind(kind), State(state), attempts, generation, updated), err
}
func (r *Repository) Diagnostics(ctx context.Context) (map[string]int64, error) {
	var accepted, queued, attempted, unknown, retryable int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='accepted'),count(*) FILTER (WHERE state='queued'),count(*) FILTER (WHERE state='attempted'),count(*) FILTER (WHERE state='outcome_unknown'),count(*) FILTER (WHERE state='retryable_failed') FROM external_effects`).Scan(&accepted, &queued, &attempted, &unknown, &retryable)
	return map[string]int64{"accepted": accepted, "queued": queued, "attempted": attempted, "outcome_unknown": unknown, "retryable_failed": retryable}, err
}

func (r *Repository) control(ctx context.Context, command ControlCommand, operation string) (Projection, Receipt, error) {
	if !command.Valid() || (operation == "reconcile" && !ValidDigest(command.EvidenceDigest)) {
		return Projection{}, Receipt{}, ErrInvalid
	}
	id, err := parseEffectID(command.EffectID)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	defer tx.Rollback(ctx)
	var owner, kind, state string
	var attempts int32
	var generation, fence int64
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT owner,kind,state,attempt_count,generation,lease_fence,updated_at FROM external_effects WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &kind, &state, &attempts, &generation, &fence, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, ErrNotFound
	}
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	digest := command.Digest(operation)
	var rid int64
	var oldDigest, oldState string
	var completed time.Time
	receiptErr := tx.QueryRow(ctx, `SELECT id,command_digest,state,completed_at FROM external_effect_operation_receipts WHERE operation=$1 AND effect_id=$2 AND receipt_key_digest=$3`, operation, id, command.ReceiptKey).Scan(&rid, &oldDigest, &oldState, &completed)
	if receiptErr == nil {
		if Digest(oldDigest) != digest {
			return Projection{}, Receipt{}, ErrPayloadMismatch
		}
		return commitProjection(tx, projection(id, Owner(owner), Kind(kind), State(state), attempts, generation, updated), Receipt{ID: "eerop_" + strconv.FormatInt(rid, 10), EffectID: command.EffectID, CommandDigest: Digest(oldDigest), State: State(oldState), CompletedAt: completed})
	}
	if !errors.Is(receiptErr, pgx.ErrNoRows) {
		return Projection{}, Receipt{}, receiptErr
	}
	var next State
	switch operation {
	case "cancel":
		if State(state) != StateAccepted && State(state) != StateQueued {
			return Projection{}, Receipt{}, ErrTransition
		}
		next = StateCancelled
	case "retry":
		if State(state) != StateRetryable {
			return Projection{}, Receipt{}, ErrTransition
		}
		next = StateQueued
		generation++
	case "reconcile":
		if State(state) != StateUnknown {
			return Projection{}, Receipt{}, ErrReconcileRequired
		}
		next = StateReconciled
	default:
		return Projection{}, Receipt{}, ErrInvalid
	}
	if operation == "retry" {
		if _, err = tx.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES($1,$2)`, id, generation); err != nil {
			return Projection{}, Receipt{}, err
		}
		inserted, insertErr := platformjobqueue.InsertTx(ctx, r.river, tx, EffectJobArgs{EffectID: id, Generation: generation})
		if insertErr != nil {
			return Projection{}, Receipt{}, insertErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO external_effect_jobs(effect_id,generation,river_job_id,queue,args_digest,scheduled_at) VALUES ($1,$2,$3,'outbound',$4,clock_timestamp())`, id, generation, inserted.Job.ID, Hash("river-args", command.EffectID, strconv.FormatInt(generation, 10))); err != nil {
			return Projection{}, Receipt{}, err
		}
	}
	if operation == "reconcile" {
		result, updateErr := tx.Exec(ctx, `UPDATE external_effect_attempts SET state='reconciled',evidence_digest=$2,completed_at=clock_timestamp() WHERE effect_id=$1 AND generation=$3 AND fence=$4 AND state='outcome_unknown'`, id, command.EvidenceDigest, generation, fence)
		if updateErr != nil {
			return Projection{}, Receipt{}, updateErr
		}
		if result.RowsAffected() != 1 {
			return Projection{}, Receipt{}, ErrReconcileRequired
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE external_effects SET state=$2,generation=$3,updated_at=clock_timestamp() WHERE id=$1`, id, next, generation); err != nil {
		return Projection{}, Receipt{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO external_effect_operation_receipts(operation,effect_id,receipt_key_digest,command_digest,state) VALUES ($1,$2,$3,$4,$5) RETURNING id,completed_at`, operation, id, command.ReceiptKey, digest, next).Scan(&rid, &completed)
	if err != nil {
		return Projection{}, Receipt{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT updated_at FROM external_effects WHERE id=$1`, id).Scan(&updated); err != nil {
		return Projection{}, Receipt{}, err
	}
	return commitProjection(tx, projection(id, Owner(owner), Kind(kind), next, attempts, generation, updated), Receipt{ID: "eerop_" + strconv.FormatInt(rid, 10), EffectID: command.EffectID, CommandDigest: digest, State: next, CompletedAt: completed})
}
func (r *Repository) Cancel(ctx context.Context, c ControlCommand) (Projection, Receipt, error) {
	return r.control(ctx, c, "cancel")
}
func (r *Repository) Retry(ctx context.Context, c ControlCommand) (Projection, Receipt, error) {
	return r.control(ctx, c, "retry")
}
func (r *Repository) Reconcile(ctx context.Context, c ControlCommand) (Projection, Receipt, error) {
	return r.control(ctx, c, "reconcile")
}

// RunLocalAttempt records attempted before the provider boundary. With the
// default-disabled provider it deterministically ends final_failed and makes no
// external call. A future outbound adapter may replace only that post-commit boundary.
// RunAttempt first atomically proves this exact River job owns the queued
// generation, records attempted, commits, and only then calls the adapter.
// A nil adapter is the default-disabled provider: it final-fails locally
// without a provider call. A transport error is always outcome_unknown.
func (r *Repository) RunAttempt(ctx context.Context, id, generation, riverJobID int64, adapter ProviderAdapter) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state string
	var attempts int32
	var fence int64
	err = tx.QueryRow(ctx, `SELECT effect.state,effect.attempt_count,effect.lease_fence FROM external_effects effect JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation WHERE effect.id=$1 AND effect.generation=$2 AND job.river_job_id=$3 FOR UPDATE OF effect`, id, generation, riverJobID).Scan(&state, &attempts, &fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if State(state) != StateQueued {
		return nil
	}
	attempts++
	fence++
	if result, updateErr := tx.Exec(ctx, `UPDATE external_effects SET state='attempted',attempt_count=$2,lease_fence=$3,lease_expires_at=clock_timestamp()+interval '5 minutes',updated_at=clock_timestamp() WHERE id=$1 AND generation=$4 AND state='queued'`, id, attempts, fence, generation); updateErr != nil || result.RowsAffected() != 1 {
		if updateErr != nil {
			return updateErr
		}
		return ErrTransition
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state) VALUES($1,$2,$3,$4,'attempted')`, id, attempts, generation, fence); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	var next State
	var receipt Digest
	var callAttempted, realExternalCallExecuted bool
	if adapter == nil {
		next = StateFinalFailed // Provider disabled: no call was attempted.
		receipt = Hash("provider-disabled", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
	} else {
		var owner, kind, source, target, payload, policy string
		if err := r.pool.QueryRow(ctx, `SELECT owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash FROM external_effects WHERE id=$1 AND generation=$2`, id, generation).Scan(&owner, &kind, &source, &target, &payload, &policy); err != nil {
			return err
		}
		result, callErr := adapter.Execute(ctx, Envelope{Owner: Owner(owner), Kind: Kind(kind), SourceRefDigest: Digest(source), TargetRefDigest: Digest(target), PayloadDigest: Digest(payload), PolicyVersionHash: Digest(policy)}, Attempt{Number: attempts, Generation: generation, Fence: fence})
		callAttempted, realExternalCallExecuted = result.CallAttempted, result.RealExternalCallExecuted
		if callErr != nil {
			if result.CallAttempted {
				next = StateUnknown
			} else {
				next = StateRetryable
			}
			receipt = Hash("provider-error", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
		} else if !ValidDigest(result.ReceiptDigest) || (result.RealExternalCallExecuted && !result.CallAttempted) {
			if result.CallAttempted {
				next = StateUnknown
			} else {
				next = StateFinalFailed
			}
			receipt = Hash("provider-invalid", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
		} else if result.Completion == StateExecuted && result.CallAttempted && result.RealExternalCallExecuted {
			next = StateExecuted
			receipt = result.ReceiptDigest
		} else if result.Completion == StateUnknown && result.CallAttempted {
			next = StateUnknown
			receipt = result.ReceiptDigest
		} else if result.Completion == StateRetryable || result.Completion == StateFinalFailed {
			next = result.Completion
			receipt = result.ReceiptDigest
		} else {
			if result.CallAttempted {
				next = StateUnknown
			} else {
				next = StateFinalFailed
			}
			receipt = Hash("provider-invalid", strconv.FormatInt(id, 10), strconv.Itoa(int(attempts)))
		}
	}
	tx, err = r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if result, updateErr := tx.Exec(ctx, `UPDATE external_effect_attempts SET state=$2,receipt_digest=$3,call_attempted=$4,real_external_call_executed=$5,completed_at=clock_timestamp() WHERE effect_id=$1 AND number=$6 AND generation=$7 AND fence=$8 AND state='attempted'`, id, next, receipt, callAttempted, realExternalCallExecuted, attempts, generation, fence); updateErr != nil || result.RowsAffected() != 1 {
		if updateErr != nil {
			return updateErr
		}
		return ErrTransition
	}
	if result, updateErr := tx.Exec(ctx, `UPDATE external_effects SET state=$2,updated_at=clock_timestamp() WHERE id=$1 AND generation=$3 AND lease_fence=$4 AND state='attempted'`, id, next, generation, fence); updateErr != nil || result.RowsAffected() != 1 {
		if updateErr != nil {
			return updateErr
		}
		return ErrTransition
	}
	return tx.Commit(ctx)
}
