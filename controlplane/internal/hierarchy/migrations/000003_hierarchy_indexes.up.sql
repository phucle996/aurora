-- ======================================================================================================
-- 📂 MIGRATION: 000003_hierarchy_indexes.up.sql
--            Hierarchy/Hierarchy Module — Indexes for lookup acceleration
-- ======================================================================================================

-- [COMMENT]: Indexes cho các trường trong zones và zone_services
CREATE UNIQUE INDEX IF NOT EXISTS ux_zones_code ON zones(code);
CREATE INDEX IF NOT EXISTS ix_zones_status ON zones(status);
-- [COMMENT]: Index phục vụ tăng tốc độ lookup dịch vụ theo zone và trạng thái mong muốn
CREATE INDEX IF NOT EXISTS ix_zone_services_zone_desired_state ON zone_services(zone_id, desired_state);

-- [COMMENT]: Indexes cho các trường trong phân hệ Tenant
CREATE UNIQUE INDEX IF NOT EXISTS tenant_domains_domain_uidx ON tenant_domains(domain);
CREATE UNIQUE INDEX IF NOT EXISTS tenant_memberships_tenant_user_uidx ON tenant_memberships(tenant_id, user_id);

-- [COMMENT]: Index phục vụ tìm kiếm Workspace theo Zone, Tenant hoặc Chủ sở hữu cá nhân
CREATE INDEX IF NOT EXISTS idx_workspaces_lookup ON workspaces (zone_id, tenant_id, owner_id);

-- [COMMENT]: Ràng buộc duy nhất: mã workspace theo từng tenant scope (nếu thuộc tenant)
CREATE UNIQUE INDEX IF NOT EXISTS ux_workspaces_tenant_code
ON workspaces(tenant_id, code)
WHERE tenant_id IS NOT NULL;

-- [COMMENT]: Ràng buộc duy nhất: mã workspace theo từng owner scope (nếu là cá nhân, tenant_id is null)
CREATE UNIQUE INDEX IF NOT EXISTS ux_workspaces_owner_code
ON workspaces(owner_id, code)
WHERE tenant_id IS NULL;
