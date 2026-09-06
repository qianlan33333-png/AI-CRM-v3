-- Owner: internal/access. Machine clients are independent of admin sessions
-- and can never inherit an administrator role. Secret material is Argon2id
-- hashed and cannot be recovered by the UI or historical importer.

CREATE TABLE access_machine_clients (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id TEXT NOT NULL UNIQUE CHECK (client_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{2,119}$'),
    display_name TEXT NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 160),
    purpose TEXT NOT NULL CHECK (purpose IN ('external_agent', 'mcp', 'direct_api_key', 'identity', 'group_broadcast', 'campaign_agent', 'ops_reporter', 'operation_runner')),
    secret_hash TEXT NOT NULL CHECK (secret_hash LIKE '$argon2id$%'),
    credential_hint TEXT NOT NULL CHECK (length(credential_hint) BETWEEN 4 AND 40),
    audiences TEXT[] NOT NULL CHECK (cardinality(audiences) BETWEEN 1 AND 16),
    scopes TEXT[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 16),
    allowed_cidrs CIDR[] NOT NULL DEFAULT '{}',
    corp_id TEXT NOT NULL DEFAULT '',
    owner_scope JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(owner_scope) = 'object'),
    token_ttl_seconds INTEGER NOT NULL CHECK (token_ttl_seconds BETWEEN 60 AND 3600),
    expires_at TIMESTAMPTZ,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    reissue_required BOOLEAN NOT NULL DEFAULT FALSE,
    auth_version BIGINT NOT NULL DEFAULT 1 CHECK (auth_version > 0),
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE access_machine_client_grants (
    machine_client_id BIGINT NOT NULL REFERENCES access_machine_clients(id) ON DELETE CASCADE,
    capability TEXT NOT NULL CHECK (length(capability) BETWEEN 3 AND 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (machine_client_id, capability)
);

CREATE TABLE access_machine_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    machine_client_id BIGINT NOT NULL REFERENCES access_machine_clients(id),
    actor_admin_user_id BIGINT REFERENCES admin_users(id),
    action TEXT NOT NULL CHECK (length(action) BETWEEN 3 AND 120),
    outcome TEXT NOT NULL CHECK (length(outcome) BETWEEN 2 AND 80),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ix_access_machine_audit_client_created ON access_machine_audit(machine_client_id, created_at DESC, id DESC);

-- Each protected source revision has one digest. The row is written before
-- individual client/audit receipts so an overlapping snapshot cannot grow an
-- already-reviewed migration batch.
CREATE TABLE access_machine_import_batches (
    import_run_id TEXT PRIMARY KEY CHECK (length(import_run_id) BETWEEN 1 AND 160),
    source_system TEXT NOT NULL CHECK (source_system = 'ai-crm'),
    source_revision TEXT NOT NULL CHECK (source_revision ~ '^[a-f0-9]{40}$'),
    manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest) = 32),
    snapshot_at TIMESTAMPTZ NOT NULL,
    client_count INTEGER NOT NULL CHECK (client_count >= 0),
    audit_count INTEGER NOT NULL CHECK (audit_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_system, source_revision)
);

-- Historical source records are never credentials. Every non-reissued legacy
-- client stays disabled and needs an explicit newly generated secret. The
-- source authorization facts are retained verbatim when V3 supports them;
-- unsupported records have an excluded receipt and no machine client.
CREATE TABLE access_machine_import_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id TEXT NOT NULL CHECK (length(import_run_id) BETWEEN 1 AND 160),
    source_row_id TEXT NOT NULL CHECK (length(source_row_id) BETWEEN 1 AND 240),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    source_client_id TEXT NOT NULL CHECK (length(source_client_id) BETWEEN 1 AND 120),
    source_principal_id TEXT NOT NULL DEFAULT '' CHECK (length(source_principal_id) <= 240),
    source_principal_type TEXT NOT NULL DEFAULT '' CHECK (length(source_principal_type) <= 80),
    source_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    source_auth_version BIGINT NOT NULL DEFAULT 1 CHECK (source_auth_version > 0),
    machine_client_id BIGINT REFERENCES access_machine_clients(id),
    outcome TEXT NOT NULL CHECK (outcome IN ('inactive', 'reissue_required', 'excluded', 'invalid', 'replayed')),
    reason_code TEXT NOT NULL DEFAULT '' CHECK (length(reason_code) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, source_row_id),
    CHECK ((outcome = 'reissue_required' AND machine_client_id IS NOT NULL AND reason_code = '')
        OR (outcome = 'excluded' AND machine_client_id IS NULL AND reason_code <> ''))
);
CREATE INDEX ix_access_machine_import_receipts_client ON access_machine_import_receipts(machine_client_id) WHERE machine_client_id IS NOT NULL;

-- Legacy actions are immutable historical facts, separate from the V3 import
-- audit above. Only canonical digests of source before/after JSON are retained
-- so importing historical audit never exposes a donor secret or owner scope.
CREATE TABLE access_machine_historical_audit_facts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id TEXT NOT NULL CHECK (length(import_run_id) BETWEEN 1 AND 160),
    source_audit_id BIGINT NOT NULL CHECK (source_audit_id > 0),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    source_operator TEXT NOT NULL CHECK (length(source_operator) <= 240),
    source_action TEXT NOT NULL CHECK (length(source_action) <= 240),
    source_target_type TEXT NOT NULL CHECK (source_target_type = 'api_client'),
    source_target_id TEXT NOT NULL CHECK (length(source_target_id) <= 240),
    before_payload_digest BYTEA NOT NULL CHECK (octet_length(before_payload_digest) = 32),
    after_payload_digest BYTEA NOT NULL CHECK (octet_length(after_payload_digest) = 32),
    occurred_at TIMESTAMPTZ NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, source_audit_id)
);
CREATE INDEX ix_access_machine_historical_audit_target ON access_machine_historical_audit_facts(import_run_id, source_target_id, source_audit_id);
