-- Mail migration layer 000004
-- Dedicated outbox records table for reliable async job scheduling inside mail module

CREATE TABLE IF NOT EXISTS mail_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) UNIQUE NOT NULL,
    zone_id VARCHAR(64) NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload_json JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,

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
ON mail_outbox_records (status, created_at ASC) 
WHERE status = 'PENDING';

-- Đảm bảo các cột mới tồn tại kể cả khi bảng đã có sẵn từ trước
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS job_version INT NOT NULL DEFAULT 1;
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS resource_id VARCHAR(64);
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS payload_schema_version INT NOT NULL DEFAULT 1;
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS trace_id VARCHAR(64);
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS idle INT;
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS error_code VARCHAR(100);
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS error_message TEXT;

-- Đảm bảo cột payload_json luôn ở kiểu JSONB trong trường hợp bảng đã tồn tại sẵn
ALTER TABLE mail_outbox_records ALTER COLUMN payload_json TYPE JSONB USING payload_json::jsonb;




