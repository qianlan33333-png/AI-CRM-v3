package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type PostgreSQLCustomerSyncStore struct{}

func NewPostgreSQLCustomerSyncStore() PostgreSQLCustomerSyncStore {
	return PostgreSQLCustomerSyncStore{}
}

func (PostgreSQLCustomerSyncStore) Create(ctx context.Context, command CreateCustomerSyncRun) (CustomerSyncRun, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CustomerSyncRun{}, false, err
	}
	var run CustomerSyncRun
	err = tx.QueryRow(ctx, `SELECT id,run_key,trigger_type,status,COALESCE(resume_status,''),corp_scope,staff_ids,staff_index,provider_cursor,
		discovered_count,activated_count,already_linked_count,conflict_count,terminal_failed_count,projected_count,stale_count,
		version,COALESCE(last_error_code,''),COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at
		FROM wecom_customer_sync_runs WHERE run_key=$1`, command.RunKey).Scan(runScan(&run)...)
	if err == nil {
		return run, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CustomerSyncRun{}, false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,requested_by)
		VALUES($1,$2,'queued',$3,$4) RETURNING id`, command.RunKey, command.Trigger, command.CorpScope, command.RequestedBy).Scan(&run.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return CustomerSyncRun{}, false, ErrSyncConflict
		}
		return CustomerSyncRun{}, false, err
	}
	run, err = (PostgreSQLCustomerSyncStore{}).Get(ctx, run.ID)
	return run, false, err
}

func (PostgreSQLCustomerSyncStore) Active(ctx context.Context) (CustomerSyncRun, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CustomerSyncRun{}, false, err
	}
	var run CustomerSyncRun
	err = tx.QueryRow(ctx, `SELECT id,run_key,trigger_type,status,COALESCE(resume_status,''),corp_scope,staff_ids,staff_index,provider_cursor,
		discovered_count,activated_count,already_linked_count,conflict_count,terminal_failed_count,projected_count,stale_count,
		version,COALESCE(last_error_code,''),COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at
		FROM wecom_customer_sync_runs WHERE status IN ('queued','listing_staff','fetching_profiles','ingesting','reconciling','failed_retryable') ORDER BY id LIMIT 1`).Scan(runScan(&run)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerSyncRun{}, false, nil
	}
	return run, err == nil, err
}

func (PostgreSQLCustomerSyncStore) Get(ctx context.Context, id int64) (CustomerSyncRun, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return CustomerSyncRun{}, err
	}
	var run CustomerSyncRun
	err = tx.QueryRow(ctx, `SELECT id,run_key,trigger_type,status,COALESCE(resume_status,''),corp_scope,staff_ids,staff_index,provider_cursor,
		discovered_count,activated_count,already_linked_count,conflict_count,terminal_failed_count,projected_count,stale_count,
		version,COALESCE(last_error_code,''),COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at
		FROM wecom_customer_sync_runs WHERE id=$1`, id).Scan(runScan(&run)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerSyncRun{}, ErrSyncNotFound
	}
	return run, err
}

func (PostgreSQLCustomerSyncStore) List(ctx context.Context, limit int) ([]CustomerSyncRun, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrSyncCAS
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,run_key,trigger_type,status,COALESCE(resume_status,''),corp_scope,staff_ids,staff_index,provider_cursor,
		discovered_count,activated_count,already_linked_count,conflict_count,terminal_failed_count,projected_count,stale_count,
		version,COALESCE(last_error_code,''),COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at
		FROM wecom_customer_sync_runs ORDER BY created_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []CustomerSyncRun{}
	for rows.Next() {
		var run CustomerSyncRun
		if err = rows.Scan(runScan(&run)...); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (PostgreSQLCustomerSyncStore) Transition(ctx context.Context, id, version int64, from, to CustomerSyncStatus) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_customer_sync_runs SET status=$4,resume_status=NULL,version=version+1,
		started_at=COALESCE(started_at,clock_timestamp()),last_error_code=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 AND status=$3`, id, version, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSyncCAS
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) SaveStaff(ctx context.Context, id, version int64, staff []string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(staff)
	if err != nil {
		return err
	}
	status := SyncFetchingProfiles
	if len(staff) == 0 {
		status = SyncReconciling
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_customer_sync_runs SET staff_ids=$3,status=$4,version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 AND status='listing_staff'`, id, version, raw, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSyncCAS
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) InsertItem(ctx context.Context, runID int64, corpScope string, item SyncItem) (bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var customerID, identityID any
	if item.CustomerID > 0 {
		customerID = item.CustomerID
	}
	if item.IdentityID > 0 {
		identityID = item.IdentityID
	}
	tag, err := tx.Exec(ctx, `INSERT INTO wecom_customer_sync_items(run_id,corp_scope,external_userid,external_userid_digest,staff_id_digest,payload_digest,outcome,customer_id,identity_id,error_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')) ON CONFLICT(run_id,corp_scope,external_userid) DO NOTHING`, runID, corpScope, item.ExternalUserID,
		item.ExternalUserIDDigest[:], item.StaffIDDigest[:], item.PayloadDigest[:], item.Outcome, customerID, identityID, item.ErrorCode)
	return tag.RowsAffected() == 1, err
}

func (PostgreSQLCustomerSyncStore) UpsertProfile(ctx context.Context, runID int64, corpScope string, provision identityport.ProvisionResult, contact wecomport.ExternalContact, digest [32]byte, fetchedAt time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,avatar_url,gender,contact_type,corp_name,activation_status,profile_digest,last_seen_run_id,fetched_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$10,$11,$11)
		ON CONFLICT(customer_id) DO UPDATE SET external_identity_id=EXCLUDED.external_identity_id,display_name=EXCLUDED.display_name,
		avatar_url=EXCLUDED.avatar_url,gender=EXCLUDED.gender,contact_type=EXCLUDED.contact_type,corp_name=EXCLUDED.corp_name,
		activation_status='active',profile_digest=EXCLUDED.profile_digest,last_seen_run_id=EXCLUDED.last_seen_run_id,fetched_at=EXCLUDED.fetched_at,
		stale_at=NULL,version=wecom_external_contact_profiles.version+1,updated_at=EXCLUDED.updated_at`, provision.CustomerID, corpScope, provision.IdentityID,
		contact.Name, contact.AvatarURL, contact.Gender, contact.Type, contact.CorpName, digest[:], runID, fetchedAt)
	return err
}

func (PostgreSQLCustomerSyncStore) UpsertProfileObservations(ctx context.Context, runID int64, corpScope string, customerID customerdomain.CustomerID, followInfo []wecomport.ExternalContactFollowInfo, observedAt time.Time) error {
	if runID < 1 || customerID < 1 || corpScope == "" || observedAt.IsZero() {
		return ErrSyncCAS
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	// FollowInfo is a provider observation returned for this page, not a claim
	// that this page is the customer's complete follow-user set. The request
	// employee is intentionally not injected: only the reconciled, completed
	// run may derive a primary from these page observations.
	owners := map[string]wecomport.ExternalContactFollowInfo{}
	for _, follow := range followInfo {
		if follow.EmployeeID == "" {
			return ErrSyncCAS
		}
		owners[follow.EmployeeID] = follow
	}
	for employeeID, follow := range owners {
		if _, err = tx.Exec(ctx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at)
			VALUES($1,$2,$3,'active',$4,$5) ON CONFLICT(customer_id,corp_scope,employee_id) DO UPDATE SET
			relationship_status='active',last_seen_run_id=EXCLUDED.last_seen_run_id,observed_at=EXCLUDED.observed_at,stale_at=NULL,updated_at=clock_timestamp()`,
			customerID, corpScope, employeeID, runID, observedAt.UTC()); err != nil {
			return err
		}
		seenTags := map[string]struct{}{}
		for _, tag := range follow.Tags {
			if tag.ProviderTagID == "" || tag.Type < 1 || tag.Type > 2 {
				return ErrSyncCAS
			}
			if _, duplicate := seenTags[tag.ProviderTagID]; duplicate {
				continue
			}
			seenTags[tag.ProviderTagID] = struct{}{}
			if _, err = tx.Exec(ctx, `INSERT INTO wecom_customer_tag_observations(customer_id,corp_scope,employee_id,provider_tag_id,provider_tag_type,observed_name,observation_status,last_seen_run_id,observed_at)
				VALUES($1,$2,$3,$4,$5,$6,'active',$7,$8) ON CONFLICT(customer_id,corp_scope,employee_id,provider_tag_id) DO UPDATE SET
				provider_tag_type=EXCLUDED.provider_tag_type,observed_name=EXCLUDED.observed_name,observation_status='active',last_seen_run_id=EXCLUDED.last_seen_run_id,
				observed_at=EXCLUDED.observed_at,stale_at=NULL,updated_at=clock_timestamp()`, customerID, corpScope, employeeID,
				tag.ProviderTagID, tag.Type, tag.Name, runID, observedAt.UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) AddCountsAndAdvance(ctx context.Context, id, version, activated, linked, conflict, terminal, projected int64, staffIndex int, cursor string, status CustomerSyncStatus) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	discovered := activated + linked + conflict + terminal
	tag, err := tx.Exec(ctx, `UPDATE wecom_customer_sync_runs SET discovered_count=discovered_count+$3,activated_count=activated_count+$4,
		already_linked_count=already_linked_count+$5,conflict_count=conflict_count+$6,terminal_failed_count=terminal_failed_count+$7,
		projected_count=projected_count+$8,staff_index=$9,provider_cursor=$10,status=$11,version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 AND status IN ('fetching_profiles','ingesting')`, id, version, discovered, activated, linked, conflict, terminal, projected, staffIndex, cursor, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSyncCAS
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) StaleCustomers(ctx context.Context, runID int64) ([]customerdomain.CustomerID, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `UPDATE wecom_external_contact_profiles SET activation_status='stale',stale_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp()
		WHERE last_seen_run_id<>$1 AND activation_status<>'stale' RETURNING customer_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (PostgreSQLCustomerSyncStore) ReconcileProfileObservations(ctx context.Context, runID int64, at time.Time) error {
	if runID < 1 || at.IsZero() {
		return ErrSyncCAS
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wecom_customer_owner_observations SET relationship_status='stale',stale_at=$2,updated_at=$2
		WHERE corp_scope=(SELECT corp_scope FROM wecom_customer_sync_runs WHERE id=$1)
		AND last_seen_run_id<>$1 AND relationship_status='active'`, runID, at.UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wecom_customer_tag_observations SET observation_status='stale',stale_at=$2,updated_at=$2
		WHERE corp_scope=(SELECT corp_scope FROM wecom_customer_sync_runs WHERE id=$1)
		AND last_seen_run_id<>$1 AND observation_status='active'`, runID, at.UTC())
	return err
}

// RefreshProfilePrimaryOwners derives the old bridge's primary-owner rule only
// after ReconcileProfileObservations has consumed the complete directory run.
// A scope without any trusted follow users is intentionally absent from the
// candidates CTE, so its existing primary owner remains untouched.
func (PostgreSQLCustomerSyncStore) RefreshProfilePrimaryOwners(ctx context.Context, runID int64, at time.Time) error {
	if runID < 1 || at.IsZero() {
		return ErrSyncCAS
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `WITH candidates AS (
		SELECT profile.customer_id,profile.corp_scope,min(observation.employee_id) AS primary_owner_userid
		FROM wecom_external_contact_profiles profile
		JOIN wecom_customer_owner_observations observation
			ON observation.customer_id=profile.customer_id AND observation.corp_scope=profile.corp_scope
		WHERE profile.last_seen_run_id=$1 AND profile.activation_status='active'
			AND observation.last_seen_run_id=$1 AND observation.relationship_status='active'
		GROUP BY profile.customer_id,profile.corp_scope
	)
	UPDATE wecom_external_contact_profiles profile
	SET primary_owner_userid=candidate.primary_owner_userid,primary_owner_run_id=$1,version=profile.version+1,updated_at=$2
	FROM candidates candidate
	WHERE profile.customer_id=candidate.customer_id AND profile.corp_scope=candidate.corp_scope
		AND (profile.primary_owner_userid IS DISTINCT FROM candidate.primary_owner_userid OR profile.primary_owner_run_id IS DISTINCT FROM $1)`, runID, at.UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH candidates AS (
		SELECT profile.customer_id,profile.corp_scope,min(observation.employee_id) AS primary_owner_userid
		FROM wecom_external_contact_profiles profile
		JOIN wecom_customer_owner_observations observation
			ON observation.customer_id=profile.customer_id AND observation.corp_scope=profile.corp_scope
		WHERE profile.last_seen_run_id=$1 AND profile.activation_status='active'
			AND observation.last_seen_run_id=$1 AND observation.relationship_status='active'
		GROUP BY profile.customer_id,profile.corp_scope
	)
	UPDATE wecom_customer_owner_observations observation
	SET primary_owner_userid=candidate.primary_owner_userid,updated_at=$2
	FROM candidates candidate
	WHERE observation.customer_id=candidate.customer_id AND observation.corp_scope=candidate.corp_scope
		AND observation.last_seen_run_id=$1 AND observation.relationship_status='active'
		AND observation.primary_owner_userid IS DISTINCT FROM candidate.primary_owner_userid`, runID, at.UTC())
	return err
}

