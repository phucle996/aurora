CREATE TABLE IF NOT EXISTS service_categories (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_catalog_state NOT NULL DEFAULT 'active',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS service_definitions (
    id UUID PRIMARY KEY,
    category_id UUID NOT NULL REFERENCES service_categories(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_catalog_state NOT NULL DEFAULT 'active',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_service_definitions_category_code UNIQUE (category_id, code)
);

CREATE TABLE IF NOT EXISTS service_versions (
    id UUID PRIMARY KEY,
    definition_id UUID NOT NULL REFERENCES service_definitions(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9.-]{0,62}$'),
    display_version TEXT NOT NULL CHECK (char_length(display_version) BETWEEN 1 AND 120),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_version_state NOT NULL DEFAULT 'available',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    deprecated_by TEXT NULL CHECK (deprecated_by IS NULL OR char_length(deprecated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_service_versions_definition_code UNIQUE (definition_id, code)
);

CREATE TABLE IF NOT EXISTS service_blueprints (
    id UUID PRIMARY KEY,
    version_id UUID NOT NULL REFERENCES service_versions(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_catalog_state NOT NULL DEFAULT 'active',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- [COMMENT]: V1 exposes one blueprint line per application version. A
    -- future multi-blueprint product change is a catalog contract change.
    CONSTRAINT ux_service_blueprints_version UNIQUE (version_id),
    CONSTRAINT ux_service_blueprints_version_code UNIQUE (version_id, code)
);

CREATE TABLE IF NOT EXISTS catalog_audit_events (
    id UUID PRIMARY KEY,
    actor_subject TEXT NOT NULL CHECK (char_length(actor_subject) BETWEEN 1 AND 128),
    critical_proof_id UUID NULL,
    action TEXT NOT NULL CHECK (char_length(action) BETWEEN 1 AND 96),
    record_kind TEXT NOT NULL CHECK (char_length(record_kind) BETWEEN 1 AND 64),
    record_id UUID NOT NULL,
    record_version BIGINT NOT NULL CHECK (record_version > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'rejected')),
    error_code TEXT NULL CHECK (error_code IS NULL OR char_length(error_code) <= 128),
    before_hash BYTEA NULL CHECK (before_hash IS NULL OR octet_length(before_hash) = 32),
    after_hash BYTEA NULL CHECK (after_hash IS NULL OR octet_length(after_hash) = 32),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- [COMMENT]: The application is not in production yet, but bootstrap remains
-- rerunnable so a P01 development database can be promoted without hidden
-- manual SQL. Legacy scalar name/description stay populated as the English
-- projection; the canonical display contract is the bounded i18n document.
ALTER TABLE service_categories ADD COLUMN IF NOT EXISTS name_i18n JSONB NOT NULL DEFAULT '{"en":"Unnamed"}'::jsonb;
ALTER TABLE service_categories ADD COLUMN IF NOT EXISTS description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE service_categories ADD COLUMN IF NOT EXISTS icon_key TEXT NOT NULL DEFAULT '';
ALTER TABLE service_categories ADD COLUMN IF NOT EXISTS row_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE service_categories ALTER COLUMN state SET DEFAULT 'active';
UPDATE service_categories SET state = 'active' WHERE state::text IN ('draft', 'published');

ALTER TABLE service_definitions ADD COLUMN IF NOT EXISTS name_i18n JSONB NOT NULL DEFAULT '{"en":"Unnamed"}'::jsonb;
ALTER TABLE service_definitions ADD COLUMN IF NOT EXISTS description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE service_definitions ADD COLUMN IF NOT EXISTS icon_key TEXT NOT NULL DEFAULT '';
ALTER TABLE service_definitions ADD COLUMN IF NOT EXISTS row_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE service_definitions ALTER COLUMN state SET DEFAULT 'active';
UPDATE service_definitions SET state = 'active' WHERE state::text IN ('draft', 'published');

ALTER TABLE service_versions ADD COLUMN IF NOT EXISTS name_i18n JSONB NOT NULL DEFAULT '{"en":"Unnamed"}'::jsonb;
ALTER TABLE service_versions ADD COLUMN IF NOT EXISTS description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE service_versions ADD COLUMN IF NOT EXISTS icon_key TEXT NOT NULL DEFAULT '';
ALTER TABLE service_versions ADD COLUMN IF NOT EXISTS row_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE service_blueprints ADD COLUMN IF NOT EXISTS name_i18n JSONB NOT NULL DEFAULT '{"en":"Unnamed"}'::jsonb;
ALTER TABLE service_blueprints ADD COLUMN IF NOT EXISTS description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE service_blueprints ADD COLUMN IF NOT EXISTS icon_key TEXT NOT NULL DEFAULT '';
ALTER TABLE service_blueprints ADD COLUMN IF NOT EXISTS row_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE service_blueprints ALTER COLUMN state SET DEFAULT 'active';
UPDATE service_blueprints SET state = 'active' WHERE state::text IN ('draft', 'published');

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'catalog_audit_events' AND column_name = 'actor_user_id'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'catalog_audit_events' AND column_name = 'actor_subject'
    ) THEN
        ALTER TABLE catalog_audit_events RENAME COLUMN actor_user_id TO actor_subject;
    END IF;
END $$;

ALTER TABLE catalog_audit_events ALTER COLUMN actor_subject TYPE TEXT USING actor_subject::text;
ALTER TABLE catalog_audit_events ADD COLUMN IF NOT EXISTS critical_proof_id UUID NULL;
ALTER TABLE catalog_audit_events ADD COLUMN IF NOT EXISTS record_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE catalog_audit_events ADD COLUMN IF NOT EXISTS outcome TEXT NOT NULL DEFAULT 'succeeded';
ALTER TABLE catalog_audit_events ADD COLUMN IF NOT EXISTS error_code TEXT NULL;

ALTER TABLE service_categories ALTER COLUMN created_by TYPE TEXT USING created_by::text;
ALTER TABLE service_categories ALTER COLUMN updated_by TYPE TEXT USING updated_by::text;
ALTER TABLE service_categories ALTER COLUMN retired_by TYPE TEXT USING retired_by::text;
ALTER TABLE service_definitions ALTER COLUMN created_by TYPE TEXT USING created_by::text;
ALTER TABLE service_definitions ALTER COLUMN updated_by TYPE TEXT USING updated_by::text;
ALTER TABLE service_definitions ALTER COLUMN retired_by TYPE TEXT USING retired_by::text;
ALTER TABLE service_versions ALTER COLUMN created_by TYPE TEXT USING created_by::text;
ALTER TABLE service_versions ALTER COLUMN updated_by TYPE TEXT USING updated_by::text;
ALTER TABLE service_versions ALTER COLUMN deprecated_by TYPE TEXT USING deprecated_by::text;
ALTER TABLE service_versions ALTER COLUMN retired_by TYPE TEXT USING retired_by::text;
ALTER TABLE service_blueprints ALTER COLUMN created_by TYPE TEXT USING created_by::text;
ALTER TABLE service_blueprints ALTER COLUMN updated_by TYPE TEXT USING updated_by::text;
ALTER TABLE service_blueprints ALTER COLUMN retired_by TYPE TEXT USING retired_by::text;

CREATE INDEX IF NOT EXISTS ix_service_definitions_category ON service_definitions(category_id);
CREATE INDEX IF NOT EXISTS ix_service_versions_definition ON service_versions(definition_id);
CREATE INDEX IF NOT EXISTS ix_service_blueprints_version ON service_blueprints(version_id);
CREATE INDEX IF NOT EXISTS ix_catalog_audit_events_record ON catalog_audit_events(record_kind, record_id, occurred_at DESC);
