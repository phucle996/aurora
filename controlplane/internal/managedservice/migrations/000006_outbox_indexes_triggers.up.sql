CREATE TABLE IF NOT EXISTS managed_service_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    job_topic VARCHAR(100) NOT NULL CHECK (job_topic = 'managed_service.instance.execute'),
    payload BYTEA NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 1000000),
    -- This is a module transport record, not a customer aggregate. Owner fields
    -- route notification/billing context while personal and tenant state stay split.
    owner_id UUID NOT NULL,
    owner_type VARCHAR(16) NOT NULL CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    actor_user_id UUID NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ NULL,
    job_version INT NOT NULL DEFAULT 1 CHECK (job_version = 1),
    resource_id VARCHAR(64) NOT NULL,
    payload_schema_version INT NOT NULL DEFAULT 1 CHECK (payload_schema_version = 1),
    trace_id BYTEA NULL CHECK (trace_id IS NULL OR octet_length(trace_id) = 16),
    idle INT NULL CHECK (idle IS NULL OR idle >= 0),
    error_code VARCHAR(100) NULL,
    error_message TEXT NULL CHECK (error_message IS NULL OR char_length(error_message) <= 1024),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_managed_service_outbox_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_managed_service_outbox_completed CHECK (
        (status IN ('SUCCEEDED', 'FAILED')) = (completed_at IS NOT NULL)
    )
);

-- At-least-once dispatch may replay an event, but only one unresolved operation
-- can own an instance. These partial indexes are the P04 CTE race boundary.
CREATE UNIQUE INDEX IF NOT EXISTS ux_personal_managed_service_operations_nonterminal
    ON personal_managed_service_operations(instance_id)
    WHERE state IN ('accepted', 'dispatching', 'running', 'retrying');
CREATE UNIQUE INDEX IF NOT EXISTS ux_tenant_managed_service_operations_nonterminal
    ON tenant_managed_service_operations(instance_id)
    WHERE state IN ('accepted', 'dispatching', 'running', 'retrying');
CREATE INDEX IF NOT EXISTS ix_managed_service_outbox_pending
    ON managed_service_outbox_records(available_at, id)
    WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS ix_managed_service_outbox_zone_pending
    ON managed_service_outbox_records(zone_id, available_at, id)
    WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS ix_managed_service_outbox_terminal_retention
    ON managed_service_outbox_records(completed_at, id)
    WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;

CREATE OR REPLACE FUNCTION reject_managed_service_outbox_payload_rewrite()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    -- JO may update dispatch outcome only. Replacing a committed envelope would
    -- make a replayed WAL event diverge from the durable desired-state intent.
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.zone_id IS DISTINCT FROM OLD.zone_id
       OR NEW.job_topic IS DISTINCT FROM OLD.job_topic
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.owner_id IS DISTINCT FROM OLD.owner_id
       OR NEW.owner_type IS DISTINCT FROM OLD.owner_type
       OR NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id
       OR NEW.available_at IS DISTINCT FROM OLD.available_at
       OR NEW.job_version IS DISTINCT FROM OLD.job_version
       OR NEW.resource_id IS DISTINCT FROM OLD.resource_id
       OR NEW.payload_schema_version IS DISTINCT FROM OLD.payload_schema_version
       OR NEW.trace_id IS DISTINCT FROM OLD.trace_id
       OR NEW.idle IS DISTINCT FROM OLD.idle
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'managed service outbox intent is immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_managed_service_outbox_immutable ON managed_service_outbox_records;
CREATE TRIGGER trg_managed_service_outbox_immutable
BEFORE UPDATE ON managed_service_outbox_records
FOR EACH ROW EXECUTE FUNCTION reject_managed_service_outbox_payload_rewrite();
