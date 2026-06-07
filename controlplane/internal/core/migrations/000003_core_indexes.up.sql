

CREATE UNIQUE INDEX IF NOT EXISTS ux_zones_code
ON zones(code);

CREATE INDEX IF NOT EXISTS ix_zones_status
ON zones(status);

CREATE INDEX IF NOT EXISTS ix_zone_services_zone_enabled
ON zone_services(zone_id, enabled);

CREATE INDEX IF NOT EXISTS ix_dataplane_nodes_zone_id
ON dataplane_nodes(zone_id);

CREATE INDEX IF NOT EXISTS ix_dataplane_nodes_status_zone_id
ON dataplane_nodes(status, zone_id);
