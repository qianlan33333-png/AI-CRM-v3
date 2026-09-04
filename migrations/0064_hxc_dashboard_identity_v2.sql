-- Owner: hxcdashboard.
-- Extends the immutable projection with safe OneID resolution metadata.

ALTER TABLE hxc_dashboard_versions
    ADD COLUMN matched_by_unionid_count BIGINT NOT NULL DEFAULT 0 CHECK (matched_by_unionid_count >= 0),
    ADD COLUMN matched_by_phone_count BIGINT NOT NULL DEFAULT 0 CHECK (matched_by_phone_count >= 0),
    ADD COLUMN matched_by_both_count BIGINT NOT NULL DEFAULT 0 CHECK (matched_by_both_count >= 0),
    ADD COLUMN pending_observation_count BIGINT NOT NULL DEFAULT 0 CHECK (pending_observation_count >= 0),
    ADD COLUMN invalid_identity_count BIGINT NOT NULL DEFAULT 0 CHECK (invalid_identity_count >= 0);

ALTER TABLE hxc_dashboard_versions ADD CONSTRAINT ck_hxc_dashboard_v2_matched
    CHECK (matched_count = matched_by_unionid_count + matched_by_phone_count + matched_by_both_count);
ALTER TABLE hxc_dashboard_versions ADD CONSTRAINT ck_hxc_dashboard_v2_unmatched
    CHECK (unmatched_count = pending_observation_count + invalid_identity_count);

ALTER TABLE hxc_dashboard_rows
    ADD COLUMN matched_by TEXT NOT NULL DEFAULT 'none'
        CHECK (matched_by IN ('none','unionid','phone','both')),
    ADD COLUMN identity_reason_code TEXT NOT NULL DEFAULT 'legacy_unmatched'
        CHECK (identity_reason_code <> '' AND length(identity_reason_code) <= 80),
    ADD COLUMN identity_case_id BIGINT,
    ADD COLUMN merge_candidate_id BIGINT;

ALTER TABLE hxc_dashboard_rows ADD CONSTRAINT ck_hxc_dashboard_v2_row_match
    CHECK ((identity_state = 'matched' AND customer_id IS NOT NULL AND matched_by <> 'none')
        OR (identity_state <> 'matched' AND customer_id IS NULL AND matched_by = 'none'));

ALTER TABLE hxc_dashboard_refresh_runs
    ADD COLUMN identity_mode TEXT NOT NULL DEFAULT 'inspect'
        CHECK (identity_mode IN ('inspect','apply'));
