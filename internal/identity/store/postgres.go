// Package store contains the PostgreSQL persistence adapter for OneID.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PostgresStore has no pool deliberately: every operation obtains the
// UnitOfWork-bound transaction from its context, so an accidental autocommit
// call is rejected by platformpostgres.RequireTransaction.
type PostgresStore struct{}

func NewPostgresStore() *PostgresStore { return &PostgresStore{} }

var _ identityapp.Store = (*PostgresStore)(nil)

var errStore = errors.New("identity persistence failed")

func (store *PostgresStore) Resolve(ctx context.Context, reference identitydomain.NormalizedReference) (identityapp.StoredIdentity, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.StoredIdentity{}, false, err
	}
	var id, customerID int64
	var kind, scope, value, assurance, source string
	var normalizer int16
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE lineage(id, status, merged_into_customer_id) AS (
			SELECT c.id, c.status, c.merged_into_customer_id FROM customers c
			JOIN customer_identities i ON i.customer_id=c.id
			WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'
			UNION ALL SELECT c.id, c.status, c.merged_into_customer_id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id
		) SELECT i.id, l.id, i.kind, i.scope_key, i.normalized_value, i.assurance, i.source, i.normalizer_version
		FROM customer_identities i JOIN lineage l ON true
		WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active' AND l.status <> 'merged'
		LIMIT 1`, string(reference.Kind), reference.Scope, reference.NormalizedValue).Scan(&id, &customerID, &kind, &scope, &value, &assurance, &source, &normalizer)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityapp.StoredIdentity{}, false, nil
	}
	if err != nil {
		return identityapp.StoredIdentity{}, false, errors.New("identity store lookup")
	}
	return identityapp.StoredIdentity{ID: id, CustomerID: customerdomain.CustomerID(customerID), Reference: identitydomain.NormalizedReference{Kind: identitydomain.Kind(kind), Scope: scope, NormalizedValue: value, Assurance: identitydomain.Assurance(assurance), Source: source, NormalizerVersion: normalizer}}, true, nil
}

func (store *PostgresStore) Provision(ctx context.Context, fact identitydomain.VerifiedFact) (identityapp.ProvisionedIdentity, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.ProvisionedIdentity{}, err
	}
	ref := fact.Reference()
	if err = lockIdentityKey(ctx, tx, ref); err != nil {
		return identityapp.ProvisionedIdentity{}, errStore
	}
	if existing, found, lookupErr := existingIdentity(ctx, tx, ref); lookupErr != nil {
		return identityapp.ProvisionedIdentity{}, lookupErr
	} else if found {
		return identityapp.ProvisionedIdentity{CustomerID: existing.CustomerID, IdentityID: existing.ID}, nil
	}
	var customerID, identityID int64
	if err = tx.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		return identityapp.ProvisionedIdentity{}, errStore
	}
	if err = tx.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,$2,$3,$4,'verified',$5,$6,CURRENT_TIMESTAMP) RETURNING id`, customerID, string(ref.Kind), ref.Scope, ref.NormalizedValue, ref.Source, ref.NormalizerVersion).Scan(&identityID); err != nil {
		// The key lock serializes cooperating callers; this re-read also makes a
		// unique-index race replay stable without leaving a customer committed.
		if existing, found, lookupErr := existingIdentity(ctx, tx, ref); lookupErr == nil && found {
			return identityapp.ProvisionedIdentity{CustomerID: existing.CustomerID, IdentityID: existing.ID}, nil
		}
		return identityapp.ProvisionedIdentity{}, errStore
	}
	return identityapp.ProvisionedIdentity{CustomerID: customerdomain.CustomerID(customerID), IdentityID: identityID, Created: true}, nil
}

func (store *PostgresStore) Link(ctx context.Context, command identityapp.LinkCommand) (identityapp.LinkResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	return link(ctx, tx, command)
}

func (store *PostgresStore) CreateLinkIntent(ctx context.Context, command identityapp.LinkIntentCommand) (identityapp.CreatedLinkIntent, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.CreatedLinkIntent{}, err
	}
	root, err := lockActiveRoot(ctx, tx, command.SourceCustomerID)
	if err != nil {
		return identityapp.CreatedLinkIntent{}, identityapp.ErrInvalidLinkCommand
	}
	raw, err := randomToken()
	if err != nil {
		return identityapp.CreatedLinkIntent{}, errStore
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO identity_link_intents(token_hash,source_customer_id,source_customer_version,purpose,target_kind,expected_scope_key,expires_at,source,source_event_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, tokenHash(raw), root.ID, root.Version, string(command.Purpose), string(command.TargetKind), command.ExpectedScope, command.ExpiresAt, command.Source, command.SourceEventID).Scan(&id)
	if err != nil {
		return identityapp.CreatedLinkIntent{}, errStore
	}
	return identityapp.CreatedLinkIntent{ID: id, Token: raw, ExpiresAt: command.ExpiresAt}, nil
}

