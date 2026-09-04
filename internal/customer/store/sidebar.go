package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (PostgreSQL) ReadSidebarProfile(ctx context.Context, customerID customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.SidebarProfile{}, err
	}
	var p customerport.SidebarProfile
	err = tx.QueryRow(ctx, `SELECT customer_id,display_name,avatar_url,phone_masked,COALESCE(phone_assurance,''),customer_status,activation_status,gender,contact_type,corp_name,source,source_version,last_synced_at,updated_at FROM customer_directory_projection WHERE customer_id=$1`, customerID).Scan(&p.CustomerID, &p.DisplayName, &p.AvatarURL, &p.PhoneMasked, &p.PhoneAssurance, &p.Status, &p.ActivationState, &p.Gender, &p.ContactType, &p.CorpName, &p.Source, &p.Version, &p.LastSyncedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.SidebarProfile{}, customerapp.ErrNotFound
	}
	return p, err
}

func (PostgreSQL) FindSidebarProfileReceipt(ctx context.Context, key [32]byte) (customerapp.SidebarProfileReceipt, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerapp.SidebarProfileReceipt{}, false, err
	}
	var receipt customerapp.SidebarProfileReceipt
	var digest []byte
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT payload_digest,outcome,result_snapshot FROM customer_sidebar_profile_receipts WHERE key_digest=$1`, key[:]).Scan(&digest, &receipt.Outcome, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, false, nil
	}
	if err != nil || len(digest) != 32 || json.Unmarshal(raw, &receipt.Profile) != nil {
		return receipt, false, err
	}
	copy(receipt.PayloadDigest[:], digest)
	return receipt, true, nil
}

func (PostgreSQL) UpdateSidebarProfile(ctx context.Context, command customerport.SidebarProfileUpdate, _, _ [32]byte, at time.Time) (customerport.SidebarProfile, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.SidebarProfile{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `UPDATE customer_directory_projection SET display_name=$3,gender=$4,corp_name=$5,source='sidebar',source_version=source_version+1,updated_at=$6 WHERE customer_id=$1 AND source_version=$2 RETURNING customer_id`, command.CustomerID, command.ExpectedVersion, command.DisplayName, command.Gender, command.CorpName, at).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerport.SidebarProfile{}, customerapp.ErrSidebarProfileConflict
	}
	if err != nil {
		return customerport.SidebarProfile{}, err
	}
	return PostgreSQL{}.ReadSidebarProfile(ctx, command.CustomerID)
}

func (PostgreSQL) RecordSidebarProfileReceipt(ctx context.Context, key, payload [32]byte, command customerport.SidebarProfileUpdate, outcome string, profile customerport.SidebarProfile) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(profile)
	employee := sha256Digest(command.EmployeeID)
	_, err = tx.Exec(ctx, `INSERT INTO customer_sidebar_profile_receipts(key_digest,payload_digest,customer_id,employee_digest,outcome,result_snapshot) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, key[:], payload[:], command.CustomerID, employee[:], outcome, raw)
	return err
}

func sha256Digest(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

var _ customerapp.SidebarProfileStore = PostgreSQL{}
