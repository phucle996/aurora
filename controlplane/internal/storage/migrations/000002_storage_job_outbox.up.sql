-- Greenfield durable job and ownership-delivery boundary.
CREATE TABLE storage_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL,
    zone_id UUID NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    payload_key_id UUID NOT NULL,
    owner_id UUID NOT NULL,
    owner_type VARCHAR(16) NOT NULL,
    actor_user_id UUID,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    completed_at TIMESTAMPTZ,
    job_version INTEGER NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    resource_name VARCHAR(255),
    rollback_quota_bytes BIGINT,
    payload_schema_version INTEGER NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INTEGER,
    error_code VARCHAR(100),
    error_message TEXT,
    ownership_published_at TIMESTAMPTZ,
    ownership_attempt_count INTEGER NOT NULL DEFAULT 0,
    ownership_last_error TEXT,
    ownership_locked_by VARCHAR(128),
    ownership_locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_storage_outbox_status
        CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT ck_storage_outbox_owner_type
        CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_storage_outbox_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_storage_outbox_payload_key_id
        CHECK (payload_key_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_storage_outbox_bucket_resource_name CHECK (
        job_topic NOT IN ('storage.bucket.create', 'storage.bucket.resize', 'storage.bucket.delete')
        OR length(btrim(resource_name)) BETWEEN 1 AND 255
    ),
    CONSTRAINT ck_storage_outbox_resize_rollback
        CHECK (job_topic <> 'storage.bucket.resize' OR rollback_quota_bytes IS NOT NULL),
    CONSTRAINT ck_storage_outbox_rollback_quota
        CHECK (rollback_quota_bytes IS NULL OR rollback_quota_bytes >= 0)
);

CREATE INDEX idx_storage_outbox_pending
    ON storage_outbox_records(status, id)
    WHERE status = 'PENDING';

CREATE INDEX idx_storage_outbox_ownership_pending
    ON storage_outbox_records(completed_at, id)
    WHERE status = 'SUCCEEDED'
      AND job_topic IN ('storage.bucket.create', 'storage.bucket.delete')
      AND ownership_published_at IS NULL;

CREATE INDEX idx_storage_outbox_terminal_cleanup
    ON storage_outbox_records(completed_at, id)
    WHERE status IN ('SUCCEEDED', 'FAILED')
      AND completed_at IS NOT NULL
      AND (
          status = 'FAILED'
          OR job_topic NOT IN ('storage.bucket.create', 'storage.bucket.delete')
          OR ownership_published_at IS NOT NULL
      );

CREATE FUNCTION enforce_storage_outbox_payload_key()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM hierarchy.zones WHERE id = NEW.zone_id FOR KEY SHARE;
    PERFORM 1
    FROM hierarchy.zone_encryption_keys
    WHERE id = NEW.payload_key_id
      AND zone_id = NEW.zone_id
      AND status IN ('active', 'decrypt_only')
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'storage outbox payload key is not decryptable for target Zone';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_storage_outbox_payload_key
BEFORE INSERT ON storage_outbox_records
FOR EACH ROW EXECUTE FUNCTION enforce_storage_outbox_payload_key();
