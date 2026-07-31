-- Storage migration layer 000002
-- Dedicated outbox records table for reliable async job scheduling inside storage module

CREATE TABLE IF NOT EXISTS storage_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL,
    zone_id UUID NOT NULL, -- Zone đích duy nhất của runtime command
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    payload_key_id UUID NOT NULL,
    -- CÁC CỘT ĐỊNH DANH SỞ HỮU BILLING (SoT):
    owner_id UUID NOT NULL,          -- Payer: personal_workspaces.owner_id hoặc tenant_id
    owner_type VARCHAR(16) NOT NULL, -- 'PERSONAL' | 'TENANT' (Bắt buộc truyền giá trị rõ ràng, không fallback default)
    actor_user_id UUID,              -- User thực hiện request; chỉ dùng notification/audit, không dùng chọn wallet

    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMP WITH TIME ZONE,


    -- CÁC CỘT ĐỒNG BỘ CONTRACT VỚI DATAPLANE:
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    resource_name VARCHAR(255),
    rollback_quota_bytes BIGINT CHECK (rollback_quota_bytes IS NULL OR rollback_quota_bytes >= 0),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT, -- NULL means no timeout/limit

    -- CÁC CỘT LƯU KẾT QUẢ PHẢN HỒI TỪ DATAPLANE:
    error_code VARCHAR(100),
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT ck_storage_outbox_owner_type CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_storage_outbox_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_storage_outbox_payload_key_id
        CHECK (payload_key_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_storage_outbox_bucket_resource_name CHECK (
        job_topic NOT IN ('storage.bucket.create', 'storage.bucket.resize', 'storage.bucket.delete')
        OR length(btrim(resource_name)) BETWEEN 1 AND 255
    ),
    CONSTRAINT ck_storage_outbox_resize_rollback CHECK (
        job_topic <> 'storage.bucket.resize' OR rollback_quota_bytes IS NOT NULL
    )
);

-- Index for high-performance outbox polling
CREATE INDEX IF NOT EXISTS idx_storage_outbox_pending 
ON storage_outbox_records (status, id ASC) 
WHERE status = 'PENDING';
