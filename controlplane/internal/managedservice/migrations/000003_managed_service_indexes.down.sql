-- [COMMENT]: Migration 000003_managed_service_indexes.down.sql
-- Rollback tất cả các chỉ mục INDEXES của phân hệ Managed Service Platform.

DROP INDEX IF EXISTS ix_managed_service_outbox_terminal_retention;
DROP INDEX IF EXISTS ix_managed_service_outbox_zone_pending;
DROP INDEX IF EXISTS ix_managed_service_outbox_pending;
DROP INDEX IF EXISTS ix_tenant_managed_service_operations_instance_id;
DROP INDEX IF EXISTS ux_tenant_managed_service_operations_nonterminal;
DROP INDEX IF EXISTS ix_tenant_managed_service_deletion_fences_retention;
DROP INDEX IF EXISTS ix_tenant_managed_service_revisions_zone;
DROP INDEX IF EXISTS ix_tenant_managed_service_instances_workspace_state;
DROP INDEX IF EXISTS ix_personal_managed_service_operations_instance_id;
DROP INDEX IF EXISTS ux_personal_managed_service_operations_nonterminal;
DROP INDEX IF EXISTS ix_personal_managed_service_deletion_fences_retention;
DROP INDEX IF EXISTS ix_personal_managed_service_revisions_zone;
DROP INDEX IF EXISTS ix_personal_managed_service_instances_workspace_state;
DROP INDEX IF EXISTS ix_blueprint_revisions_blueprint_state;
DROP INDEX IF EXISTS ix_catalog_audit_events_record;
DROP INDEX IF EXISTS ix_service_blueprints_version;
DROP INDEX IF EXISTS ix_service_versions_definition;
DROP INDEX IF EXISTS ix_service_definitions_category;
