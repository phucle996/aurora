-- Storage migration layer 000002 - rollback
DROP INDEX IF EXISTS idx_storage_outbox_pending;
DROP TABLE IF EXISTS storage_outbox_records;