func (store *PostgresStore) ConsumeLinkIntent(ctx context.Context, command identityapp.ConsumeLinkIntentCommand) (identityapp.LinkResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	ref := command.Target.Reference()
	if err = lockIdentityKey(ctx, tx, ref); err != nil {
		return identityapp.LinkResult{}, errStore
	}
	var sourceID int64
	err = tx.QueryRow(ctx, `SELECT source_customer_id FROM identity_link_intents WHERE token_hash=$1`, tokenHash(command.Token)).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityapp.LinkResult{Status: identityapp.LinkIntentReplay}, nil
	}
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	root, rootErr := lockActiveRoot(ctx, tx, customerdomain.CustomerID(sourceID))
	// A merged source must still lock its old customer row, then be cancelled.
	if rootErr != nil {
		if err = lockCustomers(ctx, tx, []int64{sourceID}); err != nil {
			return identityapp.LinkResult{}, errStore
		}
	}
	intent, err := lockIntent(ctx, tx, tokenHash(command.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		return identityapp.LinkResult{Status: identityapp.LinkIntentReplay}, nil
	}
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	fingerprint := consumptionFingerprint(command)
	if intent.Status == "consumed" {
		if intent.Fingerprint != fingerprint {
			return identityapp.LinkResult{}, identityapp.ErrLinkIntentPayloadMismatch
		}
		return replay(intent.Result), nil
	}
	if intent.Status == "expired" {
		return identityapp.LinkResult{Status: identityapp.LinkIntentExpired}, nil
	}
	if intent.Status == "cancelled" {
		return identityapp.LinkResult{Status: identityapp.LinkIntentInvalidated}, nil
	}
	if !intent.ExpiresAt.After(time.Now()) {
		if _, err = tx.Exec(ctx, `UPDATE identity_link_intents SET status='expired',updated_at=CURRENT_TIMESTAMP WHERE id=$1`, intent.ID); err != nil {
			return identityapp.LinkResult{}, errStore
		}
		return identityapp.LinkResult{Status: identityapp.LinkIntentExpired}, nil
	}
	if ref.Kind != identitydomain.Kind(intent.TargetKind) || (intent.ExpectedScope != "" && ref.Scope != intent.ExpectedScope) {
		return identityapp.LinkResult{Status: identityapp.LinkScopeMismatch}, nil
	}
	if rootErr != nil || root.ID != intent.SourceID || root.Version != intent.SourceVersion {
		if _, err = tx.Exec(ctx, `UPDATE identity_link_intents SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE id=$1`, intent.ID); err != nil {
			return identityapp.LinkResult{}, errStore
		}
		return identityapp.LinkResult{Status: identityapp.LinkIntentInvalidated}, nil
	}
	// Consumption itself is an immutable identity-owned event, including an
	// attach/already-linked outcome that has no candidate or conflict row.
	evidenceID, err := writeEvidence(ctx, tx, int64(root.ID), 0, 0, 0, command.Evidence)
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	result, err := linkLocked(ctx, tx, root, command.Target, command.Evidence)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE identity_link_intents SET status='consumed',consumed_at=CURRENT_TIMESTAMP,consumption_fingerprint=$2,consumed_evidence_id=$3,consumed_identity_id=NULLIF($4,0),consumed_customer_id=$5,result_status=$6,result_candidate_id=$7,result_conflict_id=$8,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, intent.ID, fingerprint, evidenceID, result.IdentityID, result.CustomerID, string(result.Status), nullableID(candidateID(result)), nullableID(conflictID(result)))
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	return result, nil
}

