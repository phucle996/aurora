-- ======================================================================================================
-- 📂 MIGRATION: 000003_hierarchy_indexes.down.sql
--            Hierarchy/Hierarchy Module — Drop Indexes
-- ======================================================================================================

DROP INDEX IF EXISTS idx_personal_workspaces_lookup;
DROP INDEX IF EXISTS ux_personal_workspaces_owner_code;
DROP INDEX IF EXISTS idx_tenant_workspaces_lookup;
DROP INDEX IF EXISTS ux_tenant_workspaces_tenant_code;
DROP INDEX IF EXISTS tenant_memberships_tenant_user_uidx;
DROP INDEX IF EXISTS tenant_domains_domain_uidx;
DROP INDEX IF EXISTS ix_zone_services_zone_desired_state;
DROP INDEX IF EXISTS ix_zones_status;
DROP INDEX IF EXISTS ux_zones_code;
