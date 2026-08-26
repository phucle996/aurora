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

ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS address varchar(500) NULL;

COMMENT ON COLUMN user_profiles.address IS 'Optional user-managed postal or locality address. It is profile metadata and is never an authentication or recovery identifier.';

-- Long-lived opaque credential bound only to one active user/device pair.
-- Runtime tenant, workspace, Zone and role context are resolved at recovery time.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id uuid NOT NULL,
    token_hash text NOT NULL,
    issued_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CONSTRAINT refresh_tokens_user_device_uk UNIQUE (user_id, device_id)
);

COMMENT ON TABLE refresh_tokens IS 'Opaque user/device credentials; never stores active tenant or authorization context.';
COMMENT ON COLUMN refresh_tokens.device_id IS 'Required device binding. Device revocation invalidates and cascades the credential.';

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
ALTER TABLE external_identities ADD COLUMN IF NOT EXISTS linked_at timestamptz NOT NULL DEFAULT now();
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

-- [COMMENT]: Bounded outbox for the two reviewed IAM/Hierarchy lifecycle facts.
CREATE TABLE IF NOT EXISTS lifecycle_fact_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    event_type VARCHAR(128) NOT NULL,
    schema_version INT NOT NULL CHECK (schema_version > 0),
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id UUID NOT NULL,
    aggregate_version BIGINT NOT NULL CHECK (aggregate_version > 0),
    owner_id UUID NOT NULL,
    owner_type lifecycle_owner_type NOT NULL,
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
    CONSTRAINT ck_lifecycle_fact_outbox_event_type_format
        CHECK (event_type ~ '^[a-z0-9]+([._][a-z0-9]+)*\.v[1-9][0-9]*$'),
    CONSTRAINT ck_lifecycle_fact_outbox_trace_id
        CHECK (trace_id IS NULL OR octet_length(trace_id) = 16)
);

-- Resource-first revoke workflow: the same PostgreSQL transaction changes the
-- device state, removes refresh tokens and records the runtime command. ACR
-- receives the command only from the relay after this durable boundary commits.
CREATE TABLE IF NOT EXISTS device_runtime_revoke_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_device_ids TEXT[] NOT NULL CHECK (cardinality(client_device_ids) > 0),
    status VARCHAR(16) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PUBLISHING', 'PUBLISHED', 'DEAD')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE device_runtime_revoke_outbox_records IS 'Durable handoff from IAM device revoke mutations to ACR runtime session eviction.';

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

-- [COMMENT]: Platform role definitions remain context-free. Tenant-owned role
-- definitions live in tenant_roles so ownership cannot be inferred from a
-- mutable scope column.
CREATE TABLE IF NOT EXISTS platform_roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code varchar(255) NOT NULL UNIQUE,
    name varchar(255) NOT NULL,
    description text NULL,
    role_level integer NOT NULL DEFAULT 100,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT platform_roles_role_level_check CHECK (role_level >= 0)
);

CREATE TABLE IF NOT EXISTS platform_role_permissions (
    role_id uuid NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, permission_id)
);

-- [COMMENT]: A tenant role is an immutable V1 definition owned by exactly one
-- tenant. Permission rows stay normalized at three levels; only assignments
-- below contain the compiled five-level Protobuf projection.
CREATE TABLE IF NOT EXISTS tenant_roles (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES hierarchy.tenants(id) ON DELETE CASCADE,
    code varchar(100) NOT NULL,
    name varchar(255) NOT NULL,
    description text NULL,
    role_level integer NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenant_roles_role_level_check CHECK (role_level >= 3),
    CONSTRAINT tenant_roles_code_format CHECK (code ~ '^[a-z0-9_]+$'),
    CONSTRAINT tenant_roles_tenant_code_uk UNIQUE (tenant_id, code)
);

CREATE TABLE IF NOT EXISTS tenant_role_permissions (
    tenant_role_id uuid NOT NULL REFERENCES tenant_roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_role_id, permission_id)
);

-- [COMMENT]: Bảng gán vai trò người dùng theo workspace/platform kèm danh sách binary permissions đã biên dịch
CREATE TABLE IF NOT EXISTS user_role (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username varchar(64) NOT NULL,
    workspace_id uuid NOT NULL,
    role_id uuid NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    role_name varchar(255) NOT NULL,
    role_level integer NOT NULL,
    role_version bigint NOT NULL DEFAULT 1 CHECK (role_version > 0),
    list_perm bytea NOT NULL,
    permission_hash bytea GENERATED ALWAYS AS (digest(list_perm, 'sha256')) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_role_permission_hash_size CHECK (octet_length(permission_hash) = 32),
    CONSTRAINT unique_user_workspace_role UNIQUE (user_id, workspace_id, role_id)
);

-- [COMMENT]: Durable tenant authorization binding. list_perm is a serialized
-- iam.rpc.RoleEntry whose keys already contain tenant/workspace context.
CREATE TABLE IF NOT EXISTS membership_role (
    id uuid PRIMARY KEY,
    membership_id uuid NOT NULL REFERENCES hierarchy.tenant_memberships(id) ON DELETE CASCADE,
    tenant_role_id uuid NOT NULL REFERENCES tenant_roles(id) ON DELETE RESTRICT,
    workspace_id uuid NOT NULL,
    role_name varchar(255) NOT NULL,
    role_level integer NOT NULL,
    role_version bigint NOT NULL CHECK (role_version > 0),
    list_perm bytea NOT NULL,
    permission_hash bytea GENERATED ALWAYS AS (digest(list_perm, 'sha256')) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT membership_role_permission_hash_size CHECK (octet_length(permission_hash) = 32),
    CONSTRAINT membership_role_scope_uk UNIQUE (membership_id, workspace_id)
);

-- [COMMENT]: Invitation stores only the token hash and the exact compiled grant
-- pinned at creation. Successful join hard-deletes this one-time record.
CREATE TABLE IF NOT EXISTS tenant_invitations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES hierarchy.tenants(id) ON DELETE CASCADE,
    inviter_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_role_id uuid NOT NULL REFERENCES tenant_roles(id) ON DELETE RESTRICT,
    workspace_id uuid NOT NULL,
    role_version bigint NOT NULL CHECK (role_version > 0),
    role_level integer NOT NULL,
    list_perm bytea NOT NULL,
    permission_hash bytea GENERATED ALWAYS AS (digest(list_perm, 'sha256')) STORED,
    token_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenant_invitations_permission_hash_size CHECK (octet_length(permission_hash) = 32),
    CONSTRAINT tenant_invitations_token_hash_size CHECK (octet_length(token_hash) = 32),
    CONSTRAINT tenant_invitations_distinct_actor CHECK (inviter_user_id <> target_user_id),
    CONSTRAINT tenant_invitations_token_hash_uk UNIQUE (token_hash),
    CONSTRAINT tenant_invitations_target_uk UNIQUE (tenant_id, target_user_id)
);

-- Index phục vụ query cực nhanh ở read-path không cần JOIN.
CREATE INDEX IF NOT EXISTS idx_user_role_lookup ON user_role (user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_membership_role_lookup ON membership_role (membership_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_tenant_roles_tenant_level ON tenant_roles (tenant_id, role_level, id);
CREATE INDEX IF NOT EXISTS idx_tenant_invitations_expiry ON tenant_invitations (expires_at, id);
CREATE INDEX IF NOT EXISTS idx_tenant_invitations_target ON tenant_invitations (target_user_id, expires_at);
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