func (store *PostgresStore) ConfirmMerge(ctx context.Context, command identityapp.ConfirmMergeCommand) (identityapp.LinkResult, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	var left, right int64
	err = tx.QueryRow(ctx, `SELECT left_customer_id,right_customer_id FROM customer_merge_candidates WHERE id=$1`, command.CandidateID).Scan(&left, &right)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	if err = lockCustomers(ctx, tx, []int64{left, right}); err != nil {
		return identityapp.LinkResult{}, errStore
	}
	candidate, err := lockCandidate(ctx, tx, command.CandidateID)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	if candidate.Status != "open" || candidate.Evidence.Strength != identitydomain.EvidenceStrong || (int64(command.SurvivorCustomerID) != int64(candidate.LeftCustomerID) && int64(command.SurvivorCustomerID) != int64(candidate.RightCustomerID)) {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	leftRoot, err1 := activeCustomerLocked(ctx, tx, int64(candidate.LeftCustomerID))
	rightRoot, err2 := activeCustomerLocked(ctx, tx, int64(candidate.RightCustomerID))
	if err1 != nil || err2 != nil || leftRoot.Version != candidate.LeftVersion || rightRoot.Version != candidate.RightVersion {
		_, _ = tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by='system',resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND status='open'`, candidate.ID)
		return identityapp.LinkResult{}, identityapp.ErrConcurrentIdentityChange
	}
	if reason, err := crossRootConflict(ctx, tx, int64(candidate.LeftCustomerID), int64(candidate.RightCustomerID)); err != nil {
		return identityapp.LinkResult{}, errStore
	} else if reason != "" {
		conflict, err := createConflict(ctx, tx, int64(candidate.LeftCustomerID), int64(candidate.RightCustomerID), reason, candidate.Evidence)
		if err != nil {
			return identityapp.LinkResult{}, errStore
		}
		_, err = tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by='system',resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1`, candidate.ID)
		if err != nil {
			return identityapp.LinkResult{}, errStore
		}
		candidate.Status = "rejected"
		return identityapp.LinkResult{Status: identityapp.LinkConflict, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Conflict: &conflict}, nil
	}
	leftWecom, _ := hasWeCom(ctx, tx, int64(candidate.LeftCustomerID))
	rightWecom, _ := hasWeCom(ctx, tx, int64(candidate.RightCustomerID))
	if leftWecom != rightWecom && ((leftWecom && int64(command.SurvivorCustomerID) != int64(candidate.LeftCustomerID)) || (rightWecom && int64(command.SurvivorCustomerID) != int64(candidate.RightCustomerID))) {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	loser := int64(candidate.LeftCustomerID)
	if int64(command.SurvivorCustomerID) == int64(candidate.LeftCustomerID) {
		loser = int64(candidate.RightCustomerID)
	}
	// The migration's composite FK deliberately permits a merge ledger only
	// after the candidate names its explicit survivor. Both writes share this
	// transaction, so no externally visible intermediate confirmation exists.
	_, err = tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='confirmed',selected_survivor_customer_id=$2,resolved_by=$3,resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1 AND status='open'`, candidate.ID, command.SurvivorCustomerID, command.Operator)
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	merge, err := performMerge(ctx, tx, candidate, loser, int64(command.SurvivorCustomerID), command.Operator)
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	candidate.Status = "confirmed"
	return identityapp.LinkResult{Status: identityapp.LinkMerged, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Merge: &merge}, nil
}

func (store *PostgresStore) RevertMerge(ctx context.Context, mergeID int64) (identityapp.MergeRecord, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	var from, to int64
	if err = tx.QueryRow(ctx, `SELECT from_customer_id,to_customer_id FROM customer_merges WHERE id=$1`, mergeID).Scan(&from, &to); err != nil {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	if err = lockCustomers(ctx, tx, []int64{from, to}); err != nil {
		return identityapp.MergeRecord{}, errStore
	}
	merge, err := lockMerge(ctx, tx, mergeID)
	if err != nil || merge.Reversed {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	fromC, e1 := activeOrMergedCustomer(ctx, tx, from)
	toC, e2 := activeCustomerLocked(ctx, tx, to)
	if e1 != nil || e2 != nil || fromC.Status != "merged" || fromC.MergedTo != to || fromC.Lineage != merge.FromLineageAfter || toC.Lineage != merge.ToLineageAfter {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	rows, err := tx.Query(ctx, `SELECT identity_id,identity_version_after FROM customer_merge_identity_members WHERE merge_id=$1 ORDER BY identity_id FOR UPDATE`, mergeID)
	if err != nil {
		return identityapp.MergeRecord{}, errStore
	}
	defer rows.Close()
	type member struct{ id, version int64 }
	members := []member{}
	for rows.Next() {
		var m member
		if err = rows.Scan(&m.id, &m.version); err != nil {
			return identityapp.MergeRecord{}, errStore
		}
		members = append(members, m)
	}
	if rows.Err() != nil {
		return identityapp.MergeRecord{}, errStore
	}
	for _, m := range members {
		var owner, version int64
		var status string
		err = tx.QueryRow(ctx, `SELECT customer_id,version,status FROM customer_identities WHERE id=$1`, m.id).Scan(&owner, &version, &status)
		if err != nil || owner != to || version != m.version || status != "active" {
			return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
		}
	}
	var later bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_merges WHERE id>$1 AND reversible_status<>'reversed' AND (from_customer_id=$2 OR to_customer_id=$2 OR from_customer_id=$3 OR to_customer_id=$3))`, mergeID, from, to).Scan(&later)
	if err != nil {
		return identityapp.MergeRecord{}, errStore
	}
	if later {
		return identityapp.MergeRecord{}, identityapp.ErrMergeNotReversible
	}
	for _, m := range members {
		if _, err = tx.Exec(ctx, `UPDATE customer_identities SET customer_id=$2,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND customer_id=$3 AND version=$4`, m.id, from, to, m.version); err != nil {
			return identityapp.MergeRecord{}, errStore
		}
		if _, err = tx.Exec(ctx, `UPDATE customer_merge_identity_members SET restored_at=CURRENT_TIMESTAMP,identity_version_after_restore=identity_version_after+1 WHERE merge_id=$1 AND identity_id=$2`, mergeID, m.id); err != nil {
			return identityapp.MergeRecord{}, errStore
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE customers SET status='active',merged_into_customer_id=NULL,merged_at=NULL,version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, from); err != nil {
		return identityapp.MergeRecord{}, errStore
	}
	if _, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, to); err != nil {
		return identityapp.MergeRecord{}, errStore
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_merges SET reversible_status='reversed',reversed_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1`, mergeID); err != nil {
		return identityapp.MergeRecord{}, errStore
	}
	merge.Reversed = true
	return merge, nil
}

// Helpers below deliberately keep all SQL error text private. The public
// application contract maps expected failures to app errors above.
type customer struct {
	ID               int64
	Status           string
	MergedTo         int64
	Version, Lineage int64
}

func lockIdentityKey(ctx context.Context, tx pgx.Tx, ref identitydomain.NormalizedReference) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identityLockValue(ref))
	return err
}

func identityLockValue(ref identitydomain.NormalizedReference) string {
	// PostgreSQL text cannot contain NUL. Length prefixes preserve the full
	// namespace tuple without delimiter ambiguity or PII-bearing error output.
	parts := []string{string(ref.Kind), ref.Scope, ref.NormalizedValue}
	value := ""
	for _, part := range parts {
		value += strconv.Itoa(len(part)) + ":" + part
	}
	return value
}
func lockCustomers(ctx context.Context, tx pgx.Tx, ids []int64) error {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `SELECT id FROM customers WHERE id=$1 FOR UPDATE`, id); err != nil {
			return err
		}
	}
	return nil
}
func activeCustomerLocked(ctx context.Context, tx pgx.Tx, id int64) (customer, error) {
	var c customer
	err := tx.QueryRow(ctx, `SELECT id,status,COALESCE(merged_into_customer_id,0),version,lineage_version FROM customers WHERE id=$1`, id).Scan(&c.ID, &c.Status, &c.MergedTo, &c.Version, &c.Lineage)
	if err != nil || c.Status != "active" {
		return customer{}, errors.New("inactive")
	}
	return c, nil
}
func activeOrMergedCustomer(ctx context.Context, tx pgx.Tx, id int64) (customer, error) {
	var c customer
	err := tx.QueryRow(ctx, `SELECT id,status,COALESCE(merged_into_customer_id,0),version,lineage_version FROM customers WHERE id=$1`, id).Scan(&c.ID, &c.Status, &c.MergedTo, &c.Version, &c.Lineage)
	return c, err
}
func lockActiveRoot(ctx context.Context, tx pgx.Tx, id customerdomain.CustomerID) (customer, error) {
	var root int64
	err := tx.QueryRow(ctx, `WITH RECURSIVE l AS (SELECT id,status,merged_into_customer_id FROM customers WHERE id=$1 UNION ALL SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN l ON c.id=l.merged_into_customer_id) SELECT id FROM l WHERE status<>'merged' LIMIT 1`, id).Scan(&root)
	if err != nil {
		return customer{}, err
	}
	if err = lockCustomers(ctx, tx, []int64{root}); err != nil {
		return customer{}, err
	}
	return activeCustomerLocked(ctx, tx, root)
}
func existingIdentity(ctx context.Context, tx pgx.Tx, ref identitydomain.NormalizedReference) (identityapp.StoredIdentity, bool, error) {
	var id, cid int64
	var kind, scope, value, assurance, source string
	var nv int16
	err := tx.QueryRow(ctx, `SELECT i.id,i.customer_id,i.kind,i.scope_key,i.normalized_value,i.assurance,i.source,i.normalizer_version FROM customer_identities i WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'`, string(ref.Kind), ref.Scope, ref.NormalizedValue).Scan(&id, &cid, &kind, &scope, &value, &assurance, &source, &nv)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityapp.StoredIdentity{}, false, nil
	}
	if err != nil {
		return identityapp.StoredIdentity{}, false, errStore
	}
	root, err := lockActiveRoot(ctx, tx, customerdomain.CustomerID(cid))
	if err != nil {
		return identityapp.StoredIdentity{}, false, errors.New("identity store root lookup")
	}
	return identityapp.StoredIdentity{ID: id, CustomerID: customerdomain.CustomerID(root.ID), Reference: identitydomain.NormalizedReference{Kind: identitydomain.Kind(kind), Scope: scope, NormalizedValue: value, Assurance: identitydomain.Assurance(assurance), Source: source, NormalizerVersion: nv}}, true, nil
}

