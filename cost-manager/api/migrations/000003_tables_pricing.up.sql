-- Pricing authority tables. There is one catalog for every charge kind and
-- immutable schedule versions are the only source of rate snapshots.

CREATE TABLE billing.charge_kind_catalog (
    code                  TEXT PRIMARY KEY,
    module_code           TEXT NOT NULL,
    pricing_model         billing.pricing_model NOT NULL,
    raw_input_unit        TEXT NOT NULL,
    observation_semantics TEXT NOT NULL,
    metering_contract     TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'ENABLED',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_charge_kind_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT uq_charge_kind_module_code UNIQUE (module_code, code)
);

CREATE TABLE billing.pricing_schedules (
    id                UUID PRIMARY KEY,
    code              TEXT NOT NULL UNIQUE,
    display_name      TEXT NOT NULL,
    charge_kind_code  TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    pricing_model     billing.pricing_model NOT NULL,
    scope_type        billing.pricing_scope NOT NULL,
    zone_id           UUID,
    currency          CHAR(3) NOT NULL DEFAULT 'USD',
    metadata_version  INT NOT NULL DEFAULT 1,
    status            TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pricing_schedule_id_model UNIQUE (id, pricing_model),
    CONSTRAINT uq_pricing_schedule_kind_scope UNIQUE (id, charge_kind_code, pricing_model),
    CONSTRAINT ck_pricing_schedule_scope CHECK ((scope_type = 'GLOBAL' AND zone_id IS NULL) OR (scope_type = 'ZONE' AND zone_id IS NOT NULL)),
    CONSTRAINT ck_pricing_schedule_currency CHECK (currency = UPPER(currency)),
    CONSTRAINT ck_pricing_schedule_status CHECK (status IN ('ACTIVE', 'DISABLED'))
);

CREATE TABLE billing.pricing_schedule_versions (
    id                    UUID PRIMARY KEY,
    pricing_schedule_id   UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    pricing_model         billing.pricing_model NOT NULL,
    version_number        INT NOT NULL,
    status                TEXT NOT NULL,
    effective_from        TIMESTAMPTZ NOT NULL,
    effective_to          TIMESTAMPTZ,
    definition_schema     TEXT,
    definition_json       JSONB,
    checksum              CHAR(64) NOT NULL,
    change_reason         TEXT NOT NULL,
    created_by            UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pricing_schedule_version_number UNIQUE (pricing_schedule_id, version_number),
    CONSTRAINT uq_pricing_schedule_version_model UNIQUE (id, pricing_schedule_id, pricing_model),
    CONSTRAINT ck_pricing_schedule_version_positive CHECK (version_number > 0),
    CONSTRAINT ck_pricing_schedule_version_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_pricing_schedule_version_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ck_pricing_schedule_definition_shape CHECK (
        (pricing_model = 'PROGRESSIVE_UNIT' AND definition_schema IS NULL AND definition_json IS NULL)
        OR (pricing_model = 'FIXED_BUNDLE' AND definition_schema IS NOT NULL AND definition_json IS NOT NULL)
    ),
    CONSTRAINT fk_pricing_schedule_version_model
        FOREIGN KEY (pricing_schedule_id, pricing_model)
        REFERENCES billing.pricing_schedules(id, pricing_model)
        ON DELETE RESTRICT,
    CONSTRAINT ex_pricing_schedule_version_effective_window EXCLUDE USING gist (
        pricing_schedule_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    )
);

CREATE TABLE billing.pricing_schedule_scalar_brackets (
    id                           UUID PRIMARY KEY,
    pricing_schedule_version_id UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    range_start_quantity        BIGINT NOT NULL,
    range_end_quantity          BIGINT,
    price_numerator_micro_units BIGINT NOT NULL,
    price_denominator_quantity  BIGINT NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_scalar_range_start_non_negative CHECK (range_start_quantity >= 0),
    CONSTRAINT ck_scalar_range_end_after_start CHECK (range_end_quantity IS NULL OR range_end_quantity > range_start_quantity),
    CONSTRAINT ck_scalar_price_numerator_non_negative CHECK (price_numerator_micro_units >= 0),
    CONSTRAINT ck_scalar_price_denominator_positive CHECK (price_denominator_quantity > 0),
    CONSTRAINT uq_scalar_bracket_start UNIQUE (pricing_schedule_version_id, range_start_quantity)
);

CREATE TABLE billing.pricing_outbox (
    id                    UUID PRIMARY KEY,
    event_type            VARCHAR(64) NOT NULL,
    pricing_schedule_id   UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    version_id            UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    module_code           TEXT NOT NULL,
    charge_kind_code      TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    scope_type            billing.pricing_scope NOT NULL,
    zone_id               UUID,
    effective_from        TIMESTAMPTZ NOT NULL,
    checksum              CHAR(64) NOT NULL,
    occurred_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at          TIMESTAMPTZ,
    retry_count           INT NOT NULL DEFAULT 0,
    last_error            TEXT,
    CONSTRAINT ck_schedule_outbox_retry CHECK (retry_count >= 0),
    CONSTRAINT ck_schedule_outbox_scope CHECK ((scope_type = 'GLOBAL' AND zone_id IS NULL) OR (scope_type = 'ZONE' AND zone_id IS NOT NULL))
);

CREATE TABLE billing.usage_settlement_runs (
    id                            UUID PRIMARY KEY,
    source_module                 TEXT NOT NULL,
    source_report_id              UUID NOT NULL,
    charge_kind_code              TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    zone_id                       UUID NOT NULL,
    window_start                  TIMESTAMPTZ NOT NULL,
    window_end                    TIMESTAMPTZ NOT NULL,
    pricing_schedule_id           UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    pricing_schedule_version_id   UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum              CHAR(64) NOT NULL,
    fencing_token                 BIGINT NOT NULL,
    status                        VARCHAR(16) NOT NULL DEFAULT 'RUNNING',
    retry_count                   INT NOT NULL DEFAULT 0,
    checkpoint                    TIMESTAMPTZ,
    started_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at                  TIMESTAMPTZ,
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_usage_settlement_report_kind UNIQUE (source_module, source_report_id, charge_kind_code),
    CONSTRAINT ck_usage_settlement_window CHECK (window_end > window_start),
    CONSTRAINT ck_usage_settlement_status CHECK (status IN ('RUNNING', 'RETRYING', 'COMPLETED', 'FAILED')),
    CONSTRAINT ck_usage_settlement_retry CHECK (retry_count >= 0)
);