func (PostgreSQLCustomerSyncStore) CustomerOwnerObservations(ctx context.Context, customerID customerdomain.CustomerID) ([]wecomport.OwnerObservation, error) {
	if customerID < 1 {
		return nil, ErrSyncNotFound
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT employee_id,relationship_status,observed_at FROM wecom_customer_owner_observations
		WHERE customer_id=$1 ORDER BY (relationship_status='active') DESC,observed_at DESC,employee_id`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []wecomport.OwnerObservation{}
	for rows.Next() {
		var item wecomport.OwnerObservation
		if err = rows.Scan(&item.EmployeeID, &item.Status, &item.ObservedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (PostgreSQLCustomerSyncStore) AudiencePrimaryOwners(ctx context.Context, customerIDs []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	if len(customerIDs) > maximumAudiencePrimaryOwnerBatch {
		return nil, ErrSyncCAS
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := map[customerdomain.CustomerID]struct{}{}
	for _, customerID := range customerIDs {
		if customerID < 1 {
			return nil, ErrSyncNotFound
		}
		if _, exists := seen[customerID]; exists {
			continue
		}
		seen[customerID] = struct{}{}
		ids = append(ids, int64(customerID))
	}
	if len(ids) == 0 {
		return []wecomport.AudiencePrimaryOwner{}, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH requested AS (
		SELECT unnest($1::bigint[]) AS customer_id
	), profiles AS (
		SELECT profile.customer_id,profile.corp_scope,profile.primary_owner_userid,profile.primary_owner_run_id
		FROM wecom_external_contact_profiles profile
		JOIN requested ON requested.customer_id=profile.customer_id
		JOIN wecom_customer_sync_runs primary_run ON primary_run.id=profile.primary_owner_run_id AND primary_run.status='succeeded'
		WHERE profile.activation_status='active' AND profile.primary_owner_userid<>''
	), owner_values AS (
		SELECT customer_id,primary_owner_userid AS owner_userid
		FROM profiles WHERE primary_owner_userid<>''
		UNION
		SELECT observation.customer_id,observation.primary_owner_userid
		FROM wecom_customer_owner_observations observation
		JOIN profiles ON profiles.customer_id=observation.customer_id
		JOIN wecom_customer_sync_runs run ON run.id=observation.last_seen_run_id AND run.status='succeeded'
		WHERE observation.relationship_status='active' AND observation.primary_owner_userid<>''
	), conflicts AS (
		SELECT customer_id,count(DISTINCT owner_userid) AS owner_count
		FROM owner_values GROUP BY customer_id
	)
		SELECT requested.customer_id,COALESCE(profiles.corp_scope,''),
		CASE WHEN COALESCE(conflicts.owner_count,0)>1 THEN '' ELSE COALESCE(profiles.primary_owner_userid,'') END,
		CASE WHEN COALESCE(conflicts.owner_count,0)>1 THEN 'ambiguous'
			WHEN COALESCE(profiles.primary_owner_userid,'')<>'' THEN 'known'
			ELSE 'unknown' END
	FROM requested
	LEFT JOIN profiles ON profiles.customer_id=requested.customer_id
	LEFT JOIN conflicts ON conflicts.customer_id=requested.customer_id
	ORDER BY requested.customer_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]wecomport.AudiencePrimaryOwner, 0, len(ids))
	for rows.Next() {
		var item wecomport.AudiencePrimaryOwner
		if err = rows.Scan(&item.CustomerID, &item.CorpScope, &item.OwnerUserID, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (PostgreSQLCustomerSyncStore) CustomerTagObservations(ctx context.Context, customerID customerdomain.CustomerID) ([]wecomport.TagObservation, error) {
	if customerID < 1 {
		return nil, ErrSyncNotFound
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT provider_tag_id,observed_name,provider_tag_type,observation_status,max(observed_at)
		FROM wecom_customer_tag_observations WHERE customer_id=$1
		GROUP BY provider_tag_id,observed_name,provider_tag_type,observation_status
		ORDER BY (observation_status='active') DESC,max(observed_at) DESC,provider_tag_id`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []wecomport.TagObservation{}
	for rows.Next() {
		var item wecomport.TagObservation
		if err = rows.Scan(&item.ProviderTagID, &item.ObservedName, &item.ProviderType, &item.Status, &item.ObservedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (PostgreSQLCustomerSyncStore) Complete(ctx context.Context, id, version, stale int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_customer_sync_runs SET status='succeeded',stale_count=$3,completed_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 AND status='reconciling' AND discovered_count=activated_count+already_linked_count+conflict_count+terminal_failed_count
		AND projected_count=activated_count+already_linked_count`, id, version, stale)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSyncCAS
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) Fail(ctx context.Context, id, version int64, status CustomerSyncStatus, code string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_customer_sync_runs SET resume_status=CASE WHEN $3='failed_retryable' THEN status ELSE NULL END,status=$3,last_error_code=$4,version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 AND status IN ('listing_staff','fetching_profiles','ingesting')`, id, version, status, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSyncCAS
	}
	return nil
}

func (PostgreSQLCustomerSyncStore) Terminate(ctx context.Context, id int64, code string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE wecom_customer_sync_runs SET status='failed_terminal',resume_status=NULL,last_error_code=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND status IN ('queued','listing_staff','fetching_profiles','ingesting','reconciling','failed_retryable')`, id, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSyncCAS
	}
	return nil
}

func runScan(run *CustomerSyncRun) []any {
	var staffJSON []byte
	return []any{&run.ID, &run.RunKey, &run.Trigger, &run.Status, &run.ResumeStatus, &run.CorpScope, staffJSONScanner{target: &run.StaffIDs, raw: &staffJSON}, &run.StaffIndex, &run.ProviderCursor,
		&run.Discovered, &run.Activated, &run.AlreadyLinked, &run.Conflict, &run.TerminalFailed, &run.Projected, &run.Stale, &run.Version,
		&run.LastErrorCode, &run.RequestedBy, &run.StartedAt, &run.CompletedAt, &run.CreatedAt, &run.UpdatedAt}
}

type staffJSONScanner struct {
	target *[]string
	raw    *[]byte
}

func (scanner staffJSONScanner) Scan(src any) error {
	var raw []byte
	switch value := src.(type) {
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		return errors.New("invalid staff json")
	}
	return json.Unmarshal(raw, scanner.target)
}