func link(ctx context.Context, tx pgx.Tx, command identityapp.LinkCommand) (identityapp.LinkResult, error) {
	ref := command.Target.Reference()
	if err := lockIdentityKey(ctx, tx, ref); err != nil {
		return identityapp.LinkResult{}, errStore
	}
	// The key lock is always taken before roots; root lock order is ascending.
	source, err := lockActiveRoot(ctx, tx, command.SourceCustomerID)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	return linkLocked(ctx, tx, source, command.Target, command.Evidence)
}

func linkLocked(ctx context.Context, tx pgx.Tx, source customer, fact identitydomain.VerifiedFact, evidence identitydomain.LinkEvidence) (identityapp.LinkResult, error) {
	ref := fact.Reference()
	existing, found, err := existingIdentity(ctx, tx, ref)
	if err != nil {
		return identityapp.LinkResult{}, err
	}
	if !found {
		if evidence.Strength != identitydomain.EvidenceStrong {
			return identityapp.LinkResult{}, identityapp.ErrInsufficientLinkEvidence
		}
		if reason, err := sameRootConflict(ctx, tx, source.ID, ref); err != nil {
			return identityapp.LinkResult{}, errStore
		} else if reason != "" {
			conflict, err := createConflict(ctx, tx, source.ID, source.ID, reason, evidence)
			if err != nil {
				return identityapp.LinkResult{}, errStore
			}
			return identityapp.LinkResult{Status: identityapp.LinkConflict, CustomerID: customerdomain.CustomerID(source.ID), Conflict: &conflict}, nil
		}
		var identityID int64
		err = tx.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,$2,$3,$4,'verified',$5,$6,CURRENT_TIMESTAMP) RETURNING id`, source.ID, string(ref.Kind), ref.Scope, ref.NormalizedValue, ref.Source, ref.NormalizerVersion).Scan(&identityID)
		if err != nil {
			return identityapp.LinkResult{}, errStore
		}
		if _, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1`, source.ID); err != nil {
			return identityapp.LinkResult{}, errStore
		}
		return identityapp.LinkResult{Status: identityapp.LinkAttached, CustomerID: customerdomain.CustomerID(source.ID), IdentityID: identityID}, nil
	}
	if existing.CustomerID == customerdomain.CustomerID(source.ID) {
		return identityapp.LinkResult{Status: identityapp.LinkAlreadyLinked, CustomerID: existing.CustomerID, IdentityID: existing.ID}, nil
	}
	// existingIdentity locked target root after source; lock both roots again in
	// canonical order before candidate materialization to avoid a lock inversion.
	if err = lockCustomers(ctx, tx, []int64{source.ID, int64(existing.CustomerID)}); err != nil {
		return identityapp.LinkResult{}, errStore
	}
	left, err := activeCustomerLocked(ctx, tx, source.ID)
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	right, err := activeCustomerLocked(ctx, tx, int64(existing.CustomerID))
	if err != nil {
		return identityapp.LinkResult{}, identityapp.ErrInvalidLinkCommand
	}
	candidate, err := createCandidate(ctx, tx, left, right, evidence, existing.ID)
	if err != nil {
		return identityapp.LinkResult{}, errStore
	}
	return identityapp.LinkResult{Status: identityapp.LinkCandidate, CustomerID: customerdomain.CustomerID(left.ID), IdentityID: existing.ID, Candidate: &candidate}, nil
}

