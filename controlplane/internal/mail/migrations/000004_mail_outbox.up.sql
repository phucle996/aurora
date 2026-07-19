-- [COMMENT]: Một outbox transport duy nhất cho Mail, cùng shape job-outbox của các module khác.
-- Mọi metadata hay thay đổi theo consumer/template phải nằm trong protobuf payload.
CREATE TABLE IF NOT EXISTS mail_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL,
    routing_scope VARCHAR(100) NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    actor_user_id UUID,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMPTZ,
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT,
    error_code VARCHAR(100),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mail_outbox_pending
ON mail_outbox_records (status, id ASC)
WHERE status = 'PENDING';

-- [COMMENT]: CronJob cleanup dùng index này để xóa terminal rows theo batch sau retention.
CREATE INDEX IF NOT EXISTS idx_mail_outbox_terminal_cleanup
ON mail_outbox_records (completed_at, id)
WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;
