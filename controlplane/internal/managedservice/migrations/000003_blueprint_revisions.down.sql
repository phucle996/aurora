DROP TRIGGER IF EXISTS trg_blueprint_revisions_default_pointer ON blueprint_revisions;
DROP FUNCTION IF EXISTS validate_blueprint_revision_default_pointer();
DROP TRIGGER IF EXISTS trg_service_blueprints_published_revision ON service_blueprints;
DROP FUNCTION IF EXISTS validate_service_blueprint_published_revision();
DROP TRIGGER IF EXISTS trg_blueprint_revisions_immutable ON blueprint_revisions;
DROP FUNCTION IF EXISTS reject_blueprint_revision_rewrite();
ALTER TABLE IF EXISTS service_blueprints DROP CONSTRAINT IF EXISTS fk_service_blueprints_published_revision;
ALTER TABLE IF EXISTS service_blueprints DROP COLUMN IF EXISTS published_revision_id;
DROP TABLE IF EXISTS blueprint_revisions;
