-- [COMMENT]: Migration 000002_managed_service_tables.down.sql
-- Rollback toàn bộ sơ đồ bảng của phân hệ Managed Service Platform theo thứ tự ngược lại.

DROP TABLE IF EXISTS managed_service_outbox_records;
DROP TABLE IF EXISTS tenant_managed_service_deletion_fences;
DROP TABLE IF EXISTS tenant_managed_service_operations;
DROP TABLE IF EXISTS tenant_managed_service_instance_revisions;
DROP TABLE IF EXISTS tenant_managed_service_instances;
DROP TABLE IF EXISTS personal_managed_service_deletion_fences;
DROP TABLE IF EXISTS personal_managed_service_operations;
DROP TABLE IF EXISTS personal_managed_service_instance_revisions;
DROP TABLE IF EXISTS personal_managed_service_instances;
DROP TABLE IF EXISTS catalog_audit_events;
DROP TABLE IF EXISTS blueprint_revisions;
DROP TABLE IF EXISTS service_blueprints;
DROP TABLE IF EXISTS service_versions;
DROP TABLE IF EXISTS service_definitions;
DROP TABLE IF EXISTS service_categories;
