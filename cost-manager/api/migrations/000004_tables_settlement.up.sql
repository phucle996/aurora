-- Settlement evidence and immutable financial projections.

CREATE TABLE billing.storage_usage_report_inbox (
    report_id                 UUID PRIMARY KEY,
    zone_id                   UUID NOT NULL,
    window_start              TIMESTAMPTZ NOT NULL,
    window_end                TIMESTAMPTZ NOT NULL,
    sequence                  BIGINT NOT NULL,
    correction                BOOLEAN NOT NULL DEFAULT FALSE,
    correction_of_report_id   UUID,
    payload_sha256            BYTEA NOT NULL,
    payload                   BYTEA NOT NULL,
    status                    VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    retry_count               INT NOT NULL DEFAULT 0,
    last_error                TEXT,
    received_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at                TIMESTAMPTZ,
    CONSTRAINT ck_storage_report_window CHECK (window_end > window_start),
    CONSTRAINT ck_storage_report_checksum CHECK (octet_length(payload_sha256) = 32),
    CONSTRAINT ck_storage_report_payload_size CHECK (octet_length(payload) <= 524288),
    CONSTRAINT ck_storage_report_status CHECK (status IN ('RECEIVED', 'PROCESSING', 'SETTLED', 'UNRATED', 'DEAD')),
    CONSTRAINT ck_storage_report_retry_non_negative CHECK (retry_count >= 0),
    CONSTRAINT ck_storage_report_correction_parent CHECK (
        (correction = FALSE AND correction_of_report_id IS NULL)
        OR (correction = TRUE AND correction_of_report_id IS NOT NULL AND correction_of_report_id <> report_id)
    )
);

CREATE TABLE billing.storage_usage_line_inbox (
    line_id                       UUID PRIMARY KEY,
    report_id                     UUID NOT NULL REFERENCES billing.storage_usage_report_inbox(report_id) ON DELETE RESTRICT,
    zone_id                       UUID NOT NULL,
    resource_id                   UUID NOT NULL,
    resource_name                 VARCHAR(255),
    direction                     VARCHAR(16) NOT NULL,
    usage_quantity                BIGINT NOT NULL,
    usage_unit                    VARCHAR(24) NOT NULL,
    request_count                 BIGINT NOT NULL DEFAULT 0,
    amount_micro_units            BIGINT,
    owner_id                      UUID,
    owner_type                    billing.owner_type,
    module_code                   TEXT NOT NULL DEFAULT 'storage',
    charge_kind_code              TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    usage_settlement_run_id       UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    pricing_schedule_version_id   UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum              CHAR(64) NOT NULL,
    status                        VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    reason                        VARCHAR(64),
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at                    TIMESTAMPTZ,
    CONSTRAINT uq_storage_usage_line_identity UNIQUE (report_id, resource_id, direction),
    CONSTRAINT ck_storage_usage_line_direction CHECK (direction IN ('NETWORK_IN', 'NETWORK_OUT', 'STORAGE')),
    CONSTRAINT ck_storage_usage_line_unit CHECK (usage_unit IN ('BYTE', 'BYTE_HOUR')),
    CONSTRAINT ck_storage_usage_line_direction_unit CHECK (
        (direction IN ('NETWORK_IN', 'NETWORK_OUT') AND usage_unit = 'BYTE')
        OR (direction = 'STORAGE' AND usage_unit = 'BYTE_HOUR')
    ),
    CONSTRAINT ck_storage_usage_line_resource_reference CHECK (resource_id IS NOT NULL OR resource_name IS NOT NULL),
    CONSTRAINT ck_storage_usage_line_quantity CHECK (usage_quantity >= 0),
    CONSTRAINT ck_storage_usage_line_requests CHECK (request_count >= 0),
    CONSTRAINT ck_storage_usage_line_status CHECK (status IN ('PENDING', 'SETTLED', 'UNRATED', 'DEAD'))
);

