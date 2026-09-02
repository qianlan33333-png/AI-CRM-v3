package wecom

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQLFollowRelationshipStore struct{}

func NewPostgreSQLFollowRelationshipStore() *PostgreSQLFollowRelationshipStore {
	return &PostgreSQLFollowRelationshipStore{}
}

func (*PostgreSQLFollowRelationshipStore) Upsert(ctx context.Context, relationship FollowRelationship) error {
	if relationship.CorpID == "" || relationship.EmployeeID == "" || relationship.CustomerID < 1 {
		return errors.New("invalid follow relationship")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wecom_follow_relationships (corp_id, employee_id, customer_id, active)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (corp_id, employee_id, customer_id) DO UPDATE
		SET active = EXCLUDED.active, updated_at = clock_timestamp()`,
		relationship.CorpID, relationship.EmployeeID, relationship.CustomerID, relationship.Active)
	return err
}

func (*PostgreSQLFollowRelationshipStore) IsActive(ctx context.Context, corpID, employeeID string, customerID customerdomain.CustomerID) (bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return false, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM wecom_follow_relationships WHERE corp_id=$1 AND employee_id=$2 AND customer_id=$3`, corpID, employeeID, customerID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}

type OAuthState struct {
	Purpose   OAuthPurpose
	Redirect  string
	ExpiresAt time.Time
}

type OAuthStateStore interface {
	Create(context.Context, OAuthState, [32]byte, [32]byte) error
	Consume(context.Context, OAuthPurpose, [32]byte, time.Time) (OAuthState, error)
}

type PostgreSQLOAuthStateStore struct{}

func NewPostgreSQLOAuthStateStore() *PostgreSQLOAuthStateStore { return &PostgreSQLOAuthStateStore{} }

func (*PostgreSQLOAuthStateStore) Create(ctx context.Context, state OAuthState, digest, nonceDigest [32]byte) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wecom_oauth_states (purpose, state_digest, nonce_digest, redirect_path, expires_at) VALUES ($1, $2, $3, $4, $5)`, state.Purpose, digest[:], nonceDigest[:], state.Redirect, state.ExpiresAt)
	return err
}

func (*PostgreSQLOAuthStateStore) Consume(ctx context.Context, purpose OAuthPurpose, digest [32]byte, now time.Time) (OAuthState, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return OAuthState{}, err
	}
	var state OAuthState
	err = tx.QueryRow(ctx, `
		UPDATE wecom_oauth_states
		SET used_at = $4
		WHERE purpose = $1 AND state_digest = $2 AND used_at IS NULL AND expires_at >= $3
		RETURNING purpose, redirect_path, expires_at`, purpose, digest[:], now, now).Scan(&state.Purpose, &state.Redirect, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthState{}, ErrInvalidOAuth
	}
	return state, err
}

func oauthDigest(state string) [32]byte { return sha256.Sum256([]byte(state)) }
