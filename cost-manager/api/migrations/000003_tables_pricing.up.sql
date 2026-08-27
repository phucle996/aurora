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
    currency          CHAR(3) NOT NULL DEFAULT 'USD',
    metadata_version  INT NOT NULL DEFAULT 1,
    status            TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pricing_schedule_id_model UNIQUE (id, pricing_model),
    CONSTRAINT uq_pricing_schedule_kind_model UNIQUE (id, charge_kind_code, pricing_model),
    CONSTRAINT uq_pricing_schedule_charge_kind UNIQUE (charge_kind_code),
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
    effective_from        TIMESTAMPTZ NOT NULL,
    checksum              CHAR(64) NOT NULL,
    occurred_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at          TIMESTAMPTZ,
    claim_token           UUID,
    lease_until           TIMESTAMPTZ,
    available_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count           INT NOT NULL DEFAULT 0,
    last_error            TEXT,
    CONSTRAINT ck_schedule_outbox_retry CHECK (retry_count >= 0)
);

-- Storage owns this module adjustment workflow. PAYG base schedules remain
-- Global-only and no other module may infer pricing scope from these rows.
CREATE TABLE billing.storage_zone_price_adjustment_versions (
    id                       UUID PRIMARY KEY,
    zone_id                  UUID NOT NULL,
    version_number           INT NOT NULL,
    status                   VARCHAR(16) NOT NULL,
    effective_from           TIMESTAMPTZ NOT NULL,
    effective_to             TIMESTAMPTZ,
    multiplier_numerator     BIGINT NOT NULL,
    multiplier_denominator   BIGINT NOT NULL,
    checksum                 CHAR(64) NOT NULL,
    change_reason            TEXT NOT NULL,
    created_by               UUID NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_storage_zone_adjustment_version UNIQUE (zone_id, version_number),
    CONSTRAINT ck_storage_zone_adjustment_version_positive CHECK (version_number > 0),
    CONSTRAINT ck_storage_zone_adjustment_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_storage_zone_adjustment_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ck_storage_zone_adjustment_numerator CHECK (multiplier_numerator >= 0),
    CONSTRAINT ck_storage_zone_adjustment_denominator CHECK (multiplier_denominator > 0),
    CONSTRAINT ex_storage_zone_adjustment_window EXCLUDE USING gist (
        zone_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    )
);

-- Hypervisor owns its Zone multiplier independently from Storage. The generic
-- PAYG kernel receives only the immutable rational snapshot and lineage.
CREATE TABLE billing.hypervisor_zone_price_adjustment_versions (
    id                       UUID PRIMARY KEY,
    zone_id                  UUID NOT NULL,
    version_number           INT NOT NULL,
    status                   VARCHAR(16) NOT NULL,
    effective_from           TIMESTAMPTZ NOT NULL,
    effective_to             TIMESTAMPTZ,
    multiplier_numerator     BIGINT NOT NULL,
    multiplier_denominator   BIGINT NOT NULL,
    checksum                 CHAR(64) NOT NULL,
    change_reason            TEXT NOT NULL,
    created_by               UUID NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_hypervisor_zone_adjustment_version UNIQUE (zone_id, version_number),
    CONSTRAINT ck_hypervisor_zone_adjustment_version_positive CHECK (version_number > 0),
    CONSTRAINT ck_hypervisor_zone_adjustment_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_hypervisor_zone_adjustment_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ck_hypervisor_zone_adjustment_numerator CHECK (multiplier_numerator >= 0),
    CONSTRAINT ck_hypervisor_zone_adjustment_denominator CHECK (multiplier_denominator > 0),
    CONSTRAINT ex_hypervisor_zone_adjustment_window EXCLUDE USING gist (
        zone_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    )
);

