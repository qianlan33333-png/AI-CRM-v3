-- Owners: internal/order, internal/product, internal/outbound and
-- internal/externaleffects.  This is the forward-only bridge from a native
-- first-paid Order fact to one controlled commerce webhook effect.  Raw
-- identity values and signing material never enter EER or these audit rows.

-- 0010 predates configuration revisions. Existing rows are release-compatible
-- at revision 1; Product increments this value on each later write.
ALTER TABLE product_external_push_configurations
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);

-- Old 0010 intentionally admitted only one local placeholder test per
-- configuration digest. A real explicit test operation is keyed by its
-- Product receipt instead, so an administrator can make a second deliberate
-- test without configuration churn. Replaying the same receipt remains one
-- operation.
ALTER TABLE product_external_push_tests
    DROP CONSTRAINT IF EXISTS product_external_push_tests_configuration_unique;
CREATE UNIQUE INDEX IF NOT EXISTS product_external_push_tests_receipt_unique
    ON product_external_push_tests(receipt_id);

-- The original check accepted order.paid but not the versioned durable event.
ALTER TABLE order_outbox
    DROP CONSTRAINT IF EXISTS order_outbox_event_type_check;
ALTER TABLE order_outbox
    ADD CONSTRAINT order_outbox_event_type_check
    CHECK (event_type ~ '^order[.][a-z_]+([.]v[0-9]+)?$');

CREATE TABLE order_paid_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    order_version BIGINT NOT NULL CHECK (order_version > 1),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(order_id),
    UNIQUE(order_id, order_version)
);
CREATE INDEX order_paid_events_occurred_idx ON order_paid_events(occurred_at, id);

-- Preserve the complete existing EER union from 0093 before adding the sole
-- commerce-push kind. Do not narrow customer owner-handoff/tag commands or
-- prior payment/outbound kinds while this independent migration is applied.
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_owner_kind_shape;
ALTER TABLE external_effects DROP CONSTRAINT IF EXISTS external_effects_kind_check;
ALTER TABLE external_effects ADD CONSTRAINT external_effects_kind_check CHECK (kind IN (
  'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
  'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
  'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
  'customer_owner_handoff','customer_tag_command','commerce_product_push',
  'wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'
));
ALTER TABLE external_effects ADD CONSTRAINT external_effects_owner_kind_shape CHECK (
  (owner='outbound' AND kind IN (
    'outbound_message','automation_message','outbound_media','wecom_tag_catalog','group_message',
    'channel_acquisition_asset','channel_welcome_message','channel_entry_tag',
    'channel_acquisition_link_mutation','sidebar_jssdk_send','survey_completion',
    'customer_owner_handoff','customer_tag_command','commerce_product_push'
  )) OR
  (owner='payment' AND kind IN ('wechat_pay_prepay_v1','wechat_pay_refund_v1','wechat_shop_refund_v1'))
);

