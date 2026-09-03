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
