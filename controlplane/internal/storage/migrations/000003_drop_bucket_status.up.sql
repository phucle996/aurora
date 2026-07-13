-- ============================================================================
-- MIGRATION: 000003_drop_bucket_status.up.sql
-- Storage Module — Drop status column from bucket tables
-- ============================================================================
-- [COMMENT]: Xóa cột status khỏi personal_buckets và tenant_buckets.
-- Bucket không có lifecycle state nữa — tồn tại trong DB là đủ để xác định active.
-- Khi tạo bucket thất bại, JO sẽ DELETE record khỏi DB (clean rollback).
-- ============================================================================

ALTER TABLE personal_buckets DROP COLUMN IF EXISTS status;
ALTER TABLE tenant_buckets   DROP COLUMN IF EXISTS status;
