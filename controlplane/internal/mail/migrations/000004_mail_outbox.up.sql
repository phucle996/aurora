-- Mail migration layer 000004
-- Dedicated outbox records table for reliable async job scheduling inside mail module

CREATE TABLE IF NOT EXISTS mail_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) UNIQUE NOT NULL,
    zone_id VARCHAR(64) NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHED', 'PROCESSING', 'COMPLETED', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMP WITH TIME ZONE,

    -- CÁC CỘT ĐỒNG BỘ CONTRACT VỚI DATAPLANE:
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id VARCHAR(64),
    idle INT, -- NULL means no timeout/limit

    -- CÁC CỘT LƯU KẾT QUẢ PHẢN HỒI TỪ DATAPLANE:
    error_code VARCHAR(100),
    error_message TEXT
);

-- Index for high-performance outbox polling
CREATE INDEX IF NOT EXISTS idx_mail_outbox_pending 
ON mail_outbox_records (status, id ASC) 
WHERE status = 'PENDING';