package store

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

var _ accessport.MachineRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalVerificationRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalAuditRepository = (*PostgreSQL)(nil)
var _ accessport.MachineHistoricalBatchRepository = (*PostgreSQL)(nil)

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
	clients := make([]domain.MachineClient, 0)
	for rows.Next() {
		client, scanErr := scanMachineClient(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		clients = append(clients, client)
	}
	// pgx transactions use one connection. Close the outer result before
	// loading grants so a multi-row management list never re-enters a busy
	// connection with an active cursor.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range clients {
		capabilities, capabilityErr := machineCapabilities(ctx, database, clients[index].ID)
		if capabilityErr != nil {
			return nil, capabilityErr
		}
		clients[index].Capabilities = capabilities
	}
	return clients, nil
}

func (*PostgreSQL) BeginHistoricalMachineImport(ctx context.Context, batch accessport.HistoricalMachineImportBatch) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var digest []byte
	var sourceSystem, sourceRevision string
	var snapshotAt time.Time
	var clientCount, auditCount int
	err = database.QueryRow(ctx, `SELECT manifest_digest,source_system,source_revision,snapshot_at,client_count,audit_count FROM access_machine_import_batches WHERE import_run_id=$1 FOR UPDATE`, batch.ImportRunID).Scan(&digest, &sourceSystem, &sourceRevision, &snapshotAt, &clientCount, &auditCount)
	if err == nil {
		if !bytes.Equal(digest, batch.ManifestDigest[:]) || sourceSystem != batch.SourceSystem || sourceRevision != batch.SourceRevision || !snapshotAt.Equal(batch.SnapshotAt.UTC()) || clientCount != batch.ClientCount || auditCount != batch.AuditCount {
			return false, domain.ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if _, err = database.Exec(ctx, `INSERT INTO access_machine_import_batches(import_run_id,source_system,source_revision,manifest_digest,snapshot_at,client_count,audit_count) VALUES($1,$2,$3,$4,$5,$6,$7)`, batch.ImportRunID, batch.SourceSystem, batch.SourceRevision, batch.ManifestDigest[:], batch.SnapshotAt.UTC(), batch.ClientCount, batch.AuditCount); err != nil {
		return false, mapDatabaseError(err)
	}
	return false, nil
}

func (*PostgreSQL) VerifyHistoricalMachineImport(ctx context.Context, batch accessport.HistoricalMachineImportBatch) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	var digest []byte
	var sourceSystem, sourceRevision string
	var snapshotAt time.Time
	var clientCount, auditCount int
	err = database.QueryRow(ctx, `SELECT manifest_digest,source_system,source_revision,snapshot_at,client_count,audit_count FROM access_machine_import_batches WHERE import_run_id=$1`, batch.ImportRunID).Scan(&digest, &sourceSystem, &sourceRevision, &snapshotAt, &clientCount, &auditCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, batch.ManifestDigest[:]) || sourceSystem != batch.SourceSystem || sourceRevision != batch.SourceRevision || !snapshotAt.Equal(batch.SnapshotAt.UTC()) || clientCount != batch.ClientCount || auditCount != batch.AuditCount {
		return domain.ErrConflict
	}
	return nil
}

