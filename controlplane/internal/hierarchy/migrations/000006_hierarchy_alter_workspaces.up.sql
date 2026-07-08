-- ======================================================================================================
-- 📂 MIGRATION: 000006_hierarchy_alter_workspaces.up.sql
--            Hierarchy Module — Alter Workspaces: Remove status, Add description
-- ======================================================================================================

ALTER TABLE hierarchy.workspaces DROP COLUMN IF EXISTS status;
ALTER TABLE hierarchy.workspaces ADD COLUMN IF NOT EXISTS description TEXT NULL;

COMMENT ON COLUMN hierarchy.workspaces.description IS 'Optional description of the workspace purpose and operational notes.';
