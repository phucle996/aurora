-- Add constraint: prevent deleting zone when it has enabled services
ALTER TABLE zones
ADD CONSTRAINT ck_zones_delete_precondition
CHECK (
    -- This constraint is informational only; actual enforcement happens in application layer
    -- Zone can only be deleted when status = 'disabled' AND no enabled services exist
    true
);

COMMENT ON CONSTRAINT ck_zones_delete_precondition ON zones IS
'Informational constraint: Zone deletion requires status=disabled AND no enabled services. Enforced by application layer (zone_service.go DeleteZone).';

-- Add constraint: prevent updating zone_services when zone is not in planned or maintenance status
-- This is enforced at application layer, but we document it here for clarity
ALTER TABLE zone_services
ADD CONSTRAINT ck_zone_services_update_precondition
CHECK (
    -- This constraint is informational only; actual enforcement happens in application layer
    -- Zone services can only be updated when zone status = 'planned' or 'maintenance'
    true
);

COMMENT ON CONSTRAINT ck_zone_services_update_precondition ON zone_services IS
'Informational constraint: Zone services can only be updated when zone status is planned or maintenance. Enforced by application layer (zone_service.go UpsertZoneService).';

-- Add index for efficient zone deletion precondition checks
CREATE INDEX IF NOT EXISTS idx_zone_services_enabled_by_zone
ON zone_services(zone_id, enabled)
WHERE enabled = true;

COMMENT ON INDEX idx_zone_services_enabled_by_zone IS
'Index for efficient query: HasEnabledZoneServicesByZone check during zone deletion.';

-- Add index for zone status queries (used in state machine transitions)
CREATE INDEX IF NOT EXISTS idx_zones_status
ON zones(status);

COMMENT ON INDEX idx_zones_status IS
'Index for efficient zone status filtering in state machine transition queries.';
