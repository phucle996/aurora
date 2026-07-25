DROP INDEX IF EXISTS storage.idx_storage_outbox_ownership_pending;

ALTER TABLE storage.storage_outbox_records
    DROP COLUMN IF EXISTS ownership_locked_until,
    DROP COLUMN IF EXISTS ownership_locked_by,
    DROP COLUMN IF EXISTS ownership_last_error,
    DROP COLUMN IF EXISTS ownership_attempt_count,
    DROP COLUMN IF EXISTS ownership_published_at;

DROP INDEX IF EXISTS storage.idx_storage_outbox_terminal_cleanup;
CREATE INDEX idx_storage_outbox_terminal_cleanup
    ON storage.storage_outbox_records (completed_at ASC, id ASC)
    WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;
