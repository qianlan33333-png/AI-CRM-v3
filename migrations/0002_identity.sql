-- Owner: identity
-- Retention: customers, identities, evidence, conflicts and merge lineage are
-- durable business records. This migration is forward-only; rollback disables
-- the feature and never deletes OneID evidence.

CREATE TABLE customers (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'merged', 'closed')),
    merged_into_customer_id BIGINT REFERENCES customers(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at TIMESTAMPTZ,
    CONSTRAINT ck_customers_merged_target CHECK (
        (status = 'merged' AND merged_into_customer_id IS NOT NULL)
        OR (status <> 'merged' AND merged_into_customer_id IS NULL)
    )
);

-- The active identity key is the external identity's complete namespace.
-- No provider's value is a customer primary key.
CREATE TABLE customer_identities (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id BIGINT NOT NULL REFERENCES customers(id),
    kind TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    assurance TEXT NOT NULL CHECK (assurance IN ('verified', 'declared')),
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    normalizer_version SMALLINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'conflicted', 'retired')),
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_customer_identities_nonempty CHECK (
        kind <> '' AND scope_key <> '' AND normalized_value <> '' AND source <> ''
    ),
    CONSTRAINT ck_verified_identity_timestamp CHECK (
        (assurance = 'verified' AND verified_at IS NOT NULL)
        OR assurance = 'declared'
    )
);

CREATE UNIQUE INDEX ux_customer_identities_active_key
    ON customer_identities(kind, scope_key, normalized_value)
    WHERE status = 'active';
CREATE INDEX ix_customer_identities_customer_active
    ON customer_identities(customer_id, status);

CREATE TABLE identity_link_evidence (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    left_customer_id BIGINT NOT NULL REFERENCES customers(id),
    right_customer_id BIGINT REFERENCES customers(id),
    left_identity_id BIGINT REFERENCES customer_identities(id),
    right_identity_id BIGINT REFERENCES customer_identities(id),
    evidence_type TEXT NOT NULL,
    strength TEXT NOT NULL CHECK (strength IN ('strong', 'medium', 'weak')),
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    evidence_digest TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    operator TEXT NOT NULL DEFAULT 'system',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_identity_link_evidence_nonempty CHECK (
        evidence_type <> '' AND source <> '' AND evidence_digest <> '' AND policy_version <> ''
    )
);
CREATE INDEX ix_identity_link_evidence_customers
    ON identity_link_evidence(left_customer_id, right_customer_id);

CREATE TABLE identity_link_intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    source_customer_id BIGINT NOT NULL REFERENCES customers(id),
    purpose TEXT NOT NULL CHECK (purpose IN ('bind_wecom', 'bind_provider_identity')),
    target_kind TEXT NOT NULL,
    expected_scope_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'consumed', 'expired', 'cancelled')),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    consumed_identity_id BIGINT REFERENCES customer_identities(id),
    consumed_customer_id BIGINT REFERENCES customers(id),
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_identity_link_intent_consumed CHECK (
        (status = 'consumed' AND consumed_at IS NOT NULL AND consumed_identity_id IS NOT NULL)
        OR status <> 'consumed'
    )
);
CREATE INDEX ix_identity_link_intents_pending_expiry
    ON identity_link_intents(expires_at) WHERE status = 'pending';

CREATE TABLE customer_merge_candidates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    left_customer_id BIGINT NOT NULL REFERENCES customers(id),
    right_customer_id BIGINT NOT NULL REFERENCES customers(id),
    evidence_id BIGINT NOT NULL REFERENCES identity_link_evidence(id),
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'confirmed', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ,
    CONSTRAINT ck_customer_merge_candidates_distinct CHECK (left_customer_id <> right_customer_id)
);
CREATE UNIQUE INDEX ux_customer_merge_candidates_open_pair
    ON customer_merge_candidates(LEAST(left_customer_id, right_customer_id), GREATEST(left_customer_id, right_customer_id))
    WHERE status = 'open';

CREATE TABLE customer_identity_conflicts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    left_customer_id BIGINT NOT NULL REFERENCES customers(id),
    right_customer_id BIGINT NOT NULL REFERENCES customers(id),
    evidence_id BIGINT REFERENCES identity_link_evidence(id),
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'ignored')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ,
    CONSTRAINT ck_customer_identity_conflicts_distinct CHECK (left_customer_id <> right_customer_id)
);
CREATE UNIQUE INDEX ux_customer_identity_conflicts_open_pair_reason
    ON customer_identity_conflicts(LEAST(left_customer_id, right_customer_id), GREATEST(left_customer_id, right_customer_id), reason)
    WHERE status = 'open';

-- A merge is a ledger entry, never a deletion. customer_merge_identity_members
-- captures the moved identities required for a guarded, reversible merge.
CREATE TABLE customer_merges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_customer_id BIGINT NOT NULL REFERENCES customers(id),
    to_customer_id BIGINT NOT NULL REFERENCES customers(id),
    evidence_id BIGINT NOT NULL REFERENCES identity_link_evidence(id),
    rule TEXT NOT NULL,
    operator TEXT NOT NULL DEFAULT 'system',
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    reversible_status TEXT NOT NULL DEFAULT 'not_reversed'
        CHECK (reversible_status IN ('not_reversed', 'reversed', 'not_reversible')),
    merged_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reversed_at TIMESTAMPTZ,
    CONSTRAINT ck_customer_merges_distinct CHECK (from_customer_id <> to_customer_id)
);
CREATE INDEX ix_customer_merges_from_customer ON customer_merges(from_customer_id);
CREATE INDEX ix_customer_merges_to_customer ON customer_merges(to_customer_id);

CREATE TABLE customer_merge_identity_members (
    merge_id BIGINT NOT NULL REFERENCES customer_merges(id),
    identity_id BIGINT NOT NULL REFERENCES customer_identities(id),
    PRIMARY KEY (merge_id, identity_id)
);
