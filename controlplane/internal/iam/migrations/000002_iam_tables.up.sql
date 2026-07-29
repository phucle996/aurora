-- IAM migration layer 000002
-- Source of truth tables for auth, device-bound auth, MFA, RBAC, external identities, billing outbox, admin API keys, and audit.

-- [COMMENT]: Bảng thông tin người dùng chính
CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID user
    username varchar(64) NOT NULL, -- Username định danh thật, unique bằng unique index lower(username)
    email varchar(320) NOT NULL, -- Email đăng nhập, unique bằng unique index lower(email)
    phone varchar(32) NULL, -- Số điện thoại, nullable
    password_hash text NOT NULL, -- Password hash hiện tại, không lưu raw password
    status user_status NOT NULL DEFAULT 'pending-active', -- Trạng thái user: pending-active, active, suspended, disabled
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo user
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật user
);

COMMENT ON TABLE users IS 'Main global user account table. Stores login email, optional phone, current password hash, and status.';

-- [COMMENT]: Bảng lịch sử mật khẩu để ngăn tái sử dụng mật khẩu cũ
CREATE TABLE IF NOT EXISTS password_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- [COMMENT]: Bảng thông tin hồ sơ hiển thị của người dùng
CREATE TABLE IF NOT EXISTS user_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    fullname varchar(120) NOT NULL,
    avatar_url text NULL,
    bio text NULL,
    address varchar(500) NULL,
    locale varchar(16) NOT NULL DEFAULT 'vi-VN',
    timezone varchar(64) NOT NULL DEFAULT 'Asia/Ho_Chi_Minh',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN user_profiles.address IS 'Optional user-managed postal or locality address. It is profile metadata and is never an authentication or recovery identifier.';

-- [COMMENT]: Bảng lưu vết refresh token cho xác thực JWT
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id uuid NOT NULL,
    token_hash text NOT NULL,
    tenant_id uuid NULL,
    issued_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz NULL,
    revoked_at timestamptz NULL
);

-- [COMMENT]: Bảng lưu thiết bị đăng nhập và khóa công khai xác thực thiết bị
CREATE TABLE IF NOT EXISTS devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name varchar(255) NOT NULL,
    device_type varchar(64) NULL,
    os_name varchar(128) NULL,
    browser_name varchar(128) NULL,
    public_key text NOT NULL,
    public_key_fingerprint varchar(255) NOT NULL,
    risk_flags jsonb NOT NULL DEFAULT '{}'::jsonb,
    revoked_at timestamptz NULL,
    client_device_id varchar(128) NULL,
    last_seen_ip inet NULL,
    last_seen_user_agent text NULL,
    last_seen_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_device_id_fkey,
    ADD CONSTRAINT refresh_tokens_device_id_fkey
        FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE;

-- [COMMENT]: Bảng lưu thông tin căn cước xác thực từ các nhà cung cấp OAuth external (Google, GitHub)
CREATE TABLE IF NOT EXISTS external_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider varchar(32) NOT NULL CHECK (provider IN ('google', 'github')),
    provider_subject varchar(255) NOT NULL,
    provider_email varchar(320) NOT NULL,
    email_verified_at timestamptz NOT NULL,
    display_name varchar(120) NOT NULL,
    avatar_url varchar(2048) NULL,
    last_login_at timestamptz NULL,
    linked_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT external_identities_provider_subject_uk UNIQUE (provider, provider_subject)
);

COMMENT ON TABLE external_identities IS 'Verified external login identities.';
COMMENT ON COLUMN external_identities.linked_at IS 'Most recent successful explicit link time. Re-linking a previously revoked provider advances this timestamp.';

-- [COMMENT]: Bảng cài đặt MFA bảo mật của người dùng (Mỗi người dùng có tối đa 1 bản ghi cấu hình)
CREATE TABLE IF NOT EXISTS mfa_settings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    secret_ciphertext text NOT NULL,
    secret_key_id varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE mfa_settings IS 'One active MFA enrollment per user. Removing MFA hard-deletes this row.';

-- [COMMENT]: Bảng lưu danh sách mã khôi phục MFA khẩn cấp đã băm
CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mfa_setting_id uuid NOT NULL REFERENCES mfa_settings(id) ON DELETE CASCADE,
    code_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE mfa_recovery_codes IS 'Unused recovery-code hashes only. Consuming a code hard-deletes its row.';