CREATE TABLE billing.hypervisor_allocation_event_inbox (
    event_id            UUID PRIMARY KEY,
    event_type          VARCHAR(16) NOT NULL,
    payload_hash        CHAR(64) NOT NULL,
    resource_id         UUID NOT NULL,
    source_version      BIGINT NOT NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    error_message       VARCHAR(512),
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at        TIMESTAMPTZ,
    CONSTRAINT uq_hypervisor_allocation_event_resource_version UNIQUE (resource_id, source_version),
    CONSTRAINT ck_hypervisor_allocation_event_type CHECK (event_type IN ('ACTIVATE', 'REVISE', 'TERMINATE')),
    CONSTRAINT ck_hypervisor_allocation_event_version CHECK (source_version > 0),
    CONSTRAINT ck_hypervisor_allocation_event_status CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

CREATE TABLE billing.hypervisor_allocation_heads (
    resource_id         UUID PRIMARY KEY,
    zone_id             UUID NOT NULL,
    last_source_version BIGINT NOT NULL,
    state               VARCHAR(16) NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_hypervisor_allocation_head_version CHECK (last_source_version > 0),
    CONSTRAINT ck_hypervisor_allocation_head_state CHECK (state IN ('ACTIVE', 'TERMINATED'))
);

CREATE TABLE billing.hypervisor_allocation_intervals (
    id                  UUID PRIMARY KEY,
    resource_id         UUID NOT NULL,
    zone_id             UUID NOT NULL,
    allocation_version  BIGINT NOT NULL,
    effective_from      TIMESTAMPTZ NOT NULL,
    effective_to        TIMESTAMPTZ,
    cpu_cores           BIGINT NOT NULL,
    memory_mib          BIGINT NOT NULL,
    disk_gib            BIGINT NOT NULL,
    gpu_sku             VARCHAR(64),
    gpu_count           BIGINT NOT NULL DEFAULT 0,
    source_event_id     UUID NOT NULL REFERENCES billing.hypervisor_allocation_event_inbox(event_id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_hypervisor_allocation_interval_version UNIQUE (resource_id, allocation_version),
    CONSTRAINT ck_hypervisor_allocation_interval_version CHECK (allocation_version > 0),
    CONSTRAINT ck_hypervisor_allocation_interval_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ck_hypervisor_allocation_cpu CHECK (cpu_cores BETWEEN 1 AND 1024),
    CONSTRAINT ck_hypervisor_allocation_memory CHECK (memory_mib BETWEEN 1 AND 4194304),
    CONSTRAINT ck_hypervisor_allocation_disk CHECK (disk_gib BETWEEN 1 AND 1048576),
    CONSTRAINT ck_hypervisor_allocation_gpu CHECK (
        (gpu_count = 0 AND gpu_sku IS NULL)
        OR (gpu_count BETWEEN 1 AND 64 AND gpu_sku IS NOT NULL AND length(btrim(gpu_sku)) BETWEEN 1 AND 64)
    ),
    CONSTRAINT ex_hypervisor_allocation_interval_window EXCLUDE USING gist (
        resource_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    )
);

CREATE TABLE billing.hypervisor_allocation_windows (
    id                  UUID PRIMARY KEY,
    zone_id             UUID NOT NULL,
    shard_id            INT NOT NULL,
    window_start        TIMESTAMPTZ NOT NULL,
    window_end          TIMESTAMPTZ NOT NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    retry_count         INT NOT NULL DEFAULT 0,
    last_error          VARCHAR(512),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at          TIMESTAMPTZ,
    CONSTRAINT uq_hypervisor_allocation_window UNIQUE (zone_id, shard_id, window_start, window_end),
    CONSTRAINT ck_hypervisor_allocation_window_shard CHECK (shard_id >= 0),
    CONSTRAINT ck_hypervisor_allocation_window_time CHECK (window_end = window_start + INTERVAL '1 hour'),
    CONSTRAINT ck_hypervisor_allocation_window_status CHECK (status IN ('PENDING', 'PROCESSING', 'SETTLED', 'UNRATED', 'DEAD')),
    CONSTRAINT ck_hypervisor_allocation_window_retry CHECK (retry_count >= 0)
);

CREATE TABLE billing.hypervisor_allocation_lines (
    id                              UUID PRIMARY KEY,
    window_id                       UUID NOT NULL REFERENCES billing.hypervisor_allocation_windows(id) ON DELETE RESTRICT,
    resource_id                     UUID NOT NULL,
    zone_id                         UUID NOT NULL,
    allocation_version              BIGINT NOT NULL,
    component                       VARCHAR(16) NOT NULL,
    usage_quantity                  BIGINT NOT NULL,
    usage_unit                      VARCHAR(24) NOT NULL,
    amount_micro_units              BIGINT,
    owner_id                        UUID,
    owner_type                      billing.owner_type,
    charge_kind_code                TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    usage_settlement_run_id         UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    pricing_schedule_version_id     UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum                CHAR(64),
    source_evidence_hash            CHAR(64) NOT NULL,
    status                          VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    reason                          VARCHAR(64),
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at                      TIMESTAMPTZ,
    CONSTRAINT uq_hypervisor_allocation_line UNIQUE (window_id, resource_id, allocation_version, component),
    CONSTRAINT ck_hypervisor_allocation_line_component CHECK (component IN ('VCPU', 'MEMORY', 'DISK', 'GPU')),
    CONSTRAINT ck_hypervisor_allocation_line_unit CHECK (usage_unit IN ('CORE_SECOND', 'MIB_SECOND', 'GIB_SECOND', 'GPU_SECOND')),
    CONSTRAINT ck_hypervisor_allocation_line_component_unit CHECK (
        (component = 'VCPU' AND usage_unit = 'CORE_SECOND')
        OR (component = 'MEMORY' AND usage_unit = 'MIB_SECOND')
        OR (component = 'DISK' AND usage_unit = 'GIB_SECOND')
        OR (component = 'GPU' AND usage_unit = 'GPU_SECOND')
    ),
    CONSTRAINT ck_hypervisor_allocation_line_quantity CHECK (usage_quantity > 0),
    CONSTRAINT ck_hypervisor_allocation_line_status CHECK (status IN ('PENDING', 'SETTLED', 'UNRATED', 'DEAD'))
);

CREATE TABLE billing.hypervisor_network_usage_report_inbox (
    report_id           UUID PRIMARY KEY,
    zone_id             UUID NOT NULL,
    resource_id         UUID NOT NULL,
    window_start        TIMESTAMPTZ NOT NULL,
    window_end          TIMESTAMPTZ NOT NULL,
    sequence            BIGINT NOT NULL,
    payload_sha256      BYTEA NOT NULL,
    payload             BYTEA NOT NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'PROCESSING',
    retry_count         INT NOT NULL DEFAULT 0,
    last_error          VARCHAR(512),
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at          TIMESTAMPTZ,
    CONSTRAINT uq_hypervisor_network_report_window UNIQUE (zone_id, resource_id, window_start, window_end),
    CONSTRAINT ck_hypervisor_network_report_window CHECK (window_end = window_start + INTERVAL '1 hour'),
    CONSTRAINT ck_hypervisor_network_report_sequence CHECK (sequence > 0),
    CONSTRAINT ck_hypervisor_network_report_checksum CHECK (octet_length(payload_sha256) = 32),
    CONSTRAINT ck_hypervisor_network_report_status CHECK (status IN ('PROCESSING', 'SETTLED', 'UNRATED', 'DEAD')),
    CONSTRAINT ck_hypervisor_network_report_retry CHECK (retry_count >= 0)
);

CREATE TABLE billing.hypervisor_network_usage_lines (
    id                              UUID PRIMARY KEY,
    report_id                       UUID NOT NULL REFERENCES billing.hypervisor_network_usage_report_inbox(report_id) ON DELETE RESTRICT,
    resource_id                     UUID NOT NULL,
    zone_id                         UUID NOT NULL,
    direction                       VARCHAR(16) NOT NULL,
    usage_quantity                  BIGINT NOT NULL,
    usage_unit                      VARCHAR(16) NOT NULL,
    amount_micro_units              BIGINT,
    owner_id                        UUID,
    owner_type                      billing.owner_type,
    charge_kind_code                TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    usage_settlement_run_id         UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    pricing_schedule_version_id     UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum                CHAR(64),
    source_evidence_hash            CHAR(64) NOT NULL,
    status                          VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    reason                          VARCHAR(64),
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at                      TIMESTAMPTZ,
    CONSTRAINT uq_hypervisor_network_line UNIQUE (report_id, direction),
    CONSTRAINT ck_hypervisor_network_line_direction CHECK (direction IN ('NETWORK_IN', 'NETWORK_OUT')),
    CONSTRAINT ck_hypervisor_network_line_unit CHECK (usage_unit = 'BYTE'),
    CONSTRAINT ck_hypervisor_network_line_quantity CHECK (usage_quantity > 0),
    CONSTRAINT ck_hypervisor_network_line_status CHECK (status IN ('PENDING', 'SETTLED', 'UNRATED', 'DEAD'))
);

CREATE TABLE billing.mail_accepted_usage_inbox (
    evidence_id                    UUID PRIMARY KEY,
    zone_id                        UUID NOT NULL,
    resource_id                    UUID NOT NULL,
    accepted_at                    TIMESTAMPTZ NOT NULL,
    recipient_quantity             BIGINT NOT NULL,
    payload_sha256                 BYTEA NOT NULL,
    payload                        BYTEA NOT NULL,
    amount_micro_units             BIGINT,
    owner_id                       UUID,
    owner_type                     billing.owner_type,
    charge_kind_code               TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    usage_settlement_run_id        UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    pricing_schedule_version_id    UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum               CHAR(64),
    status                         VARCHAR(16) NOT NULL DEFAULT 'PROCESSING',
    reason                         VARCHAR(64),
    retry_count                    INT NOT NULL DEFAULT 0,
    received_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at                     TIMESTAMPTZ,
    CONSTRAINT ck_mail_accepted_quantity CHECK (recipient_quantity = 1),
    CONSTRAINT ck_mail_accepted_checksum CHECK (octet_length(payload_sha256) = 32),
    CONSTRAINT ck_mail_accepted_payload_size CHECK (octet_length(payload) <= 16384),
    CONSTRAINT ck_mail_accepted_status CHECK (status IN ('PROCESSING', 'SETTLED', 'UNRATED', 'DEAD')),
    CONSTRAINT ck_mail_accepted_retry CHECK (retry_count >= 0)
);

CREATE TABLE billing.wallet_ledger_entries (
    id                            UUID PRIMARY KEY,
    wallet_id                     UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                      UUID NOT NULL,
    owner_type                    billing.owner_type NOT NULL,
    actor_user_id                 UUID,
    amount_micro_units            BIGINT NOT NULL,
    cash_balance_after            BIGINT NOT NULL,
    promotional_balance_after     BIGINT NOT NULL,
    currency                      CHAR(3) NOT NULL,
    entry_type                    billing.ledger_entry_type NOT NULL,
    module_code                   TEXT,
    charge_kind_code              TEXT REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    reference_id                  VARCHAR(255) NOT NULL,
    description                   TEXT NOT NULL,
    usage_settlement_run_id       UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    pricing_schedule_id           UUID REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    pricing_schedule_version_id   UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum              CHAR(64),
    adjustment_of_ledger_entry_id UUID REFERENCES billing.wallet_ledger_entries(id) ON DELETE RESTRICT,
    adjustment_reason             VARCHAR(64),
    resource_id                   UUID,
    resource_type                 VARCHAR(32),
    usage_quantity                BIGINT,
    usage_unit                    VARCHAR(24),
    occurred_at                   TIMESTAMPTZ NOT NULL,
    source_evidence_hash          CHAR(64),
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_ledger_amount_non_zero CHECK (amount_micro_units <> 0),
    CONSTRAINT ck_ledger_promo_after_non_negative CHECK (promotional_balance_after >= 0),
    CONSTRAINT ck_ledger_usage_pair CHECK ((usage_quantity IS NULL) = (usage_unit IS NULL))
);

CREATE TABLE billing.unrated_usage (
    id                            UUID PRIMARY KEY,
    module_code                   TEXT NOT NULL,
    charge_kind_code              TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    resource_type                 VARCHAR(32) NOT NULL,
    resource_id                   UUID,
    resource_name                 VARCHAR(255) NOT NULL,
    metering_hour                 TIMESTAMPTZ NOT NULL,
    usage_quantity                BIGINT NOT NULL,
    usage_unit                    VARCHAR(24) NOT NULL,
    reason                        VARCHAR(64) NOT NULL,
    source_report_id              UUID,
    source_evidence_hash          CHAR(64),
    pricing_schedule_version_id   UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    status                        VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    retry_count                   INT NOT NULL DEFAULT 0,
    last_error                    TEXT,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_unrated_usage_non_negative CHECK (usage_quantity >= 0),
    CONSTRAINT ck_unrated_retry_non_negative CHECK (retry_count >= 0),
    CONSTRAINT ck_unrated_status CHECK (status IN ('PENDING', 'PROCESSING', 'RESOLVED', 'DEAD'))
);
