package customer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrOwnerHandoffConflict = customerport.ErrOwnerHandoffConflict

type LocalOwner = customerport.LocalOwner

type PostgreSQLOwnerHandoffStore struct{ cipher *OwnerHandoffCipher }

func NewPostgreSQLOwnerHandoffStore() *PostgreSQLOwnerHandoffStore {
	return &PostgreSQLOwnerHandoffStore{}
}

func NewPostgreSQLOwnerHandoffStoreWithCipher(cipher *OwnerHandoffCipher) *PostgreSQLOwnerHandoffStore {
	return &PostgreSQLOwnerHandoffStore{cipher: cipher}
}

func (*PostgreSQLOwnerHandoffStore) LocalOwner(ctx context.Context, customerID customerdomain.CustomerID, lock bool) (LocalOwner, bool, error) {
	if customerID < 1 {
		return LocalOwner{}, false, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return LocalOwner{}, false, err
	}
	query := "SELECT customer_id,staff_id,version,source,updated_at FROM customer_local_owners WHERE customer_id=$1"
	if lock {
		query += " FOR UPDATE"
	}
	var owner LocalOwner
	err = tx.QueryRow(ctx, query, customerID).Scan(&owner.CustomerID, &owner.StaffID, &owner.Version, &owner.Source, &owner.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LocalOwner{}, false, nil
	}
	return owner, err == nil, err
}