func writeEvidence(ctx context.Context, tx pgx.Tx, left, right, leftIdentity, rightIdentity int64, evidence identitydomain.LinkEvidence) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO identity_link_evidence(left_customer_id,right_customer_id,left_identity_id,right_identity_id,evidence_type,strength,source,source_event_id,evidence_digest,policy_version) VALUES($1,NULLIF($2,0),NULLIF($3,0),NULLIF($4,0),$5,$6,$7,$8,$9,$10) RETURNING id`, left, right, leftIdentity, rightIdentity, evidence.Type, string(evidence.Strength), evidence.Source, evidence.EventID, evidence.Digest, evidence.PolicyVersion).Scan(&id)
	return id, err
}

func createCandidate(ctx context.Context, tx pgx.Tx, left, right customer, evidence identitydomain.LinkEvidence, rightIdentity int64) (identityapp.MergeCandidate, error) {
	// Candidate row is locked after roots. The partial unique index is the final
	// cross-session arbiter; retries reread its committed candidate.
	var id int64
	var oldStrength string
	var oldLeft, oldRight, oldLeftVersion, oldRightVersion int64
	var status, reason string
	err := tx.QueryRow(ctx, `SELECT id,left_customer_id,right_customer_id,left_customer_version,right_customer_version,evidence_strength,reason,status FROM customer_merge_candidates WHERE status='open' AND LEAST(left_customer_id,right_customer_id)=LEAST($1::bigint,$2::bigint) AND GREATEST(left_customer_id,right_customer_id)=GREATEST($1::bigint,$2::bigint) FOR UPDATE`, left.ID, right.ID).Scan(&id, &oldLeft, &oldRight, &oldLeftVersion, &oldRightVersion, &oldStrength, &reason, &status)
	if err == nil {
		if oldLeftVersion == left.Version && oldRightVersion == right.Version {
			if rank(evidence.Strength) > rank(identitydomain.EvidenceStrength(oldStrength)) {
				evidenceID, e := writeEvidence(ctx, tx, left.ID, right.ID, 0, rightIdentity, evidence)
				if e != nil {
					return identityapp.MergeCandidate{}, e
				}
				_, e = tx.Exec(ctx, `UPDATE customer_merge_candidates SET evidence_id=$2,evidence_strength=$3,reason=$4,version=version+1 WHERE id=$1`, id, evidenceID, string(evidence.Strength), reasonFor(evidence))
				if e != nil {
					return identityapp.MergeCandidate{}, e
				}
				oldStrength = string(evidence.Strength)
				reason = reasonFor(evidence)
			}
			return identityapp.MergeCandidate{ID: id, LeftCustomerID: customerdomain.CustomerID(oldLeft), RightCustomerID: customerdomain.CustomerID(oldRight), Evidence: evidence, Reason: reason, Status: status, LeftVersion: oldLeftVersion, RightVersion: oldRightVersion}, nil
		}
		if _, e := tx.Exec(ctx, `UPDATE customer_merge_candidates SET status='rejected',resolved_by='system',resolved_at=CURRENT_TIMESTAMP,version=version+1 WHERE id=$1`, id); e != nil {
			return identityapp.MergeCandidate{}, e
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return identityapp.MergeCandidate{}, errStore
	}
	evidenceID, err := writeEvidence(ctx, tx, left.ID, right.ID, 0, rightIdentity, evidence)
	if err != nil {
		return identityapp.MergeCandidate{}, errors.New("identity store candidate evidence")
	}
	err = tx.QueryRow(ctx, `INSERT INTO customer_merge_candidates(left_customer_id,right_customer_id,left_customer_version,right_customer_version,evidence_id,evidence_strength,reason) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, left.ID, right.ID, left.Version, right.Version, evidenceID, string(evidence.Strength), reasonFor(evidence)).Scan(&id)
	if err != nil {
		if existing, scanErr := lockCandidateByPair(ctx, tx, left.ID, right.ID); scanErr == nil {
			return existing, nil
		}
		return identityapp.MergeCandidate{}, errors.New("identity store candidate insert")
	}
	return identityapp.MergeCandidate{ID: id, LeftCustomerID: customerdomain.CustomerID(left.ID), RightCustomerID: customerdomain.CustomerID(right.ID), Evidence: evidence, Reason: reasonFor(evidence), Status: "open", LeftVersion: left.Version, RightVersion: right.Version}, nil
}
func reasonFor(e identitydomain.LinkEvidence) string {
	if e.Strength == identitydomain.EvidenceStrong {
		return "cross_root_link_requires_confirmation"
	}
	return "non_strong_evidence"
}
func rank(v identitydomain.EvidenceStrength) int {
	if v == identitydomain.EvidenceStrong {
		return 3
	}
	if v == identitydomain.EvidenceMedium {
		return 2
	}
	if v == identitydomain.EvidenceWeak {
		return 1
	}
	return 0
}
func lockCandidateByPair(ctx context.Context, tx pgx.Tx, left, right int64) (identityapp.MergeCandidate, error) {
	var id, l, r, lv, rv int64
	var strength, reason, status string
	err := tx.QueryRow(ctx, `SELECT id,left_customer_id,right_customer_id,left_customer_version,right_customer_version,evidence_strength,reason,status FROM customer_merge_candidates WHERE status='open' AND LEAST(left_customer_id,right_customer_id)=LEAST($1::bigint,$2::bigint) AND GREATEST(left_customer_id,right_customer_id)=GREATEST($1::bigint,$2::bigint) FOR UPDATE`, left, right).Scan(&id, &l, &r, &lv, &rv, &strength, &reason, &status)
	if err != nil {
		return identityapp.MergeCandidate{}, err
	}
	return identityapp.MergeCandidate{ID: id, LeftCustomerID: customerdomain.CustomerID(l), RightCustomerID: customerdomain.CustomerID(r), Evidence: identitydomain.LinkEvidence{Strength: identitydomain.EvidenceStrength(strength)}, Reason: reason, Status: status, LeftVersion: lv, RightVersion: rv}, nil
}

