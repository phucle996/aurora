-- Reuse the durable storage job outbox as the ownership delivery source.
-- No second lifecycle payload is stored: event_id/type/payload are derived
-- deterministically from the authoritative job row by Job Orchestrator.
ALTER TABLE storage.storage_outbox_records
    ADD COLUMN IF NOT EXISTS ownership_published_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS ownership_attempt_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ownership_last_error TEXT,
    ADD COLUMN IF NOT EXISTS ownership_locked_by VARCHAR(128),
    ADD COLUMN IF NOT EXISTS ownership_locked_until TIMESTAMPTZ;

-- [COMMENT]: Only terminal bucket lifecycle jobs enter the ownership relay.
-- SKIP LOCKED claims use this partial index and never scan unrelated storage jobs.
CREATE INDEX IF NOT EXISTS idx_storage_outbox_ownership_pending
    ON storage.storage_outbox_records (completed_at ASC, id ASC)
    WHERE status = 'SUCCEEDED'
      AND job_topic IN ('storage.bucket.create', 'storage.bucket.delete')
      AND ownership_published_at IS NULL;

-- [COMMENT]: The migration runner replays idempotent files at startup. Replace
-- the legacy index once, not on every replica restart, because an index rebuild
-- on a large outbox would create avoidable locks and I/O.
DO $ownership_cleanup_index$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_class index_class
        JOIN pg_namespace index_namespace
          ON index_namespace.oid = index_class.relnamespace
        JOIN pg_index index_metadata
          ON index_metadata.indexrelid = index_class.oid
        WHERE index_namespace.nspname = 'storage'
          AND index_class.relname = 'idx_storage_outbox_terminal_cleanup'
          AND pg_get_indexdef(index_metadata.indexrelid) LIKE '%ownership_published_at%'
    ) THEN
        DROP INDEX IF EXISTS storage.idx_storage_outbox_terminal_cleanup;
        CREATE INDEX idx_storage_outbox_terminal_cleanup
            ON storage.storage_outbox_records (completed_at ASC, id ASC)
            WHERE status IN ('SUCCEEDED', 'FAILED')
              AND completed_at IS NOT NULL
              AND (
                  status = 'FAILED'
                  OR job_topic NOT IN ('storage.bucket.create', 'storage.bucket.delete')
                  OR ownership_published_at IS NOT NULL
              );
    END IF;
END
$ownership_cleanup_index$;