-- [COMMENT]: Outbox dùng chung cho mọi domain event từ IAM sang Billing
CREATE TABLE IF NOT EXISTS billing_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    event_type VARCHAR(128) NOT NULL,
    schema_version INT NOT NULL CHECK (schema_version > 0),
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id UUID NOT NULL,
    aggregate_version BIGINT NOT NULL CHECK (aggregate_version > 0),
    owner_id UUID NOT NULL,
    owner_type billing_owner_type NOT NULL,
    actor_user_id UUID NOT NULL,
    payload BYTEA NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PUBLISHING', 'PUBLISHED', 'DEAD')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    trace_id BYTEA,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_billing_outbox_event_type_format
        CHECK (event_type ~ '^[a-z0-9]+([._][a-z0-9]+)*\.v[1-9][0-9]*$'),
    CONSTRAINT ck_billing_outbox_trace_id
        CHECK (trace_id IS NULL OR octet_length(trace_id) = 16)
);

-- [COMMENT]: Danh mục quyền tĩnh 3 cấp (<module>:<object>:<behavior>)
CREATE TABLE IF NOT EXISTS permissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    module varchar(100) NOT NULL,
    object varchar(100) NOT NULL,
    behavior varchar(100) NOT NULL,
    description text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT unique_module_object_behavior UNIQUE (module, object, behavior)
);

-- [COMMENT]: Bảng định nghĩa vai trò người dùng trong hệ thống
CREATE TABLE IF NOT EXISTS roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code varchar(255) NOT NULL UNIQUE,
    name varchar(255) NOT NULL,
    description text NULL,
    role_level integer NOT NULL DEFAULT 100,
    scope varchar(32) NOT NULL DEFAULT 'platform',
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT roles_role_level_check CHECK (role_level >= 0)
);

-- [COMMENT]: Bảng ánh xạ vai trò và quyền tĩnh
CREATE TABLE IF NOT EXISTS role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, permission_id)
);

-- [COMMENT]: Bảng gán vai trò người dùng theo workspace/platform kèm danh sách binary permissions đã biên dịch
CREATE TABLE IF NOT EXISTS user_role (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username varchar(64) NOT NULL,
    workspace_id uuid NOT NULL,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    role_name varchar(255) NOT NULL,
    role_level integer NOT NULL,
    list_perm bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT unique_user_workspace_role UNIQUE (user_id, workspace_id, role_id)
);

-- [COMMENT]: Bảng gán vai trò tổ chức (Tenant) theo workspace kèm binary permissions
CREATE TABLE IF NOT EXISTS tenant_role (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    role_name varchar(255) NOT NULL,
    role_level integer NOT NULL,
    list_perm bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT unique_tenant_workspace_role UNIQUE (tenant_id, workspace_id, role_id)
);

-- Index phục vụ query cực nhanh ở read-path không cần JOIN
CREATE INDEX IF NOT EXISTS idx_user_role_lookup ON user_role (user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_tenant_role_lookup ON tenant_role (tenant_id, workspace_id, role_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_role_platform
    ON user_role (user_id)
    WHERE workspace_id = '00000000-0000-0000-0000-000000000000';

-- [COMMENT]: Bảng quản lý thiết bị của quản trị viên
CREATE TABLE IF NOT EXISTS admin_devices (
    id uuid PRIMARY KEY,
    device_name varchar(128) NULL,
    device_type varchar(32) NULL,
    os_name varchar(64) NULL,
    browser_name varchar(64) NULL,
    public_key text NOT NULL,
    public_key_fingerprint varchar(128) NOT NULL,
    quarantined_at timestamptz NULL,
    revoked_at timestamptz NULL,
    last_seen_ip inet NULL,
    last_seen_user_agent text NULL,
    last_seen_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Sửa các ràng buộc CHECK regex lỗi định dạng khoảng ký tự '-'
ALTER TABLE hierarchy.tenants DROP CONSTRAINT IF EXISTS ck_tenants_code_format;
ALTER TABLE hierarchy.tenants ADD CONSTRAINT ck_tenants_code_format CHECK (code ~ '^[a-z0-9_\-]+$');

ALTER TABLE hierarchy.personal_workspaces DROP CONSTRAINT IF EXISTS ck_personal_workspaces_code_format;
ALTER TABLE hierarchy.personal_workspaces ADD CONSTRAINT ck_personal_workspaces_code_format CHECK (code ~ '^[a-z0-9_\-]+$');

ALTER TABLE hierarchy.tenant_workspaces DROP CONSTRAINT IF EXISTS ck_tenant_workspaces_code_format;
ALTER TABLE hierarchy.tenant_workspaces ADD CONSTRAINT ck_tenant_workspaces_code_format CHECK (code ~ '^[a-z0-9_\-]+$');
