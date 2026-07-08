-- ======================================================================================================
-- 📂 MIGRATION: 000006_hierarchy_alter_workspaces.down.sql
--            Hierarchy Module — Alter Workspaces Rollback
-- ======================================================================================================

ALTER TABLE hierarchy.workspaces ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'active';
ALTER TABLE hierarchy.workspaces DROP COLUMN IF EXISTS description;

COMMENT ON COLUMN hierarchy.workspaces.status IS 'Trạng thái hoạt động của Workspace (active, suspended, deleted).';
