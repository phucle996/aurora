CREATE UNIQUE INDEX IF NOT EXISTS ux_core_secret_families_code
ON core_secret_families(code);

CREATE UNIQUE INDEX IF NOT EXISTS ux_core_secret_versions_family_version
ON core_secret_versions(family_id, version);

CREATE UNIQUE INDEX IF NOT EXISTS ux_core_secret_versions_secret_fingerprint
ON core_secret_versions(secret_fingerprint);

CREATE INDEX IF NOT EXISTS ix_core_secret_versions_family_status
ON core_secret_versions(family_id, status);

CREATE INDEX IF NOT EXISTS ix_core_secret_versions_family_created_at_desc
ON core_secret_versions(family_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS ux_core_secret_versions_one_primary
ON core_secret_versions(family_id)
WHERE is_primary = true AND revoked_at IS NULL;

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
