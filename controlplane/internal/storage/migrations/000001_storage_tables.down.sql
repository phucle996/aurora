-- ======================================================================================================
-- 📂 MIGRATION: 000001_storage_tables.down.sql
--            Storage Module — Rollback SQL Statements
-- ======================================================================================================

DROP INDEX IF EXISTS idx_tenant_credentials_bucket;
DROP INDEX IF EXISTS idx_personal_credentials_bucket;
DROP INDEX IF EXISTS idx_tenant_buckets_tenant_zone;
DROP INDEX IF EXISTS idx_personal_buckets_workspace;

DROP TABLE IF EXISTS tenant_credentials;
DROP TABLE IF EXISTS personal_credentials;
DROP TABLE IF EXISTS tenant_buckets;
DROP TABLE IF EXISTS personal_buckets;
