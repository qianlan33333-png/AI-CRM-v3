package query

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PostgreSQL reads only Identity-owned tables and requires a transaction-bound
// context for every operation, including administration reads.
type PostgreSQL struct{}

var _ Reader = PostgreSQL{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

func (PostgreSQL) Customer(ctx context.Context, customerID customerdomain.CustomerID) (CustomerDetail, error) {
	if customerID < 1 {
		return CustomerDetail{}, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CustomerDetail{}, err
	}

	detail := CustomerDetail{CustomerID: customerID, Identities: []IdentitySummary{}, MergeLineage: []MergeLineageSummary{}}
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE customer_chain AS (
			SELECT id, status, merged_into_customer_id, 0 AS depth, ARRAY[id] AS visited
			FROM customers
			WHERE id = $1
			UNION ALL
			SELECT next.id, next.status, next.merged_into_customer_id, chain.depth + 1, chain.visited || next.id
			FROM customer_chain chain
			JOIN customers next ON next.id = chain.merged_into_customer_id
			WHERE NOT next.id = ANY(chain.visited)
		)
		SELECT
			(SELECT status FROM customer_chain WHERE id = $1),
			id,
			status
		FROM customer_chain
		ORDER BY depth DESC
		LIMIT 1`, int64(customerID)).Scan(&detail.Status, &detail.CanonicalCustomerID, &detail.CanonicalStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerDetail{}, ErrNotFound
	}
	if err != nil {
		return CustomerDetail{}, fmt.Errorf("query customer root: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT kind, scope_key
		FROM customer_identities
		WHERE customer_id = $1 AND status = 'active'
		ORDER BY id`, int64(detail.CanonicalCustomerID))
	if err != nil {
		return CustomerDetail{}, fmt.Errorf("query active customer identities: %w", err)
	}
	for rows.Next() {
		var identity IdentitySummary
		if err = rows.Scan(&identity.Kind, &identity.Scope); err != nil {
			rows.Close()
			return CustomerDetail{}, fmt.Errorf("scan active customer identity: %w", err)
		}
		detail.Identities = append(detail.Identities, identity)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return CustomerDetail{}, fmt.Errorf("iterate active customer identities: %w", err)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		WITH RECURSIVE related_customers(id) AS (
			VALUES ($1::bigint)
			UNION
			SELECT CASE WHEN merge.from_customer_id = related.id
				THEN merge.to_customer_id ELSE merge.from_customer_id END
			FROM related_customers related
			JOIN customer_merges merge
				ON merge.from_customer_id = related.id OR merge.to_customer_id = related.id
		)
		SELECT DISTINCT merge.id, merge.from_customer_id, merge.to_customer_id,
			merge.reversible_status, merge.merged_at, merge.reversed_at
		FROM customer_merges merge
		JOIN related_customers related
			ON related.id = merge.from_customer_id OR related.id = merge.to_customer_id
		ORDER BY merge.id`, int64(customerID))
	if err != nil {
		return CustomerDetail{}, fmt.Errorf("query customer merge lineage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lineage MergeLineageSummary
		if err = rows.Scan(&lineage.ID, &lineage.FromCustomerID, &lineage.ToCustomerID,
			&lineage.ReversibleStatus, &lineage.MergedAt, &lineage.ReversedAt); err != nil {
			return CustomerDetail{}, fmt.Errorf("scan customer merge lineage: %w", err)
		}
		detail.MergeLineage = append(detail.MergeLineage, lineage)
	}
	if err = rows.Err(); err != nil {
		return CustomerDetail{}, fmt.Errorf("iterate customer merge lineage: %w", err)
	}
	return detail, nil
}

func (PostgreSQL) Conflicts(ctx context.Context, options ListOptions) (ConflictPage, error) {
	options, err := normalizeOptions(options, map[string]struct{}{"open": {}, "resolved": {}, "ignored": {}})
	if err != nil {
		return ConflictPage{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return ConflictPage{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, left_customer_id, right_customer_id, reason, status, created_at, resolved_at
		FROM customer_identity_conflicts
		WHERE status = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`, options.Status, options.Limit, options.Offset)
	if err != nil {
		return ConflictPage{}, fmt.Errorf("query identity conflicts: %w", err)
	}
	defer rows.Close()
	page := ConflictPage{Items: []Conflict{}, Limit: options.Limit, Offset: options.Offset}
	for rows.Next() {
		var item Conflict
		if err = rows.Scan(&item.ID, &item.LeftCustomerID, &item.RightCustomerID, &item.Reason,
			&item.Status, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return ConflictPage{}, fmt.Errorf("scan identity conflict: %w", err)
		}
		item.Reason = publicReason(item.Reason)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return ConflictPage{}, fmt.Errorf("iterate identity conflicts: %w", err)
	}
	return page, nil
}

func (PostgreSQL) MergeCandidates(ctx context.Context, options ListOptions) (MergeCandidatePage, error) {
	options, err := normalizeOptions(options, map[string]struct{}{"open": {}, "confirmed": {}, "rejected": {}})
	if err != nil {
		return MergeCandidatePage{}, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return MergeCandidatePage{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, left_customer_id, right_customer_id, evidence_strength, reason, status,
			selected_survivor_customer_id, created_at, resolved_at
		FROM customer_merge_candidates
		WHERE status = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`, options.Status, options.Limit, options.Offset)
	if err != nil {
		return MergeCandidatePage{}, fmt.Errorf("query merge candidates: %w", err)
	}
	defer rows.Close()
	page := MergeCandidatePage{Items: []MergeCandidate{}, Limit: options.Limit, Offset: options.Offset}
	for rows.Next() {
		var item MergeCandidate
		if err = rows.Scan(&item.ID, &item.LeftCustomerID, &item.RightCustomerID, &item.EvidenceStrength,
			&item.Reason, &item.Status, &item.SelectedSurvivorCustomerID, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return MergeCandidatePage{}, fmt.Errorf("scan merge candidate: %w", err)
		}
		item.Reason = publicReason(item.Reason)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return MergeCandidatePage{}, fmt.Errorf("iterate merge candidates: %w", err)
	}
	return page, nil
}

func normalizeOptions(options ListOptions, allowed map[string]struct{}) (ListOptions, error) {
	if options.Status == "" {
		options.Status = "open"
	}
	if _, ok := allowed[options.Status]; !ok || options.Offset < 0 || options.Limit < 0 || options.Limit > MaximumLimit {
		return ListOptions{}, ErrInvalidQuery
	}
	if options.Limit == 0 {
		options.Limit = DefaultLimit
	}
	return options, nil
}

// reason columns are intentionally free text in the ledger. Only application
// reason codes may cross the HTTP query boundary; unexpected historical text
// is collapsed so an accidentally stored identifier cannot become an API leak.
func publicReason(reason string) string {
	switch reason {
	case "cross_root_link_requires_confirmation", "non_strong_evidence",
		"two_wecom_roots", "two_wecom_identities_same_root", "single_value_strong_namespace":
		return reason
	default:
		return "other"
	}
}
