-- [COMMENT]: Personal và Tenant dùng namespace vật lý riêng; không cần scope discriminator.
CREATE TABLE IF NOT EXISTS personal_mail_templates (
    id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    current_version BIGINT NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    template_revision BIGINT NOT NULL DEFAULT 0 CHECK (template_revision >= 0),
    next_version BIGINT NOT NULL DEFAULT 1 CHECK (next_version > current_version),
    next_template_revision BIGINT NOT NULL DEFAULT 1 CHECK (next_template_revision > template_revision),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_personal_mail_template_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS personal_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES personal_mail_templates(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    template_revision BIGINT NOT NULL CHECK (template_revision > 0),
    event_id UUID NOT NULL UNIQUE,
    subject_template VARCHAR(998) NOT NULL,
    -- [COMMENT]: html_template dạng BYTEA hỗ trợ nén binary zstd.
    html_template BYTEA NOT NULL,
    content_sha256 BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, version),
    CONSTRAINT ck_personal_mail_template_body CHECK (octet_length(html_template) > 0),
    CONSTRAINT ck_personal_mail_template_hash CHECK (octet_length(content_sha256) = 32)
);

CREATE TABLE IF NOT EXISTS tenant_mail_templates (
    id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    current_version BIGINT NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    template_revision BIGINT NOT NULL DEFAULT 0 CHECK (template_revision >= 0),
    next_version BIGINT NOT NULL DEFAULT 1 CHECK (next_version > current_version),
    next_template_revision BIGINT NOT NULL DEFAULT 1 CHECK (next_template_revision > template_revision),
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_tenant_mail_template_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS tenant_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES tenant_mail_templates(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    template_revision BIGINT NOT NULL CHECK (template_revision > 0),
    event_id UUID NOT NULL UNIQUE,
    subject_template VARCHAR(998) NOT NULL,
    -- [COMMENT]: html_template dạng BYTEA hỗ trợ nén binary zstd.
    html_template BYTEA NOT NULL CHECK (octet_length(html_template) > 0),
    content_sha256 BYTEA NOT NULL CHECK (octet_length(content_sha256) = 32),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, version)
);

