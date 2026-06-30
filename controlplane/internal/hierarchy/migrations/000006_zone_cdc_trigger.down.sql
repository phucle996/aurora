DROP TRIGGER IF EXISTS trg_zone_service_change ON hierarchy.zone_services;
DROP TRIGGER IF EXISTS trg_zone_metadata_change ON hierarchy.zones;
DROP FUNCTION IF EXISTS hierarchy.notify_zone_metadata_change();
