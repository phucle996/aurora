-- Compatibility bridge for the historical 000003 index migration.
--
-- 000003 already contains storage indexes in released databases, while the
-- original storage tables were introduced by 000007. A clean database would
-- otherwise try to create those indexes before the tables exist. Keep this
-- idempotent pre-index shape so old checksums remain immutable; 000008 upgrades
-- the tables to the current metering contract.
CREATE TABLE IF NOT EXISTS billing.storage_usage_report_inbox (
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
    CONSTRAINT ck_storage_report_payload_size CHECK (octet_length(payload) <= 4194304),
    CONSTRAINT ck_storage_report_status CHECK (status IN ('RECEIVED', 'PROCESSING', 'SETTLED', 'UNRATED', 'DEAD')),
    CONSTRAINT ck_storage_report_retry_non_negative CHECK (retry_count >= 0),
    CONSTRAINT ck_storage_report_correction_parent CHECK (
        (correction = FALSE AND correction_of_report_id IS NULL)
        OR (correction = TRUE AND correction_of_report_id IS NOT NULL AND correction_of_report_id <> report_id)
    )
);

CREATE TABLE IF NOT EXISTS billing.storage_usage_line_inbox (
    line_id             UUID PRIMARY KEY,
    report_id           UUID NOT NULL REFERENCES billing.storage_usage_report_inbox(report_id) ON DELETE RESTRICT,
    zone_id             UUID NOT NULL,
    resource_id         UUID NOT NULL,
    direction           VARCHAR(16) NOT NULL,
    usage_quantity      BIGINT NOT NULL,
    request_count       BIGINT NOT NULL,
    amount_micro_units  BIGINT,
    owner_id            UUID,
    owner_type          billing.owner_type,
    status              VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    reason              VARCHAR(64),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at          TIMESTAMPTZ,
    CONSTRAINT uq_storage_usage_line_identity UNIQUE (report_id, resource_id, direction),
    CONSTRAINT ck_storage_usage_line_direction CHECK (direction IN ('NETWORK_OUT')),
    CONSTRAINT ck_storage_usage_line_quantity CHECK (usage_quantity >= 0),
    CONSTRAINT ck_storage_usage_line_requests CHECK (request_count >= 0),
    CONSTRAINT ck_storage_usage_line_status CHECK (status IN ('PENDING', 'SETTLED', 'UNRATED', 'DEAD'))
);
