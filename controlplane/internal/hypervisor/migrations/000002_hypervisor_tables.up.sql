-- Resource tables are declared before transport/operation records. A terminal
-- VM provisioning failure removes the personal_vms row but retains its outbox
-- fence for bounded diagnostics and duplicate-result settlement.
CREATE TABLE IF NOT EXISTS image_artifacts (
    id UUID PRIMARY KEY,
    zone_id UUID NOT NULL,
    name TEXT NOT NULL,
    code VARCHAR(128) NOT NULL,
    distribution VARCHAR(32) NOT NULL,
    release VARCHAR(32) NOT NULL,
    revision BIGINT NOT NULL,
    architecture VARCHAR(16) NOT NULL,
    format VARCHAR(16) NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 BYTEA NOT NULL,
    object_key VARCHAR(512) NOT NULL,
    state hypervisor_image_state NOT NULL DEFAULT 'UPLOADING',
    created_by VARCHAR(128) NOT NULL,
    provider_template_vmid BIGINT,
    error_code VARCHAR(128),
    error_message VARCHAR(4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at TIMESTAMPTZ,
    CONSTRAINT ux_hypervisor_image_zone_code_revision
        UNIQUE (zone_id, code, revision),
    CONSTRAINT ux_hypervisor_image_object_key UNIQUE (object_key),
    CONSTRAINT ck_hypervisor_image_name
        CHECK (length(btrim(name)) BETWEEN 1 AND 512),
    CONSTRAINT ck_hypervisor_image_code
        CHECK (code ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    CONSTRAINT ck_hypervisor_image_revision CHECK (revision > 0),
    CONSTRAINT ck_hypervisor_image_size CHECK (size_bytes > 0),
    CONSTRAINT ck_hypervisor_image_sha256 CHECK (octet_length(sha256) = 32),
    CONSTRAINT ck_hypervisor_image_architecture
        CHECK (architecture IN ('x86_64', 'aarch64')),
    CONSTRAINT ck_hypervisor_image_format CHECK (format IN ('qcow2', 'raw')),
    CONSTRAINT ck_hypervisor_image_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_hypervisor_image_provider_vmid
        CHECK (provider_template_vmid IS NULL OR provider_template_vmid > 0)
);

CREATE TABLE IF NOT EXISTS personal_vms (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    zone_id UUID NOT NULL,
    owner_user_id UUID NOT NULL,
    name VARCHAR(63) NOT NULL,
    image TEXT NOT NULL,
    image_id UUID NOT NULL,
    image_revision BIGINT NOT NULL,
    image_sha256 BYTEA NOT NULL,
    cpu_cores INT NOT NULL,
    memory_mb BIGINT NOT NULL,
    disk_gb BIGINT NOT NULL,
    ssh_public_key TEXT NOT NULL,
    spec_hash BYTEA NOT NULL,
    status hypervisor_vm_status NOT NULL DEFAULT 'PROVISIONING',
    operation_id UUID NOT NULL,
    provider_name VARCHAR(80) NOT NULL,
    provider_vmid BIGINT,
    ipv4_address INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    provisioned_at TIMESTAMPTZ,
    CONSTRAINT ux_hypervisor_personal_vms_workspace_name
        UNIQUE (workspace_id, name),
    CONSTRAINT ux_hypervisor_personal_vms_operation UNIQUE (operation_id),
    CONSTRAINT ux_hypervisor_personal_vms_provider_name UNIQUE (provider_name),
    CONSTRAINT ck_hypervisor_personal_vms_name
        CHECK (name ~ '^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$'
               AND name !~ '--'),
    CONSTRAINT ck_hypervisor_personal_vms_image_revision
        CHECK (image_revision > 0),
    CONSTRAINT ck_hypervisor_personal_vms_image_sha256
        CHECK (octet_length(image_sha256) = 32),
    CONSTRAINT ck_hypervisor_personal_vms_cpu
        CHECK (cpu_cores BETWEEN 1 AND 64),
    CONSTRAINT ck_hypervisor_personal_vms_memory
        CHECK (memory_mb BETWEEN 512 AND 262144 AND memory_mb % 256 = 0),
    CONSTRAINT ck_hypervisor_personal_vms_disk
        CHECK (disk_gb BETWEEN 8 AND 4096),
    CONSTRAINT ck_hypervisor_personal_vms_spec_hash
        CHECK (octet_length(spec_hash) = 32),
    CONSTRAINT ck_hypervisor_personal_vms_provider_vmid
        CHECK (provider_vmid IS NULL OR provider_vmid > 0),
    CONSTRAINT ck_hypervisor_personal_vms_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE TABLE IF NOT EXISTS hypervisor_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    zone_id UUID NOT NULL,
    job_topic VARCHAR(128) NOT NULL,
    payload BYTEA NOT NULL,
    actor_user_id UUID,
    status hypervisor_outbox_status NOT NULL DEFAULT 'PENDING',
    completed_at TIMESTAMPTZ,
    job_version INT NOT NULL DEFAULT 1,
    resource_id TEXT NOT NULL,
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT NOT NULL DEFAULT 600,
    error_code VARCHAR(128),
    error_message VARCHAR(4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_hypervisor_outbox_job_topic
        CHECK (length(btrim(job_topic)) BETWEEN 1 AND 128),
    CONSTRAINT ck_hypervisor_outbox_payload
        CHECK (octet_length(payload) BETWEEN 1 AND 1000000),
    CONSTRAINT ck_hypervisor_outbox_job_version CHECK (job_version > 0),
    CONSTRAINT ck_hypervisor_outbox_payload_version
        CHECK (payload_schema_version > 0),
    CONSTRAINT ck_hypervisor_outbox_resource
        CHECK (length(resource_id) BETWEEN 1 AND 512),
    CONSTRAINT ck_hypervisor_outbox_trace
        CHECK (trace_id IS NULL OR octet_length(trace_id) IN (0, 16)),
    CONSTRAINT ck_hypervisor_outbox_idle CHECK (idle BETWEEN 1 AND 86400),
    CONSTRAINT ck_hypervisor_outbox_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);
