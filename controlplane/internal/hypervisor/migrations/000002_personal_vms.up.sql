CREATE TABLE IF NOT EXISTS personal_vms (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    zone_id UUID NOT NULL,
    owner_user_id UUID NOT NULL,
    name VARCHAR(63) NOT NULL,
    image VARCHAR(64) NOT NULL,
    cpu_cores INT NOT NULL,
    memory_mb BIGINT NOT NULL,
    disk_gb BIGINT NOT NULL,
    ssh_public_key TEXT NOT NULL,
    spec_hash BYTEA NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PROVISIONING',
    operation_id UUID NOT NULL,
    provider_name VARCHAR(80) NOT NULL,
    provider_node VARCHAR(128),
    provider_vmid BIGINT,
    ipv4_address INET,
    error_code VARCHAR(128),
    error_message VARCHAR(4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    provisioned_at TIMESTAMPTZ,
    CONSTRAINT ux_hypervisor_personal_vms_workspace_name UNIQUE (workspace_id, name),
    CONSTRAINT ux_hypervisor_personal_vms_operation UNIQUE (operation_id),
    CONSTRAINT ux_hypervisor_personal_vms_provider_name UNIQUE (provider_name),
    CONSTRAINT ck_hypervisor_personal_vms_status
        CHECK (status IN ('PROVISIONING', 'READY', 'FAILED')),
    CONSTRAINT ck_hypervisor_personal_vms_cpu CHECK (cpu_cores BETWEEN 1 AND 128),
    CONSTRAINT ck_hypervisor_personal_vms_memory CHECK (memory_mb BETWEEN 512 AND 1048576),
    CONSTRAINT ck_hypervisor_personal_vms_disk CHECK (disk_gb BETWEEN 8 AND 65536),
    CONSTRAINT ck_hypervisor_personal_vms_spec_hash CHECK (octet_length(spec_hash) = 32)
);

CREATE INDEX IF NOT EXISTS idx_hypervisor_personal_vms_scope
    ON personal_vms (workspace_id, zone_id, created_at DESC);

CREATE TABLE IF NOT EXISTS vm_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    routing_scope TEXT NOT NULL,
    job_topic VARCHAR(128) NOT NULL,
    payload BYTEA NOT NULL,
    actor_user_id UUID NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    completed_at TIMESTAMPTZ,
    job_version INT NOT NULL DEFAULT 1,
    resource_id TEXT NOT NULL,
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT NOT NULL DEFAULT 600,
    error_code VARCHAR(128),
    error_message VARCHAR(4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_hypervisor_vm_outbox_status
        CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT ck_hypervisor_vm_outbox_version CHECK (job_version > 0),
    CONSTRAINT ck_hypervisor_vm_outbox_payload_version CHECK (payload_schema_version > 0),
    CONSTRAINT ck_hypervisor_vm_outbox_trace
        CHECK (trace_id IS NULL OR octet_length(trace_id) IN (0, 16))
);

CREATE INDEX IF NOT EXISTS idx_hypervisor_vm_outbox_status
    ON vm_outbox_records (status, id ASC);
CREATE INDEX IF NOT EXISTS idx_hypervisor_vm_outbox_resource
    ON vm_outbox_records (resource_id, job_version DESC);