func sameRootConflict(ctx context.Context, tx pgx.Tx, customerID int64, ref identitydomain.NormalizedReference) (string, error) {
	if !singleStrong(ref.Kind) {
		return "", nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities WHERE customer_id=$1 AND status='active' AND assurance='verified' AND kind=$2 AND scope_key=$3 AND normalized_value<>$4)`, customerID, string(ref.Kind), ref.Scope, ref.NormalizedValue).Scan(&exists)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	if ref.Kind == identitydomain.KindWeComExternalUserID {
		return "two_wecom_identities_same_root", nil
	}
	return "single_value_strong_namespace", nil
}
func hasWeCom(ctx context.Context, tx pgx.Tx, customerID int64) (bool, error) {
	var yes bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities WHERE customer_id=$1 AND status='active' AND kind='wecom_external_userid')`, customerID).Scan(&yes)
	return yes, err
}
func crossRootConflict(ctx context.Context, tx pgx.Tx, left, right int64) (string, error) {
	lw, e := hasWeCom(ctx, tx, left)
	if e != nil {
		return "", e
	}
	rw, e := hasWeCom(ctx, tx, right)
	if e != nil {
		return "", e
	}
	if lw && rw {
		return "two_wecom_roots", nil
	}
	var exists bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_identities a JOIN customer_identities b ON a.kind=b.kind AND a.scope_key=b.scope_key AND a.normalized_value<>b.normalized_value WHERE a.customer_id=$1 AND b.customer_id=$2 AND a.status='active' AND b.status='active' AND a.assurance='verified' AND b.assurance='verified' AND a.kind IN ('wecom_external_userid','unionid','mp_openid','oa_openid','alipay_user_id','alipay_oauth_user_id','alipay_oauth_open_id','alipay_buyer_id','alipay_buyer_open_id','first_party_member_id'))`, left, right).Scan(&exists)
	if e != nil {
		return "", e
	}
	if exists {
		return "single_value_strong_namespace", nil
	}
	return "", nil
}
func singleStrong(k identitydomain.Kind) bool {
	return k != identitydomain.KindPhone && k != identitydomain.KindExtension
}

type intentRow struct {
	ID, SourceID, SourceVersion                    int64
	TargetKind, ExpectedScope, Status, Fingerprint string
	ExpiresAt                                      time.Time
	Result                                         identityapp.LinkResult
}

func lockIntent(ctx context.Context, tx pgx.Tx, hash string) (intentRow, error) {
	var row intentRow
	var resultStatus string
	var customerID, identityID, candidateID, conflictID *int64
	err := tx.QueryRow(ctx, `SELECT id,source_customer_id,source_customer_version,target_kind,expected_scope_key,status,expires_at,COALESCE(consumption_fingerprint,''),COALESCE(result_status,''),consumed_customer_id,consumed_identity_id,result_candidate_id,result_conflict_id FROM identity_link_intents WHERE token_hash=$1 FOR UPDATE`, hash).Scan(&row.ID, &row.SourceID, &row.SourceVersion, &row.TargetKind, &row.ExpectedScope, &row.Status, &row.ExpiresAt, &row.Fingerprint, &resultStatus, &customerID, &identityID, &candidateID, &conflictID)
	if err != nil {
		return intentRow{}, err
	}
	if resultStatus != "" {
		row.Result = identityapp.LinkResult{Status: identityapp.LinkStatus(resultStatus)}
		if customerID != nil {
			row.Result.CustomerID = customerdomain.CustomerID(*customerID)
		}
		if identityID != nil {
			row.Result.IdentityID = *identityID
		}
		if candidateID != nil {
			c, e := lockCandidate(ctx, tx, *candidateID)
			if e == nil {
				row.Result.Candidate = &c
			}
		}
		if conflictID != nil {
			c, e := readConflict(ctx, tx, *conflictID)
			if e == nil {
				row.Result.Conflict = &c
			}
		}
	}
	return row, nil
}
func candidateID(result identityapp.LinkResult) int64 {
	if result.Candidate != nil {
		return result.Candidate.ID
	}
	return 0
}
func conflictID(result identityapp.LinkResult) int64 {
	if result.Conflict != nil {
		return result.Conflict.ID
	}
	return 0
}
func nullableID(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
func replay(original identityapp.LinkResult) identityapp.LinkResult {
	original.ReplayOf = original.Status
	original.Status = identityapp.LinkIntentReplay
	return original
}

func lockCandidate(ctx context.Context, tx pgx.Tx, id int64) (identityapp.MergeCandidate, error) {
	var c identityapp.MergeCandidate
	var strength string
	err := tx.QueryRow(ctx, `SELECT c.id,c.left_customer_id,c.right_customer_id,c.left_customer_version,c.right_customer_version,c.evidence_strength,c.reason,c.status,e.evidence_type,e.source,e.source_event_id,e.evidence_digest,e.policy_version FROM customer_merge_candidates c JOIN identity_link_evidence e ON e.id=c.evidence_id WHERE c.id=$1 FOR UPDATE`, id).Scan(&c.ID, &c.LeftCustomerID, &c.RightCustomerID, &c.LeftVersion, &c.RightVersion, &strength, &c.Reason, &c.Status, &c.Evidence.Type, &c.Evidence.Source, &c.Evidence.EventID, &c.Evidence.Digest, &c.Evidence.PolicyVersion)
	c.Evidence.Strength = identitydomain.EvidenceStrength(strength)
	return c, err
}
func readConflict(ctx context.Context, tx pgx.Tx, id int64) (identityapp.Conflict, error) {
	var c identityapp.Conflict
	var strength string
	err := tx.QueryRow(ctx, `SELECT c.id,c.left_customer_id,c.right_customer_id,c.reason,e.evidence_type,e.strength,e.source,e.source_event_id,e.evidence_digest,e.policy_version FROM customer_identity_conflicts c LEFT JOIN identity_link_evidence e ON e.id=c.evidence_id WHERE c.id=$1`, id).Scan(&c.ID, &c.LeftCustomerID, &c.RightCustomerID, &c.Reason, &c.Evidence.Type, &strength, &c.Evidence.Source, &c.Evidence.EventID, &c.Evidence.Digest, &c.Evidence.PolicyVersion)
	c.Evidence.Strength = identitydomain.EvidenceStrength(strength)
	return c, err
}
func lockMerge(ctx context.Context, tx pgx.Tx, id int64) (identityapp.MergeRecord, error) {
	var m identityapp.MergeRecord
	var reversedAt *time.Time
	var status string
	err := tx.QueryRow(ctx, `SELECT id,candidate_id,from_customer_id,to_customer_id,from_customer_version_before,from_customer_version_after,to_customer_version_before,to_customer_version_after,from_lineage_version_before,from_lineage_version_after,to_lineage_version_before,to_lineage_version_after,rule,operator,reversible_status,reversed_at FROM customer_merges WHERE id=$1 FOR UPDATE`, id).Scan(&m.ID, &m.CandidateID, &m.FromCustomerID, &m.ToCustomerID, &m.FromVersionBefore, &m.FromVersionAfter, &m.ToVersionBefore, &m.ToVersionAfter, &m.FromLineageBefore, &m.FromLineageAfter, &m.ToLineageBefore, &m.ToLineageAfter, &m.Rule, &m.Operator, &status, &reversedAt)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	m.Reversed = status == "reversed"
	return m, nil
}
func createConflict(ctx context.Context, tx pgx.Tx, left, right int64, reason string, e identitydomain.LinkEvidence) (identityapp.Conflict, error) {
	// canonicalization is only for pair de-duplication, never a survivor choice.
	if right < left {
		left, right = right, left
	}
	evidenceID, err := writeEvidence(ctx, tx, left, right, 0, 0, e)
	if err != nil {
		return identityapp.Conflict{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO customer_identity_conflicts(left_customer_id,right_customer_id,evidence_id,reason) VALUES($1,$2,$3,$4) ON CONFLICT (LEAST(left_customer_id,right_customer_id),GREATEST(left_customer_id,right_customer_id),reason) WHERE status='open' DO UPDATE SET updated_at=CURRENT_TIMESTAMP RETURNING id`, left, right, evidenceID, reason).Scan(&id)
	if err != nil {
		return identityapp.Conflict{}, err
	}
	return identityapp.Conflict{ID: id, LeftCustomerID: customerdomain.CustomerID(left), RightCustomerID: customerdomain.CustomerID(right), Reason: reason, Evidence: e}, nil
}