-- [COMMENT]: Personal Consumer có namespace vật lý riêng và không lưu actor audit;
-- ownership được chứng minh qua personal workspace trong từng transaction.
CREATE TABLE IF NOT EXISTS personal_mail_consumers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    source_type mail_source_type NOT NULL,
    broker_resource_id UUID NOT NULL,
    source_config_envelope BYTEA NOT NULL DEFAULT ''::bytea,
    topic VARCHAR(249) NOT NULL,
    consumer_group VARCHAR(255) NOT NULL,
    template_id VARCHAR(128) NOT NULL,
    template_version BIGINT NOT NULL CHECK (template_version > 0),
    sender_profile_id VARCHAR(128) NOT NULL,
    sender_version BIGINT NOT NULL CHECK (sender_version > 0),
    desired_state mail_consumer_desired_state NOT NULL DEFAULT 'paused',
    parallelism INTEGER NOT NULL DEFAULT 1 CHECK (parallelism BETWEEN 1 AND 256),
    config_version BIGINT NOT NULL DEFAULT 1 CHECK (config_version > 0),
    next_config_version BIGINT NOT NULL DEFAULT 2 CHECK (next_config_version > config_version),
    config_sha256 BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_personal_mail_consumer_config_hash CHECK (octet_length(config_sha256) = 32),
    CONSTRAINT ck_personal_mail_consumer_source_config_envelope CHECK (octet_length(source_config_envelope) <= 16384),
    CONSTRAINT ck_personal_mail_consumer_enabled_source_config CHECK (
        desired_state <> 'enabled' OR octet_length(source_config_envelope) > 0
    ),
    CONSTRAINT ck_personal_mail_consumer_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS personal_mail_consumer_update_versions (
    consumer_id UUID NOT NULL REFERENCES personal_mail_consumers(id) ON DELETE CASCADE,
    config_version BIGINT NOT NULL CHECK (config_version > 1),
    event_id UUID NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    source_type mail_source_type NOT NULL,
    broker_resource_id UUID NOT NULL,
    source_config_envelope BYTEA NOT NULL,
    topic VARCHAR(249) NOT NULL,
    consumer_group VARCHAR(255) NOT NULL,
    template_id VARCHAR(128) NOT NULL,
    template_version BIGINT NOT NULL CHECK (template_version > 0),
    sender_profile_id VARCHAR(128) NOT NULL,
    sender_version BIGINT NOT NULL CHECK (sender_version > 0),
    desired_state mail_consumer_desired_state NOT NULL,
    parallelism INTEGER NOT NULL CHECK (parallelism BETWEEN 1 AND 256),
    config_sha256 BYTEA NOT NULL CHECK (octet_length(config_sha256) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (consumer_id, config_version),
    CONSTRAINT ck_personal_mail_consumer_update_envelope CHECK (octet_length(source_config_envelope) <= 16384),
    CONSTRAINT ck_personal_mail_consumer_update_enabled_envelope CHECK (
        desired_state <> 'enabled' OR octet_length(source_config_envelope) > 0
    )
);

-- [COMMENT]: Tenant Consumer giữ actor audit vì mutation diễn ra qua membership dùng chung.
CREATE TABLE IF NOT EXISTS tenant_mail_consumers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    source_type mail_source_type NOT NULL,
    broker_resource_id UUID NOT NULL,
    source_config_envelope BYTEA NOT NULL DEFAULT ''::bytea,
    topic VARCHAR(249) NOT NULL,
    consumer_group VARCHAR(255) NOT NULL,
    template_id VARCHAR(128) NOT NULL,
    template_version BIGINT NOT NULL CHECK (template_version > 0),
    sender_profile_id VARCHAR(128) NOT NULL,
    sender_version BIGINT NOT NULL CHECK (sender_version > 0),
    desired_state mail_consumer_desired_state NOT NULL DEFAULT 'paused',
    parallelism INTEGER NOT NULL DEFAULT 1 CHECK (parallelism BETWEEN 1 AND 256),
    config_version BIGINT NOT NULL DEFAULT 1 CHECK (config_version > 0),
    next_config_version BIGINT NOT NULL DEFAULT 2 CHECK (next_config_version > config_version),
    config_sha256 BYTEA NOT NULL,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_tenant_mail_consumer_config_hash CHECK (octet_length(config_sha256) = 32),
    CONSTRAINT ck_tenant_mail_consumer_source_config_envelope CHECK (octet_length(source_config_envelope) <= 16384),
    CONSTRAINT ck_tenant_mail_consumer_enabled_source_config CHECK (
        desired_state <> 'enabled' OR octet_length(source_config_envelope) > 0
    ),
    CONSTRAINT ck_tenant_mail_consumer_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS tenant_mail_consumer_update_versions (
    consumer_id UUID NOT NULL REFERENCES tenant_mail_consumers(id) ON DELETE CASCADE,
    config_version BIGINT NOT NULL CHECK (config_version > 1),
    event_id UUID NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    source_type mail_source_type NOT NULL,
    broker_resource_id UUID NOT NULL,
    source_config_envelope BYTEA NOT NULL,
    topic VARCHAR(249) NOT NULL,
    consumer_group VARCHAR(255) NOT NULL,
    template_id VARCHAR(128) NOT NULL,
    template_version BIGINT NOT NULL CHECK (template_version > 0),
    sender_profile_id VARCHAR(128) NOT NULL,
    sender_version BIGINT NOT NULL CHECK (sender_version > 0),
    desired_state mail_consumer_desired_state NOT NULL,
    parallelism INTEGER NOT NULL CHECK (parallelism BETWEEN 1 AND 256),
    config_sha256 BYTEA NOT NULL CHECK (octet_length(config_sha256) = 32),
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (consumer_id, config_version),
    CONSTRAINT ck_tenant_mail_consumer_update_envelope CHECK (octet_length(source_config_envelope) <= 16384),
    CONSTRAINT ck_tenant_mail_consumer_update_enabled_envelope CHECK (
        desired_state <> 'enabled' OR octet_length(source_config_envelope) > 0
    )
);

CREATE TABLE IF NOT EXISTS mail_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL,
    zone_id UUID NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    payload_key_id UUID NOT NULL,
    actor_user_id UUID,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PROCESSING', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMPTZ,
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(128),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA,
    idle INT,
    error_code VARCHAR(100),
    error_message TEXT,
    result_attempt INT NOT NULL DEFAULT 0 CHECK (result_attempt >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Durable handoff for the independent Mail consumer ownership workflow. JO
-- writes this row only in the same transaction that accepts the Zone result.
CREATE TABLE IF NOT EXISTS mail_consumer_billing_outbox (
    source_event_id UUID PRIMARY KEY,
    event_type VARCHAR(32) NOT NULL,
    resource_id UUID NOT NULL,
    resource_name VARCHAR(255) NOT NULL,
    owner_id UUID NOT NULL,
    owner_type VARCHAR(16) NOT NULL,
    zone_id UUID NOT NULL,
    source_version BIGINT NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    locked_by VARCHAR(255),
    locked_until TIMESTAMPTZ,
    attempt_count INT NOT NULL DEFAULT 0,
    last_error VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_mail_consumer_billing_version UNIQUE (resource_id, source_version),
    CONSTRAINT ck_mail_consumer_billing_event CHECK (event_type IN ('RESOURCE_CREATED','RESOURCE_DELETED')),
    CONSTRAINT ck_mail_consumer_billing_owner CHECK (owner_type IN ('PERSONAL','TENANT')),
    CONSTRAINT ck_mail_consumer_billing_version CHECK (
        (event_type='RESOURCE_CREATED' AND source_version=1)
        OR (event_type='RESOURCE_DELETED' AND source_version=2)
    ),
    CONSTRAINT ck_mail_consumer_billing_name CHECK (length(btrim(resource_name)) BETWEEN 1 AND 255),
    CONSTRAINT ck_mail_consumer_billing_attempt CHECK (attempt_count >= 0),
    CONSTRAINT ck_mail_consumer_billing_lock CHECK (
        (locked_by IS NULL AND locked_until IS NULL)
        OR (locked_by IS NOT NULL AND locked_until IS NOT NULL)
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
    CONSTRAINT ck_mail_commercial_admission_owner CHECK (owner_type IN ('PERSONAL','TENANT')),
    CONSTRAINT ck_mail_commercial_decision CHECK (decision IN ('ALLOW','SUSPEND_BILLABLE')),
    CONSTRAINT ck_mail_commercial_admission_reason CHECK (
        (decision='ALLOW' AND restriction_reason IS NULL)
        OR (decision='SUSPEND_BILLABLE' AND restriction_reason IS NOT NULL)
    ),
    CONSTRAINT ck_mail_commercial_admission_version CHECK (policy_version > 0),
    CONSTRAINT ck_mail_commercial_admission_window CHECK (valid_until IS NULL OR valid_until > effective_at)
);

-- [COMMENT]: JO reconciliation reads only the latest opaque projection. It no
-- longer reconstructs plaintext commands from Mail business columns.
CREATE TABLE IF NOT EXISTS mail_protected_projections (
    event_id UUID PRIMARY KEY,
    resource_kind VARCHAR(32) NOT NULL CHECK (resource_kind IN ('consumer', 'template')),
    resource_id VARCHAR(128) NOT NULL,
    zone_id UUID NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    payload_key_id UUID NOT NULL,
    source_outbox_id BIGINT NOT NULL UNIQUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION project_mail_protected_payload()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    kind VARCHAR(32);
BEGIN
    kind := split_part(NEW.job_topic, '.', 2);
    IF kind NOT IN ('consumer', 'template') THEN
        RAISE EXCEPTION 'unsupported mail protected projection topic: %', NEW.job_topic;
    END IF;
    INSERT INTO mail_protected_projections (
        event_id, resource_kind, resource_id, zone_id, job_topic, payload,
        payload_key_id, source_outbox_id, updated_at
    ) VALUES (
        NEW.event_id, kind, NEW.resource_id, NEW.zone_id, NEW.job_topic, NEW.payload,
        NEW.payload_key_id, NEW.id, now()
    )
    ON CONFLICT (event_id) DO NOTHING;
    RETURN NEW;
END;
$$;

-- [COMMENT]: Thêm DROP TRIGGER IF EXISTS để đảm bảo tính idempotent khi chạy migration song song nhiều replica
DROP TRIGGER IF EXISTS trg_mail_protected_projection ON mail_outbox_records;
CREATE TRIGGER trg_mail_protected_projection
AFTER INSERT ON mail_outbox_records
FOR EACH ROW EXECUTE FUNCTION project_mail_protected_payload();

CREATE TABLE IF NOT EXISTS personal_mail_template_projection_tombstones (
    template_id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    template_revision BIGINT NOT NULL CHECK (template_revision > 0),
    last_published_version BIGINT NOT NULL CHECK (last_published_version > 0),
    event_id UUID NOT NULL UNIQUE,
    deleted_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS tenant_mail_template_projection_tombstones (
    template_id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    template_revision BIGINT NOT NULL CHECK (template_revision > 0),
    last_published_version BIGINT NOT NULL CHECK (last_published_version > 0),
    event_id UUID NOT NULL UNIQUE,
    deleted_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS personal_mail_consumer_projection_tombstones (
    consumer_id UUID PRIMARY KEY,
    -- [COMMENT]: zone_id là routing snapshot; không FK cascade để không mất delete intent.
    zone_id UUID NOT NULL,
    config_version BIGINT NOT NULL CHECK (config_version > 0),
    delete_event_id UUID UNIQUE NOT NULL,
    tombstoned_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS tenant_mail_consumer_projection_tombstones (
    consumer_id UUID PRIMARY KEY,
    -- [COMMENT]: Tombstone phải sống độc lập với lifecycle của hierarchy zone.
    zone_id UUID NOT NULL,
    config_version BIGINT NOT NULL CHECK (config_version > 0),
    delete_event_id UUID UNIQUE NOT NULL,
    tombstoned_at TIMESTAMPTZ NOT NULL
);