// ImportHistoricalMachineClient writes a source-row receipt and an inert
// replacement credential together. A replay with the same source digest reads
// the original result; a changed source row cannot silently alter it.
func (*PostgreSQL) ImportHistoricalMachineClient(ctx context.Context, input accessport.HistoricalMachineImportInput, client domain.MachineClient) (domain.MachineClient, bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, false, err
	}
	var storedDigest []byte
	var storedClientID *int64
	var outcome, reason string
	err = database.QueryRow(ctx, `SELECT source_row_digest,machine_client_id,outcome,reason_code FROM access_machine_import_receipts WHERE import_run_id=$1 AND source_row_id=$2 FOR UPDATE`, input.ImportRunID, input.SourceRowID).Scan(&storedDigest, &storedClientID, &outcome, &reason)
	switch {
	case err == nil:
		if !bytes.Equal(storedDigest, input.SourceRowDigest[:]) || outcome != "reissue_required" || reason != "" || storedClientID == nil {
			return domain.MachineClient{}, false, domain.ErrConflict
		}
		result, readErr := scanMachineClient(database.QueryRow(ctx, machineClientSelect+` WHERE c.id=$1`, *storedClientID))
		if readErr != nil {
			return domain.MachineClient{}, false, readErr
		}
		result.Capabilities, readErr = machineCapabilities(ctx, database, result.ID)
		return result, true, readErr
	case !errors.Is(err, pgx.ErrNoRows):
		return domain.MachineClient{}, false, err
	}
	created, err := createMachineClient(ctx, database, client)
	if err != nil {
		return domain.MachineClient{}, false, err
	}
	if _, err = database.Exec(ctx, `INSERT INTO access_machine_import_receipts
		(import_run_id,source_row_id,source_row_digest,source_client_id,source_principal_id,source_principal_type,source_enabled,source_auth_version,machine_client_id,outcome,reason_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'reissue_required','')`, input.ImportRunID, input.SourceRowID, input.SourceRowDigest[:], input.ClientID, input.PrincipalID, input.PrincipalType, input.SourceEnabled, input.SourceAuthVersion, created.ID); err != nil {
		return domain.MachineClient{}, false, err
	}
	return created, false, nil
}

