-- ======================================================================================================
-- 📂 MIGRATION: 000002_hierarchy_tables.down.sql
--            Hierarchy/Hierarchy Module — Drop Tables in reverse dependency order
-- ======================================================================================================

DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS tenant_memberships;
DROP TABLE IF EXISTS tenant_domains;
DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS zone_services;
DROP TABLE IF EXISTS zones;
