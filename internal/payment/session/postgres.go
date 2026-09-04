package session

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQL struct{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

func (PostgreSQL) Insert(ctx context.Context, record Record) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_sessions(
			token_digest,payer_identity_id,payer_customer_id,beneficiary_customer_id,
			app_scope_digest,payment_channel,expires_at,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		record.TokenDigest[:], record.PayerIdentityID, record.PayerCustomerID,
		record.BeneficiaryCustomerID, record.AppScopeDigest[:], record.Channel, record.ExpiresAt,
		record.CreatedAt,
	).Scan(&record.ID)
	return record, err
}

func (PostgreSQL) Consume(ctx context.Context, digest [32]byte, now time.Time) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	var record Record
	var tokenDigest, scopeDigest []byte
	var payer, beneficiary int64
	err = tx.QueryRow(ctx, `
		UPDATE payment_sessions
		SET consumed_at=$2
		WHERE token_digest=$1 AND consumed_at IS NULL AND expires_at>$2
		RETURNING id,token_digest,payer_identity_id,payer_customer_id,
			beneficiary_customer_id,app_scope_digest,payment_channel,expires_at,consumed_at,created_at`,
		digest[:], now,
	).Scan(
		&record.ID, &tokenDigest, &record.PayerIdentityID, &payer, &beneficiary,
		&scopeDigest, &record.Channel, &record.ExpiresAt, &record.ConsumedAt, &record.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrExpired
	}
	if err != nil {
		return Record{}, err
	}
	if len(tokenDigest) != 32 || len(scopeDigest) != 32 {
		return Record{}, ErrInvalid
	}
	copy(record.TokenDigest[:], tokenDigest)
	copy(record.AppScopeDigest[:], scopeDigest)
	record.PayerCustomerID = customerdomain.CustomerID(payer)
	record.BeneficiaryCustomerID = customerdomain.CustomerID(beneficiary)
	return record, nil
}

func (PostgreSQL) Lookup(ctx context.Context, digest [32]byte, now time.Time) (Record, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return Record{}, err
	}
	var record Record
	var tokenDigest, scopeDigest []byte
	var payer, beneficiary int64
	err = tx.QueryRow(ctx, `
		SELECT id,token_digest,payer_identity_id,payer_customer_id,
			beneficiary_customer_id,app_scope_digest,payment_channel,expires_at,consumed_at,created_at
		FROM payment_sessions WHERE token_digest=$1 AND expires_at>$2`, digest[:], now).Scan(
		&record.ID, &tokenDigest, &record.PayerIdentityID, &payer, &beneficiary,
		&scopeDigest, &record.Channel, &record.ExpiresAt, &record.ConsumedAt, &record.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrExpired
	}
	if err != nil {
		return Record{}, err
	}
	if len(tokenDigest) != 32 || len(scopeDigest) != 32 {
		return Record{}, ErrInvalid
	}
	copy(record.TokenDigest[:], tokenDigest)
	copy(record.AppScopeDigest[:], scopeDigest)
	record.PayerCustomerID = customerdomain.CustomerID(payer)
	record.BeneficiaryCustomerID = customerdomain.CustomerID(beneficiary)
	return record, nil
}
