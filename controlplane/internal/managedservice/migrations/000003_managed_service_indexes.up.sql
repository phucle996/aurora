-- [COMMENT]: Migration 000003_managed_service_indexes.up.sql
-- Khởi tạo tất cả các chỉ mục (Indexes DDL) cho phân hệ Managed Service Platform.

-- 1. Index cho System Catalog
CREATE INDEX IF NOT EXISTS ix_service_definitions_category ON service_definitions(category_id);
CREATE INDEX IF NOT EXISTS ix_service_versions_definition ON service_versions(definition_id);
CREATE INDEX IF NOT EXISTS ix_service_blueprints_version ON service_blueprints(version_id);
CREATE INDEX IF NOT EXISTS ix_catalog_audit_events_record ON catalog_audit_events(record_kind, record_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS ix_blueprint_revisions_blueprint_state ON blueprint_revisions(blueprint_id, state, revision DESC);

-- 2. Index cho Personal Aggregate
CREATE INDEX IF NOT EXISTS ix_personal_managed_service_instances_workspace_state
    ON personal_managed_service_instances(workspace_id, state, updated_at DESC);
CREATE INDEX IF NOT EXISTS ix_personal_managed_service_revisions_zone
    ON personal_managed_service_instance_revisions(zone_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_personal_managed_service_deletion_fences_retention
    ON personal_managed_service_deletion_fences(retained_until, deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS ux_personal_managed_service_operations_nonterminal
    ON personal_managed_service_operations(instance_id)
    WHERE state IN ('accepted', 'dispatching', 'running', 'retrying');
CREATE INDEX IF NOT EXISTS ix_personal_managed_service_operations_instance_id
    ON personal_managed_service_operations(instance_id, id DESC);

-- 3. Index cho Tenant Aggregate
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_instances_workspace_state
    ON tenant_managed_service_instances(workspace_id, state, updated_at DESC);
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_revisions_zone
    ON tenant_managed_service_instance_revisions(zone_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_deletion_fences_retention
    ON tenant_managed_service_deletion_fences(retained_until, deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tenant_managed_service_operations_nonterminal
    ON tenant_managed_service_operations(instance_id)
    WHERE state IN ('accepted', 'dispatching', 'running', 'retrying');
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_operations_instance_id
    ON tenant_managed_service_operations(instance_id, id DESC);

-- 4. Index cho Outbox
CREATE INDEX IF NOT EXISTS ix_managed_service_outbox_pending
    ON managed_service_outbox_records(available_at, id)
    WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS ix_managed_service_outbox_zone_pending
    ON managed_service_outbox_records(zone_id, available_at, id)
    WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS ix_managed_service_outbox_terminal_retention
    ON managed_service_outbox_records(completed_at, id)
    WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;
