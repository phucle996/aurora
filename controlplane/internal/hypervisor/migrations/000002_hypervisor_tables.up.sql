-- Resource tables are declared before transport/operation records. A terminal
-- VM provisioning failure retains the personal_vms row in FAILED together with
-- its outbox fence for truthful recovery and duplicate-result settlement.
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

-- Cost owns the catalog; Hypervisor keeps this immutable projection only so
-- the VM create CTE can make its final durable decision without trusting a
-- browser payload or a volatile cache hit.
CREATE TABLE IF NOT EXISTS hypervisor_resource_plan_revisions (
    revision_id UUID PRIMARY KEY,
    plan_id UUID NOT NULL,
    revision_number BIGINT NOT NULL,
    code VARCHAR(128) NOT NULL,
    display_name VARCHAR(256) NOT NULL,
    description TEXT NOT NULL,
    billing_model VARCHAR(32) NOT NULL,
    cpu_cores INT NOT NULL,
    memory_mib BIGINT NOT NULL,
    boot_disk_gib BIGINT NOT NULL,
    content_sha256 BYTEA NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL,
    effective_to TIMESTAMPTZ,
    state VARCHAR(16) NOT NULL,
    allow_create BOOLEAN NOT NULL,
    source_event_id UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ux_hypervisor_resource_plan_revision_number UNIQUE (plan_id, revision_number),
    CONSTRAINT ck_hypervisor_resource_plan_code CHECK (code ~ '^[a-z0-9][a-z0-9._-]{0,127}$'),
    CONSTRAINT ck_hypervisor_resource_plan_billing_model CHECK (billing_model = 'LIMIT_HOURLY'),
    CONSTRAINT ck_hypervisor_resource_plan_cpu CHECK (cpu_cores BETWEEN 1 AND 1024),
    CONSTRAINT ck_hypervisor_resource_plan_memory CHECK (memory_mib BETWEEN 1 AND 4194304),
    CONSTRAINT ck_hypervisor_resource_plan_boot_disk CHECK (boot_disk_gib BETWEEN 1 AND 65536),
    CONSTRAINT ck_hypervisor_resource_plan_hash CHECK (octet_length(content_sha256) = 32),
    CONSTRAINT ck_hypervisor_resource_plan_state CHECK (state IN ('ACTIVE', 'RETIRED')),
    CONSTRAINT ck_hypervisor_resource_plan_window CHECK (effective_to IS NULL OR effective_to > effective_from)
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
    resource_plan_id UUID NOT NULL,
    resource_plan_revision_id UUID NOT NULL,
    resource_plan_revision_number BIGINT NOT NULL,
    resource_plan_content_sha256 BYTEA NOT NULL,
    cpu_cores INT NOT NULL,
    memory_mb BIGINT NOT NULL,
    boot_disk_gb BIGINT NOT NULL,
    disk_gb BIGINT NOT NULL,
    additional_disk_sizes_gb BIGINT[] NOT NULL DEFAULT '{}',
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
    CONSTRAINT ck_hypervisor_personal_vms_resource_plan_revision CHECK (resource_plan_revision_number > 0),
    CONSTRAINT ck_hypervisor_personal_vms_resource_plan_hash CHECK (octet_length(resource_plan_content_sha256) = 32),
    CONSTRAINT ck_hypervisor_personal_vms_disk
        CHECK (disk_gb BETWEEN boot_disk_gb AND 65536),
    CONSTRAINT ck_hypervisor_personal_vms_additional_disks
        CHECK (cardinality(additional_disk_sizes_gb) BETWEEN 0 AND 15
               AND array_position(additional_disk_sizes_gb, NULL) IS NULL),
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
    payload_key_id UUID NOT NULL,
    actor_user_id UUID,
    owner_id UUID NOT NULL,
    owner_type VARCHAR(16) NOT NULL,
    status hypervisor_outbox_status NOT NULL DEFAULT 'PENDING',
    completed_at TIMESTAMPTZ,
    job_version INT NOT NULL DEFAULT 1,
    resource_id TEXT NOT NULL,
    resource_name VARCHAR(255) NOT NULL,
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT NOT NULL DEFAULT 600,
    error_code VARCHAR(128),
    error_message VARCHAR(4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_hypervisor_outbox_job_topic
        CHECK (length(btrim(job_topic)) BETWEEN 1 AND 128),
    CONSTRAINT ck_hypervisor_outbox_owner_type
        CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_hypervisor_outbox_payload
        CHECK (octet_length(payload) BETWEEN 1 AND 1000000),
    CONSTRAINT ck_hypervisor_outbox_job_version CHECK (job_version > 0),
    CONSTRAINT ck_hypervisor_outbox_payload_version
        CHECK (payload_schema_version > 0),
    CONSTRAINT ck_hypervisor_outbox_resource
        CHECK (length(resource_id) BETWEEN 1 AND 512),
    CONSTRAINT ck_hypervisor_outbox_resource_name
        CHECK (length(btrim(resource_name)) BETWEEN 1 AND 255),
    CONSTRAINT ck_hypervisor_outbox_trace
        CHECK (trace_id IS NULL OR octet_length(trace_id) IN (0, 16)),
    CONSTRAINT ck_hypervisor_outbox_idle CHECK (idle BETWEEN 1 AND 86400),
    CONSTRAINT ck_hypervisor_outbox_zone_id
        CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE TABLE IF NOT EXISTS hypervisor_allocation_outbox (
    id BIGSERIAL PRIMARY KEY,
    source_job_id UUID NOT NULL UNIQUE,
    event_type VARCHAR(16) NOT NULL,
    resource_id UUID NOT NULL,
    owner_id UUID NOT NULL,
    owner_type VARCHAR(16) NOT NULL,
    resource_name VARCHAR(255) NOT NULL,
    zone_id UUID NOT NULL,
    source_version BIGINT NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL,
    cpu_cores BIGINT NOT NULL,
    memory_mib BIGINT NOT NULL,
    disk_gib BIGINT NOT NULL,
    gpu_sku VARCHAR(128) NOT NULL DEFAULT '',
    gpu_count BIGINT NOT NULL DEFAULT 0,
    published_at TIMESTAMPTZ,
    locked_by VARCHAR(255),
    locked_until TIMESTAMPTZ,
    attempt_count INT NOT NULL DEFAULT 0,
    last_error VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ux_hypervisor_allocation_resource_version UNIQUE (resource_id, source_version),
    CONSTRAINT ck_hypervisor_allocation_event_type CHECK (event_type IN ('ACTIVATE', 'REVISE', 'TERMINATE')),
    CONSTRAINT ck_hypervisor_allocation_owner_type CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_hypervisor_allocation_identity CHECK (
        resource_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND owner_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND zone_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND length(btrim(resource_name)) BETWEEN 1 AND 255
    ),
    CONSTRAINT ck_hypervisor_allocation_version CHECK (source_version > 0),
    CONSTRAINT ck_hypervisor_allocation_limits CHECK (
        cpu_cores >= 0 AND memory_mib >= 0 AND disk_gib >= 0 AND gpu_count >= 0
        AND (
            (event_type IN ('ACTIVATE', 'REVISE') AND cpu_cores > 0 AND memory_mib > 0 AND disk_gib > 0)
            OR
            (event_type = 'TERMINATE' AND cpu_cores = 0 AND memory_mib = 0 AND disk_gib = 0 AND gpu_count = 0 AND gpu_sku = '')
        )
    ),
    CONSTRAINT ck_hypervisor_allocation_attempt CHECK (attempt_count >= 0),
    CONSTRAINT ck_hypervisor_allocation_lock CHECK (
        (locked_by IS NULL AND locked_until IS NULL)
        OR (locked_by IS NOT NULL AND locked_until IS NOT NULL)
    ),
    CONSTRAINT ck_hypervisor_allocation_published CHECK (
        published_at IS NULL OR (locked_by IS NULL AND locked_until IS NULL)
    )
);

CREATE TABLE IF NOT EXISTS commercial_admission_projection (
    owner_id UUID NOT NULL,
    owner_type VARCHAR(16) NOT NULL,
    policy_version BIGINT NOT NULL,
    decision VARCHAR(32) NOT NULL,
    restriction_reason VARCHAR(64),
    effective_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ,
    source_event_id UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, owner_type),
    CONSTRAINT ck_hypervisor_commercial_admission_owner_type CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_hypervisor_commercial_decision CHECK (decision IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_hypervisor_commercial_admission_reason CHECK (
        (decision = 'ALLOW' AND restriction_reason IS NULL)
        OR (decision = 'SUSPEND_BILLABLE' AND restriction_reason IS NOT NULL)
    ),
    CONSTRAINT ck_hypervisor_commercial_admission_version CHECK (policy_version > 0),
    CONSTRAINT ck_hypervisor_commercial_admission_window CHECK (valid_until IS NULL OR valid_until > effective_at)
);
