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


CREATE TABLE IF NOT EXISTS core_secret_families (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE core_secret_families IS 'Registry of shared secret families used by the controlplane. Each family rotates independently and is resolved by a stable code.';
COMMENT ON COLUMN core_secret_families.id IS 'Primary key of the secret family. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN core_secret_families.code IS 'Stable secret family lookup code used by runtime secret provider, for example access_token or one_time_token.';
COMMENT ON COLUMN core_secret_families.name IS 'Human-readable display name of the secret family.';
COMMENT ON COLUMN core_secret_families.description IS 'Optional description of the secret family purpose and operational meaning.';
COMMENT ON COLUMN core_secret_families.created_at IS 'Timestamp when the secret family registry row was created.';

CREATE TABLE IF NOT EXISTS core_secret_versions (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL REFERENCES core_secret_families(id) ON DELETE CASCADE,
    version INT NOT NULL,
    secret_ciphertext TEXT NOT NULL,
    secret_fingerprint TEXT NOT NULL,
    status core_secret_status NOT NULL DEFAULT 'pending',
    is_primary BOOLEAN NOT NULL DEFAULT false,
    not_before TIMESTAMPTZ NOT NULL DEFAULT now(),
    not_after TIMESTAMPTZ NULL,
    activated_at TIMESTAMPTZ NULL,
    retired_at TIMESTAMPTZ NULL,
    revoked_at TIMESTAMPTZ NULL,
    rotation_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_core_secret_versions_revoked_at
        CHECK ((status = 'revoked' AND revoked_at IS NOT NULL) OR status <> 'revoked'),
    CONSTRAINT ck_core_secret_versions_primary_status
        CHECK (NOT (is_primary = true AND status IN ('retired', 'revoked')))
);

COMMENT ON TABLE core_secret_versions IS 'Versioned shared secret storage for safe rotation. One family may have many versions, with one primary signer and optional active overlap versions.';
COMMENT ON COLUMN core_secret_versions.id IS 'Primary key of the secret version row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN core_secret_versions.family_id IS 'Foreign key to the UUIDv7 secret family that owns this version.';
COMMENT ON COLUMN core_secret_versions.version IS 'Monotonic version number inside one secret family.';
COMMENT ON COLUMN core_secret_versions.secret_ciphertext IS 'Encrypted secret material. Raw plaintext secret must never be stored in the database.';
COMMENT ON COLUMN core_secret_versions.secret_fingerprint IS 'Deterministic non-sensitive fingerprint used for duplicate detection and operational reference.';
COMMENT ON COLUMN core_secret_versions.status IS 'Lifecycle status of the secret version: pending, active, retired, or revoked.';
COMMENT ON COLUMN core_secret_versions.is_primary IS 'Whether this version is the primary version used for new sign or issue operations.';
COMMENT ON COLUMN core_secret_versions.not_before IS 'Earliest timestamp when this secret version may be treated as usable.';
COMMENT ON COLUMN core_secret_versions.not_after IS 'Latest timestamp when this secret version may still be considered valid for verification if runtime respects expiry boundaries.';
COMMENT ON COLUMN core_secret_versions.activated_at IS 'Timestamp when this secret version became operationally active.';
COMMENT ON COLUMN core_secret_versions.retired_at IS 'Timestamp when this secret version was retired from active serving.';
COMMENT ON COLUMN core_secret_versions.revoked_at IS 'Timestamp when this secret version was explicitly revoked.';
COMMENT ON COLUMN core_secret_versions.rotation_reason IS 'Optional short human-readable explanation for why this version was created or rotated.';
COMMENT ON COLUMN core_secret_versions.created_at IS 'Timestamp when this secret version row was created.';
COMMENT ON COLUMN core_secret_versions.updated_at IS 'Timestamp when this secret version row was last updated.';

CREATE TABLE IF NOT EXISTS zones (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    status zone_status NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE zones IS 'Zone catalog as independent edge location taxonomy used by dataplane placement and runtime decisions.';
COMMENT ON COLUMN zones.id IS 'Primary key of zone row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN zones.code IS 'Stable unique zone code, for example edge-hcm-1.';
COMMENT ON COLUMN zones.name IS 'Human-readable zone display name.';
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
