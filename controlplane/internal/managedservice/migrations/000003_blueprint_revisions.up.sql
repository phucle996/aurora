CREATE TABLE IF NOT EXISTS blueprint_revisions (
    id UUID PRIMARY KEY,
    blueprint_id UUID NOT NULL REFERENCES service_blueprints(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    state managed_service_blueprint_revision_state NOT NULL DEFAULT 'draft',
    template_yaml TEXT NOT NULL CHECK (octet_length(template_yaml) BETWEEN 1 AND 1048576),
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    contract_version TEXT NOT NULL DEFAULT 'platform-form/v1' CHECK (char_length(contract_version) BETWEEN 1 AND 64),
    contract_sha256 BYTEA NOT NULL CHECK (octet_length(contract_sha256) = 32),
    component_contract JSONB NOT NULL CHECK (jsonb_typeof(component_contract) = 'array'),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_schema JSONB NOT NULL CHECK (jsonb_typeof(input_schema) = 'object'),
    input_schema_sha256 BYTEA NOT NULL CHECK (octet_length(input_schema_sha256) = 32),
    ui_schema JSONB NOT NULL CHECK (jsonb_typeof(ui_schema) = 'object'),
    ui_schema_sha256 BYTEA NOT NULL CHECK (octet_length(ui_schema_sha256) = 32),
    safe_observed_output_schema JSONB NOT NULL CHECK (jsonb_typeof(safe_observed_output_schema) = 'object'),
    safe_observed_output_schema_sha256 BYTEA NOT NULL CHECK (octet_length(safe_observed_output_schema_sha256) = 32),
    zone_selector JSONB NOT NULL CHECK (jsonb_typeof(zone_selector) = 'object'),
    zone_selector_sha256 BYTEA NOT NULL CHECK (octet_length(zone_selector_sha256) = 32),
    capability_requirement JSONB NOT NULL CHECK (jsonb_typeof(capability_requirement) = 'object'),
    capability_requirement_sha256 BYTEA NOT NULL CHECK (octet_length(capability_requirement_sha256) = 32),
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    validated_row_version BIGINT NULL CHECK (validated_row_version IS NULL OR validated_row_version > 0),
    validation_contract_version TEXT NULL CHECK (validation_contract_version IS NULL OR char_length(validation_contract_version) BETWEEN 1 AND 64),
    validated_bundle_sha256 BYTEA NULL CHECK (validated_bundle_sha256 IS NULL OR octet_length(validated_bundle_sha256) = 32),
    validated_contract_sha256 BYTEA NULL CHECK (validated_contract_sha256 IS NULL OR octet_length(validated_contract_sha256) = 32),
    validated_at TIMESTAMPTZ NULL,
    validated_by TEXT NULL CHECK (validated_by IS NULL OR char_length(validated_by) BETWEEN 1 AND 128),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ NULL,
    retired_at TIMESTAMPTZ NULL,
    published_by TEXT NULL CHECK (published_by IS NULL OR char_length(published_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    CONSTRAINT ux_blueprint_revisions_blueprint_revision UNIQUE (blueprint_id, revision),
    CONSTRAINT ck_blueprint_revisions_publication_time CHECK (
        (state = 'draft' AND published_at IS NULL AND retired_at IS NULL AND published_by IS NULL AND retired_by IS NULL)
        OR (state = 'published' AND published_at IS NOT NULL AND retired_at IS NULL AND published_by IS NOT NULL AND retired_by IS NULL)
        OR (state = 'retired' AND published_at IS NOT NULL AND retired_at IS NOT NULL AND retired_at >= published_at AND published_by IS NOT NULL AND retired_by IS NOT NULL)
    ),
    CONSTRAINT ck_blueprint_revisions_validation_receipt CHECK (
        (validated_row_version IS NULL AND validation_contract_version IS NULL AND validated_bundle_sha256 IS NULL
            AND validated_contract_sha256 IS NULL AND validated_at IS NULL AND validated_by IS NULL)
        OR (validated_row_version = row_version AND validation_contract_version IS NOT NULL
            AND validated_bundle_sha256 IS NOT NULL AND validated_contract_sha256 IS NOT NULL
            AND validated_at IS NOT NULL AND validated_by IS NOT NULL)
    )
);

-- [COMMENT]: Keep a rerunnable path for P01 development databases. Published
-- revision bytes remain immutable; these columns only make validation evidence
-- explicit before the first P02 route is enabled.
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS contract_version TEXT NOT NULL DEFAULT 'platform-form/v1';
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS contract_sha256 BYTEA NULL;
UPDATE blueprint_revisions SET contract_sha256 = template_bundle_sha256 WHERE contract_sha256 IS NULL;
ALTER TABLE blueprint_revisions ALTER COLUMN contract_sha256 SET NOT NULL;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS row_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS validated_row_version BIGINT NULL;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS validation_contract_version TEXT NULL;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS validated_bundle_sha256 BYTEA NULL;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS validated_contract_sha256 BYTEA NULL;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS validated_at TIMESTAMPTZ NULL;
ALTER TABLE blueprint_revisions ADD COLUMN IF NOT EXISTS validated_by TEXT NULL;
ALTER TABLE blueprint_revisions ALTER COLUMN created_by TYPE TEXT USING created_by::text;
ALTER TABLE blueprint_revisions ALTER COLUMN published_by TYPE TEXT USING published_by::text;
ALTER TABLE blueprint_revisions ALTER COLUMN retired_by TYPE TEXT USING retired_by::text;

ALTER TABLE service_blueprints
    ADD COLUMN IF NOT EXISTS published_revision_id UUID NULL;

DO $$
BEGIN
    ALTER TABLE service_blueprints
        ADD CONSTRAINT fk_service_blueprints_published_revision
        FOREIGN KEY (published_revision_id) REFERENCES blueprint_revisions(id) ON DELETE RESTRICT;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE OR REPLACE FUNCTION reject_blueprint_revision_rewrite()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.blueprint_id IS DISTINCT FROM OLD.blueprint_id
       OR NEW.revision IS DISTINCT FROM OLD.revision
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'blueprint revision identity is immutable';
    END IF;

    -- [COMMENT]: Draft content and validation receipt are mutable. Once
    -- published, exact render bytes, contracts and their validation evidence
    -- must remain replayable forever.
    IF OLD.state <> 'draft' AND (
        NEW.template_yaml IS DISTINCT FROM OLD.template_yaml
        OR NEW.template_bundle_sha256 IS DISTINCT FROM OLD.template_bundle_sha256
        OR NEW.contract_version IS DISTINCT FROM OLD.contract_version
        OR NEW.contract_sha256 IS DISTINCT FROM OLD.contract_sha256
        OR NEW.component_contract IS DISTINCT FROM OLD.component_contract
        OR NEW.component_contract_sha256 IS DISTINCT FROM OLD.component_contract_sha256
        OR NEW.input_schema IS DISTINCT FROM OLD.input_schema
        OR NEW.input_schema_sha256 IS DISTINCT FROM OLD.input_schema_sha256
        OR NEW.ui_schema IS DISTINCT FROM OLD.ui_schema
        OR NEW.ui_schema_sha256 IS DISTINCT FROM OLD.ui_schema_sha256
        OR NEW.safe_observed_output_schema IS DISTINCT FROM OLD.safe_observed_output_schema
        OR NEW.safe_observed_output_schema_sha256 IS DISTINCT FROM OLD.safe_observed_output_schema_sha256
        OR NEW.zone_selector IS DISTINCT FROM OLD.zone_selector
        OR NEW.zone_selector_sha256 IS DISTINCT FROM OLD.zone_selector_sha256
        OR NEW.capability_requirement IS DISTINCT FROM OLD.capability_requirement
        OR NEW.capability_requirement_sha256 IS DISTINCT FROM OLD.capability_requirement_sha256
        OR NEW.row_version IS DISTINCT FROM OLD.row_version
        OR NEW.validated_row_version IS DISTINCT FROM OLD.validated_row_version
        OR NEW.validation_contract_version IS DISTINCT FROM OLD.validation_contract_version
        OR NEW.validated_bundle_sha256 IS DISTINCT FROM OLD.validated_bundle_sha256
        OR NEW.validated_contract_sha256 IS DISTINCT FROM OLD.validated_contract_sha256
        OR NEW.validated_at IS DISTINCT FROM OLD.validated_at
        OR NEW.validated_by IS DISTINCT FROM OLD.validated_by
    ) THEN
        RAISE EXCEPTION 'blueprint revision content is immutable';
    END IF;

    IF OLD.state = 'draft' THEN
        IF NEW.state NOT IN ('draft', 'published') OR NEW.retired_at IS NOT NULL OR NEW.retired_by IS NOT NULL THEN
            RAISE EXCEPTION 'draft blueprint revision may only remain draft or publish';
        END IF;
        IF NEW.state = 'draft' AND (NEW.published_at IS NOT NULL OR NEW.published_by IS NOT NULL) THEN
            RAISE EXCEPTION 'draft blueprint revision cannot have publish metadata';
        END IF;
        IF NEW.state = 'published' AND (
            NEW.published_at IS NULL
            OR NEW.published_by IS NULL
            OR NEW.validated_row_version IS DISTINCT FROM NEW.row_version
            OR NEW.validated_bundle_sha256 IS DISTINCT FROM NEW.template_bundle_sha256
            OR NEW.validated_contract_sha256 IS DISTINCT FROM NEW.contract_sha256
        ) THEN
            RAISE EXCEPTION 'published blueprint revision requires current validation evidence';
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.state = 'published'
       AND NEW.state = 'retired'
       AND NEW.published_at IS NOT DISTINCT FROM OLD.published_at
       AND NEW.published_by IS NOT DISTINCT FROM OLD.published_by
       AND NEW.retired_at IS NOT NULL
       AND NEW.retired_by IS NOT NULL THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'published or retired blueprint revision transition is immutable';
END;
$$;

DROP TRIGGER IF EXISTS trg_blueprint_revisions_immutable ON blueprint_revisions;
CREATE TRIGGER trg_blueprint_revisions_immutable
BEFORE UPDATE ON blueprint_revisions
FOR EACH ROW EXECUTE FUNCTION reject_blueprint_revision_rewrite();

CREATE OR REPLACE FUNCTION validate_service_blueprint_published_revision()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.published_revision_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM blueprint_revisions revision
        WHERE revision.id = NEW.published_revision_id
          AND revision.blueprint_id = NEW.id
          AND revision.state = 'published'
    ) THEN
        RAISE EXCEPTION 'published revision must belong to blueprint and be published';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_service_blueprints_published_revision ON service_blueprints;
CREATE CONSTRAINT TRIGGER trg_service_blueprints_published_revision
AFTER INSERT OR UPDATE OF published_revision_id ON service_blueprints
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_service_blueprint_published_revision();

CREATE OR REPLACE FUNCTION validate_blueprint_revision_default_pointer()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state <> 'published' AND EXISTS (
        SELECT 1 FROM service_blueprints blueprint
        WHERE blueprint.published_revision_id = NEW.id
    ) THEN
        RAISE EXCEPTION 'default blueprint revision must remain published';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_blueprint_revisions_default_pointer ON blueprint_revisions;
CREATE CONSTRAINT TRIGGER trg_blueprint_revisions_default_pointer
AFTER UPDATE OF state ON blueprint_revisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_blueprint_revision_default_pointer();

CREATE INDEX IF NOT EXISTS ix_blueprint_revisions_blueprint_state
    ON blueprint_revisions(blueprint_id, state, revision DESC);
