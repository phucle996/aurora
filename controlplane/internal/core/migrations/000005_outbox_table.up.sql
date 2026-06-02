CREATE TABLE IF NOT EXISTS outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL UNIQUE,
    entity VARCHAR(64) NOT NULL,
    op VARCHAR(16) NOT NULL,
    payload BYTEA NOT NULL,
    version BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    attempts INT NOT NULL DEFAULT 0,
    last_attempt TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_outbox_records_status_created ON outbox_records(status, created_at);
