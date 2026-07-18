-- ============================================================================
-- MIGRATION: 000005_outbox_retention_index.up.sql
-- Storage Module — Index phục vụ cleanup job 30 ngày
-- ============================================================================
-- [COMMENT]: Index (completed_at, id) trên các terminal records (SUCCEEDED/FAILED)
-- để cleanup worker có thể efficiently scan và delete theo batch nhỏ mà không
-- gây full table scan hoặc lock lớn.
-- ============================================================================

-- [COMMENT]: Index phục vụ cleanup worker: chỉ scan terminal records có completed_at
-- Worker query: WHERE status IN ('SUCCEEDED','FAILED') AND completed_at < NOW() - INTERVAL '30 days'
CREATE INDEX IF NOT EXISTS idx_storage_outbox_terminal_cleanup
    ON storage.storage_outbox_records (completed_at ASC, id ASC)
    WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;
