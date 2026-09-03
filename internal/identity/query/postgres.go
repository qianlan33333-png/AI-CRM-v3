package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// PostgreSQL reads only Identity-owned tables and requires a transaction-bound
// context for every operation, including administration reads.
type PostgreSQL struct{}

var _ Reader = PostgreSQL{}
var _ identityport.DirectoryIdentityReader = PostgreSQL{}
var _ identityport.CommerceResolver = PostgreSQL{}
var _ identityport.PaymentIdentityReader = PostgreSQL{}
var _ identityport.HXCUnionIDBatchResolver = PostgreSQL{}
var _ identityport.ExternalIdentityValueReader = PostgreSQL{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

func (PostgreSQL) ResolveHXCUnionIDs(ctx context.Context, references []identityport.ScopedUnionID) ([]identityport.ScopedUnionIDResult, error) {
	if len(references) == 0 {
		return []identityport.ScopedUnionIDResult{}, nil
	}
	if len(references) > 1000 {
		return nil, identitydomain.ErrInvalidReference
	}
	positions := make([]int32, 0, len(references))
	scopes := make([]string, 0, len(references))
	values := make([]string, 0, len(references))
	for _, reference := range references {
		if reference.Position < 0 || !strings.HasPrefix(reference.Scope, "wechat-open-platform:") || len(reference.Scope) <= len("wechat-open-platform:") || strings.TrimSpace(reference.UnionID) != reference.UnionID || reference.UnionID == "" {
			return nil, identitydomain.ErrInvalidReference
		}
		positions = append(positions, int32(reference.Position))
		scopes = append(scopes, reference.Scope)
		values = append(values, reference.UnionID)
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE input AS (
		SELECT * FROM unnest($1::integer[], $2::text[], $3::text[]) AS i(position, scope_key, value)
	), lineage AS (
		SELECT i.position, c.id AS customer_id, c.status, c.merged_into_customer_id, ARRAY[c.id] visited
		FROM input i JOIN customer_identities ci ON ci.kind='unionid' AND ci.scope_key=i.scope_key AND ci.normalized_value=i.value AND ci.status='active'
		JOIN customers c ON c.id=ci.customer_id
		UNION ALL
		SELECT l.position,c.id,c.status,c.merged_into_customer_id,l.visited||c.id FROM lineage l JOIN customers c ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	), roots AS (
		SELECT position,customer_id FROM lineage WHERE status<>'merged'
	), aggregate_roots AS (
		SELECT position, count(DISTINCT customer_id) AS root_count, min(customer_id) AS customer_id FROM roots GROUP BY position
	)
	SELECT i.position, COALESCE(a.root_count,0), a.customer_id FROM input i LEFT JOIN aggregate_roots a USING(position) ORDER BY i.position`, positions, scopes, values)
	if err != nil {
		return nil, fmt.Errorf("resolve HXC unionids: %w", err)
	}
	defer rows.Close()
	results := make([]identityport.ScopedUnionIDResult, 0, len(references))
	for rows.Next() {
		var position int
		var count int64
		var customerID *int64
		if err = rows.Scan(&position, &count, &customerID); err != nil {
			return nil, fmt.Errorf("scan HXC unionid: %w", err)
		}
		result := identityport.ScopedUnionIDResult{Position: position, Status: identityport.ResolveNotFound}
		if count == 1 && customerID != nil {
			result.Status = identityport.ResolveFound
			result.CustomerID = customerdomain.CustomerID(*customerID)
		}
		if count > 1 {
			result.Status = identityport.ResolveConflict
		}
		results = append(results, result)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HXC unionids: %w", err)
	}
	return results, nil
}

func (PostgreSQL) VerifiedPaymentIdentity(ctx context.Context, identityID int64, kind identitydomain.Kind, scope string) (identityport.VerifiedCommerceIdentity, bool, error) {
	if identityID < 1 || identitydomain.ValidateNamespace(kind, scope) != nil {
		return identityport.VerifiedCommerceIdentity{}, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.VerifiedCommerceIdentity{}, false, err
	}
	var result identityport.VerifiedCommerceIdentity
	err = tx.QueryRow(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id,visited) AS (
		SELECT c.id,c.status,c.merged_into_customer_id,ARRAY[c.id] FROM customers c JOIN customer_identities i ON i.customer_id=c.id
		WHERE i.id=$1 AND i.kind=$2 AND i.scope_key=$3 AND i.assurance='verified' AND i.status='active'
		UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,l.visited||c.id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
	) SELECT i.id,l.id,i.kind,i.scope_key,i.normalized_value FROM customer_identities i JOIN lineage l ON TRUE WHERE i.id=$1 AND l.status<>'merged' LIMIT 1`, identityID, kind, scope).Scan(&result.IdentityID, &result.CustomerID, &result.Kind, &result.Scope, &result.Value)
	if errors.Is(err, pgx.ErrNoRows) {
		return identityport.VerifiedCommerceIdentity{}, false, nil
	}
	if err != nil {
		return identityport.VerifiedCommerceIdentity{}, false, fmt.Errorf("query verified payment identity: %w", err)
	}
	return result, true, nil
}

func (PostgreSQL) ResolveCommerce(ctx context.Context, set identityport.CommerceReferenceSet) (identityport.CommerceResolution, error) {
	if len(set.References) == 0 || len(set.References) > identityport.MaximumCommerceReferences {
		return identityport.CommerceResolution{Status: identityport.CommerceInvalid}, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return identityport.CommerceResolution{}, err
	}
	result := identityport.CommerceResolution{Matches: make([]identityport.CommerceIdentityMatch, 0, len(set.References))}
	missing := 0
	for position, reference := range set.References {
		normalized, normalizeErr := identitydomain.Normalize(reference)
		if normalizeErr != nil {
			return identityport.CommerceResolution{Status: identityport.CommerceInvalid}, nil
		}
		var identityID, customerID int64
		var assurance identitydomain.Assurance
		queryErr := tx.QueryRow(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id,visited) AS (
			SELECT c.id,c.status,c.merged_into_customer_id,ARRAY[c.id] FROM customers c
			JOIN customer_identities i ON i.customer_id=c.id
			WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'
			UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,l.visited || c.id FROM customers c
			JOIN lineage l ON c.id=l.merged_into_customer_id WHERE NOT c.id=ANY(l.visited)
		) SELECT i.id,l.id,i.assurance FROM customer_identities i JOIN lineage l ON TRUE
		WHERE i.kind=$1 AND i.scope_key=$2 AND i.normalized_value=$3 AND i.status='active'
		AND l.status<>'merged' AND ($4<>'verified' OR i.assurance='verified')
		LIMIT 1`, string(normalized.Kind), normalized.Scope, normalized.NormalizedValue, string(normalized.Assurance)).Scan(&identityID, &customerID, &assurance)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			missing++
			continue
		}
		if queryErr != nil {
			return identityport.CommerceResolution{}, fmt.Errorf("resolve commerce identity: %w", queryErr)
		}
		match := identityport.CommerceIdentityMatch{Position: position, IdentityID: identityID, CustomerID: customerdomain.CustomerID(customerID), Assurance: assurance}
		result.Matches = append(result.Matches, match)
		if result.CustomerID == 0 {
			result.CustomerID = match.CustomerID
		} else if result.CustomerID != match.CustomerID {
			result.Status, result.CustomerID = identityport.CommerceConflict, 0
			return result, nil
		}
	}
	if len(result.Matches) == 0 {
		result.Status, result.CustomerID = identityport.CommerceNotFound, 0
	} else if missing > 0 {
		result.Status, result.CustomerID = identityport.CommercePartial, 0
	} else {
		result.Status = identityport.CommerceResolved
	}
	return result, nil
}

func (PostgreSQL) VerifiedWeComCustomer(ctx context.Context, corpID, externalUserID string) (customerdomain.CustomerID, bool, error) {
	if strings.TrimSpace(corpID) != corpID || corpID == "" || strings.TrimSpace(externalUserID) != externalUserID || externalUserID == "" {
		return 0, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var customerID customerdomain.CustomerID
	err = tx.QueryRow(ctx, `WITH RECURSIVE lineage(id,status,merged_into_customer_id) AS (
		SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN customer_identities i ON i.customer_id=c.id
		WHERE i.kind='wecom_external_userid' AND i.scope_key=$1 AND i.normalized_value=$2 AND i.assurance='verified' AND i.status='active'
		UNION ALL SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id
	) SELECT id FROM lineage WHERE status<>'merged' LIMIT 1`, "wecom-corp:"+corpID, externalUserID).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query verified wecom owner: %w", err)
	}
	return customerID, true, nil
}

func (PostgreSQL) CustomerForPhone(ctx context.Context, phone string) (customerdomain.CustomerID, bool, error) {
	ref, err := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:e164", Value: phone, Assurance: identitydomain.AssuranceDeclared, Source: "customer_directory"})
	if err != nil {
		return 0, false, identitydomain.ErrInvalidReference
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, false, err
	}
	var customerID customerdomain.CustomerID
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE lineage(id,status,merged_into_customer_id) AS (
			SELECT c.id,c.status,c.merged_into_customer_id FROM customers c
			JOIN customer_identities i ON i.customer_id=c.id
			WHERE i.kind='phone' AND i.scope_key='phone:e164' AND i.normalized_value=$1 AND i.status='active'
			UNION ALL SELECT c.id,c.status,c.merged_into_customer_id FROM customers c JOIN lineage l ON c.id=l.merged_into_customer_id
		) SELECT id FROM lineage WHERE status <> 'merged' LIMIT 1`, ref.NormalizedValue).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query phone owner: %w", err)
	}
	return customerID, true, nil
}