func performMerge(ctx context.Context, tx pgx.Tx, candidate identityapp.MergeCandidate, loser, survivor int64, operator string) (identityapp.MergeRecord, error) {
	from, err := activeCustomerLocked(ctx, tx, loser)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	to, err := activeCustomerLocked(ctx, tx, survivor)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	// Members are locked only after candidate and roots. Snapshot every active
	// member before moving it, so reversal cannot move later identities.
	rows, err := tx.Query(ctx, `SELECT id,version FROM customer_identities WHERE customer_id=$1 AND status='active' ORDER BY id FOR UPDATE`, loser)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	type member struct{ id, version int64 }
	members := []member{}
	for rows.Next() {
		var m member
		if err = rows.Scan(&m.id, &m.version); err != nil {
			rows.Close()
			return identityapp.MergeRecord{}, err
		}
		members = append(members, m)
	}
	rows.Close()
	if rows.Err() != nil {
		return identityapp.MergeRecord{}, rows.Err()
	}
	var evidenceID int64
	err = tx.QueryRow(ctx, `SELECT evidence_id FROM customer_merge_candidates WHERE id=$1`, candidate.ID).Scan(&evidenceID)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	var mergeID int64
	err = tx.QueryRow(ctx, `INSERT INTO customer_merges(candidate_id,candidate_left_customer_id,candidate_right_customer_id,from_customer_id,to_customer_id,evidence_id,from_customer_version_before,from_customer_version_after,to_customer_version_before,to_customer_version_after,from_lineage_version_before,from_lineage_version_after,to_lineage_version_before,to_lineage_version_after,rule,operator,source) VALUES($1,$2,$3,$4,$5,$6,$7,$7+1,$8,$8+1,$9,$9+1,$10,$10+1,'confirmed_candidate',$11,'identity') RETURNING id`, candidate.ID, candidate.LeftCustomerID, candidate.RightCustomerID, loser, survivor, evidenceID, from.Version, to.Version, from.Lineage, to.Lineage, operator).Scan(&mergeID)
	if err != nil {
		return identityapp.MergeRecord{}, err
	}
	for _, m := range members {
		tag, e := tx.Exec(ctx, `UPDATE customer_identities SET customer_id=$2,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND customer_id=$3 AND version=$4`, m.id, survivor, loser, m.version)
		if e != nil || tag.RowsAffected() != 1 {
			return identityapp.MergeRecord{}, errStore
		}
		if _, e = tx.Exec(ctx, `INSERT INTO customer_merge_identity_members(merge_id,identity_id,from_customer_id,to_customer_id,identity_version_before,identity_version_after) VALUES($1,$2,$3,$4,$5,$5+1)`, mergeID, m.id, loser, survivor, m.version); e != nil {
			return identityapp.MergeRecord{}, e
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE customers SET status='merged',merged_into_customer_id=$2,merged_at=CURRENT_TIMESTAMP,version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active' AND version=$3`, loser, survivor, from.Version); err != nil {
		return identityapp.MergeRecord{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE customers SET version=version+1,lineage_version=lineage_version+1,updated_at=CURRENT_TIMESTAMP WHERE id=$1 AND status='active' AND version=$2`, survivor, to.Version); err != nil {
		return identityapp.MergeRecord{}, err
	}
	return identityapp.MergeRecord{ID: mergeID, CandidateID: candidate.ID, FromCustomerID: customerdomain.CustomerID(loser), ToCustomerID: customerdomain.CustomerID(survivor), FromVersionBefore: from.Version, FromVersionAfter: from.Version + 1, ToVersionBefore: to.Version, ToVersionAfter: to.Version + 1, FromLineageBefore: from.Lineage, FromLineageAfter: from.Lineage + 1, ToLineageBefore: to.Lineage, ToLineageAfter: to.Lineage + 1, Evidence: candidate.Evidence, Rule: "confirmed_candidate", Operator: operator}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(token string) string {
	d := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(d[:])
}
func consumptionFingerprint(command identityapp.ConsumeLinkIntentCommand) string {
	r := command.Target.Reference()
	fields := []string{string(r.Kind), r.Scope, r.NormalizedValue, string(r.Assurance), r.Source, strconv.FormatInt(int64(r.NormalizerVersion), 10), command.Evidence.Type, string(command.Evidence.Strength), command.Evidence.Source, command.Evidence.EventID, command.Evidence.Digest, command.Evidence.PolicyVersion}
	h := sha256.New()
	for _, f := range fields {
		_, _ = h.Write([]byte(strconv.Itoa(len(f))))
		_, _ = h.Write([]byte{':'})
		_, _ = h.Write([]byte(f))
	}
	return base64.RawStdEncoding.EncodeToString(h.Sum(nil))
}
