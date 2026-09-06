package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

var _ accessport.MachineRepository = (*PostgreSQL)(nil)

func (*PostgreSQL) MachineClientByID(ctx context.Context, clientID string, lock bool) (domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, err
	}
	query := machineClientSelect + ` WHERE c.client_id=$1`
	if lock {
		query += ` FOR UPDATE OF c`
	}
	client, err := scanMachineClient(database.QueryRow(ctx, query, clientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MachineClient{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.MachineClient{}, err
	}
	client.Capabilities, err = machineCapabilities(ctx, database, client.ID)
	return client, err
}

func (*PostgreSQL) ListMachineClients(ctx context.Context) ([]domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, machineClientSelect+` ORDER BY c.created_at DESC, c.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clients := make([]domain.MachineClient, 0)
	for rows.Next() {
		client, scanErr := scanMachineClient(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		client.Capabilities, scanErr = machineCapabilities(ctx, database, client.ID)
		if scanErr != nil {
			return nil, scanErr
		}
		clients = append(clients, client)
	}
	return clients, rows.Err()
}

func (*PostgreSQL) CreateMachineClient(ctx context.Context, client domain.MachineClient) (domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, err
	}
	err = database.QueryRow(ctx, `
		INSERT INTO access_machine_clients
			(client_id, display_name, purpose, secret_hash, credential_hint, audiences, scopes,
			 allowed_cidrs, corp_id, owner_scope, token_ttl_seconds, expires_at, enabled, reissue_required, auth_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::cidr[],$9,$10::jsonb,$11,$12,$13,$14,$15)
		RETURNING id, created_at, updated_at`,
		client.ClientID, client.DisplayName, client.Purpose, client.SecretHash, client.CredentialHint,
		client.Audiences, client.Scopes, client.AllowedCIDRs, client.CorpID, client.OwnerScope.JSON(), client.TokenTTLSeconds, client.ExpiresAt,
		client.Enabled, client.ReissueRequired, client.AuthVersion,
	).Scan(&client.ID, &client.CreatedAt, &client.UpdatedAt)
	if err != nil {
		return domain.MachineClient{}, mapDatabaseError(err)
	}
	if err = replaceMachineGrants(ctx, database, client.ID, client.Capabilities); err != nil {
		return domain.MachineClient{}, err
	}
	return client, nil
}

func (*PostgreSQL) ReplaceMachineClient(ctx context.Context, client domain.MachineClient) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := database.Exec(ctx, `
		UPDATE access_machine_clients SET display_name=$2, purpose=$3, secret_hash=$4,
			credential_hint=$5, audiences=$6, scopes=$7, allowed_cidrs=$8::cidr[], corp_id=$9, owner_scope=$10::jsonb,
			token_ttl_seconds=$11, expires_at=$12, enabled=$13, reissue_required=$14,
			auth_version=$15, updated_at=clock_timestamp()
		WHERE id=$1`,
		client.ID, client.DisplayName, client.Purpose, client.SecretHash, client.CredentialHint,
		client.Audiences, client.Scopes, client.AllowedCIDRs, client.CorpID, client.OwnerScope.JSON(), client.TokenTTLSeconds, client.ExpiresAt,
		client.Enabled, client.ReissueRequired, client.AuthVersion,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return replaceMachineGrants(ctx, database, client.ID, client.Capabilities)
}

func (*PostgreSQL) SetMachineClientLastUsed(ctx context.Context, id int64, now time.Time) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `UPDATE access_machine_clients SET last_used_at=$2 WHERE id=$1`, id, now)
	return err
}

func (*PostgreSQL) AppendMachineAudit(ctx context.Context, audit domain.MachineAudit) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, `INSERT INTO access_machine_audit
		(machine_client_id, actor_admin_user_id, action, outcome, details, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, audit.MachineClientID, audit.ActorAdminID,
		audit.Action, audit.Outcome, audit.Details, audit.CreatedAt)
	return err
}

const machineClientSelect = `SELECT c.id, c.client_id, c.display_name, c.purpose, c.secret_hash,
	c.credential_hint, c.audiences, c.scopes, COALESCE(c.allowed_cidrs::text[], '{}'), c.corp_id, c.owner_scope,
	c.token_ttl_seconds, c.expires_at, c.enabled, c.reissue_required, c.auth_version,
	c.last_used_at, c.created_at, c.updated_at
	FROM access_machine_clients c`

type machineRow interface {
	Scan(...any) error
}

func scanMachineClient(row machineRow) (domain.MachineClient, error) {
	var client domain.MachineClient
	var ownerScope []byte
	err := row.Scan(&client.ID, &client.ClientID, &client.DisplayName, &client.Purpose, &client.SecretHash,
		&client.CredentialHint, &client.Audiences, &client.Scopes, &client.AllowedCIDRs, &client.CorpID, &ownerScope,
		&client.TokenTTLSeconds, &client.ExpiresAt, &client.Enabled, &client.ReissueRequired,
		&client.AuthVersion, &client.LastUsedAt, &client.CreatedAt, &client.UpdatedAt)
	if err != nil {
		return client, err
	}
	client.OwnerScope, err = domain.NormalizeOwnerScope(ownerScope)
	return client, err
}

func machineCapabilities(ctx context.Context, database pgx.Tx, clientID int64) ([]string, error) {
	rows, err := database.Query(ctx, `SELECT capability FROM access_machine_client_grants WHERE machine_client_id=$1 ORDER BY capability`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	capabilities := make([]string, 0)
	for rows.Next() {
		var capability string
		if err := rows.Scan(&capability); err != nil {
			return nil, err
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities, rows.Err()
}

func replaceMachineGrants(ctx context.Context, database pgx.Tx, clientID int64, capabilities []string) error {
	if _, err := database.Exec(ctx, `DELETE FROM access_machine_client_grants WHERE machine_client_id=$1`, clientID); err != nil {
		return err
	}
	for _, capability := range capabilities {
		if _, err := database.Exec(ctx, `INSERT INTO access_machine_client_grants (machine_client_id, capability) VALUES ($1,$2)`, clientID, capability); err != nil {
			return err
		}
	}
	return nil
}
