-- ============================================================================
-- MIGRATION: 000003_drop_bucket_status.down.sql
-- Storage Module — Restore status column to bucket tables
-- ============================================================================
-- [COMMENT]: Khôi phục cột status nếu cần rollback. Default 'active' vì tất cả
-- bucket hiện tại đang hoạt động bình thường.
-- ============================================================================

ALTER TABLE personal_buckets ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'active';
ALTER TABLE tenant_buckets   ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'active';