func (PostgreSQL) DirectoryIdentities(ctx context.Context, customerID customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	if customerID < 1 {
		return nil, nil, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT kind,scope_key,assurance,status,source,normalized_value,created_at FROM customer_identities WHERE customer_id=$1 AND status='active' ORDER BY id`, customerID)
	if err != nil {
		return nil, nil, fmt.Errorf("query directory identities: %w", err)
	}
	defer rows.Close()
	identities := []identityport.DirectoryIdentitySummary{}
	phones := []identityport.MaskedPhone{}
	for rows.Next() {
		var summary identityport.DirectoryIdentitySummary
		var value string
		if err = rows.Scan(&summary.Kind, &summary.Scope, &summary.Assurance, &summary.Status, &summary.Source, &value, &summary.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan directory identity: %w", err)
		}
		if summary.Kind == identitydomain.KindPhone {
			phones = append(phones, identityport.MaskedPhone{Masked: maskPhone(value), Assurance: summary.Assurance})
		}
		identities = append(identities, summary)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate directory identities: %w", err)
	}
	return identities, phones, nil
}

func (PostgreSQL) RevealPhone(ctx context.Context, customerID customerdomain.CustomerID) (string, bool, error) {
	if customerID < 1 {
		return "", false, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	var phone string
	err = tx.QueryRow(ctx, `SELECT normalized_value FROM customer_identities WHERE customer_id=$1 AND kind='phone' AND scope_key='phone:e164' AND status='active' ORDER BY (assurance='verified') DESC, id DESC LIMIT 1`, customerID).Scan(&phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query active phone: %w", err)
	}
	return phone, true, nil
}

func (PostgreSQL) VerifiedExternalIdentityValue(ctx context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (string, bool, error) {
	if customerID < 1 || kind == identitydomain.KindPhone || scope == "" {
		return "", false, ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", false, err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE chain(id,status,merged_into_customer_id,visited) AS (
		SELECT id,status,merged_into_customer_id,ARRAY[id] FROM customers WHERE id=$1
		UNION ALL SELECT c.id,c.status,c.merged_into_customer_id,chain.visited||c.id FROM chain JOIN customers c ON c.id=chain.merged_into_customer_id WHERE NOT c.id=ANY(chain.visited)
	), root AS (SELECT id FROM chain WHERE status<>'merged' ORDER BY cardinality(visited) DESC LIMIT 1)
	SELECT normalized_value FROM customer_identities WHERE customer_id=(SELECT id FROM root) AND kind=$2 AND scope_key=$3 AND assurance='verified' AND status='active' ORDER BY id LIMIT 2`, customerID, kind, scope)
	if err != nil {
		return "", false, fmt.Errorf("query external identity: %w", err)
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return "", false, fmt.Errorf("scan external identity: %w", err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterate external identity: %w", err)
	}
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, ErrInvalidQuery
	}
	return values[0], true, nil
}

func maskPhone(value string) string {
	if len(value) <= 7 {
		return "***"
	}
	return value[:len(value)-8] + "****" + value[len(value)-4:]
}

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