func (store *PostgreSQLOwnerHandoffStore) AssignLocalOwner(ctx context.Context, customerID customerdomain.CustomerID, targetStaffID, expectedVersion int64, source string, now time.Time) (LocalOwner, error) {
	if customerID < 1 || targetStaffID < 1 || (source != "owner_handoff_local_only" && source != "owner_handoff_wecom_then_crm") {
		return LocalOwner{}, ErrOwnerHandoffConflict
	}
	owner, found, err := store.LocalOwner(ctx, customerID, true)
	if err != nil {
		return LocalOwner{}, err
	}
	// Zero is the frozen version for a preview row with no local owner.  It
	// must never authorize replacing an owner which appeared after preview.
	if found && owner.Version != expectedVersion {
		return LocalOwner{}, ErrOwnerHandoffConflict
	}
	if !found && expectedVersion != 0 {
		return LocalOwner{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return LocalOwner{}, err
	}
	if !found {
		// A concurrent insert after the preview's zero-version read is a normal
		// CAS conflict. ON CONFLICT keeps this transaction usable so the caller
		// can persist its per-line conflict fact instead of receiving 23505.
		err = tx.QueryRow(ctx, "INSERT INTO customer_local_owners(customer_id,staff_id,version,source,updated_at) VALUES($1,$2,1,$3,$4) ON CONFLICT (customer_id) DO NOTHING RETURNING version,updated_at", customerID, targetStaffID, source, now.UTC()).Scan(&owner.Version, &owner.UpdatedAt)
	} else {
		err = tx.QueryRow(ctx, "UPDATE customer_local_owners SET staff_id=$2,version=version+1,source=$3,updated_at=$4 WHERE customer_id=$1 AND version=$5 RETURNING version,updated_at", customerID, targetStaffID, source, now.UTC(), owner.Version).Scan(&owner.Version, &owner.UpdatedAt)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isUniqueViolation(err) {
			return LocalOwner{}, ErrOwnerHandoffConflict
		}
		return LocalOwner{}, err
	}
	owner.CustomerID, owner.StaffID, owner.Source = customerID, targetStaffID, source
	return owner, nil
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

// ReadOwnerHandoffExecution returns only the single immutable line owned by
// the requested effect.  It refuses a line whose stored ciphertext cannot be
// authenticated against its batch/line binding; no current directory lookup
// can substitute a changed provider identity.
func (store *PostgreSQLOwnerHandoffStore) ReadOwnerHandoffExecution(ctx context.Context, effectID string) (customerport.OwnerHandoffExecution, error) {
	if store == nil || store.cipher == nil || effectID == "" {
		return customerport.OwnerHandoffExecution{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffExecution{}, err
	}
	var batchID, previewID, mode, scope string
	var sourceStaffID, targetStaffID int64
	var line int64
	var sourceCipher, targetCipher, externalCipher, welcomeCipher []byte
	var sourceDigest, targetDigest, payloadDigest, policyDigest []byte
	err = tx.QueryRow(ctx, `SELECT line.batch_id,b.preview_id,line.line_no,b.mode,b.corp_scope,b.source_staff_id,b.target_staff_id,line.source_userid_ciphertext,line.target_userid_ciphertext,line.external_identity_ciphertext,line.welcome_message_ciphertext,line.source_userid_digest,line.target_userid_digest,line.payload_digest,line.policy_digest
		FROM customer_owner_handoff_lines line JOIN customer_owner_handoff_batches b ON b.id=line.batch_id
		WHERE line.effect_id=$1 AND line.mode='wecom_then_crm' FOR UPDATE`, effectID).Scan(&batchID, &previewID, &line, &mode, &scope, &sourceStaffID, &targetStaffID, &sourceCipher, &targetCipher, &externalCipher, &welcomeCipher, &sourceDigest, &targetDigest, &payloadDigest, &policyDigest)
	if err != nil {
		return customerport.OwnerHandoffExecution{}, err
	}
	source, err := store.cipher.Open(previewID, line, "source_userid", sourceCipher)
	if err != nil {
		return customerport.OwnerHandoffExecution{}, err
	}
	target, err := store.cipher.Open(previewID, line, "target_userid", targetCipher)
	if err != nil {
		return customerport.OwnerHandoffExecution{}, err
	}
	external, err := store.cipher.Open(previewID, line, "external_userid", externalCipher)
	if err != nil {
		return customerport.OwnerHandoffExecution{}, err
	}
	welcome := ""
	if len(welcomeCipher) > 0 {
		welcome, err = store.cipher.Open(previewID, line, "welcome_message", welcomeCipher)
		if err != nil {
			return customerport.OwnerHandoffExecution{}, err
		}
	}
	if len(sourceDigest) != 32 || len(targetDigest) != 32 || len(payloadDigest) != 32 || len(policyDigest) != 32 ||
		!sameSnapshotDigest(sourceDigest, "source-userid", source) || !sameSnapshotDigest(targetDigest, "target-userid", target) ||
		!sameSnapshotDigest(payloadDigest, "transfer-payload", external, welcome) ||
		!sameSnapshotDigest(policyDigest, "policy", mode, scope, strconv.FormatInt(sourceStaffID, 10), strconv.FormatInt(targetStaffID, 10)) {
		return customerport.OwnerHandoffExecution{}, ErrOwnerHandoffConflict
	}
	return customerport.OwnerHandoffExecution{EffectID: effectID, SourceStaffID: sourceStaffID, TargetStaffID: targetStaffID, CorpScope: scope, SourceRefDigest: string(effectDigestForLine("source-ref", batchID, line)), TargetRefDigest: string(effectDigestForLine("target-ref", batchID, line)), PayloadRefDigest: string(effectDigestForLine("payload-ref", batchID, line)), PolicyRefDigest: string(effectDigestForLine("policy-ref", batchID, line)), SourceUserID: source, TargetUserID: target, ExternalUserID: external, WelcomeMessage: welcome,
		SourceDigest: effectDigestString(sourceDigest), TargetDigest: effectDigestString(targetDigest), PayloadDigest: effectDigestString(payloadDigest), PolicyDigest: effectDigestString(policyDigest)}, nil
}

func effectDigestString(raw []byte) string { return "sha256:" + hex.EncodeToString(raw) }

func ownerHandoffSnapshotDigest(label string, values ...string) [32]byte {
	input := label
	for _, value := range values {
		input += "\x00" + value
	}
	return sha256.Sum256([]byte(input))
}

func sameSnapshotDigest(actual []byte, label string, values ...string) bool {
	expected := ownerHandoffSnapshotDigest(label, values...)
	return len(actual) == len(expected) && string(actual) == string(expected[:])
}

func (store *PostgreSQLOwnerHandoffStore) CreateOwnerHandoffPreview(ctx context.Context, record customerport.OwnerHandoffPreviewRecord) (customerport.OwnerHandoffPreview, error) {
	if record.Preview.ID == "" || record.ActorAdminUserID < 1 || len(record.Candidates) == 0 || len(record.Candidates) > 20000 || len(record.RequestDigest) != 32 {
		return customerport.OwnerHandoffPreview{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffPreview{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_previews(id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, record.Preview.ID, record.ActorAdminUserID, record.Preview.Mode, record.Preview.SourceStaffID, record.Preview.TargetStaffID, record.Preview.CorpScope, record.RequestDigest[:], record.Preview.ConfirmationPhrase, record.Preview.ExpiresAt.UTC()); err != nil {
		return customerport.OwnerHandoffPreview{}, err
	}
	rows := make([]customerport.OwnerHandoffPreviewRow, 0, len(record.Candidates))
	for index, candidate := range record.Candidates {
		line := int64(index + 1)
		if candidate.CustomerID < 1 || candidate.State == "" || len(candidate.RelationshipDigest) != 32 {
			return customerport.OwnerHandoffPreview{}, ErrOwnerHandoffConflict
		}
		var expectedOwner, expectedVersion any
		if candidate.ExpectedLocalOwnerID > 0 {
			expectedOwner = candidate.ExpectedLocalOwnerID
		}
		if candidate.ExpectedLocalVersion > 0 {
			expectedVersion = candidate.ExpectedLocalVersion
		}
		var sourceCipher, targetCipher, externalCipher, welcomeCipher []byte
		var sourceDigest, targetDigest, externalDigest, payloadDigest, policyDigest []byte
		if record.Preview.Mode == customerport.OwnerHandoffWeComThenCRM && candidate.State == "ready" {
			if store.cipher == nil || candidate.SourceUserID == "" || candidate.TargetUserID == "" || candidate.ExternalUserID == "" {
				return customerport.OwnerHandoffPreview{}, ErrOwnerHandoffConflict
			}
			if sourceCipher, err = store.cipher.Seal(record.Preview.ID, line, "source_userid", candidate.SourceUserID); err != nil {
				return customerport.OwnerHandoffPreview{}, err
			}
			if targetCipher, err = store.cipher.Seal(record.Preview.ID, line, "target_userid", candidate.TargetUserID); err != nil {
				return customerport.OwnerHandoffPreview{}, err
			}
			if externalCipher, err = store.cipher.Seal(record.Preview.ID, line, "external_userid", candidate.ExternalUserID); err != nil {
				return customerport.OwnerHandoffPreview{}, err
			}
			if record.WelcomeMessage != "" {
				if welcomeCipher, err = store.cipher.Seal(record.Preview.ID, line, "welcome_message", record.WelcomeMessage); err != nil {
					return customerport.OwnerHandoffPreview{}, err
				}
			}
			source := ownerHandoffSnapshotDigest("source-userid", candidate.SourceUserID)
			target := ownerHandoffSnapshotDigest("target-userid", candidate.TargetUserID)
			external := ownerHandoffSnapshotDigest("external-userid", candidate.ExternalUserID)
			payload := ownerHandoffSnapshotDigest("transfer-payload", candidate.ExternalUserID, record.WelcomeMessage)
			policy := ownerHandoffSnapshotDigest("policy", string(record.Preview.Mode), record.Preview.CorpScope, strconv.FormatInt(record.Preview.SourceStaffID, 10), strconv.FormatInt(record.Preview.TargetStaffID, 10))
			sourceDigest, targetDigest, externalDigest, payloadDigest, policyDigest = source[:], target[:], external[:], payload[:], policy[:]
		}
		if _, err = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_preview_rows(preview_id,line_no,customer_id,expected_local_owner_staff_id,expected_local_owner_version,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,welcome_message_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,state,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, record.Preview.ID, line, candidate.CustomerID, expectedOwner, expectedVersion, candidate.RelationshipDigest[:], sourceCipher, targetCipher, externalCipher, welcomeCipher, sourceDigest, targetDigest, externalDigest, payloadDigest, policyDigest, candidate.State, candidate.Reason); err != nil {
			return customerport.OwnerHandoffPreview{}, err
		}
		rows = append(rows, customerport.OwnerHandoffPreviewRow{Line: line, CustomerID: candidate.CustomerID, ExpectedOwnerID: candidate.ExpectedLocalOwnerID, ExpectedVersion: candidate.ExpectedLocalVersion, State: candidate.State, Reason: candidate.Reason})
	}
	record.Preview.Rows = rows
	return record.Preview, nil
}

func (store *PostgreSQLOwnerHandoffStore) LoadOwnerHandoffPreview(ctx context.Context, previewID string, lock bool) (customerport.OwnerHandoffPreviewRecord, error) {
	if previewID == "" {
		return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffPreviewRecord{}, err
	}
	query := `SELECT id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at,COALESCE(executed_batch_id,'') FROM customer_owner_handoff_previews WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var record customerport.OwnerHandoffPreviewRecord
	var requestDigest []byte
	if err = tx.QueryRow(ctx, query, previewID).Scan(&record.Preview.ID, &record.ActorAdminUserID, &record.Preview.Mode, &record.Preview.SourceStaffID, &record.Preview.TargetStaffID, &record.Preview.CorpScope, &requestDigest, &record.Preview.ConfirmationPhrase, &record.Preview.ExpiresAt, &record.ExecutedBatchID); err != nil {
		return customerport.OwnerHandoffPreviewRecord{}, err
	}
	if len(requestDigest) != 32 {
		return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
	}
	copy(record.RequestDigest[:], requestDigest)
	record.Preview.Hash = hex.EncodeToString(requestDigest)
	rows, err := tx.Query(ctx, `SELECT line_no,customer_id,COALESCE(expected_local_owner_staff_id,0),COALESCE(expected_local_owner_version,0),relation_digest,state,reason,
		source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest
		FROM customer_owner_handoff_preview_rows WHERE preview_id=$1 ORDER BY line_no`, previewID)
	if err != nil {
		return customerport.OwnerHandoffPreviewRecord{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate customerport.OwnerHandoffCandidate
		var line int64
		var digest, sourceCipher, targetCipher, externalCipher, sourceDigest, targetDigest, externalDigest []byte
		if err = rows.Scan(&line, &candidate.CustomerID, &candidate.ExpectedLocalOwnerID, &candidate.ExpectedLocalVersion, &digest, &candidate.State, &candidate.Reason,
			&sourceCipher, &targetCipher, &externalCipher, &sourceDigest, &targetDigest, &externalDigest); err != nil {
			return customerport.OwnerHandoffPreviewRecord{}, err
		}
		if line != int64(len(record.Candidates)+1) || len(digest) != 32 {
			return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
		}
		copy(candidate.RelationshipDigest[:], digest)
		if record.Preview.Mode == customerport.OwnerHandoffWeComThenCRM && candidate.State == "ready" {
			if store.cipher == nil {
				return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
			}
			if candidate.SourceUserID, err = store.cipher.Open(record.Preview.ID, line, "source_userid", sourceCipher); err != nil {
				return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
			}
			if candidate.TargetUserID, err = store.cipher.Open(record.Preview.ID, line, "target_userid", targetCipher); err != nil {
				return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
			}
			if candidate.ExternalUserID, err = store.cipher.Open(record.Preview.ID, line, "external_userid", externalCipher); err != nil {
				return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
			}
			if !sameSnapshotDigest(sourceDigest, "source-userid", candidate.SourceUserID) || !sameSnapshotDigest(targetDigest, "target-userid", candidate.TargetUserID) || !sameSnapshotDigest(externalDigest, "external-userid", candidate.ExternalUserID) {
				return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
			}
		}
		record.Candidates = append(record.Candidates, candidate)
		record.Preview.Rows = append(record.Preview.Rows, customerport.OwnerHandoffPreviewRow{Line: line, CustomerID: candidate.CustomerID, ExpectedOwnerID: candidate.ExpectedLocalOwnerID, ExpectedVersion: candidate.ExpectedLocalVersion, State: candidate.State, Reason: candidate.Reason})
	}
	if err = rows.Err(); err != nil {
		return customerport.OwnerHandoffPreviewRecord{}, err
	}
	if len(record.Candidates) == 0 {
		return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
	}
	return record, nil
}

// LockOwnerHandoffCustomersAndRejectActiveWeCom takes deterministic Customer
// row locks, then rejects another transfer_customer while a prior effect for
// the same canonical customer can still be sent or has an unknown outcome.
// The lock lives in the caller's Customer UoW, so two independently previewed
// confirmations cannot both accept a provider write. It deliberately leaves
// provider_accepted/observed lines out: those already have a single immutable
// effect and are resolved by the local CAS/result-readback paths.
func (store *PostgreSQLOwnerHandoffStore) LockOwnerHandoffCustomersAndRejectActiveWeCom(ctx context.Context, customerIDs []customerdomain.CustomerID) error {
	if len(customerIDs) == 0 || len(customerIDs) > 20000 {
		return ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[int64]struct{}, len(customerIDs))
	for _, customerID := range customerIDs {
		if customerID < 1 {
			return ErrOwnerHandoffConflict
		}
		id := int64(customerID)
		if _, duplicate := seen[id]; duplicate {
			return ErrOwnerHandoffConflict
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM customers WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	locked := 0
	for rows.Next() {
		locked++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return rowsErr
	}
	rows.Close()
	if locked != len(ids) {
		return ErrOwnerHandoffConflict
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM customer_owner_handoff_lines line
		JOIN customer_owner_handoff_batches batch ON batch.id=line.batch_id
		WHERE line.customer_id=ANY($1::bigint[])
		  AND line.mode='wecom_then_crm'
		  AND batch.mode='wecom_then_crm'
		  AND batch.state IN ('accepted','executing','needs_attention')
		  AND line.state IN ('queued','retryable_failed','outcome_unknown')
	)`, ids).Scan(&active)
	if err != nil {
		return err
	}
	if active {
		return ErrOwnerHandoffConflict
	}
	return nil
}

// LoadOwnerHandoffBatchSegment locks the batch and returns exactly one bounded
// durable work segment. The worker retries this same segment safely: provider
// lines whose effect is already bound are omitted, and local rows no longer
// queued are omitted. A following segment is enqueued in the same UoW.
func (store *PostgreSQLOwnerHandoffStore) LoadOwnerHandoffBatchSegment(ctx context.Context, batchID string, segment int64, size int) (customerport.OwnerHandoffBatchSegment, error) {
	if batchID == "" || segment < 0 || size < 1 || size > 1000 {
		return customerport.OwnerHandoffBatchSegment{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffBatchSegment{}, err
	}
	var result customerport.OwnerHandoffBatchSegment
	var state string
	if err = tx.QueryRow(ctx, `SELECT id,preview_id,actor_admin_user_id,target_staff_id,mode,state FROM customer_owner_handoff_batches WHERE id=$1 FOR UPDATE`, batchID).Scan(&result.BatchID, &result.PreviewID, &result.ActorID, &result.TargetStaffID, &result.Mode, &state); err != nil {
		return customerport.OwnerHandoffBatchSegment{}, err
	}
	if state == "completed" || state == "failed" {
		return result, nil
	}
	start := segment*int64(size) + 1
	end := start + int64(size) - 1
	query := `SELECT line_no,customer_id,state,COALESCE(effect_id,''),observed_at,COALESCE(transfer_status,0),transfer_takeover_at,COALESCE(expected_local_owner_version,0)
		FROM customer_owner_handoff_lines
		WHERE batch_id=$1 AND line_no BETWEEN $2 AND $3 AND state='queued'`
	if result.Mode == customerport.OwnerHandoffWeComThenCRM {
		query += " AND effect_id IS NULL"
	} else if result.Mode != customerport.OwnerHandoffLocalOnly {
		return customerport.OwnerHandoffBatchSegment{}, ErrOwnerHandoffConflict
	}
	query += " ORDER BY line_no FOR UPDATE"
	rows, err := tx.Query(ctx, query, batchID, start, end)
	if err != nil {
		return customerport.OwnerHandoffBatchSegment{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item customerport.OwnerHandoffSegmentLine
		if err = rows.Scan(&item.Line, &item.CustomerID, &item.State, &item.EffectID, &item.ObservedAt, &item.TransferStatus, &item.TakeoverAt, &item.ExpectedLocalVersion); err != nil {
			return customerport.OwnerHandoffBatchSegment{}, err
		}
		result.Lines = append(result.Lines, item)
	}
	if err = rows.Err(); err != nil {
		return customerport.OwnerHandoffBatchSegment{}, err
	}
	if result.Mode == customerport.OwnerHandoffWeComThenCRM {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_owner_handoff_lines WHERE batch_id=$1 AND line_no>$2 AND state='queued' AND effect_id IS NULL)`, batchID, end).Scan(&result.HasNext)
	} else {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_owner_handoff_lines WHERE batch_id=$1 AND line_no>$2 AND state='queued')`, batchID, end).Scan(&result.HasNext)
	}
	if err != nil {
		return customerport.OwnerHandoffBatchSegment{}, err
	}
	return result, nil
}

func (store *PostgreSQLOwnerHandoffStore) SetOwnerHandoffLineState(ctx context.Context, batchID string, line int64, state string) error {
	if batchID == "" || line < 1 || (state != "local_updated" && state != "cas_conflict") {
		return ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE customer_owner_handoff_lines SET state=$3,updated_at=clock_timestamp() WHERE batch_id=$1 AND line_no=$2 AND mode='local_only' AND state='queued'`, batchID, line, state)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrOwnerHandoffConflict
	}
	return nil
}

// RecomputeOwnerHandoffBatchState derives a batch projection from durable line
// facts. It deliberately preserves an accepted batch with no worker mutation
// only until its first segment runs; thereafter queued lines mean executing.
func (store *PostgreSQLOwnerHandoffStore) RecomputeOwnerHandoffBatchState(ctx context.Context, batchID string) error {
	if batchID == "" {
		return ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var pending, attention, failed bool
	if err = tx.QueryRow(ctx, `SELECT
		COALESCE(bool_or(state IN ('queued','retryable_failed')),false),
		COALESCE(bool_or(state IN ('outcome_unknown','cas_conflict')),false),
		COALESCE(bool_or(state='final_failed'),false)
		FROM customer_owner_handoff_lines WHERE batch_id=$1`, batchID).Scan(&pending, &attention, &failed); err != nil {
		return err
	}
	state := "completed"
	if attention {
		state = "needs_attention"
	} else if pending {
		state = "executing"
	} else if failed {
		state = "failed"
	}
	command, err := tx.Exec(ctx, `UPDATE customer_owner_handoff_batches SET state=$2,updated_at=clock_timestamp() WHERE id=$1`, batchID, state)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrOwnerHandoffConflict
	}
	return nil
}

func (store *PostgreSQLOwnerHandoffStore) CreateLocalOnlyOwnerHandoffBatch(ctx context.Context, record customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error) {
	if record.Preview.Preview.Mode != customerport.OwnerHandoffLocalOnly || record.ActorID < 1 || record.Idempotency == "" || len(record.Lines) != len(record.Preview.Candidates) {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	batchID, err := ownerHandoffStoreID()
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	var created time.Time
	err = tx.QueryRow(ctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES($1,$2,$3,$4,$5,'local_only',$6,$7,$8,'accepted') ON CONFLICT (actor_admin_user_id,idempotency_key) DO NOTHING RETURNING created_at`, batchID, record.Preview.Preview.ID, record.ActorID, record.Idempotency, record.RequestDigest[:], record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, record.Preview.Preview.CorpScope).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		prior, priorDigest, found, readErr := store.OwnerHandoffBatchByIdempotency(ctx, record.ActorID, record.Idempotency)
		if readErr != nil {
			return customerport.OwnerHandoffBatch{}, readErr
		}
		if !found || priorDigest != record.RequestDigest {
			return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
		}
		return prior, nil
	}
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	// The preview already owns all immutable candidates. Copying its rows with
	// one INSERT … SELECT avoids 20k client/server round trips while preserving
	// the exact frozen scope and line numbers for River segments.
	command, err := tx.Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,expected_local_owner_version,relation_digest,state)
		SELECT $1,line_no,customer_id,'local_only',$2,$3,expected_local_owner_version,relation_digest,CASE WHEN state='ready' THEN 'queued' ELSE state END
		FROM customer_owner_handoff_preview_rows WHERE preview_id=$4 ORDER BY line_no`, batchID, record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, record.Preview.Preview.ID)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	if command.RowsAffected() != int64(len(record.Lines)) {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_previews SET executed_batch_id=$2 WHERE id=$1 AND executed_batch_id IS NULL`, record.Preview.Preview.ID, batchID); err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	return customerport.OwnerHandoffBatch{ID: batchID, Mode: customerport.OwnerHandoffLocalOnly, State: "accepted", Lines: append([]customerport.OwnerHandoffLine(nil), record.Lines...), CreatedAt: created.UTC(), UpdatedAt: created.UTC()}, nil
}

// CreateWeComOwnerHandoffBatch copies the already encrypted, preview-bound
// snapshots.  It intentionally does not decrypt or re-encrypt identifiers;
// the preview ID remains the AEAD binding throughout execution.
func (store *PostgreSQLOwnerHandoffStore) CreateWeComOwnerHandoffBatch(ctx context.Context, record customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error) {
	if record.Preview.Preview.Mode != customerport.OwnerHandoffWeComThenCRM || record.ActorID < 1 || record.Idempotency == "" || len(record.Lines) != len(record.Preview.Candidates) {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	batchID, err := ownerHandoffStoreID()
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	var created time.Time
	err = tx.QueryRow(ctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state)
		VALUES($1,$2,$3,$4,$5,'wecom_then_crm',$6,$7,$8,'accepted') ON CONFLICT (actor_admin_user_id,idempotency_key) DO NOTHING RETURNING created_at`, batchID, record.Preview.Preview.ID, record.ActorID, record.Idempotency, record.RequestDigest[:], record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, record.Preview.Preview.CorpScope).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		prior, digest, found, readErr := store.OwnerHandoffBatchByIdempotency(ctx, record.ActorID, record.Idempotency)
		if readErr != nil {
			return customerport.OwnerHandoffBatch{}, readErr
		}
		if !found || digest != record.RequestDigest {
			return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
		}
		return prior, nil
	}
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	// Keep encrypted provider snapshots inside Customer ownership, but copy the
	// entire frozen preview in one set operation before bounded River execution.
	command, err := tx.Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,expected_local_owner_version,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,welcome_message_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,state)
		SELECT $1,line_no,customer_id,'wecom_then_crm',$2,$3,expected_local_owner_version,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,welcome_message_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,CASE WHEN state='ready' THEN 'queued' ELSE state END
		FROM customer_owner_handoff_preview_rows WHERE preview_id=$4 ORDER BY line_no`, batchID, record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, record.Preview.Preview.ID)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	if command.RowsAffected() != int64(len(record.Lines)) {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_previews SET executed_batch_id=$2 WHERE id=$1 AND executed_batch_id IS NULL`, record.Preview.Preview.ID, batchID); err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	return customerport.OwnerHandoffBatch{ID: batchID, Mode: customerport.OwnerHandoffWeComThenCRM, State: "accepted", Lines: append([]customerport.OwnerHandoffLine(nil), record.Lines...), CreatedAt: created.UTC(), UpdatedAt: created.UTC()}, nil
}

func (store *PostgreSQLOwnerHandoffStore) BindOwnerHandoffEffect(ctx context.Context, binding customerport.OwnerHandoffEffectBinding) error {
	if binding.BatchID == "" || binding.Line < 1 || binding.EffectID == "" || binding.ReceiptID == "" {
		return ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE customer_owner_handoff_lines SET effect_id=$3,effect_receipt_id=$4,state='queued',updated_at=clock_timestamp() WHERE batch_id=$1 AND line_no=$2 AND mode='wecom_then_crm' AND state='queued' AND effect_id IS NULL`, binding.BatchID, binding.Line, binding.EffectID, binding.ReceiptID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 1 {
		return nil
	}
	var priorEffect, priorReceipt string
	err = tx.QueryRow(ctx, `SELECT COALESCE(effect_id,''),COALESCE(effect_receipt_id,'') FROM customer_owner_handoff_lines WHERE batch_id=$1 AND line_no=$2 AND mode='wecom_then_crm'`, binding.BatchID, binding.Line).Scan(&priorEffect, &priorReceipt)
	if err != nil {
		return err
	}
	if priorEffect == binding.EffectID && priorReceipt == binding.ReceiptID {
		return nil
	}
	return ErrOwnerHandoffConflict
}

// CompleteOwnerHandoffEffect projects only EER's terminal transport fact.
// A successful transfer_customer response is provider acceptance. It triggers
// exactly one frozen local CAS; transfer_result remains a separate read-only
// final-observation projection and can never repeat that assignment.
func (store *PostgreSQLOwnerHandoffStore) CompleteOwnerHandoffEffect(ctx context.Context, completion customerport.OwnerHandoffCompletion) error {
	if completion.EffectID == "" || completion.Attempt < 1 || completion.Generation < 1 || completion.Fence < 1 {
		return ErrOwnerHandoffConflict
	}
	digest, err := parseOwnerHandoffDigest(completion.ResultDigest)
	if err != nil {
		return ErrOwnerHandoffConflict
	}
	lineState := ""
	switch completion.State {
	case string(effectport.StateExecuted):
		lineState = "provider_accepted"
	case string(effectport.StateUnknown):
		lineState = "outcome_unknown"
	case string(effectport.StateRetryable):
		lineState = "retryable_failed"
	case string(effectport.StateFinalFailed):
		lineState = "final_failed"
	default:
		return ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	// Every completion locks parent then child. This keeps concurrent EER
	// completions from overwriting a batch-level needs_attention outcome.
	var batchID string
	err = tx.QueryRow(ctx, `SELECT b.id FROM customer_owner_handoff_batches b JOIN customer_owner_handoff_lines l ON l.batch_id=b.id WHERE l.effect_id=$1 AND l.mode='wecom_then_crm' FOR UPDATE OF b`, completion.EffectID).Scan(&batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOwnerHandoffConflict
	}
	if err != nil {
		return err
	}
	var customerID customerdomain.CustomerID
	var targetStaffID, expectedVersion int64
	var priorState string
	var priorDigest []byte
	var priorAttempt int32
	var priorGeneration, priorFence int64
	err = tx.QueryRow(ctx, `SELECT customer_id,target_staff_id,COALESCE(expected_local_owner_version,0),state,result_digest,effect_attempt,effect_generation,effect_fence
		FROM customer_owner_handoff_lines WHERE effect_id=$1 AND batch_id=$2 FOR UPDATE`, completion.EffectID, batchID).Scan(&customerID, &targetStaffID, &expectedVersion, &priorState, &priorDigest, &priorAttempt, &priorGeneration, &priorFence)
	if err != nil {
		return err
	}
	if priorAttempt > completion.Attempt {
		return ErrOwnerHandoffConflict
	}
	if priorAttempt == completion.Attempt && priorAttempt != 0 {
		if priorGeneration == completion.Generation && priorFence == completion.Fence && bytes.Equal(priorDigest, digest) {
			return nil
		}
		return ErrOwnerHandoffConflict
	}
	if priorAttempt != 0 && (priorGeneration != completion.Generation || completion.Fence <= priorFence) {
		return ErrOwnerHandoffConflict
	}
	if priorState != "queued" && priorState != "retryable_failed" {
		return ErrOwnerHandoffConflict
	}
	if completion.State == string(effectport.StateExecuted) {
		if _, assignErr := store.AssignLocalOwner(ctx, customerID, targetStaffID, expectedVersion, "owner_handoff_wecom_then_crm", time.Now().UTC()); assignErr != nil {
			if !errors.Is(assignErr, ErrOwnerHandoffConflict) {
				return assignErr
			}
			lineState = "cas_conflict"
		}
	}
	command, err := tx.Exec(ctx, `UPDATE customer_owner_handoff_lines SET state=$2,result_digest=$3,effect_attempt=$4,effect_generation=$5,effect_fence=$6,updated_at=clock_timestamp()
		WHERE effect_id=$1 AND batch_id=$7 AND state IN ('queued','retryable_failed')`, completion.EffectID, lineState, digest, completion.Attempt, completion.Generation, completion.Fence, batchID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrOwnerHandoffConflict
	}
	var pending, attention, failed bool
	err = tx.QueryRow(ctx, `SELECT
		COALESCE(bool_or(state IN ('queued','retryable_failed')),false),
		COALESCE(bool_or(state IN ('outcome_unknown','cas_conflict')),false),
		COALESCE(bool_or(state='final_failed'),false)
		FROM customer_owner_handoff_lines WHERE batch_id=$1`, batchID).Scan(&pending, &attention, &failed)
	if err != nil {
		return err
	}
	batchState := "completed"
	if attention {
		batchState = "needs_attention"
	} else if pending {
		batchState = "executing"
	} else if failed {
		batchState = "failed"
	}
	_, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_batches SET state=$2,updated_at=clock_timestamp() WHERE id=$1`, batchID, batchState)
	return err
}

// LoadOwnerHandoffTransferRead reveals a frozen source/target pair only to
// the Customer application immediately before the read-only WeCom query. It
// never exposes provider identifiers through a Reader DTO or EER.
func (store *PostgreSQLOwnerHandoffStore) LoadOwnerHandoffTransferRead(ctx context.Context, actorID int64, batchID string) (customerport.OwnerHandoffTransferRead, error) {
	if actorID < 1 || strings.TrimSpace(batchID) != batchID || batchID == "" || store == nil || store.cipher == nil {
		return customerport.OwnerHandoffTransferRead{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffTransferRead{}, err
	}
	var previewID string
	var line int64
	var sourceCipher, targetCipher, cursorCipher []byte
	err = tx.QueryRow(ctx, `SELECT b.preview_id,l.line_no,l.source_userid_ciphertext,l.target_userid_ciphertext,b.transfer_result_cursor_ciphertext
		FROM customer_owner_handoff_batches b
		JOIN customer_owner_handoff_lines l ON l.batch_id=b.id
		WHERE b.id=$1 AND b.actor_admin_user_id=$2 AND b.mode='wecom_then_crm'
		  AND l.state IN ('provider_accepted','observed','cas_conflict')
		ORDER BY l.line_no LIMIT 1`, batchID, actorID).Scan(&previewID, &line, &sourceCipher, &targetCipher, &cursorCipher)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.OwnerHandoffTransferRead{}, ErrOwnerHandoffConflict
	}
	if err != nil {
		return customerport.OwnerHandoffTransferRead{}, err
	}
	source, err := store.cipher.Open(previewID, line, "source_userid", sourceCipher)
	if err != nil {
		return customerport.OwnerHandoffTransferRead{}, ErrOwnerHandoffConflict
	}
	target, err := store.cipher.Open(previewID, line, "target_userid", targetCipher)
	if err != nil {
		return customerport.OwnerHandoffTransferRead{}, ErrOwnerHandoffConflict
	}
	cursor := ""
	if len(cursorCipher) != 0 {
		cursor, err = store.cipher.Open(batchID, 0, "transfer_result_cursor", cursorCipher)
		if err != nil {
			return customerport.OwnerHandoffTransferRead{}, ErrOwnerHandoffConflict
		}
	}
	return customerport.OwnerHandoffTransferRead{BatchID: batchID, ActorAdminID: actorID, SourceUserID: source, TargetUserID: target, Cursor: cursor}, nil
}

// RecordOwnerHandoffTransferResult persists only documented status/time facts
// for frozen accepted lines. Unknown external IDs and provider read failures do
// not create records, update local owners, or modify EER state.
func (store *PostgreSQLOwnerHandoffStore) RecordOwnerHandoffTransferResult(ctx context.Context, read customerport.OwnerHandoffTransferRead, nextCursor string, observations []customerport.OwnerHandoffTransferObservation) (customerport.OwnerHandoffBatch, int, error) {
	if read.BatchID == "" || read.ActorAdminID < 1 || strings.TrimSpace(nextCursor) != nextCursor || store == nil || store.cipher == nil {
		return customerport.OwnerHandoffBatch{}, 0, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, 0, err
	}
	var previewID string
	if err = tx.QueryRow(ctx, `SELECT preview_id FROM customer_owner_handoff_batches WHERE id=$1 AND actor_admin_user_id=$2 AND mode='wecom_then_crm' FOR UPDATE`, read.BatchID, read.ActorAdminID).Scan(&previewID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return customerport.OwnerHandoffBatch{}, 0, ErrOwnerHandoffConflict
		}
		return customerport.OwnerHandoffBatch{}, 0, err
	}
	byDigest := make(map[[32]byte]customerport.OwnerHandoffTransferObservation, len(observations))
	for _, observation := range observations {
		if observation.ExternalUserID == "" || observation.Status < 1 || observation.Status > 5 || observation.TakeoverTime < 0 {
			return customerport.OwnerHandoffBatch{}, 0, ErrOwnerHandoffConflict
		}
		digest := sha256.Sum256([]byte(observation.ExternalUserID))
		if _, duplicate := byDigest[digest]; duplicate {
			return customerport.OwnerHandoffBatch{}, 0, ErrOwnerHandoffConflict
		}
		byDigest[digest] = observation
	}
	var changed int
	if len(byDigest) != 0 {
		digests := make([][]byte, 0, len(byDigest))
		for digest := range byDigest {
			copied := make([]byte, len(digest))
			copy(copied, digest[:])
			digests = append(digests, copied)
		}
		rows, queryErr := tx.Query(ctx, `SELECT line_no,external_identity_ciphertext,external_identity_digest
			FROM customer_owner_handoff_lines
			WHERE batch_id=$1 AND state IN ('provider_accepted','observed','cas_conflict') AND external_identity_digest=ANY($2::bytea[]) FOR UPDATE`, read.BatchID, digests)
		if queryErr != nil {
			return customerport.OwnerHandoffBatch{}, 0, queryErr
		}
		type frozenLine struct {
			line               int64
			ciphertext, digest []byte
		}
		frozen := make([]frozenLine, 0, len(byDigest))
		for rows.Next() {
			var value frozenLine
			if scanErr := rows.Scan(&value.line, &value.ciphertext, &value.digest); scanErr != nil {
				rows.Close()
				return customerport.OwnerHandoffBatch{}, 0, scanErr
			}
			frozen = append(frozen, value)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return customerport.OwnerHandoffBatch{}, 0, err
		}
		rows.Close()
		for _, value := range frozen {
			if len(value.digest) != 32 {
				return customerport.OwnerHandoffBatch{}, 0, ErrOwnerHandoffConflict
			}
			var digest [32]byte
			copy(digest[:], value.digest)
			observation, found := byDigest[digest]
			if !found {
				continue
			}
			externalID, openErr := store.cipher.Open(previewID, value.line, "external_userid", value.ciphertext)
			if openErr != nil || externalID != observation.ExternalUserID {
				return customerport.OwnerHandoffBatch{}, 0, ErrOwnerHandoffConflict
			}
			var takeover *time.Time
			if observation.TakeoverTime > 0 {
				at := time.Unix(observation.TakeoverTime, 0).UTC()
				takeover = &at
			}
			command, updateErr := tx.Exec(ctx, `UPDATE customer_owner_handoff_lines
				SET state=CASE WHEN state='cas_conflict' THEN state ELSE 'observed' END,transfer_status=$3,transfer_takeover_at=$4,observed_at=clock_timestamp(),updated_at=clock_timestamp()
				WHERE batch_id=$1 AND line_no=$2 AND state IN ('provider_accepted','observed','cas_conflict')`, read.BatchID, value.line, observation.Status, takeover)
			if updateErr != nil {
				return customerport.OwnerHandoffBatch{}, 0, updateErr
			}
			changed += int(command.RowsAffected())
		}
	}

	var cursorCipher any
	if nextCursor == "" {
		cursorCipher = nil
	} else {
		sealed, sealErr := store.cipher.Seal(read.BatchID, 0, "transfer_result_cursor", nextCursor)
		if sealErr != nil {
			return customerport.OwnerHandoffBatch{}, 0, sealErr
		}
		cursorCipher = sealed
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_batches SET transfer_result_cursor_ciphertext=$2,updated_at=clock_timestamp() WHERE id=$1`, read.BatchID, cursorCipher); err != nil {
		return customerport.OwnerHandoffBatch{}, 0, err
	}
	batch, err := store.OwnerHandoffBatch(ctx, read.BatchID)
	return batch, changed, err
}

func parseOwnerHandoffDigest(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return nil, ErrOwnerHandoffConflict
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	if err != nil || len(raw) != 32 {
		return nil, ErrOwnerHandoffConflict
	}
	return raw, nil
}

func effectDigestForLine(label, batchID string, line int64) effectport.Digest {
	return effectport.Hash("customer-owner-handoff.v1", label, batchID, strconv.FormatInt(line, 10))
}

func ownerHandoffStoreID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "owner_handoff_batch_" + hex.EncodeToString(value), nil
}

func (store *PostgreSQLOwnerHandoffStore) OwnerHandoffBatchByIdempotency(ctx context.Context, actorID int64, key string) (customerport.OwnerHandoffBatch, [32]byte, bool, error) {
	if actorID < 1 || key == "" {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
	}
	var batch customerport.OwnerHandoffBatch
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT id,mode,state,request_digest,created_at,updated_at FROM customer_owner_handoff_batches WHERE actor_admin_user_id=$1 AND idempotency_key=$2`, actorID, key).Scan(&batch.ID, &batch.Mode, &batch.State, &digest, &batch.CreatedAt, &batch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, nil
	}
	if err != nil {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
	}
	if len(digest) != 32 {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, ErrOwnerHandoffConflict
	}
	var copied [32]byte
	copy(copied[:], digest)
	rows, err := tx.Query(ctx, `SELECT line_no,customer_id,state,COALESCE(effect_id,''),observed_at,COALESCE(transfer_status,0),transfer_takeover_at FROM customer_owner_handoff_lines WHERE batch_id=$1 ORDER BY line_no`, batch.ID)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var line customerport.OwnerHandoffLine
		if err = rows.Scan(&line.Line, &line.CustomerID, &line.State, &line.EffectID, &line.ObservedAt, &line.TransferStatus, &line.TakeoverAt); err != nil {
			return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
		}
		batch.Lines = append(batch.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
	}
	return batch, copied, true, nil
}

// OwnerHandoffPreview is the digest-safe operator projection. Provider IDs
// remain encrypted and are intentionally absent from the returned DTO.
func (store *PostgreSQLOwnerHandoffStore) OwnerHandoffPreview(ctx context.Context, id string) (customerport.OwnerHandoffPreview, error) {
	record, err := store.LoadOwnerHandoffPreview(ctx, id, false)
	if err != nil {
		return customerport.OwnerHandoffPreview{}, err
	}
	return record.Preview, nil
}

func (store *PostgreSQLOwnerHandoffStore) OwnerHandoffBatch(ctx context.Context, id string) (customerport.OwnerHandoffBatch, error) {
	if id == "" {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	var batch customerport.OwnerHandoffBatch
	err = tx.QueryRow(ctx, `SELECT id,mode,state,created_at,updated_at FROM customer_owner_handoff_batches WHERE id=$1`, id).Scan(&batch.ID, &batch.Mode, &batch.State, &batch.CreatedAt, &batch.UpdatedAt)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	rows, err := tx.Query(ctx, `SELECT line_no,customer_id,state,COALESCE(effect_id,''),observed_at,COALESCE(transfer_status,0),transfer_takeover_at FROM customer_owner_handoff_lines WHERE batch_id=$1 ORDER BY line_no`, id)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var line customerport.OwnerHandoffLine
		if err = rows.Scan(&line.Line, &line.CustomerID, &line.State, &line.EffectID, &line.ObservedAt, &line.TransferStatus, &line.TakeoverAt); err != nil {
			return customerport.OwnerHandoffBatch{}, err
		}
		batch.Lines = append(batch.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	return batch, nil
}

var _ customerport.OwnerHandoffReader = (*PostgreSQLOwnerHandoffStore)(nil)
var _ customerport.OwnerHandoffCompletionWriter = (*PostgreSQLOwnerHandoffStore)(nil)