-- Hypervisor resource plans are Cost-owned commercial catalog entries. A plan
-- is the business identity; every revision is immutable and pins the limits
-- selected by a VM. Zone multipliers intentionally remain outside this table.
CREATE TABLE billing.hypervisor_resource_plans (
    id              UUID PRIMARY KEY,
    code            VARCHAR(128) NOT NULL UNIQUE,
    display_name    VARCHAR(256) NOT NULL,
    description     TEXT NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_hypervisor_resource_plan_code CHECK (code ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    CONSTRAINT ck_hypervisor_resource_plan_display_name CHECK (length(btrim(display_name)) BETWEEN 1 AND 256),
    CONSTRAINT ck_hypervisor_resource_plan_status CHECK (status IN ('ACTIVE', 'RETIRED'))
);

CREATE TABLE billing.hypervisor_resource_plan_revisions (
    id                  UUID PRIMARY KEY,
    plan_id             UUID NOT NULL REFERENCES billing.hypervisor_resource_plans(id) ON DELETE RESTRICT,
    revision_number     BIGINT NOT NULL,
    status              VARCHAR(16) NOT NULL,
    billing_model       VARCHAR(32) NOT NULL,
    cpu_cores           BIGINT NOT NULL,
    memory_mib          BIGINT NOT NULL,
    boot_disk_gib       BIGINT NOT NULL,
    content_sha256      CHAR(64) NOT NULL,
    effective_from      TIMESTAMPTZ NOT NULL,
    effective_to        TIMESTAMPTZ,
    change_reason       TEXT NOT NULL,
    created_by          UUID NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_hypervisor_resource_plan_revision UNIQUE (plan_id, revision_number),
    CONSTRAINT ck_hypervisor_resource_plan_revision_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_hypervisor_resource_plan_billing_model CHECK (billing_model = 'LIMIT_HOURLY'),
    CONSTRAINT ck_hypervisor_resource_plan_cpu CHECK (cpu_cores BETWEEN 1 AND 1024),
    CONSTRAINT ck_hypervisor_resource_plan_memory CHECK (memory_mib BETWEEN 1 AND 4194304),
    CONSTRAINT ck_hypervisor_resource_plan_boot_disk CHECK (boot_disk_gib BETWEEN 1 AND 65536),
    CONSTRAINT ck_hypervisor_resource_plan_hash CHECK (content_sha256 ~ '^[a-f0-9]{64}$'),
    CONSTRAINT ck_hypervisor_resource_plan_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ex_hypervisor_resource_plan_effective_window EXCLUDE USING gist (
        plan_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    )
);

CREATE TABLE billing.hypervisor_resource_plan_outbox (
    id              UUID PRIMARY KEY,
    event_id        UUID NOT NULL UNIQUE,
    plan_id         UUID NOT NULL REFERENCES billing.hypervisor_resource_plans(id) ON DELETE RESTRICT,
    revision_id     UUID NOT NULL REFERENCES billing.hypervisor_resource_plan_revisions(id) ON DELETE RESTRICT,
    payload         BYTEA NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    claim_token     UUID,
    lease_until     TIMESTAMPTZ,
    available_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    CONSTRAINT ck_hypervisor_resource_plan_outbox_payload CHECK (octet_length(payload) BETWEEN 1 AND 65536),
    CONSTRAINT ck_hypervisor_resource_plan_outbox_retry CHECK (retry_count >= 0)
);

-- Mail owns its Zone multiplier. Accepted-recipient evidence remains a flat
-- usage workflow and the generic PAYG kernel receives only this rational lineage.
CREATE TABLE billing.mail_zone_price_adjustment_versions (
    id                       UUID PRIMARY KEY,
    zone_id                  UUID NOT NULL,
    version_number           INT NOT NULL,
    status                   VARCHAR(16) NOT NULL,
    effective_from           TIMESTAMPTZ NOT NULL,
    effective_to             TIMESTAMPTZ,
    multiplier_numerator     BIGINT NOT NULL,
    multiplier_denominator   BIGINT NOT NULL,
    checksum                 CHAR(64) NOT NULL,
    change_reason            TEXT NOT NULL,
    created_by               UUID NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_mail_zone_adjustment_version UNIQUE (zone_id, version_number),
    CONSTRAINT ck_mail_zone_adjustment_version_positive CHECK (version_number > 0),
    CONSTRAINT ck_mail_zone_adjustment_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_mail_zone_adjustment_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ck_mail_zone_adjustment_numerator CHECK (multiplier_numerator >= 0),
    CONSTRAINT ck_mail_zone_adjustment_denominator CHECK (multiplier_denominator > 0),
    CONSTRAINT ex_mail_zone_adjustment_window EXCLUDE USING gist (
        zone_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    )
);

CREATE TABLE billing.usage_settlement_runs (
    id                            UUID PRIMARY KEY,
    source_module                 TEXT NOT NULL,
    source_report_id              UUID NOT NULL,
    charge_kind_code              TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    window_start                  TIMESTAMPTZ NOT NULL,
    window_end                    TIMESTAMPTZ NOT NULL,
    pricing_schedule_id           UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    pricing_schedule_version_id   UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum              CHAR(64) NOT NULL,
    -- Opaque immutable lineage supplied by the module adapter. The kernel
    -- never joins a module-owned adjustment table.
    rate_adjustment_id            UUID,
    rate_adjustment_version       INT,
    rate_adjustment_checksum      CHAR(64),
    rate_adjustment_numerator     BIGINT,
    rate_adjustment_denominator   BIGINT,
    fencing_token                 BIGINT NOT NULL,
    status                        VARCHAR(16) NOT NULL DEFAULT 'RUNNING',
    retry_count                   INT NOT NULL DEFAULT 0,
    checkpoint                    TIMESTAMPTZ,
    started_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at                  TIMESTAMPTZ,
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_usage_settlement_report_kind UNIQUE (source_module, source_report_id, charge_kind_code),
    CONSTRAINT fk_usage_settlement_module_kind FOREIGN KEY (source_module, charge_kind_code)
        REFERENCES billing.charge_kind_catalog(module_code, code) ON DELETE RESTRICT,
    CONSTRAINT ck_usage_settlement_window CHECK (window_end > window_start),
    CONSTRAINT ck_usage_settlement_status CHECK (status IN ('RUNNING', 'RETRYING', 'COMPLETED', 'FAILED')),
    CONSTRAINT ck_usage_settlement_retry CHECK (retry_count >= 0),
    CONSTRAINT ck_usage_settlement_fence CHECK (fencing_token > 0),
    CONSTRAINT ck_usage_settlement_adjustment_shape CHECK (
        (rate_adjustment_id IS NULL AND rate_adjustment_version IS NULL AND rate_adjustment_checksum IS NULL
         AND rate_adjustment_numerator IS NULL AND rate_adjustment_denominator IS NULL)
        OR
        (rate_adjustment_id IS NOT NULL AND rate_adjustment_version > 0 AND rate_adjustment_checksum IS NOT NULL
         AND rate_adjustment_numerator >= 0 AND rate_adjustment_denominator > 0)
    )
);
