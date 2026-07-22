-- [COMMENT]: Personal và Tenant dùng namespace vật lý riêng; không cần scope discriminator hoặc owner audit ở Personal.
CREATE TABLE IF NOT EXISTS personal_mail_templates (
    id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    current_version BIGINT NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    template_revision BIGINT NOT NULL DEFAULT 0 CHECK (template_revision >= 0),
    -- [COMMENT]: Candidate thất bại bị hard-delete nhưng sequence không lùi để result cũ không va version mới.
    next_version BIGINT NOT NULL DEFAULT 1 CHECK (next_version > current_version),
    next_template_revision BIGINT NOT NULL DEFAULT 1 CHECK (next_template_revision > template_revision),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_personal_mail_template_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS personal_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES personal_mail_templates(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
	-- [COMMENT]: Revision/event giữ mapping chính xác khi candidate FAILED làm version sequence có gap.
	template_revision BIGINT NOT NULL CHECK (template_revision > 0),
	event_id UUID NOT NULL UNIQUE,
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
    next_version BIGINT NOT NULL DEFAULT 1 CHECK (next_version > current_version),
    next_template_revision BIGINT NOT NULL DEFAULT 1 CHECK (next_template_revision > template_revision),
    created_by UUID NOT NULL, updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_tenant_mail_template_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

CREATE TABLE IF NOT EXISTS tenant_mail_template_versions (
    template_id VARCHAR(128) NOT NULL REFERENCES tenant_mail_templates(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0), subject_template VARCHAR(998) NOT NULL,
	template_revision BIGINT NOT NULL CHECK (template_revision > 0), event_id UUID NOT NULL UNIQUE,
    html_template TEXT NOT NULL CHECK (html_template <> ''), content_sha256 BYTEA NOT NULL CHECK (octet_length(content_sha256)=32),
    created_by UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(template_id,version)
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
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_personal_mail_template_versions_immutable' AND NOT tgisinternal) THEN
        CREATE TRIGGER trg_personal_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON personal_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_tenant_mail_template_versions_immutable' AND NOT tgisinternal) THEN
        CREATE TRIGGER trg_tenant_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON tenant_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
    END IF;
END $$;

-- [COMMENT]: Consumer authorization boundary là Workspace. Zone không nằm trong row này; mỗi mutation
-- phải resolve/cross-check Zone với Workspace rồi snapshot UUID vào mail outbox.
CREATE TABLE IF NOT EXISTS mail_consumers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    code VARCHAR(63) NOT NULL,
    name VARCHAR(255) NOT NULL,
    source_type mail_source_type NOT NULL,
    broker_resource_id UUID NOT NULL,
    -- [COMMENT]: Credential/config broker là business data đã mã hóa; CP chỉ lưu ciphertext,
    -- JO và Zone NATS KV chỉ chuyển tiếp opaque bytes, không phụ thuộc Vault của auth.
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
    -- [COMMENT]: Delete dùng next_config_version làm fence nhưng không mutate counter này;
    -- chỉ update COW mới advance allocator, retry delete dùng operation ID mới.
    next_config_version BIGINT NOT NULL DEFAULT 2 CHECK (next_config_version > config_version),
    config_sha256 BYTEA NOT NULL,
    created_by UUID NULL,
    updated_by UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_mail_consumer_config_hash CHECK (octet_length(config_sha256) = 32),
    CONSTRAINT ck_mail_consumer_source_config_envelope CHECK (octet_length(source_config_envelope) <= 16384),
    CONSTRAINT ck_mail_consumer_enabled_source_config CHECK (
        desired_state <> 'enabled' OR octet_length(source_config_envelope) > 0
    ),
    CONSTRAINT ck_mail_consumer_code CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$')
);

-- [COMMENT]: Chỉ update/pause/resume tạo immutable candidate. Create nằm trực tiếp ở aggregate V1;
-- JO promote candidate sau Zone ACK hoặc hard-delete candidate khi FAILED.
CREATE TABLE IF NOT EXISTS mail_consumer_update_versions (
    consumer_id UUID NOT NULL REFERENCES mail_consumers(id) ON DELETE CASCADE,
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
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (consumer_id, config_version),
    CONSTRAINT ck_mail_consumer_update_envelope CHECK (octet_length(source_config_envelope) <= 16384),
    CONSTRAINT ck_mail_consumer_update_enabled_envelope CHECK (
        desired_state <> 'enabled' OR octet_length(source_config_envelope) > 0
    )
);

-- [COMMENT]: Runtime report là per-instance lease/heartbeat read model, không sửa desired state.
CREATE TABLE IF NOT EXISTS mail_consumer_runtime_reports (
    consumer_id UUID NOT NULL REFERENCES mail_consumers(id) ON DELETE CASCADE,
    -- [COMMENT]: Chỉ giữ event gần nhất; không tạo inbox row cho từng heartbeat gây write amplification.
    event_id UUID NOT NULL,
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

COMMENT ON TABLE mail_consumers IS 'Workspace-scoped desired state for broker mail runtimes; Zone UUID is carried only by the mail outbox envelope.';
