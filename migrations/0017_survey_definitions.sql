-- Owner: internal/survey.
-- Forward-only: published definition versions and referenced questionnaire
-- history are immutable. A rollback must first prove that no Survey business
-- rows exist; production migration tooling must never drop these tables.

CREATE TABLE survey_questionnaires (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL CHECK (mode IN ('survey','assessment')),
    answer_display_mode TEXT NOT NULL CHECK (answer_display_mode IN ('all_in_one','one_by_one')),
    slug TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('draft','published','disabled')),
    active_definition_version_id BIGINT,
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    updated_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_questionnaires_name CHECK (btrim(name) = name AND char_length(name) BETWEEN 1 AND 200),
    CONSTRAINT survey_questionnaires_title CHECK (btrim(title) = title AND char_length(title) BETWEEN 1 AND 500),
    CONSTRAINT survey_questionnaires_description CHECK (btrim(description) = description AND char_length(description) <= 10000),
    CONSTRAINT survey_questionnaires_slug CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,127}$'),
    CONSTRAINT survey_questionnaires_timestamps CHECK (updated_at >= created_at)
);
CREATE INDEX survey_questionnaires_status_idx ON survey_questionnaires(status, updated_at DESC, id DESC);
CREATE INDEX survey_questionnaires_name_idx ON survey_questionnaires(name, id);

CREATE TABLE survey_definition_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    questionnaire_id BIGINT NOT NULL REFERENCES survey_questionnaires(id) ON DELETE RESTRICT,
    version_number BIGINT NOT NULL CHECK (version_number > 0),
    mode TEXT NOT NULL CHECK (mode IN ('survey','assessment')),
    answer_display_mode TEXT NOT NULL CHECK (answer_display_mode IN ('all_in_one','one_by_one')),
    title_snapshot TEXT NOT NULL,
    description_snapshot TEXT NOT NULL DEFAULT '',
    assessment_config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(assessment_config) = 'object'),
    definition_digest BYTEA NOT NULL CHECK (octet_length(definition_digest) = 32),
    is_immutable BOOLEAN NOT NULL DEFAULT FALSE,
    published_at TIMESTAMPTZ,
    created_by BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT survey_definition_versions_title CHECK (btrim(title_snapshot) = title_snapshot AND char_length(title_snapshot) BETWEEN 1 AND 500),
    CONSTRAINT survey_definition_versions_description CHECK (btrim(description_snapshot) = description_snapshot AND char_length(description_snapshot) <= 10000),
    CONSTRAINT survey_definition_versions_publish CHECK ((is_immutable = FALSE AND published_at IS NULL) OR (is_immutable = TRUE AND published_at IS NOT NULL)),
    UNIQUE(questionnaire_id, version_number),
    UNIQUE(id, questionnaire_id)
);
CREATE INDEX survey_definition_versions_questionnaire_idx ON survey_definition_versions(questionnaire_id, version_number DESC, id DESC);

ALTER TABLE survey_questionnaires
    ADD CONSTRAINT survey_questionnaires_active_version_fk
    FOREIGN KEY (active_definition_version_id, id)
    REFERENCES survey_definition_versions(id, questionnaire_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE survey_definition_questions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    definition_version_id BIGINT NOT NULL REFERENCES survey_definition_versions(id) ON DELETE CASCADE,
    question_type TEXT NOT NULL CHECK (question_type IN ('single_choice','multi_choice','textarea','mobile')),
    title TEXT NOT NULL,
    assessment_dimension_key TEXT NOT NULL DEFAULT '',
    sidebar_profile_field TEXT NOT NULL DEFAULT '',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0 AND sort_order < 200),
    placeholder_text TEXT NOT NULL DEFAULT '',
    validation JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(validation) = 'object'),
    CONSTRAINT survey_definition_questions_title CHECK (btrim(title) = title AND char_length(title) BETWEEN 1 AND 1000),
    CONSTRAINT survey_definition_questions_dimension CHECK (assessment_dimension_key = '' OR assessment_dimension_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_definition_questions_sidebar CHECK (sidebar_profile_field = '' OR sidebar_profile_field ~ '^[A-Za-z0-9._:-]{1,128}$'),
    UNIQUE(definition_version_id, sort_order),
    UNIQUE(id, definition_version_id)
);
CREATE INDEX survey_definition_questions_version_idx ON survey_definition_questions(definition_version_id, sort_order, id);

