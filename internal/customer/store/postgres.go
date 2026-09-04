package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQL struct{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

var _ customerapp.Store = PostgreSQL{}
var _ customerport.ProjectionWriter = PostgreSQL{}
var _ customerport.CallbackProjectionWriter = PostgreSQL{}
var _ customerport.AudienceReader = PostgreSQL{}
var _ customerport.DirectoryDisplayNameReader = PostgreSQL{}

func (PostgreSQL) DisplayNames(ctx context.Context, customerIDs []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	result := make(map[customerdomain.CustomerID]string)
	if len(customerIDs) == 0 {
		return result, nil
	}
	if len(customerIDs) > 200 {
		return nil, customerapp.ErrInvalidQuery
	}
	ids := make([]int64, 0, len(customerIDs))
	seen := make(map[customerdomain.CustomerID]struct{}, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, customerapp.ErrInvalidQuery
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id,display_name FROM customer_directory_projection WHERE customer_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id customerdomain.CustomerID
		var name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if name != "" {
			result[id] = name
		}
	}
	return result, rows.Err()
}

func (PostgreSQL) ActiveWithin(ctx context.Context, reference time.Time, days int) ([]customerdomain.CustomerID, time.Time, error) {
	if reference.IsZero() || days < 1 || days > 999 {
		return nil, time.Time{}, customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	rows, err := tx.Query(ctx, `SELECT customer_id FROM customer_directory_projection
		WHERE customer_status='active' AND activation_status='active' AND updated_at <= $1 AND updated_at >= $1-($2::int * interval '1 day')
		ORDER BY customer_id LIMIT 100001`, reference.UTC(), days)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	ids := []customerdomain.CustomerID{}
	for rows.Next() {
		var id customerdomain.CustomerID
		if err = rows.Scan(&id); err != nil {
			return nil, time.Time{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	var watermark time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(updated_at),to_timestamp(0)) FROM customer_directory_projection WHERE updated_at <= $1`, reference.UTC()).Scan(&watermark); err != nil {
		return nil, time.Time{}, err
	}
	return ids, watermark, nil
}

func (PostgreSQL) List(ctx context.Context, query customerapp.Query) (customerapp.PageData, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerapp.PageData{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT customer_id,customer_status,display_name,avatar_url,oneid_label,phone_masked,
			COALESCE(phone_assurance,''),activation_status,last_synced_at,updated_at
		FROM customer_directory_projection
		WHERE updated_at <= $1
		  AND ($2::text='' OR display_name ILIKE '%'||$2||'%' OR oneid_label ILIKE '%'||$2||'%')
		  AND ($3::text='' OR customer_status=$3)
		  AND ($4::text='' OR activation_status=$4)
		  AND (NOT $5::boolean)
		  AND ($6::bigint=0 OR customer_id=$6)
		  AND ($7::timestamptz IS NULL OR (updated_at,customer_id) < ($7,$8))
		ORDER BY updated_at DESC,customer_id DESC LIMIT $9`, query.Watermark, query.Filters.Keyword,
		query.Filters.Status, query.Filters.ActivationStatus, query.Filters.PhoneMatchNone,
		query.Filters.PhoneCustomerID, nullableTime(query.AfterAt), query.AfterID, query.Limit)
	if err != nil {
		return customerapp.PageData{}, err
	}
	defer rows.Close()
	data := customerapp.PageData{Items: []customerapp.Item{}}
	for rows.Next() {
		var item customerapp.Item
		if err = rows.Scan(&item.CustomerID, &item.CustomerStatus, &item.DisplayName, &item.AvatarURL, &item.OneIDLabel,
			&item.PhoneMasked, &item.PhoneAssurance, &item.ActivationState, &item.LastSyncedAt, &item.UpdatedAt); err != nil {
			return customerapp.PageData{}, err
		}
		data.Items = append(data.Items, item)
	}
	if err = rows.Err(); err != nil {
		return customerapp.PageData{}, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM customer_directory_projection WHERE updated_at <= $1
		AND ($2::text='' OR display_name ILIKE '%'||$2||'%' OR oneid_label ILIKE '%'||$2||'%')
		AND ($3::text='' OR customer_status=$3) AND ($4::text='' OR activation_status=$4)
		AND (NOT $5::boolean) AND ($6::bigint=0 OR customer_id=$6) LIMIT 10001) capped`, query.Watermark,
		query.Filters.Keyword, query.Filters.Status, query.Filters.ActivationStatus, query.Filters.PhoneMatchNone, query.Filters.PhoneCustomerID).Scan(&data.Count)
	if err != nil {
		return customerapp.PageData{}, err
	}
	if data.Count > customerapp.ExactCountCap {
		data.Count = customerapp.ExactCountCap
		data.TotalIsEstimate = true
	}
	return data, nil
}

func (PostgreSQL) Detail(ctx context.Context, customerID customerdomain.CustomerID) (customerapp.Detail, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerapp.Detail{}, err
	}
	var detail customerapp.Detail
	err = tx.QueryRow(ctx, `SELECT customer_id,customer_status,display_name,avatar_url,oneid_label,phone_masked,
		COALESCE(phone_assurance,''),activation_status,last_synced_at,updated_at,gender,contact_type,corp_name,source
		FROM customer_directory_projection WHERE customer_id=$1`, customerID).Scan(&detail.CustomerID, &detail.CustomerStatus,
		&detail.DisplayName, &detail.AvatarURL, &detail.OneIDLabel, &detail.PhoneMasked, &detail.PhoneAssurance,
		&detail.ActivationState, &detail.LastSyncedAt, &detail.UpdatedAt, &detail.Gender, &detail.ContactType,
		&detail.CorpName, &detail.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerapp.Detail{}, customerapp.ErrNotFound
	}
	return detail, err
}

func (PostgreSQL) UpsertDirectoryProjection(ctx context.Context, projection customerport.DirectoryProjection) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,avatar_url,gender,contact_type,corp_name,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15)
		ON CONFLICT(customer_id) DO UPDATE SET customer_status=EXCLUDED.customer_status,display_name=EXCLUDED.display_name,
		avatar_url=EXCLUDED.avatar_url,gender=EXCLUDED.gender,contact_type=EXCLUDED.contact_type,corp_name=EXCLUDED.corp_name,
		oneid_label=EXCLUDED.oneid_label,phone_masked=CASE WHEN EXCLUDED.phone_masked='' THEN customer_directory_projection.phone_masked ELSE EXCLUDED.phone_masked END,
		phone_assurance=COALESCE(EXCLUDED.phone_assurance,customer_directory_projection.phone_assurance),activation_status=EXCLUDED.activation_status,
		source=EXCLUDED.source,source_version=customer_directory_projection.source_version+1,last_synced_at=EXCLUDED.last_synced_at,updated_at=EXCLUDED.updated_at`, projection.CustomerID, projection.CustomerStatus,
		projection.DisplayName, projection.AvatarURL, projection.Gender, projection.ContactType, projection.CorpName,
		projection.OneIDLabel, projection.PhoneMasked, projection.PhoneAssurance, projection.ActivationState,
		projection.Source, projection.SourceVersion, projection.LastSyncedAt, projection.UpdatedAt)
	return err
}

func (PostgreSQL) ActivateDirectoryCustomer(ctx context.Context, customerID customerdomain.CustomerID, source string, at time.Time) error {
	if customerID < 1 || source == "" || at.IsZero() {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active',$2,'active',$3,1,$4,$4)
		ON CONFLICT(customer_id) DO UPDATE SET customer_status='active',activation_status='active',source=EXCLUDED.source,
		source_version=customer_directory_projection.source_version+1,last_synced_at=EXCLUDED.last_synced_at,updated_at=EXCLUDED.updated_at`,
		customerID, "CID-"+strconv.FormatInt(int64(customerID), 10), source, at)
	return err
}

func (PostgreSQL) MarkDirectoryStale(ctx context.Context, customerIDs []customerdomain.CustomerID, at time.Time) (int64, error) {
	if len(customerIDs) == 0 {
		return 0, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, len(customerIDs))
	for index, id := range customerIDs {
		ids[index] = int64(id)
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_directory_projection SET activation_status='stale',source_version=source_version+1,updated_at=$2 WHERE customer_id=ANY($1) AND activation_status <> 'stale'`, ids, at)
	return tag.RowsAffected(), err
}

func (PostgreSQL) UpdateDirectoryPhone(ctx context.Context, customerID customerdomain.CustomerID, masked string, assurance identitydomain.Assurance, sourceVersion int64, at time.Time) error {
	if customerID < 1 || masked == "" || (assurance != identitydomain.AssuranceDeclared && assurance != identitydomain.AssuranceVerified) || sourceVersion < 1 {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active',$6,$2,$3,'active','survey',$4,$5,$5)
		ON CONFLICT(customer_id) DO UPDATE SET phone_masked=EXCLUDED.phone_masked,phone_assurance=EXCLUDED.phone_assurance,
		source_version=GREATEST(customer_directory_projection.source_version+1,$4),updated_at=$5`, customerID, masked, assurance, sourceVersion, at, "CID-"+strconv.FormatInt(int64(customerID), 10))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return customerapp.ErrNotFound
	}
	return nil
}

func (PostgreSQL) ClearDirectoryPhone(ctx context.Context, customerID customerdomain.CustomerID, at time.Time) error {
	if customerID < 1 {
		return customerapp.ErrInvalidQuery
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_directory_projection SET phone_masked='',phone_assurance=NULL,source_version=source_version+1,updated_at=$2 WHERE customer_id=$1`, customerID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return customerapp.ErrNotFound
	}
	return nil
}

func nullableTime(value interface{ IsZero() bool }) any {
	if value.IsZero() {
		return nil
	}
	return value
}
