-- [COMMENT]: Migration 000004_managed_service_funcs.down.sql
-- Rollback tất cả các PL/pgSQL Stored Functions của phân hệ Managed Service Platform.

DROP FUNCTION IF EXISTS require_managed_service_instance_deleting_before_delete();
DROP FUNCTION IF EXISTS reject_managed_service_outbox_payload_rewrite();
DROP FUNCTION IF EXISTS validate_blueprint_revision_default_pointer();
DROP FUNCTION IF EXISTS validate_service_blueprint_published_revision();
DROP FUNCTION IF EXISTS reject_blueprint_revision_rewrite();
