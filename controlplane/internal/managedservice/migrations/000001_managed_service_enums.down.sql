-- [COMMENT]: Migration 000001_managed_service_enums.down.sql
-- Rollback tất cả Enum Types của phân hệ Managed Service Platform.

DROP TYPE IF EXISTS managed_service_result_outcome;
DROP TYPE IF EXISTS managed_service_operation_state;
DROP TYPE IF EXISTS managed_service_operation_kind;
DROP TYPE IF EXISTS managed_service_observed_state;
DROP TYPE IF EXISTS managed_service_instance_state;
DROP TYPE IF EXISTS managed_service_blueprint_revision_state;
DROP TYPE IF EXISTS managed_service_version_state;
DROP TYPE IF EXISTS managed_service_catalog_state;
