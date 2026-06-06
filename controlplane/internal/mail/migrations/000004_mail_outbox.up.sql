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
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Index for high-performance outbox polling
CREATE INDEX IF NOT EXISTS idx_mail_outbox_pending 
ON mail_outbox_records (status, created_at ASC) 
WHERE status = 'PENDING';
