-- [COMMENT]: Consumer là infrastructure connection nên business row bị hard-delete. Tombstone
-- riêng chỉ là rebuild authority cho Zone KV sau khi outbox terminal đã hết 30 ngày retention.
CREATE TABLE IF NOT EXISTS mail_consumer_projection_tombstones (
    consumer_id UUID PRIMARY KEY,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE CASCADE,
    config_version BIGINT NOT NULL CHECK (config_version > 0),
    delete_event_id UUID UNIQUE NOT NULL,
    tombstoned_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mail_consumer_tombstones_zone_cursor
ON mail_consumer_projection_tombstones (zone_id, consumer_id);
