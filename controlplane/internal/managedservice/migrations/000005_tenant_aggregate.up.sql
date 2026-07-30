CREATE TABLE IF NOT EXISTS tenant_managed_service_instances (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES hierarchy.tenant_workspaces(id) ON DELETE RESTRICT,
    tenant_id UUID NOT NULL REFERENCES hierarchy.tenants(id) ON DELETE RESTRICT,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    state managed_service_instance_state NOT NULL DEFAULT 'provisioning',
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
    revision_sequence BIGINT NOT NULL DEFAULT 0 CHECK (revision_sequence >= 0),
    create_intent_sha256 BYTEA NOT NULL CHECK (octet_length(create_intent_sha256) = 32),
    active_revision_id UUID NULL,
    pending_revision_id UUID NULL,
    observed_state managed_service_observed_state NOT NULL DEFAULT 'unknown',
    observed_state_version BIGINT NOT NULL DEFAULT 0 CHECK (observed_state_version >= 0),
    observed_output JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(observed_output) = 'object'
        AND octet_length(observed_output::text) <= 65536
    ),
    observed_at TIMESTAMPTZ NULL,
    metadata_version BIGINT NOT NULL DEFAULT 1 CHECK (metadata_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_tenant_managed_service_instances_workspace_code UNIQUE (workspace_id, code),
    CONSTRAINT ck_tenant_managed_service_instances_revision_heads CHECK (
        active_revision_id IS NOT NULL OR pending_revision_id IS NOT NULL
    ),
    CONSTRAINT ck_tenant_managed_service_instances_distinct_heads CHECK (
        active_revision_id IS NULL OR pending_revision_id IS NULL OR active_revision_id <> pending_revision_id
    )
);

CREATE TABLE IF NOT EXISTS tenant_managed_service_instance_revisions (
    id UUID PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES tenant_managed_service_instances(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    blueprint_revision_id UUID NOT NULL REFERENCES blueprint_revisions(id) ON DELETE RESTRICT,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_schema_sha256 BYTEA NOT NULL CHECK (octet_length(input_schema_sha256) = 32),
    parameter_envelope BYTEA NOT NULL CHECK (octet_length(parameter_envelope) BETWEEN 1 AND 262144),
    parameter_envelope_sha256 BYTEA NOT NULL CHECK (octet_length(parameter_envelope_sha256) = 32),
    input_sha256 BYTEA NOT NULL CHECK (octet_length(input_sha256) = 32),
    desired_spec_sha256 BYTEA NOT NULL CHECK (octet_length(desired_spec_sha256) = 32),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_tenant_managed_service_instance_revisions_instance_revision UNIQUE (instance_id, revision),
    CONSTRAINT ux_tenant_managed_service_instance_revisions_id_instance UNIQUE (id, instance_id)
);

CREATE TABLE IF NOT EXISTS tenant_managed_service_operations (
    id UUID PRIMARY KEY,
    -- Evidence pins snapshot IDs/hashes instead of live foreign keys. DELETE may
    -- hard-delete the aggregate only after the deletion fence is durable.
    instance_id UUID NOT NULL,
    target_revision_id UUID NOT NULL,
    blueprint_revision_id UUID NOT NULL,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    kind managed_service_operation_kind NOT NULL,
    state managed_service_operation_state NOT NULL DEFAULT 'accepted',
    generation BIGINT NOT NULL CHECK (generation > 0),
    attempt SMALLINT NOT NULL DEFAULT 0 CHECK (attempt BETWEEN 0 AND 4),
    current_command_event_id UUID NOT NULL UNIQUE,
    retry_of_operation_id UUID NULL,
    status_version BIGINT NOT NULL DEFAULT 1 CHECK (status_version > 0),
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_sha256 BYTEA NOT NULL CHECK (octet_length(input_sha256) = 32),
    desired_spec_sha256 BYTEA NOT NULL CHECK (octet_length(desired_spec_sha256) = 32),
    actor_user_id UUID NOT NULL,
    last_error_code TEXT NULL CHECK (last_error_code IS NULL OR char_length(last_error_code) <= 128),
    last_sanitized_error TEXT NULL CHECK (last_sanitized_error IS NULL OR char_length(last_sanitized_error) <= 1024),
    completed_at TIMESTAMPTZ NULL,
    retained_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_tenant_managed_service_operations_completed CHECK (
        (state IN ('succeeded', 'terminal_failed')) = (completed_at IS NOT NULL)
    ),
    CONSTRAINT ck_tenant_managed_service_operations_retention CHECK (retained_until >= created_at)
);

CREATE TABLE IF NOT EXISTS tenant_managed_service_result_inbox (
    result_event_id UUID PRIMARY KEY,
    source_command_event_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    target_revision_id UUID NOT NULL,
    blueprint_revision_id UUID NOT NULL,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    generation BIGINT NOT NULL CHECK (generation > 0),
    attempt SMALLINT NOT NULL CHECK (attempt BETWEEN 0 AND 4),
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_sha256 BYTEA NOT NULL CHECK (octet_length(input_sha256) = 32),
    desired_spec_sha256 BYTEA NOT NULL CHECK (octet_length(desired_spec_sha256) = 32),
    outcome managed_service_result_outcome NOT NULL,
    error_code TEXT NULL CHECK (error_code IS NULL OR char_length(error_code) <= 128),
    sanitized_message TEXT NULL CHECK (sanitized_message IS NULL OR char_length(sanitized_message) <= 1024),
    observed_state managed_service_observed_state NOT NULL,
    safe_observed_output JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(safe_observed_output) = 'object'
        AND octet_length(safe_observed_output::text) <= 65536
    ),
    observed_state_version BIGINT NOT NULL CHECK (observed_state_version >= 0),
    completed_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retained_until TIMESTAMPTZ NOT NULL,
    CONSTRAINT ux_tenant_managed_service_result_inbox_source UNIQUE (operation_id, attempt, source_command_event_id),
    CONSTRAINT ck_tenant_managed_service_result_inbox_retention CHECK (retained_until >= received_at)
);

CREATE TABLE IF NOT EXISTS tenant_managed_service_deletion_fences (
    instance_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retained_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (instance_id, operation_id, generation),
    CONSTRAINT ck_tenant_managed_service_deletion_fences_retention CHECK (retained_until >= deleted_at)
);

DO $$
BEGIN
    ALTER TABLE tenant_managed_service_instances
        ADD CONSTRAINT fk_tenant_managed_service_instances_active_revision
        FOREIGN KEY (active_revision_id, id)
        REFERENCES tenant_managed_service_instance_revisions(id, instance_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE tenant_managed_service_instances
        ADD CONSTRAINT fk_tenant_managed_service_instances_pending_revision
        FOREIGN KEY (pending_revision_id, id)
        REFERENCES tenant_managed_service_instance_revisions(id, instance_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_instances_workspace_state
    ON tenant_managed_service_instances(workspace_id, state, updated_at DESC);
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_revisions_zone
    ON tenant_managed_service_instance_revisions(zone_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_result_inbox_retention
    ON tenant_managed_service_result_inbox(retained_until, received_at);
CREATE INDEX IF NOT EXISTS ix_tenant_managed_service_deletion_fences_retention
    ON tenant_managed_service_deletion_fences(retained_until, deleted_at);
