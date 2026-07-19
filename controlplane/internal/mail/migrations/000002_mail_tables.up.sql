-- [COMMENT]: Personal và Tenant dùng namespace vật lý riêng; không cần scope discriminator hoặc owner audit ở Personal.
CREATE TABLE IF NOT EXISTS personal_mail_templates (
    id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    current_version BIGINT NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    template_revision BIGINT NOT NULL DEFAULT 0 CHECK (template_revision >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_personal_mail_template_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS personal_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES personal_mail_templates(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    subject_template VARCHAR(998) NOT NULL,
    html_template TEXT NOT NULL,
    content_sha256 BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, version),
    CONSTRAINT ck_personal_mail_template_body CHECK (html_template <> ''),
    CONSTRAINT ck_personal_mail_template_hash CHECK (octet_length(content_sha256) = 32)
);

CREATE TABLE IF NOT EXISTS tenant_mail_templates (
    id VARCHAR(128) PRIMARY KEY, workspace_id UUID NOT NULL, code VARCHAR(63) NOT NULL, name VARCHAR(255) NOT NULL,
    current_version BIGINT NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    template_revision BIGINT NOT NULL DEFAULT 0 CHECK (template_revision >= 0),
    created_by UUID NOT NULL, updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_tenant_mail_template_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS tenant_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES tenant_mail_templates(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0), subject_template VARCHAR(998) NOT NULL,
    html_template TEXT NOT NULL CHECK (html_template <> ''), content_sha256 BYTEA NOT NULL CHECK (octet_length(content_sha256)=32),
    created_by UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(template_id,version)
);

-- [COMMENT]: IAM system mail không mang ownership giả và không lẫn với customer template tables.
CREATE TABLE IF NOT EXISTS system_mail_templates (
    id VARCHAR(128) PRIMARY KEY, name VARCHAR(255) NOT NULL, current_version BIGINT NOT NULL CHECK(current_version>0), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS system_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES system_mail_templates(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK(version>0), subject_template VARCHAR(998) NOT NULL, html_template TEXT NOT NULL CHECK(html_template<>''),
    content_sha256 BYTEA NOT NULL CHECK(octet_length(content_sha256)=32), created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(template_id,version)
);

-- [COMMENT]: COW là invariant tại database, không chỉ convention ở service/repository.
CREATE OR REPLACE FUNCTION reject_mail_template_version_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF current_setting('mail.allow_template_version_mutation', true) = 'on' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION '% is immutable; publish a new version', TG_TABLE_NAME
        USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;

DO $$
BEGIN
    CREATE TRIGGER trg_personal_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON personal_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
    CREATE TRIGGER trg_tenant_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON tenant_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
    CREATE TRIGGER trg_system_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON system_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
END $$;

-- [COMMENT]: Consumer authorization boundary là Workspace. Zone không nằm trong row này; mỗi mutation
-- phải resolve/cross-check X-Zone-ID với Workspace rồi ghi routing_scope zone:<uuid> vào mail outbox.
CREATE TABLE IF NOT EXISTS mail_consumers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    source_type mail_source_type NOT NULL,
    broker_resource_id UUID NOT NULL,
    source_config_ref VARCHAR(512) NOT NULL,
    topic VARCHAR(249) NOT NULL,
    consumer_group VARCHAR(255) NOT NULL,
    mapping_json JSONB NOT NULL,
    template_id VARCHAR(128) NOT NULL,
    template_version BIGINT NOT NULL CHECK (template_version > 0),
    sender_profile_id VARCHAR(128) NOT NULL,
    sender_version BIGINT NOT NULL CHECK (sender_version > 0),
    desired_state mail_consumer_desired_state NOT NULL DEFAULT 'paused',
    parallelism INTEGER NOT NULL DEFAULT 1 CHECK (parallelism BETWEEN 1 AND 256),
    config_version BIGINT NOT NULL DEFAULT 1 CHECK (config_version > 0),
    config_sha256 BYTEA NOT NULL,
    deleted_at TIMESTAMPTZ NULL,
    created_by UUID NULL,
    updated_by UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_mail_consumer_mapping_object CHECK (jsonb_typeof(mapping_json) = 'object'),
    CONSTRAINT ck_mail_consumer_config_hash CHECK (octet_length(config_sha256) = 32),
    CONSTRAINT ck_mail_consumer_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'),
    CONSTRAINT ck_mail_consumer_delete_state CHECK (
        (desired_state = 'deleted' AND deleted_at IS NOT NULL)
        OR (desired_state <> 'deleted' AND deleted_at IS NULL)
    )
);

-- [COMMENT]: Runtime report là per-instance lease/heartbeat read model, không sửa desired state.
CREATE TABLE IF NOT EXISTS mail_consumer_runtime_reports (
    consumer_id UUID NOT NULL REFERENCES mail_consumers(id) ON DELETE CASCADE,
    instance_id VARCHAR(255) NOT NULL,
    config_version BIGINT NOT NULL CHECK (config_version > 0),
    runtime_state mail_consumer_runtime_state NOT NULL,
    runtime_generation BIGINT NOT NULL CHECK (runtime_generation > 0),
    report_sequence BIGINT NOT NULL CHECK (report_sequence > 0),
    consumer_lag BIGINT NOT NULL DEFAULT 0 CHECK (consumer_lag >= 0),
    error_code VARCHAR(100) NULL,
    error_message VARCHAR(1024) NULL,
    reported_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (consumer_id, instance_id),
    CONSTRAINT ck_mail_runtime_expiry CHECK (expires_at > reported_at)
);

-- [COMMENT]: Durable inbox dedupe; zone là metadata của result stream, không phải field do DP tự khai.
CREATE TABLE IF NOT EXISTS mail_result_inbox (
    event_id UUID PRIMARY KEY,
    zone_id UUID NOT NULL,
    payload BYTEA NOT NULL,
    payload_schema_version INTEGER NOT NULL CHECK (payload_schema_version > 0),
    status mail_result_inbox_status NOT NULL DEFAULT 'PENDING',
    error_code VARCHAR(100) NULL,
    error_message VARCHAR(1024) NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at TIMESTAMPTZ NULL,
    CONSTRAINT ck_mail_result_inbox_applied CHECK (
        (status = 'PENDING' AND applied_at IS NULL)
        OR (status IN ('APPLIED', 'REJECTED') AND applied_at IS NOT NULL)
    )
);

-- [COMMENT]: Workspace được derive từ retained consumer và denormalize để mọi history query bắt buộc scope sớm.
CREATE TABLE IF NOT EXISTS mail_submissions (
    submission_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    consumer_id UUID NOT NULL REFERENCES mail_consumers(id) ON DELETE RESTRICT,
    consumer_config_version BIGINT NOT NULL CHECK (consumer_config_version > 0),
    topic VARCHAR(249) NOT NULL,
    partition_id INTEGER NOT NULL CHECK (partition_id >= 0),
    offset_id BIGINT NOT NULL CHECK (offset_id >= 0),
    recipient_index INTEGER NOT NULL DEFAULT 0 CHECK (recipient_index >= 0),
    external_message_id VARCHAR(512) NULL,
    recipient_ciphertext BYTEA NULL,
    recipient_masked VARCHAR(320) NOT NULL,
    template_id VARCHAR(128) NOT NULL,
    template_version BIGINT NOT NULL CHECK (template_version > 0),
    sender_profile_id VARCHAR(128) NOT NULL,
    sender_version BIGINT NOT NULL CHECK (sender_version > 0),
    current_status mail_execution_status NOT NULL,
    current_state_version BIGINT NOT NULL CHECK (current_state_version > 0),
    jmap_submission_id VARCHAR(512) NULL,
    rendered_content_sha256 BYTEA NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (consumer_id, topic, partition_id, offset_id, recipient_index),
    CONSTRAINT ck_mail_submission_hash CHECK (
        rendered_content_sha256 IS NULL OR octet_length(rendered_content_sha256) = 32
    )
);

-- [COMMENT]: Append-only attempt/state history; monotonic head update được thực hiện ở Phase 9.
CREATE TABLE IF NOT EXISTS mail_delivery_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    submission_id UUID NOT NULL REFERENCES mail_submissions(submission_id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    state_version BIGINT NOT NULL CHECK (state_version > 0),
    status mail_execution_status NOT NULL,
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    error_code VARCHAR(100) NULL,
    error_message VARCHAR(1024) NULL,
    jmap_submission_id VARCHAR(512) NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (submission_id, state_version)
);

COMMENT ON TABLE mail_consumers IS 'Workspace-scoped desired state for broker mail runtimes; Zone is carried only by routing_scope in the mail outbox envelope.';
