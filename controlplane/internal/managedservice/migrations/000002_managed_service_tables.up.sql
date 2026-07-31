-- [COMMENT]: Migration 000002_managed_service_tables.up.sql
-- Khởi tạo toàn bộ sơ đồ bảng (Tables DDL) của phân hệ Managed Service Platform theo đúng thứ tự phụ thuộc khóa ngoại.

-- 1. Bảng danh mục dịch vụ (Catalog Categories)
CREATE TABLE IF NOT EXISTS service_categories (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_catalog_state NOT NULL DEFAULT 'active',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Bảng định nghĩa loại dịch vụ (Service Definitions)
CREATE TABLE IF NOT EXISTS service_definitions (
    id UUID PRIMARY KEY,
    category_id UUID NOT NULL REFERENCES service_categories(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_catalog_state NOT NULL DEFAULT 'active',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_service_definitions_category_code UNIQUE (category_id, code)
);

-- 3. Bảng phiên bản dịch vụ (Service Versions)
CREATE TABLE IF NOT EXISTS service_versions (
    id UUID PRIMARY KEY,
    definition_id UUID NOT NULL REFERENCES service_definitions(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9.-]{0,62}$'),
    display_version TEXT NOT NULL CHECK (char_length(display_version) BETWEEN 1 AND 120),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_version_state NOT NULL DEFAULT 'available',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    deprecated_by TEXT NULL CHECK (deprecated_by IS NULL OR char_length(deprecated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_service_versions_definition_code UNIQUE (definition_id, code)
);

-- 4. Bảng mẫu thiết kế (Service Blueprints)
CREATE TABLE IF NOT EXISTS service_blueprints (
    id UUID PRIMARY KEY,
    version_id UUID NOT NULL REFERENCES service_versions(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9-]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    name_i18n JSONB NOT NULL CHECK (
        jsonb_typeof(name_i18n) = 'object'
        AND name_i18n ? 'en'
        AND octet_length(name_i18n::text) <= 8192
    ),
    description_i18n JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(description_i18n) = 'object'
        AND octet_length(description_i18n::text) <= 32768
    ),
    icon_key TEXT NOT NULL DEFAULT '' CHECK (char_length(icon_key) <= 128),
    state managed_service_catalog_state NOT NULL DEFAULT 'active',
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_revision_id UUID NULL,
    CONSTRAINT ux_service_blueprints_version UNIQUE (version_id),
    CONSTRAINT ux_service_blueprints_version_code UNIQUE (version_id, code)
);

-- 5. Bảng phiên bản sửa đổi mẫu thiết kế (Blueprint Revisions)
CREATE TABLE IF NOT EXISTS blueprint_revisions (
    id UUID PRIMARY KEY,
    blueprint_id UUID NOT NULL REFERENCES service_blueprints(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    state managed_service_blueprint_revision_state NOT NULL DEFAULT 'draft',
    template_yaml TEXT NOT NULL CHECK (octet_length(template_yaml) BETWEEN 1 AND 1048576),
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    contract_version TEXT NOT NULL DEFAULT 'platform-form/v1' CHECK (char_length(contract_version) BETWEEN 1 AND 64),
    contract_sha256 BYTEA NOT NULL CHECK (octet_length(contract_sha256) = 32),
    component_contract JSONB NOT NULL CHECK (jsonb_typeof(component_contract) = 'array'),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_schema JSONB NOT NULL CHECK (jsonb_typeof(input_schema) = 'object'),
    input_schema_sha256 BYTEA NOT NULL CHECK (octet_length(input_schema_sha256) = 32),
    ui_schema JSONB NOT NULL CHECK (jsonb_typeof(ui_schema) = 'object'),
    ui_schema_sha256 BYTEA NOT NULL CHECK (octet_length(ui_schema_sha256) = 32),
    safe_observed_output_schema JSONB NOT NULL CHECK (jsonb_typeof(safe_observed_output_schema) = 'object'),
    safe_observed_output_schema_sha256 BYTEA NOT NULL CHECK (octet_length(safe_observed_output_schema_sha256) = 32),
    zone_selector JSONB NOT NULL CHECK (jsonb_typeof(zone_selector) = 'object'),
    zone_selector_sha256 BYTEA NOT NULL CHECK (octet_length(zone_selector_sha256) = 32),
    capability_requirement JSONB NOT NULL CHECK (jsonb_typeof(capability_requirement) = 'object'),
    capability_requirement_sha256 BYTEA NOT NULL CHECK (octet_length(capability_requirement_sha256) = 32),
    row_version BIGINT NOT NULL DEFAULT 1 CHECK (row_version > 0),
    validated_row_version BIGINT NULL CHECK (validated_row_version IS NULL OR validated_row_version > 0),
    validation_contract_version TEXT NULL CHECK (validation_contract_version IS NULL OR char_length(validation_contract_version) BETWEEN 1 AND 64),
    validated_bundle_sha256 BYTEA NULL CHECK (validated_bundle_sha256 IS NULL OR octet_length(validated_bundle_sha256) = 32),
    validated_contract_sha256 BYTEA NULL CHECK (validated_contract_sha256 IS NULL OR octet_length(validated_contract_sha256) = 32),
    validated_at TIMESTAMPTZ NULL,
    validated_by TEXT NULL CHECK (validated_by IS NULL OR char_length(validated_by) BETWEEN 1 AND 128),
    created_by TEXT NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ NULL,
    retired_at TIMESTAMPTZ NULL,
    published_by TEXT NULL CHECK (published_by IS NULL OR char_length(published_by) BETWEEN 1 AND 128),
    retired_by TEXT NULL CHECK (retired_by IS NULL OR char_length(retired_by) BETWEEN 1 AND 128),
    CONSTRAINT ux_blueprint_revisions_blueprint_revision UNIQUE (blueprint_id, revision),
    CONSTRAINT ck_blueprint_revisions_publication_time CHECK (
        (state = 'draft' AND published_at IS NULL AND retired_at IS NULL AND published_by IS NULL AND retired_by IS NULL)
        OR (state = 'published' AND published_at IS NOT NULL AND retired_at IS NULL AND published_by IS NOT NULL AND retired_by IS NULL)
        OR (state = 'retired' AND published_at IS NOT NULL AND retired_at IS NOT NULL AND retired_at >= published_at AND published_by IS NOT NULL AND retired_by IS NOT NULL)
    ),
    CONSTRAINT ck_blueprint_revisions_validation_receipt CHECK (
        (validated_row_version IS NULL AND validation_contract_version IS NULL AND validated_bundle_sha256 IS NULL
            AND validated_contract_sha256 IS NULL AND validated_at IS NULL AND validated_by IS NULL)
        OR (validated_row_version = row_version AND validation_contract_version IS NOT NULL
            AND validated_bundle_sha256 IS NOT NULL AND validated_contract_sha256 IS NOT NULL
            AND validated_at IS NOT NULL AND validated_by IS NOT NULL)
    )
);

-- 6. Bảng nhật ký kiểm toán Catalog (Catalog Audit Events)
CREATE TABLE IF NOT EXISTS catalog_audit_events (
    id UUID PRIMARY KEY,
    actor_subject TEXT NOT NULL CHECK (char_length(actor_subject) BETWEEN 1 AND 128),
    critical_proof_id UUID NULL,
    action TEXT NOT NULL CHECK (char_length(action) BETWEEN 1 AND 96),
    record_kind TEXT NOT NULL CHECK (char_length(record_kind) BETWEEN 1 AND 64),
    record_id UUID NOT NULL,
    record_version BIGINT NOT NULL CHECK (record_version > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'rejected')),
    error_code TEXT NULL CHECK (error_code IS NULL OR char_length(error_code) <= 128),
    before_hash BYTEA NULL CHECK (before_hash IS NULL OR octet_length(before_hash) = 32),
    after_hash BYTEA NULL CHECK (after_hash IS NULL OR octet_length(after_hash) = 32),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 7. Bảng Instances dành cho Personal (Personal Managed Service Instances)
CREATE TABLE IF NOT EXISTS personal_managed_service_instances (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES hierarchy.personal_workspaces(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL,
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
    CONSTRAINT ux_personal_managed_service_instances_workspace_code UNIQUE (workspace_id, code),
    CONSTRAINT ck_personal_managed_service_instances_revision_heads CHECK (
        active_revision_id IS NOT NULL OR pending_revision_id IS NOT NULL
    ),
    CONSTRAINT ck_personal_managed_service_instances_distinct_heads CHECK (
        active_revision_id IS NULL OR pending_revision_id IS NULL OR active_revision_id <> pending_revision_id
    )
);

-- 8. Bảng Revisions dành cho Personal Instance
CREATE TABLE IF NOT EXISTS personal_managed_service_instance_revisions (
    id UUID PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES personal_managed_service_instances(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    blueprint_revision_id UUID NOT NULL REFERENCES blueprint_revisions(id) ON DELETE RESTRICT,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_schema_sha256 BYTEA NOT NULL CHECK (octet_length(input_schema_sha256) = 32),
    protected_command_payload BYTEA NOT NULL CHECK (octet_length(protected_command_payload) BETWEEN 1 AND 1000256),
    protected_command_payload_sha256 BYTEA NOT NULL CHECK (octet_length(protected_command_payload_sha256) = 32),
    payload_key_id UUID NOT NULL CHECK (payload_key_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    input_sha256 BYTEA NOT NULL CHECK (octet_length(input_sha256) = 32),
    desired_spec_sha256 BYTEA NOT NULL CHECK (octet_length(desired_spec_sha256) = 32),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_personal_managed_service_instance_revisions_instance_revision UNIQUE (instance_id, revision),
    CONSTRAINT ux_personal_managed_service_instance_revisions_id_instance UNIQUE (id, instance_id)
);

-- 9. Bảng Operations dành cho Personal Instance
CREATE TABLE IF NOT EXISTS personal_managed_service_operations (
    id UUID PRIMARY KEY,
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
    CONSTRAINT ck_personal_managed_service_operations_completed CHECK (
        (state IN ('succeeded', 'terminal_failed')) = (completed_at IS NOT NULL)
    ),
    CONSTRAINT ck_personal_managed_service_operations_retention CHECK (retained_until >= created_at)
);

-- 10. Bảng Result Inbox dành cho Personal
CREATE TABLE IF NOT EXISTS personal_managed_service_result_inbox (
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
    CONSTRAINT ux_personal_managed_service_result_inbox_source UNIQUE (operation_id, attempt, source_command_event_id),
    CONSTRAINT ck_personal_managed_service_result_inbox_retention CHECK (retained_until >= received_at)
);

-- 11. Bảng Deletion Fences dành cho Personal
CREATE TABLE IF NOT EXISTS personal_managed_service_deletion_fences (
    instance_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retained_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (instance_id, operation_id, generation),
    CONSTRAINT ck_personal_managed_service_deletion_fences_retention CHECK (retained_until >= deleted_at)
);

-- 12. Bảng Instances dành cho Tenant (Tenant Managed Service Instances)
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

-- 13. Bảng Revisions dành cho Tenant Instance
CREATE TABLE IF NOT EXISTS tenant_managed_service_instance_revisions (
    id UUID PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES tenant_managed_service_instances(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    blueprint_revision_id UUID NOT NULL REFERENCES blueprint_revisions(id) ON DELETE RESTRICT,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    template_bundle_sha256 BYTEA NOT NULL CHECK (octet_length(template_bundle_sha256) = 32),
    component_contract_sha256 BYTEA NOT NULL CHECK (octet_length(component_contract_sha256) = 32),
    input_schema_sha256 BYTEA NOT NULL CHECK (octet_length(input_schema_sha256) = 32),
    protected_command_payload BYTEA NOT NULL CHECK (octet_length(protected_command_payload) BETWEEN 1 AND 1000256),
    protected_command_payload_sha256 BYTEA NOT NULL CHECK (octet_length(protected_command_payload_sha256) = 32),
    payload_key_id UUID NOT NULL CHECK (payload_key_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    input_sha256 BYTEA NOT NULL CHECK (octet_length(input_sha256) = 32),
    desired_spec_sha256 BYTEA NOT NULL CHECK (octet_length(desired_spec_sha256) = 32),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_tenant_managed_service_instance_revisions_instance_revision UNIQUE (instance_id, revision),
    CONSTRAINT ux_tenant_managed_service_instance_revisions_id_instance UNIQUE (id, instance_id)
);

-- 14. Bảng Operations dành cho Tenant Instance
CREATE TABLE IF NOT EXISTS tenant_managed_service_operations (
    id UUID PRIMARY KEY,
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

-- 15. Bảng Result Inbox dành cho Tenant
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

-- 16. Bảng Deletion Fences dành cho Tenant
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

-- 17. Bảng Outbox vận chuyển sự kiện (Outbox Records)
CREATE TABLE IF NOT EXISTS managed_service_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    zone_id UUID NOT NULL REFERENCES hierarchy.zones(id) ON DELETE RESTRICT,
    job_topic VARCHAR(100) NOT NULL CHECK (job_topic = 'managed_service.instance.execute'),
    payload BYTEA NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 1000256),
    payload_key_id UUID NOT NULL,
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
    CONSTRAINT ck_managed_service_outbox_payload_key_id
        CHECK (payload_key_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT ck_managed_service_outbox_completed CHECK (
        (status IN ('SUCCEEDED', 'FAILED')) = (completed_at IS NOT NULL)
    )
);

-- 18. Thiết lập các Foreign Keys deferred cho bảng Blueprint & Instance Revisions
DO $$
BEGIN
    ALTER TABLE service_blueprints
        ADD CONSTRAINT fk_service_blueprints_published_revision
        FOREIGN KEY (published_revision_id) REFERENCES blueprint_revisions(id) ON DELETE RESTRICT;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE personal_managed_service_instances
        ADD CONSTRAINT fk_personal_managed_service_instances_active_revision
        FOREIGN KEY (active_revision_id, id)
        REFERENCES personal_managed_service_instance_revisions(id, instance_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE personal_managed_service_instances
        ADD CONSTRAINT fk_personal_managed_service_instances_pending_revision
        FOREIGN KEY (pending_revision_id, id)
        REFERENCES personal_managed_service_instance_revisions(id, instance_id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

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
