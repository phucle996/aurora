-- Mail migration layer 000004
-- Dedicated outbox records table for reliable async job scheduling inside mail module

CREATE TABLE IF NOT EXISTS mail_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) UNIQUE NOT NULL,
    zone_id VARCHAR(64) NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload_json TEXT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    attempts INT NOT NULL DEFAULT 0,
    last_attempt TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- CÁC CỘT ĐỒNG BỘ CONTRACT VỚI DATAPLANE:
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id VARCHAR(64),
    idle INT NOT NULL DEFAULT 30,

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
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS idle INT NOT NULL DEFAULT 30;
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS error_code VARCHAR(100);
ALTER TABLE mail_outbox_records ADD COLUMN IF NOT EXISTS error_message TEXT;


-- Tự động đăng ký Publication cho bảng mail_outbox_records
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'outbox_pub') THEN
        CREATE PUBLICATION outbox_pub FOR TABLE mail_outbox_records;
    END IF;
END $$;

-- Tự động đăng ký Logical Replication Slot sử dụng output plugin pgoutput
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = 'outbox_slot') THEN
        PERFORM pg_create_logical_replication_slot('outbox_slot', 'pgoutput');
    END IF;
END $$;

