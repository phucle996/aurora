-- ======================================================================================================
-- 📂 MIGRATION: 000002_core_tables.up.sql
--            Core Module — Table Definitions & In-Table Constraints
-- ======================================================================================================
--
-- 📜 CONSTRAINT STRATEGY:
--   - In-table constraints (CHECK, NOT NULL, UNIQUE, DEFAULT) → defined HERE in 000002
--   - Cross-table constraints (FOREIGN KEY, referential integrity) → defined HERE in 000002
--   - Additional indexes & informational constraints → defined in 000004_zone_constraints.up.sql
--     to avoid breaking migrations if table structure changes.
--
-- 🔒 REFERENTIAL INTEGRITY:
--   - core_secret_versions → core_secret_families: ON DELETE CASCADE
--   - zone_services → zones: ON DELETE CASCADE
--   - dataplane_nodes → zones: UNIQUE constraint (one dataplane per zone)
--
-- ======================================================================================================


CREATE TABLE IF NOT EXISTS core_secrets (
    secret_type VARCHAR(100) PRIMARY KEY,
    active_secret TEXT NOT NULL,
    active_fingerprint VARCHAR(256) NOT NULL,
    active_created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    standby_secret TEXT NOT NULL,
    standby_fingerprint VARCHAR(256) NOT NULL,
    standby_created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE core_secrets IS 'Lưu trữ các cặp secret active-standby cho từng loại token/chữ ký, phục vụ rotation và verification liên tục.';
COMMENT ON COLUMN core_secrets.secret_type IS 'Unique identifier type code of the secret, for example access_secret, refresh_secret, admin_api_key, one_time_token.';
COMMENT ON COLUMN core_secrets.active_secret IS 'Encrypted active secret material.';
COMMENT ON COLUMN core_secrets.active_fingerprint IS 'Deterministic non-sensitive fingerprint of the active secret.';
COMMENT ON COLUMN core_secrets.active_created_at IS 'Timestamp when the active secret was created.';
COMMENT ON COLUMN core_secrets.standby_secret IS 'Encrypted standby secret material.';
COMMENT ON COLUMN core_secrets.standby_fingerprint IS 'Deterministic non-sensitive fingerprint of the standby secret.';
COMMENT ON COLUMN core_secrets.standby_created_at IS 'Timestamp when the standby secret was created.';
COMMENT ON COLUMN core_secrets.updated_at IS 'Timestamp when this row was last updated.';

CREATE TABLE IF NOT EXISTS zones (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    location TEXT NOT NULL,
    description TEXT NULL,
    status zone_status NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE zones IS 'Zone catalog as independent edge location taxonomy used by dataplane placement and runtime decisions.';
COMMENT ON COLUMN zones.id IS 'Primary key of zone row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN zones.code IS 'Stable unique zone code, for example edge-hcm-1.';
COMMENT ON COLUMN zones.name IS 'Human-readable zone display name.';
COMMENT ON COLUMN zones.location IS 'Human-readable physical location of the zone.';
COMMENT ON COLUMN zones.description IS 'Optional description of the zone purpose and operational notes.';
COMMENT ON COLUMN zones.status IS 'Operational status of zone lifecycle (planned, active, draining, maintenance, disabled).';
COMMENT ON COLUMN zones.created_at IS 'Timestamp when zone row was created.';
COMMENT ON COLUMN zones.updated_at IS 'Timestamp when zone row was last updated.';

CREATE TABLE IF NOT EXISTS zone_services (
    id UUID PRIMARY KEY,
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    service_type zone_service_type NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_zone_services_zone_service UNIQUE (zone_id, service_type)
);

COMMENT ON TABLE zone_services IS 'Per-zone service availability matrix indicating whether each service type is enabled in a specific zone.';
COMMENT ON COLUMN zone_services.id IS 'Primary key of zone service row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN zone_services.zone_id IS 'Foreign key to zone that owns this service availability flag.';
COMMENT ON COLUMN zone_services.service_type IS 'Service type supported in zone, for example mail or hypervisor.';
COMMENT ON COLUMN zone_services.enabled IS 'Whether the given service type is enabled for this zone.';
COMMENT ON COLUMN zone_services.created_at IS 'Timestamp when zone service row was created.';
COMMENT ON COLUMN zone_services.updated_at IS 'Timestamp when zone service row was last updated.';

CREATE TABLE IF NOT EXISTS dataplane_nodes (
    id UUID PRIMARY KEY,
    status dataplane_node_status NOT NULL,
    zone_id UUID NOT NULL UNIQUE REFERENCES zones(id),
    endpoint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE dataplane_nodes IS 'Dataplane cluster registry snapshot. Each zone contains exactly one active dataplane cluster.';
COMMENT ON COLUMN dataplane_nodes.id IS 'Primary key and unique identity of dataplane cluster (UUIDv7) issued by identity flow.';
COMMENT ON COLUMN dataplane_nodes.status IS 'Current runtime lifecycle status of the dataplane cluster.';
COMMENT ON COLUMN dataplane_nodes.zone_id IS 'Foreign key and unique link to the zone that this dataplane cluster belongs to.';
COMMENT ON COLUMN dataplane_nodes.endpoint IS 'The public/internal gRPC or HTTP load balancer URL of the dataplane cluster in this zone.';
COMMENT ON COLUMN dataplane_nodes.created_at IS 'Timestamp when dataplane cluster row was created.';
COMMENT ON COLUMN dataplane_nodes.updated_at IS 'Timestamp when dataplane cluster row was last updated.';
