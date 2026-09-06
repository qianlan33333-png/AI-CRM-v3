-- Owner: internal/access. Machine clients are independent of admin sessions
-- and can never inherit an administrator role. Secret material is Argon2id
-- hashed and cannot be recovered by the UI or historical importer.

CREATE TABLE access_machine_clients (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id TEXT NOT NULL UNIQUE CHECK (client_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{2,119}$'),
    display_name TEXT NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 160),
    purpose TEXT NOT NULL CHECK (purpose IN ('external_agent', 'mcp', 'direct_api_key')),
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

-- Historical source records are never credentials. Every non-reissued legacy
-- client stays disabled and needs an explicit newly generated secret.
CREATE TABLE access_machine_import_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    import_run_id TEXT NOT NULL CHECK (length(import_run_id) BETWEEN 1 AND 160),
    source_row_id TEXT NOT NULL CHECK (length(source_row_id) BETWEEN 1 AND 240),
    source_row_digest BYTEA NOT NULL CHECK (octet_length(source_row_digest) = 32),
    machine_client_id BIGINT REFERENCES access_machine_clients(id),
    outcome TEXT NOT NULL CHECK (outcome IN ('inactive', 'reissue_required', 'invalid', 'replayed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (import_run_id, source_row_id)
);
