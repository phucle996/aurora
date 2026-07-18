-- ============================================================================
-- MIGRATION: 000004_lifecycle_outbox.up.sql
-- Storage Module — Resource Lifecycle Event Outbox (Billing Domain Outbox)
-- ============================================================================
-- [COMMENT]: Bảng durable outbox cho lifecycle events của resource.
-- Được ghi trong cùng transaction với job outbox SUCCEEDED để đảm bảo
-- không bao giờ có resource commit mà không có durable lifecycle event.
-- Relay đọc UNPUBLISHED records và publish lên JetStream; chỉ sau PubAck
-- mới update status thành PUBLISHED.
-- ============================================================================

CREATE TABLE IF NOT EXISTS storage.resource_lifecycle_events (
    -- Event identity
    id               UUID PRIMARY KEY,
    event_id         UUID NOT NULL UNIQUE,  -- Deterministic: UUID_v5(namespace, job_id || event_type)
    event_type       VARCHAR(32)  NOT NULL, -- 'RESOURCE_CREATED' | 'RESOURCE_DELETED'
    schema_version   INT          NOT NULL DEFAULT 1,

    -- Resource info tại thời điểm event xảy ra
    resource_id      UUID         NOT NULL,
    resource_type    VARCHAR(32)  NOT NULL DEFAULT 'STORAGE_BUCKET',
    resource_name    VARCHAR(255) NOT NULL,

    -- Owner info — derive từ DB, không từ outbox.user_id
    owner_id         UUID         NOT NULL,
    owner_type       VARCHAR(16)  NOT NULL, -- 'PERSONAL' | 'TENANT'

    -- Spatial và temporal context
    zone_id          UUID         NOT NULL,
    source_version   BIGINT       NOT NULL DEFAULT 1, -- Tăng dần theo ownership change
    effective_at     TIMESTAMPTZ  NOT NULL,

    -- Job provenance
    source_job_id    UUID         NOT NULL,
    traceparent      VARCHAR(128),

    -- Protobuf-encoded payload (ResourceLifecycleEventV1)
    payload          BYTEA        NOT NULL,

    -- Relay state machine: UNPUBLISHED → PUBLISHED | DEAD
    status           VARCHAR(16)  NOT NULL DEFAULT 'UNPUBLISHED',
    published_at     TIMESTAMPTZ,
    attempt_count    INT          NOT NULL DEFAULT 0,
    last_error       TEXT,

    -- Lease fields cho relay batch claiming
    locked_by        VARCHAR(128),  -- Hostname/replica ID đang hold lease
    locked_until     TIMESTAMPTZ,   -- Lease expiry; relay khác có thể claim sau thời điểm này

    occurred_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT ck_lifecycle_event_type CHECK (event_type IN ('RESOURCE_CREATED', 'RESOURCE_DELETED')),
    CONSTRAINT ck_lifecycle_owner_type CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_lifecycle_status CHECK (status IN ('UNPUBLISHED', 'PUBLISHED', 'DEAD')),
    CONSTRAINT ck_lifecycle_source_version_positive CHECK (source_version > 0),
    -- Unique constraint: một resource không thể có hai events cùng version + type
    CONSTRAINT uq_lifecycle_resource_version_type UNIQUE (resource_id, source_version, event_type)
);

-- [COMMENT]: Index phục vụ relay polling UNPUBLISHED records theo batch
CREATE INDEX IF NOT EXISTS idx_lifecycle_unpublished
    ON storage.resource_lifecycle_events (occurred_at ASC, id ASC)
    WHERE status = 'UNPUBLISHED';

-- [COMMENT]: Index hỗ trợ truy vấn theo resource_id để đối chiếu version
CREATE INDEX IF NOT EXISTS idx_lifecycle_resource
    ON storage.resource_lifecycle_events (resource_id, source_version DESC);
