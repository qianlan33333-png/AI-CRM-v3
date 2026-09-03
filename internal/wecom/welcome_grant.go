package wecom

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrWelcomeGrantUnavailable = errors.New("welcome grant unavailable")

type WelcomeGrantStore interface {
	Seal(context.Context, string, string, time.Time) (string, error)
}
type WelcomeGrantRedeemer interface {
	Redeem(context.Context, string, string) (string, error)
}
type WelcomeGrantCipher struct{ aead cipher.AEAD }

func NewWelcomeGrantCipher(secret string) (*WelcomeGrantCipher, error) {
	if len(secret) < 32 {
		return nil, ErrWelcomeGrantUnavailable
	}
	key := sha256.Sum256([]byte("aicrm.wecom.welcome-grant.v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, ErrWelcomeGrantUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrWelcomeGrantUnavailable
	}
	return &WelcomeGrantCipher{aead: aead}, nil
}
func (ciphertext *WelcomeGrantCipher) encrypt(value string) ([]byte, error) {
	nonce := make([]byte, ciphertext.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return ciphertext.aead.Seal(nonce, nonce, []byte(value), nil), nil
}
func (ciphertext *WelcomeGrantCipher) decrypt(value []byte) (string, error) {
	size := ciphertext.aead.NonceSize()
	if len(value) <= size {
		return "", ErrWelcomeGrantUnavailable
	}
	plain, err := ciphertext.aead.Open(nil, value[:size], value[size:], nil)
	if err != nil {
		return "", ErrWelcomeGrantUnavailable
	}
	return string(plain), nil
}

type PostgreSQLWelcomeGrantStore struct{ cipher *WelcomeGrantCipher }

func NewPostgreSQLWelcomeGrantStore(cipher *WelcomeGrantCipher) *PostgreSQLWelcomeGrantStore {
	return &PostgreSQLWelcomeGrantStore{cipher: cipher}
}
func (store *PostgreSQLWelcomeGrantStore) Seal(ctx context.Context, callbackKey, value string, expires time.Time) (string, error) {
	if store == nil || store.cipher == nil || value == "" || len(value) > 4096 || strings.TrimSpace(value) != value || expires.Before(time.Now().UTC()) {
		return "", ErrWelcomeGrantUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	callback := sha256.Sum256([]byte(callbackKey))
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM wecom_welcome_grants WHERE callback_digest=$1`, callback[:]).Scan(&id)
	if err == nil {
		return "wgrant_" + formatGrantID(id), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	encrypted, err := store.cipher.encrypt(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(value))
	err = tx.QueryRow(ctx, `INSERT INTO wecom_welcome_grants(callback_digest,value_digest,ciphertext,expires_at) VALUES($1,$2,$3,$4) RETURNING id`, callback[:], digest[:], encrypted, expires.UTC()).Scan(&id)
	if err != nil {
		return "", err
	}
	return "wgrant_" + formatGrantID(id), nil
}
func (store *PostgreSQLWelcomeGrantStore) Redeem(ctx context.Context, reference, effectRef string) (string, error) {
	id, ok := parseGrantRef(reference)
	if !ok || !strings.HasPrefix(effectRef, "eer_") {
		return "", ErrWelcomeGrantUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return "", err
	}
	var encrypted []byte
	err = tx.QueryRow(ctx, `UPDATE wecom_welcome_grants SET consumed_at=clock_timestamp(),consumer_effect_ref=$2 WHERE id=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING ciphertext`, id, effectRef).Scan(&encrypted)
	if err != nil {
		return "", ErrWelcomeGrantUnavailable
	}
	return store.cipher.decrypt(encrypted)
}
func formatGrantID(id int64) string {
	return strings.TrimLeft(hex.EncodeToString([]byte{byte(id >> 56), byte(id >> 48), byte(id >> 40), byte(id >> 32), byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}), "0")
}
func parseGrantRef(value string) (int64, bool) {
	raw := strings.TrimPrefix(value, "wgrant_")
	if raw == "" || raw == value {
		return 0, false
	}
	decoded, err := hex.DecodeString(func() string {
		if len(raw)%2 == 1 {
			return "0" + raw
		}
		return raw
	}())
	if err != nil || len(decoded) > 8 {
		return 0, false
	}
	var id int64
	for _, b := range decoded {
		id = id<<8 | int64(b)
	}
	return id, id > 0
}
