-- ======================================================================================================
-- 📂 MIGRATION: 000003_hierarchy_indexes.up.sql
--            Hierarchy/Hierarchy Module — Indexes for lookup acceleration
-- ======================================================================================================

-- [COMMENT]: Indexes cho các trường trong zones và zone_services
CREATE UNIQUE INDEX IF NOT EXISTS ux_zones_code ON zones(code);
CREATE INDEX IF NOT EXISTS ix_zones_status ON zones(status);
CREATE INDEX IF NOT EXISTS ix_zone_services_zone_enabled ON zone_services(zone_id, enabled);

-- [COMMENT]: Indexes cho các trường trong phân hệ Tenant
CREATE UNIQUE INDEX IF NOT EXISTS tenant_domains_domain_uidx ON tenant_domains(domain);
CREATE UNIQUE INDEX IF NOT EXISTS tenant_memberships_tenant_user_uidx ON tenant_memberships(tenant_id, user_id);

-- [COMMENT]: Index phục vụ tìm kiếm Workspace theo Zone, Tenant hoặc Chủ sở hữu cá nhân
CREATE INDEX IF NOT EXISTS idx_workspaces_lookup ON workspaces (zone_id, tenant_id, owner_id);
