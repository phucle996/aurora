-- Rollback advanced bucket configurations
ALTER TABLE personal_buckets
DROP COLUMN IF EXISTS encrypt_enabled,
DROP COLUMN IF EXISTS versioning_enabled,
DROP COLUMN IF EXISTS object_locking_enabled,
DROP COLUMN IF EXISTS replication_enabled,
DROP COLUMN IF EXISTS retention_days,
DROP COLUMN IF EXISTS legal_hold_enabled,
DROP COLUMN IF EXISTS tags;

ALTER TABLE tenant_buckets
DROP COLUMN IF EXISTS encrypt_enabled,
DROP COLUMN IF EXISTS versioning_enabled,
DROP COLUMN IF EXISTS object_locking_enabled,
DROP COLUMN IF EXISTS replication_enabled,
DROP COLUMN IF EXISTS retention_days,
DROP COLUMN IF EXISTS legal_hold_enabled,
DROP COLUMN IF EXISTS tags;