CREATE TABLE survey_definition_options (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES survey_definition_questions(id) ON DELETE CASCADE,
    definition_version_id BIGINT NOT NULL,
    option_text TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (score BETWEEN -1000000 AND 1000000 AND score <> 'NaN'::float8),
    assessment_type_key TEXT NOT NULL DEFAULT '',
    tag_codes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(tag_codes) = 'array' AND jsonb_array_length(tag_codes) <= 100),
    is_other BOOLEAN NOT NULL DEFAULT FALSE,
    other_placeholder TEXT NOT NULL DEFAULT '',
    other_max_length INTEGER NOT NULL DEFAULT 0 CHECK (other_max_length BETWEEN 0 AND 200),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0 AND sort_order < 100),
    CONSTRAINT survey_definition_options_question_fk FOREIGN KEY (question_id, definition_version_id) REFERENCES survey_definition_questions(id, definition_version_id) ON DELETE CASCADE,
    CONSTRAINT survey_definition_options_text CHECK (btrim(option_text) = option_text AND char_length(option_text) BETWEEN 1 AND 1000),
    CONSTRAINT survey_definition_options_type CHECK (assessment_type_key = '' OR assessment_type_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    CONSTRAINT survey_definition_options_other CHECK ((is_other = FALSE AND other_placeholder = '' AND other_max_length = 0) OR (is_other = TRUE AND btrim(other_placeholder) = other_placeholder AND char_length(other_placeholder) <= 500 AND other_max_length BETWEEN 1 AND 200)),
    UNIQUE(question_id, sort_order),
    UNIQUE(id, definition_version_id)
);
CREATE INDEX survey_definition_options_question_idx ON survey_definition_options(question_id, sort_order, id);

CREATE TABLE survey_score_rules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    definition_version_id BIGINT NOT NULL REFERENCES survey_definition_versions(id) ON DELETE CASCADE,
    minimum_score DOUBLE PRECISION,
    maximum_score DOUBLE PRECISION,
    tag_codes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(tag_codes) = 'array' AND jsonb_array_length(tag_codes) <= 100),
    sort_order INTEGER NOT NULL CHECK (sort_order >= 0 AND sort_order < 100),
    CONSTRAINT survey_score_rules_range CHECK ((minimum_score IS NOT NULL OR maximum_score IS NOT NULL) AND (minimum_score IS NULL OR maximum_score IS NULL OR minimum_score <= maximum_score)),
    UNIQUE(definition_version_id, sort_order)
);
CREATE INDEX survey_score_rules_version_idx ON survey_score_rules(definition_version_id, sort_order, id);

CREATE TABLE survey_operation_receipts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN ('definition_create','definition_update','definition_duplicate','definition_publish','definition_disable','definition_enable','definition_delete')),
    actor_scope TEXT NOT NULL CHECK (actor_scope ~ '^admin:[1-9][0-9]*$'),
    key_digest BYTEA NOT NULL CHECK (octet_length(key_digest) = 32),
    payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest) = 32),
    state TEXT NOT NULL CHECK (state IN ('in_progress','completed')),
    result_snapshot JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT survey_operation_receipts_completion CHECK ((state = 'in_progress' AND result_snapshot IS NULL AND completed_at IS NULL) OR (state = 'completed' AND result_snapshot IS NOT NULL AND completed_at IS NOT NULL)),
    UNIQUE(operation, actor_scope, key_digest)
);
CREATE INDEX survey_operation_receipts_created_idx ON survey_operation_receipts(created_at DESC, id DESC);

CREATE FUNCTION survey_reject_immutable_version_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.is_immutable THEN
        RAISE EXCEPTION 'published survey definition version is immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER survey_definition_versions_immutable
BEFORE UPDATE OR DELETE ON survey_definition_versions
FOR EACH ROW EXECUTE FUNCTION survey_reject_immutable_version_change();

CREATE FUNCTION survey_reject_published_child_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    candidate_version_id BIGINT;
    immutable BOOLEAN;
BEGIN
    candidate_version_id := COALESCE(OLD.definition_version_id, NEW.definition_version_id);
    SELECT is_immutable INTO immutable FROM survey_definition_versions WHERE id = candidate_version_id;
    IF immutable THEN
        RAISE EXCEPTION 'published survey definition children are immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER survey_definition_questions_immutable
BEFORE UPDATE OR DELETE ON survey_definition_questions
FOR EACH ROW EXECUTE FUNCTION survey_reject_published_child_change();
CREATE TRIGGER survey_definition_options_immutable
BEFORE UPDATE OR DELETE ON survey_definition_options
FOR EACH ROW EXECUTE FUNCTION survey_reject_published_child_change();
CREATE TRIGGER survey_score_rules_immutable
BEFORE UPDATE OR DELETE ON survey_score_rules
FOR EACH ROW EXECUTE FUNCTION survey_reject_published_child_change();