// RecordHistoricalMachineExclusion preserves a source row that V3 cannot host
// without broadening its authority. Repeating its exact digest is harmless;
// a changed row remains a hard conflict for operator review.
func (*PostgreSQL) RecordHistoricalMachineExclusion(ctx context.Context, input accessport.HistoricalMachineImportInput, reason string) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var storedDigest []byte
	var outcome, storedReason string
	err = database.QueryRow(ctx, `SELECT source_row_digest,outcome,reason_code FROM access_machine_import_receipts WHERE import_run_id=$1 AND source_row_id=$2 FOR UPDATE`, input.ImportRunID, input.SourceRowID).Scan(&storedDigest, &outcome, &storedReason)
	if err == nil {
		if !bytes.Equal(storedDigest, input.SourceRowDigest[:]) || outcome != "excluded" || storedReason != reason {
			return false, domain.ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = database.Exec(ctx, `INSERT INTO access_machine_import_receipts
		(import_run_id,source_row_id,source_row_digest,source_client_id,source_principal_id,source_principal_type,source_enabled,source_auth_version,machine_client_id,outcome,reason_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULL,'excluded',$9)`, input.ImportRunID, input.SourceRowID, input.SourceRowDigest[:], input.ClientID, input.PrincipalID, input.PrincipalType, input.SourceEnabled, input.SourceAuthVersion, reason)
	return false, err
}

// VerifyHistoricalMachineClient reads a receipt without creating a row. An
// excluded fact deliberately has no client; its outcome and reason remain the
// evidence that a future rotation cannot expand an unsupported old grant.
func (*PostgreSQL) VerifyHistoricalMachineClient(ctx context.Context, input accessport.HistoricalMachineImportInput) (domain.MachineClient, string, string, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, "", "", err
	}
	var storedDigest []byte
	var clientID *int64
	var outcome, reason string
	if err = database.QueryRow(ctx, `SELECT source_row_digest,machine_client_id,outcome,reason_code FROM access_machine_import_receipts WHERE import_run_id=$1 AND source_row_id=$2`, input.ImportRunID, input.SourceRowID).Scan(&storedDigest, &clientID, &outcome, &reason); errors.Is(err, pgx.ErrNoRows) {
		return domain.MachineClient{}, "", "", domain.ErrNotFound
	} else if err != nil {
		return domain.MachineClient{}, "", "", err
	}
	if !bytes.Equal(storedDigest, input.SourceRowDigest[:]) {
		return domain.MachineClient{}, "", "", domain.ErrConflict
	}
	if outcome == "excluded" && clientID == nil {
		return domain.MachineClient{}, outcome, reason, nil
	}
	if outcome != "reissue_required" || reason != "" || clientID == nil {
		return domain.MachineClient{}, "", "", domain.ErrConflict
	}
	result, err := scanMachineClient(database.QueryRow(ctx, machineClientSelect+` WHERE c.id=$1`, *clientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MachineClient{}, "", "", domain.ErrNotFound
	}
	if err != nil {
		return domain.MachineClient{}, "", "", err
	}
	result.Capabilities, err = machineCapabilities(ctx, database, result.ID)
	return result, outcome, reason, err
}

func (*PostgreSQL) ImportHistoricalMachineAudit(ctx context.Context, input accessport.HistoricalMachineAuditInput) (bool, error) {
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var digest, before, after []byte
	var operator, action, targetType, targetID string
	var occurred time.Time
	err = database.QueryRow(ctx, `SELECT source_row_digest,before_payload_digest,after_payload_digest,source_operator,source_action,source_target_type,source_target_id,occurred_at FROM access_machine_historical_audit_facts WHERE import_run_id=$1 AND source_audit_id=$2 FOR UPDATE`, input.ImportRunID, input.SourceAuditID).Scan(&digest, &before, &after, &operator, &action, &targetType, &targetID, &occurred)
	if err == nil {
		if !bytes.Equal(digest, input.SourceRowDigest[:]) || !bytes.Equal(before, input.BeforeDigest[:]) || !bytes.Equal(after, input.AfterDigest[:]) || operator != input.Operator || action != input.Action || targetType != input.TargetType || targetID != input.TargetID || !occurred.Equal(input.OccurredAt.UTC()) {
			return false, domain.ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = database.Exec(ctx, `INSERT INTO access_machine_historical_audit_facts
		(import_run_id,source_audit_id,source_row_digest,source_operator,source_action,source_target_type,source_target_id,before_payload_digest,after_payload_digest,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, input.ImportRunID, input.SourceAuditID, input.SourceRowDigest[:], input.Operator, input.Action, input.TargetType, input.TargetID, input.BeforeDigest[:], input.AfterDigest[:], input.OccurredAt.UTC())
	return false, err
}

func (repository *PostgreSQL) VerifyHistoricalMachineAudit(ctx context.Context, input accessport.HistoricalMachineAuditInput) error {
	database, err := tx(ctx)
	if err != nil {
		return err
	}
	var digest, before, after []byte
	var operator, action, targetType, targetID string
	var occurred time.Time
	err = database.QueryRow(ctx, `SELECT source_row_digest,before_payload_digest,after_payload_digest,source_operator,source_action,source_target_type,source_target_id,occurred_at FROM access_machine_historical_audit_facts WHERE import_run_id=$1 AND source_audit_id=$2`, input.ImportRunID, input.SourceAuditID).Scan(&digest, &before, &after, &operator, &action, &targetType, &targetID, &occurred)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, input.SourceRowDigest[:]) || !bytes.Equal(before, input.BeforeDigest[:]) || !bytes.Equal(after, input.AfterDigest[:]) || operator != input.Operator || action != input.Action || targetType != input.TargetType || targetID != input.TargetID || !occurred.Equal(input.OccurredAt.UTC()) {
		return domain.ErrConflict
	}
	return nil
}

func (*PostgreSQL) CreateMachineClient(ctx context.Context, client domain.MachineClient) (domain.MachineClient, error) {
	database, err := tx(ctx)
	if err != nil {
		return domain.MachineClient{}, err
	}
	return createMachineClient(ctx, database, client)
}

func createMachineClient(ctx context.Context, database pgx.Tx, client domain.MachineClient) (domain.MachineClient, error) {
	err := database.QueryRow(ctx, `
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
