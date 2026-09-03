-- Owners: internal/payment and internal/order. Closes exact Provider
-- reconciliation and freezes the Product version used by native checkout.
ALTER TABLE payment_refunds
  ADD COLUMN provider_refund_reference TEXT
  CHECK (provider_refund_reference IS NULL OR (
    length(provider_refund_reference) BETWEEN 1 AND 200 AND
    btrim(provider_refund_reference)=provider_refund_reference
  ));

CREATE UNIQUE INDEX payment_refunds_provider_reference_unique
  ON payment_refunds(provider,provider_refund_reference)
  WHERE provider_refund_reference IS NOT NULL;

CREATE UNIQUE INDEX payment_reconciliations_evidence_unique
  ON payment_reconciliations(evidence_digest);

ALTER TABLE order_items
  ADD COLUMN product_version BIGINT
  CHECK (product_version IS NULL OR product_version > 0);

-- Owner: internal/identity. One receipt per authoritative source person or
-- safely quarantined identity row makes the one-off production import fully
-- accountable without persisting raw quarantined identifiers.
CREATE TABLE identity_history_import_receipts (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_key TEXT NOT NULL CHECK (length(run_key) BETWEEN 1 AND 200 AND btrim(run_key)=run_key),
  source_key TEXT NOT NULL CHECK (length(source_key) BETWEEN 1 AND 200 AND btrim(source_key)=source_key),
  source_digest BYTEA NOT NULL CHECK (octet_length(source_digest)=32),
  outcome TEXT NOT NULL CHECK (outcome IN ('canonical','quarantined')),
  customer_id BIGINT,
  identity_count INTEGER NOT NULL DEFAULT 0 CHECK (identity_count>=0),
  reason_code TEXT,
  safe_evidence JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(run_key,source_key),
  CHECK (
    (outcome='canonical' AND customer_id>0 AND identity_count>0 AND reason_code IS NULL AND safe_evidence IS NULL)
    OR
    (outcome='quarantined' AND customer_id IS NULL AND identity_count=0 AND reason_code IS NOT NULL AND jsonb_typeof(safe_evidence)='object')
  )
);

CREATE TRIGGER identity_history_import_receipts_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON identity_history_import_receipts
  FOR EACH STATEMENT EXECUTE FUNCTION order_immutable_facts_reject_mutation();
