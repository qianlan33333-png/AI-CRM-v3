package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"time"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func (r *Repository) ReserveIntegrationNonce(ctx context.Context, key, nonce, idempotencyKey string, payloadDigest [32]byte, timestamp, expiresAt time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if key == "" || nonce == "" || idempotencyKey == "" || timestamp.IsZero() || !expiresAt.After(timestamp) {
		return ErrInvalid
	}
	keyDigest, nonceDigest, idempotencyDigest := sha256.Sum256([]byte(key)), sha256.Sum256([]byte(nonce)), sha256.Sum256([]byte(idempotencyKey))
	tag, err := tx.Exec(ctx, `INSERT INTO ai_assistant_integration_nonces(key_digest,nonce_digest,idempotency_digest,payload_digest,request_timestamp,expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, keyDigest[:], nonceDigest[:], idempotencyDigest[:], payloadDigest[:], timestamp.UTC(), expiresAt.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var storedID, storedPayload []byte
	if err = tx.QueryRow(ctx, `SELECT idempotency_digest,payload_digest FROM ai_assistant_integration_nonces WHERE key_digest=$1 AND nonce_digest=$2 FOR UPDATE`, keyDigest[:], nonceDigest[:]).Scan(&storedID, &storedPayload); err != nil {
		return err
	}
	if !bytes.Equal(storedID, idempotencyDigest[:]) || !bytes.Equal(storedPayload, payloadDigest[:]) {
		return ErrConflict
	}
	return nil
}