-- Outbound owns the durable intention, encrypted exact body and completion
-- projection. source_reference + target_slot is the logical send identity;
-- target configuration revisions/digests therefore cannot mint a second paid
-- delivery after the first acceptance.
CREATE TABLE outbound_commerce_push_intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('order_paid','synthetic_test','history_paid')),
    source_reference TEXT NOT NULL CHECK (source_reference = btrim(source_reference) AND char_length(source_reference) BETWEEN 1 AND 200 AND source_reference !~ '[[:cntrl:]]'),
    order_paid_event_id BIGINT NULL REFERENCES order_paid_events(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    product_kind TEXT NOT NULL CHECK (product_kind IN ('wechat_pay','service_period')),
    target_reference TEXT NOT NULL CHECK (target_reference = btrim(target_reference) AND char_length(target_reference) BETWEEN 1 AND 128 AND target_reference !~ '[[:cntrl:]]'),
    target_slot TEXT NOT NULL CHECK (target_slot = btrim(target_slot) AND char_length(target_slot) BETWEEN 1 AND 128 AND target_slot !~ '[[:cntrl:]]'),
    product_configuration_revision BIGINT NOT NULL CHECK (product_configuration_revision > 0),
    source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
    target_digest BYTEA NOT NULL CHECK (octet_length(target_digest)=32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    policy_digest BYTEA NOT NULL CHECK (octet_length(policy_digest)=32),
    receipt_key_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(receipt_key_digest)=32),
    intent_digest BYTEA NOT NULL CHECK (octet_length(intent_digest)=32),
    envelope_fingerprint TEXT NULL UNIQUE CHECK (envelope_fingerprint IS NULL OR envelope_fingerprint ~ '^sha256:[0-9a-f]{64}$'),
    payload_ciphertext BYTEA NULL,
    payload_key_version SMALLINT NULL CHECK (payload_key_version IN (1)),
    effect_id TEXT NULL UNIQUE CHECK (effect_id IS NULL OR effect_id ~ '^eer_[1-9][0-9]*$'),
    queue_receipt_id TEXT NULL CHECK (queue_receipt_id IS NULL OR queue_receipt_id ~ '^eerop_[1-9][0-9]*$'),
    state TEXT NOT NULL CHECK (state IN ('planned_disabled','planned_target_unavailable','planned_identity_unavailable','planned_payload_protection_unavailable','accepted','queued','attempted','provider_accepted','final_failed','outcome_unknown','reconciled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    provider_call_attempted BOOLEAN NOT NULL DEFAULT FALSE,
    provider_real_call_executed BOOLEAN NOT NULL DEFAULT FALSE,
    provider_result_received BOOLEAN NULL,
    receipt_digest BYTEA NULL CHECK (receipt_digest IS NULL OR octet_length(receipt_digest)=32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_reference, target_slot),
    UNIQUE(order_paid_event_id, target_slot),
    CONSTRAINT outbound_commerce_push_source_shape CHECK (
      (source_kind='order_paid' AND order_paid_event_id IS NOT NULL)
      OR (source_kind IN ('synthetic_test','history_paid') AND order_paid_event_id IS NULL)
    ),
    CONSTRAINT outbound_commerce_push_payload_shape CHECK (
      (state IN ('planned_disabled','planned_target_unavailable','planned_identity_unavailable','planned_payload_protection_unavailable') AND payload_ciphertext IS NULL AND payload_key_version IS NULL AND effect_id IS NULL AND queue_receipt_id IS NULL AND envelope_fingerprint IS NULL)
      OR (state NOT IN ('planned_disabled','planned_target_unavailable','planned_identity_unavailable','planned_payload_protection_unavailable') AND payload_ciphertext IS NOT NULL AND payload_key_version IS NOT NULL AND effect_id IS NOT NULL AND queue_receipt_id IS NOT NULL AND envelope_fingerprint IS NOT NULL)
    ),
    CONSTRAINT outbound_commerce_push_call_shape CHECK (NOT provider_real_call_executed OR provider_call_attempted)
);
CREATE INDEX outbound_commerce_push_effect_idx ON outbound_commerce_push_intents(effect_id);
CREATE INDEX outbound_commerce_push_timeline_idx ON outbound_commerce_push_intents(product_id, created_at DESC, id DESC);

CREATE TABLE outbound_commerce_push_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    intent_id BIGINT NOT NULL REFERENCES outbound_commerce_push_intents(id) ON DELETE RESTRICT,
    operation TEXT NOT NULL CHECK (operation IN ('planned','accepted','completed','reconciled')),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE outbound_commerce_push_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type IN ('outbound.commerce_push.planned.v1','outbound.commerce_push.queued.v1','outbound.commerce_push.completed.v1')),
    intent_id BIGINT NOT NULL REFERENCES outbound_commerce_push_intents(id) ON DELETE RESTRICT,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    idempotency_digest BYTEA NOT NULL CHECK (octet_length(idempotency_digest)=32),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(event_type, idempotency_digest)
);
CREATE OR REPLACE FUNCTION outbound_commerce_push_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'outbound commerce push evidence is append-only'; END;
$$;
CREATE TRIGGER outbound_commerce_push_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON outbound_commerce_push_audit_events FOR EACH STATEMENT EXECUTE FUNCTION outbound_commerce_push_append_only();
CREATE TRIGGER outbound_commerce_push_outbox_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON outbound_commerce_push_outbox FOR EACH STATEMENT EXECUTE FUNCTION outbound_commerce_push_append_only();
