-- Storage migration layer 000002
-- Dedicated outbox records table for reliable async job scheduling inside storage module

CREATE TABLE IF NOT EXISTS storage_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL,
    routing_scope VARCHAR(100) NOT NULL, -- Phạm vi định tuyến (e.g. zone:vn)
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMP WITH TIME ZONE,

    -- CÁC CỘT ĐỒNG BỘ CONTRACT VỚI DATAPLANE:
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT, -- NULL means no timeout/limit

    -- CÁC CỘT LƯU KẾT QUẢ PHẢN HỒI TỪ DATAPLANE:
    error_code VARCHAR(100),
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

-- Index for high-performance outbox polling
CREATE INDEX IF NOT EXISTS idx_storage_outbox_pending 
ON storage_outbox_records (status, id ASC) 
WHERE status = 'PENDING';
