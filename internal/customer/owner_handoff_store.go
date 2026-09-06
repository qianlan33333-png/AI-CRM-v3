package customer

import (
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
		err = tx.QueryRow(ctx, "INSERT INTO customer_local_owners(customer_id,staff_id,version,source,updated_at) VALUES($1,$2,1,$3,$4) RETURNING version,updated_at", customerID, targetStaffID, source, now.UTC()).Scan(&owner.Version, &owner.UpdatedAt)
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
	return customerport.OwnerHandoffExecution{EffectID: effectID, SourceRefDigest: string(effectDigestForLine("source-ref", batchID, line)), TargetRefDigest: string(effectDigestForLine("target-ref", batchID, line)), PayloadRefDigest: string(effectDigestForLine("payload-ref", batchID, line)), PolicyRefDigest: string(effectDigestForLine("policy-ref", batchID, line)), SourceUserID: source, TargetUserID: target, ExternalUserID: external, WelcomeMessage: welcome,
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
	query := `SELECT id,actor_admin_user_id,mode,source_staff_id,target_staff_id,corp_scope,request_digest,confirmation_phrase,expires_at FROM customer_owner_handoff_previews WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var record customerport.OwnerHandoffPreviewRecord
	var requestDigest []byte
	if err = tx.QueryRow(ctx, query, previewID).Scan(&record.Preview.ID, &record.ActorAdminUserID, &record.Preview.Mode, &record.Preview.SourceStaffID, &record.Preview.TargetStaffID, &record.Preview.CorpScope, &requestDigest, &record.Preview.ConfirmationPhrase, &record.Preview.ExpiresAt); err != nil {
		return customerport.OwnerHandoffPreviewRecord{}, err
	}
	if len(requestDigest) != 32 {
		return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
	}
	copy(record.RequestDigest[:], requestDigest)
	record.Preview.Hash = hex.EncodeToString(requestDigest)
	rows, err := tx.Query(ctx, `SELECT line_no,customer_id,COALESCE(expected_local_owner_staff_id,0),COALESCE(expected_local_owner_version,0),relation_digest,state,reason FROM customer_owner_handoff_preview_rows WHERE preview_id=$1 ORDER BY line_no`, previewID)
	if err != nil {
		return customerport.OwnerHandoffPreviewRecord{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate customerport.OwnerHandoffCandidate
		var line int64
		var digest []byte
		if err = rows.Scan(&line, &candidate.CustomerID, &candidate.ExpectedLocalOwnerID, &candidate.ExpectedLocalVersion, &digest, &candidate.State, &candidate.Reason); err != nil {
			return customerport.OwnerHandoffPreviewRecord{}, err
		}
		if line != int64(len(record.Candidates)+1) || len(digest) != 32 {
			return customerport.OwnerHandoffPreviewRecord{}, ErrOwnerHandoffConflict
		}
		copy(candidate.RelationshipDigest[:], digest)
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
	err = tx.QueryRow(ctx, `INSERT INTO customer_owner_handoff_batches(id,preview_id,actor_admin_user_id,idempotency_key,request_digest,mode,source_staff_id,target_staff_id,corp_scope,state) VALUES($1,$2,$3,$4,$5,'local_only',$6,$7,$8,'completed') ON CONFLICT (actor_admin_user_id,idempotency_key) DO NOTHING RETURNING created_at`, batchID, record.Preview.Preview.ID, record.ActorID, record.Idempotency, record.RequestDigest[:], record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, record.Preview.Preview.CorpScope).Scan(&created)
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
	for index, line := range record.Lines {
		candidate := record.Preview.Candidates[index]
		if line.Line != int64(index+1) || line.CustomerID != candidate.CustomerID {
			return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
		}
		var expectedVersion any
		if candidate.ExpectedLocalVersion > 0 {
			expectedVersion = candidate.ExpectedLocalVersion
		}
		if _, err = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,expected_local_owner_version,relation_digest,state) VALUES($1,$2,$3,'local_only',$4,$5,$6,$7,$8)`, batchID, line.Line, line.CustomerID, record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, expectedVersion, candidate.RelationshipDigest[:], line.State); err != nil {
			return customerport.OwnerHandoffBatch{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_previews SET executed_batch_id=$2 WHERE id=$1 AND executed_batch_id IS NULL`, record.Preview.Preview.ID, batchID); err != nil {
		return customerport.OwnerHandoffBatch{}, err
	}
	return customerport.OwnerHandoffBatch{ID: batchID, Mode: customerport.OwnerHandoffLocalOnly, State: "completed", Lines: append([]customerport.OwnerHandoffLine(nil), record.Lines...), CreatedAt: created.UTC(), UpdatedAt: created.UTC()}, nil
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
	for index, line := range record.Lines {
		candidate := record.Preview.Candidates[index]
		if line.Line != int64(index+1) || line.CustomerID != candidate.CustomerID {
			return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO customer_owner_handoff_lines(batch_id,line_no,customer_id,mode,source_staff_id,target_staff_id,expected_local_owner_version,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,welcome_message_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,state)
			SELECT $1,line_no,customer_id,'wecom_then_crm',$2,$3,expected_local_owner_version,relation_digest,source_userid_ciphertext,target_userid_ciphertext,external_identity_ciphertext,welcome_message_ciphertext,source_userid_digest,target_userid_digest,external_identity_digest,payload_digest,policy_digest,$4
			FROM customer_owner_handoff_preview_rows WHERE preview_id=$5 AND line_no=$6`, batchID, record.Preview.Preview.SourceStaffID, record.Preview.Preview.TargetStaffID, line.State, record.Preview.Preview.ID, line.Line); err != nil {
			return customerport.OwnerHandoffBatch{}, err
		}
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
	if command.RowsAffected() != 1 {
		return ErrOwnerHandoffConflict
	}
	return nil
}

// CompleteOwnerHandoffEffect projects only EER's terminal transport fact.
// A successful transfer_customer response is provider acceptance, never a
// local owner change; the explicit transfer-result reader owns final CAS.
func (store *PostgreSQLOwnerHandoffStore) CompleteOwnerHandoffEffect(ctx context.Context, completion customerport.OwnerHandoffCompletion) error {
	if completion.EffectID == "" || completion.Attempt < 1 {
		return ErrOwnerHandoffConflict
	}
	digest, err := parseOwnerHandoffDigest(completion.ResultDigest)
	if err != nil {
		return ErrOwnerHandoffConflict
	}
	lineState, batchState := "", ""
	switch completion.State {
	case string(effectport.StateExecuted):
		lineState, batchState = "provider_accepted", "executing"
	case string(effectport.StateUnknown):
		lineState, batchState = "outcome_unknown", "needs_attention"
	case string(effectport.StateRetryable):
		lineState, batchState = "retryable_failed", "executing"
	case string(effectport.StateFinalFailed):
		lineState, batchState = "final_failed", "failed"
	default:
		return ErrOwnerHandoffConflict
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	var batchID string
	var customerID customerdomain.CustomerID
	var targetStaffID, expectedVersion int64
	// Lock the exact frozen line before local CAS.  A transport acceptance
	// never grants permission to replace a local owner that changed meanwhile.
	err = tx.QueryRow(ctx, `SELECT batch_id,customer_id,target_staff_id,COALESCE(expected_local_owner_version,0) FROM customer_owner_handoff_lines WHERE effect_id=$1 AND mode='wecom_then_crm' AND state IN ('queued','retryable_failed') FOR UPDATE`, completion.EffectID).Scan(&batchID, &customerID, &targetStaffID, &expectedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOwnerHandoffConflict
	}
	if err != nil {
		return err
	}
	if completion.State == string(effectport.StateExecuted) {
		if _, assignErr := store.AssignLocalOwner(ctx, customerID, targetStaffID, expectedVersion, "owner_handoff_wecom_then_crm", time.Now().UTC()); assignErr != nil {
			if !errors.Is(assignErr, ErrOwnerHandoffConflict) {
				return assignErr
			}
			lineState, batchState = "cas_conflict", "needs_attention"
		}
	}
	command, err := tx.Exec(ctx, `UPDATE customer_owner_handoff_lines SET state=$2,result_digest=$3,updated_at=clock_timestamp() WHERE effect_id=$1 AND state IN ('queued','retryable_failed')`, completion.EffectID, lineState, digest)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrOwnerHandoffConflict
	}
	_, err = tx.Exec(ctx, `UPDATE customer_owner_handoff_batches SET state=$2,updated_at=clock_timestamp() WHERE id=$1 AND state IN ('accepted','executing','needs_attention','failed')`, batchID, batchState)
	return err
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
	rows, err := tx.Query(ctx, `SELECT line_no,customer_id,state,COALESCE(effect_id,''),observed_at FROM customer_owner_handoff_lines WHERE batch_id=$1 ORDER BY line_no`, batch.ID)
	if err != nil {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var line customerport.OwnerHandoffLine
		if err = rows.Scan(&line.Line, &line.CustomerID, &line.State, &line.EffectID, &line.ObservedAt); err != nil {
			return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
		}
		batch.Lines = append(batch.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return customerport.OwnerHandoffBatch{}, [32]byte{}, false, err
	}
	return batch, copied, true, nil
}
