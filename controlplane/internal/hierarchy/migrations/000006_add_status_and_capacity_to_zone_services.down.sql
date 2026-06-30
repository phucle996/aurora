ALTER TABLE hierarchy.zone_services
DROP COLUMN IF EXISTS status,
DROP COLUMN IF EXISTS capacity,
DROP COLUMN IF EXISTS last_heartbeat_at;

DROP TYPE IF EXISTS zone_service_status;
