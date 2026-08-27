-- [COMMENT]: Migration 000005_managed_service_triggers.down.sql
-- Rollback tất cả các Triggers của phân hệ Managed Service Platform.

DROP TRIGGER IF EXISTS trg_tenant_managed_service_delete_requires_deleting ON tenant_managed_service_instances;
DROP TRIGGER IF EXISTS trg_personal_managed_service_delete_requires_deleting ON personal_managed_service_instances;
DROP TRIGGER IF EXISTS trg_managed_service_outbox_immutable ON managed_service_outbox_records;
DROP TRIGGER IF EXISTS trg_blueprint_revisions_default_pointer ON blueprint_revisions;
DROP TRIGGER IF EXISTS trg_service_blueprints_published_revision ON service_blueprints;
DROP TRIGGER IF EXISTS trg_blueprint_revisions_immutable ON blueprint_revisions;
