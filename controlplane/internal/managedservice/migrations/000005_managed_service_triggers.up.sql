-- [COMMENT]: Migration 000005_managed_service_triggers.up.sql
-- Khởi tạo tất cả các Triggers và Constraint Triggers bảo vệ tính toàn vẹn cho Managed Service Platform.

-- 1. Trigger bảo vệ tính bất biến của Blueprint Revision
DROP TRIGGER IF EXISTS trg_blueprint_revisions_immutable ON blueprint_revisions;
CREATE TRIGGER trg_blueprint_revisions_immutable
BEFORE UPDATE ON blueprint_revisions
FOR EACH ROW EXECUTE FUNCTION reject_blueprint_revision_rewrite();

-- 2. Constraint Trigger kiểm tra tính hợp lệ của published_revision_id trong Service Blueprint
DROP TRIGGER IF EXISTS trg_service_blueprints_published_revision ON service_blueprints;
CREATE CONSTRAINT TRIGGER trg_service_blueprints_published_revision
AFTER INSERT OR UPDATE OF published_revision_id ON service_blueprints
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_service_blueprint_published_revision();

-- 3. Constraint Trigger kiểm tra con trỏ mặc định của Blueprint Revision luôn ở trạng thái Published
DROP TRIGGER IF EXISTS trg_blueprint_revisions_default_pointer ON blueprint_revisions;
CREATE CONSTRAINT TRIGGER trg_blueprint_revisions_default_pointer
AFTER UPDATE OF state ON blueprint_revisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_blueprint_revision_default_pointer();

-- 4. Trigger bảo vệ tính bất biến của Outbox Event payload
DROP TRIGGER IF EXISTS trg_managed_service_outbox_immutable ON managed_service_outbox_records;
CREATE TRIGGER trg_managed_service_outbox_immutable
BEFORE UPDATE ON managed_service_outbox_records
FOR EACH ROW EXECUTE FUNCTION reject_managed_service_outbox_payload_rewrite();
