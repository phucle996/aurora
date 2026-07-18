-- ============================================================================
-- MIGRATION: 000005_outbox_retention_index.down.sql
-- ============================================================================

DROP INDEX IF EXISTS storage.idx_storage_outbox_terminal_cleanup;
