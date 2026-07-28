-- IAM migration layer 000002
-- Source of truth tables for auth, device-bound auth, MFA, RBAC, admin API keys, and audit.

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID user
    username varchar(64) NOT NULL, -- Username định danh thật, unique bằng unique index lower(username)
    email varchar(255) NOT NULL, -- Email đăng nhập, unique bằng unique index lower(email)
    phone varchar(32) NULL, -- Số điện thoại, nullable
    password_hash text NOT NULL, -- Password hash hiện tại, không lưu raw password
    status user_status NOT NULL DEFAULT 'pending-active', -- Trạng thái user: pending-active, active, suspended, disabled
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo user
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật user
);

COMMENT ON TABLE users IS 'Main global user account table. Stores login email, optional phone, current password hash, and status. Login is global and not scoped by project. Authentication history and reasons are tracked in audit_events.';
COMMENT ON COLUMN users.id IS 'Primary key of the user account. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN users.username IS 'Canonical global username identifier. Unique case-insensitively and used as a real user-facing handle.';
COMMENT ON COLUMN users.email IS 'Login email of the user. email_normalized is intentionally not stored. Uniqueness is enforced by a unique index on lower(email).';
COMMENT ON COLUMN users.phone IS 'Optional phone number of the user. Nullable and not required for login.';
COMMENT ON COLUMN users.password_hash IS 'Current password hash used for authentication. Raw password must never be stored.';
COMMENT ON COLUMN users.status IS 'Current account status. Allowed values are pending-active, active, suspended, and disabled.';
COMMENT ON COLUMN users.created_at IS 'Timestamp when the user record was created.';
COMMENT ON COLUMN users.updated_at IS 'Timestamp when the user record was last updated.';

CREATE TABLE IF NOT EXISTS password_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID record password history
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User sở hữu password hash cũ
    password_hash text NOT NULL, -- Hash password cũ, không lưu raw password
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm ghi nhận password hash này
);

COMMENT ON TABLE password_history IS 'Stores historical password hashes to prevent password reuse. Current password hash stays in users.password_hash.';
COMMENT ON COLUMN password_history.id IS 'Primary key of the password history record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN password_history.user_id IS 'User who owns this historical password hash. Deleted automatically when the related user is deleted.';
COMMENT ON COLUMN password_history.password_hash IS 'Historical password hash. Raw password must never be stored.';
COMMENT ON COLUMN password_history.created_at IS 'Timestamp when this password hash was recorded into password history.';

CREATE TABLE IF NOT EXISTS user_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, -- Primary key profile và FK tới users.id
    fullname varchar(120) NOT NULL, -- Họ tên hiển thị, bắt buộc
    avatar_url text NULL, -- URL avatar
    bio text NULL, -- Mô tả ngắn về user
    locale varchar(16) NOT NULL DEFAULT 'vi-VN', -- Locale mặc định vi-VN
    timezone varchar(64) NOT NULL DEFAULT 'Asia/Ho_Chi_Minh', -- Timezone mặc định Asia/Ho_Chi_Minh
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo profile
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật profile
);

COMMENT ON TABLE user_profiles IS 'Stores non-authentication display profile data such as fullname, avatar_url, bio, locale, and timezone.';
COMMENT ON COLUMN user_profiles.user_id IS 'Primary key of the profile and foreign key to users.id. Profile is deleted automatically when the related user is deleted.';
COMMENT ON COLUMN user_profiles.fullname IS 'User-visible full name. This is required and used only for display.';
COMMENT ON COLUMN user_profiles.avatar_url IS 'URL to the user avatar image.';
COMMENT ON COLUMN user_profiles.bio IS 'Short biography or profile description for the user.';
COMMENT ON COLUMN user_profiles.locale IS 'Preferred locale for the user interface. Default is vi-VN.';
COMMENT ON COLUMN user_profiles.timezone IS 'Preferred timezone for the user. Default is Asia/Ho_Chi_Minh.';
COMMENT ON COLUMN user_profiles.created_at IS 'Timestamp when the user profile was created.';
COMMENT ON COLUMN user_profiles.updated_at IS 'Timestamp when the user profile was last updated.';

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID refresh token record
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User sở hữu refresh token
    device_id uuid NOT NULL, -- Device liên quan tới refresh token
    token_hash text NOT NULL, -- Hash refresh token, không lưu raw token
    tenant_id uuid NULL, -- Tenant context nếu có, phase đầu thường là NULL
    issued_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm phát hành refresh token
    expires_at timestamptz NOT NULL, -- Thời điểm refresh token hết hạn
    used_at timestamptz NULL, -- Thời điểm token đã được sử dụng để xoay vòng
    revoked_at timestamptz NULL -- Thời điểm token bị thu hồi
);

COMMENT ON TABLE refresh_tokens IS 'Stores hashed refresh tokens for JWT refresh flow. There is no sessions table. Old tokens are marked as used during rotation, logout, or revoke flows.';
COMMENT ON COLUMN refresh_tokens.id IS 'Primary key of the refresh token record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN refresh_tokens.user_id IS 'User who owns this refresh token.';
COMMENT ON COLUMN refresh_tokens.device_id IS 'Optional device related to the refresh token. If the device is deleted, this reference is set to null.';
COMMENT ON COLUMN refresh_tokens.token_hash IS 'Hash of the refresh token. Raw refresh token must never be stored.';
COMMENT ON COLUMN refresh_tokens.tenant_id IS 'Optional tenant context for future tenant-scoped login flows. In the initial global login phase this is typically null.';
COMMENT ON COLUMN refresh_tokens.issued_at IS 'Timestamp when the refresh token was issued.';
COMMENT ON COLUMN refresh_tokens.expires_at IS 'Timestamp when the refresh token expires.';
COMMENT ON COLUMN refresh_tokens.used_at IS 'Timestamp when this refresh token was consumed/used for rotation.';
COMMENT ON COLUMN refresh_tokens.revoked_at IS 'Timestamp when this refresh token was explicitly revoked.';

CREATE TABLE IF NOT EXISTS devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID device
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User sở hữu device
    device_name varchar(255) NOT NULL, -- Tên thiết bị
    device_type varchar(64) NULL, -- Loại thiết bị: browser/mobile/desktop/cli
    os_name varchar(128) NULL, -- Tên hệ điều hành
    browser_name varchar(128) NULL, -- Tên browser nếu là web login
    public_key text NOT NULL, -- Public key của device
    public_key_fingerprint varchar(255) NOT NULL, -- Fingerprint public key, unique theo user
    risk_flags jsonb NOT NULL DEFAULT '{}'::jsonb, -- Cờ rủi ro dạng key-value, không chứa secret
    revoked_at timestamptz NULL, -- Thời điểm revoke device
    client_device_id varchar(128) NULL, -- Persistent client device ID
    last_seen_ip inet NULL, -- IP gần nhất của device
    last_seen_user_agent text NULL, -- User-agent gần nhất
    last_seen_at timestamptz NULL, -- Thời điểm hoạt động gần nhất
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo device
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật device
);

COMMENT ON TABLE devices IS 'Stores user devices and device-bound authentication public keys. Server stores public_key only and never stores private_key.';
COMMENT ON COLUMN devices.id IS 'Primary key of the device record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN devices.user_id IS 'User who owns this device.';
COMMENT ON COLUMN devices.device_name IS 'Human-readable name of the device, for example Chrome on Linux.';
COMMENT ON COLUMN devices.device_type IS 'Type of the device such as browser, mobile, desktop, or cli.';
COMMENT ON COLUMN devices.os_name IS 'Operating system name of the device.';
COMMENT ON COLUMN devices.browser_name IS 'Browser name when the device is used through a web login flow.';
COMMENT ON COLUMN devices.public_key IS 'Public key registered for device-bound authentication. Private key must remain on the client device.';
COMMENT ON COLUMN devices.public_key_fingerprint IS 'Fingerprint of the stored public key. Unique per user.';
COMMENT ON COLUMN devices.risk_flags IS 'Risk flags for the device in key-value form. Must not contain raw secrets or token material.';
COMMENT ON COLUMN devices.revoked_at IS 'Timestamp when the device was revoked.';
COMMENT ON COLUMN devices.client_device_id IS 'Persistent opaque device identifier supplied by the client (X-Client-Device-Id) or bootstrapped by server. Identity key for repeat logins; never exposes devices.id.';
COMMENT ON COLUMN devices.last_seen_ip IS 'Most recent IP address seen from this device.';
COMMENT ON COLUMN devices.last_seen_user_agent IS 'Most recent user agent seen from this device.';
COMMENT ON COLUMN devices.last_seen_at IS 'Timestamp when this device was last seen active.';
COMMENT ON COLUMN devices.created_at IS 'Timestamp when the device record was created.';
COMMENT ON COLUMN devices.updated_at IS 'Timestamp when the device record was last updated.';

ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_device_id_fkey,
    ADD CONSTRAINT refresh_tokens_device_id_fkey
        FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE;



CREATE TABLE IF NOT EXISTS mfa_settings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID MFA setting
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User sở hữu MFA setting
    secret_ciphertext text NULL, -- Secret đã mã hóa, dùng cho TOTP
    secret_key_id varchar(255) NULL, -- ID key dùng để encrypt/decrypt secret
    disabled_at timestamptz NULL, -- Thời điểm disable MFA
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo setting
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật setting
    CONSTRAINT mfa_settings_user_id_key UNIQUE (user_id)
);

COMMENT ON TABLE mfa_settings IS 'Stores MFA settings per user. Plain secrets must never be stored.';
COMMENT ON COLUMN mfa_settings.id IS 'Primary key of the MFA setting record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN mfa_settings.user_id IS 'User who owns this MFA setting.';
COMMENT ON COLUMN mfa_settings.secret_ciphertext IS 'Encrypted MFA secret, typically used for TOTP. Plain secret must never be stored.';
COMMENT ON COLUMN mfa_settings.secret_key_id IS 'Identifier of the key used to encrypt or decrypt the MFA secret.';
COMMENT ON COLUMN mfa_settings.disabled_at IS 'Timestamp when this MFA setting was disabled.';
COMMENT ON COLUMN mfa_settings.created_at IS 'Timestamp when the MFA setting was created.';
COMMENT ON COLUMN mfa_settings.updated_at IS 'Timestamp when the MFA setting was last updated.';

CREATE TABLE IF NOT EXISTS mfa_challenges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID MFA challenge
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User cần verify MFA
    status challenge_status NOT NULL DEFAULT 'pending', -- Trạng thái challenge
    allowed_methods text[] NOT NULL DEFAULT ARRAY['totp', 'recovery_code'], -- Danh sách phương thức được dùng
    expires_at timestamptz NOT NULL, -- Thời điểm challenge hết hạn
    verified_at timestamptz NULL, -- Thời điểm verify thành công
    failed_attempts integer NOT NULL DEFAULT 0, -- Số lần nhập sai MFA code
    created_ip inet NULL, -- IP tạo challenge
    created_user_agent text NULL, -- User-agent tạo challenge
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo challenge
);

COMMENT ON TABLE mfa_challenges IS 'Stores MFA verification challenges during login or other security-sensitive flows. There is intentionally no session_id column because the system uses JWT.';
COMMENT ON COLUMN mfa_challenges.id IS 'Primary key of the MFA challenge record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN mfa_challenges.user_id IS 'User who must satisfy this MFA challenge.';
COMMENT ON COLUMN mfa_challenges.status IS 'Current lifecycle status of the MFA challenge.';
COMMENT ON COLUMN mfa_challenges.allowed_methods IS 'List of allowed MFA methods for this challenge. Defaults to totp and recovery_code.';
COMMENT ON COLUMN mfa_challenges.expires_at IS 'Timestamp when the MFA challenge expires.';
COMMENT ON COLUMN mfa_challenges.verified_at IS 'Timestamp when the MFA challenge was verified successfully.';
COMMENT ON COLUMN mfa_challenges.failed_attempts IS 'Number of failed MFA attempts for this challenge.';
COMMENT ON COLUMN mfa_challenges.created_ip IS 'IP address that created the MFA challenge.';
COMMENT ON COLUMN mfa_challenges.created_user_agent IS 'User agent that created the MFA challenge.';
COMMENT ON COLUMN mfa_challenges.created_at IS 'Timestamp when the MFA challenge was created.';

CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID recovery code
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User sở hữu recovery code
    code_hash text NOT NULL, -- Hash của recovery code
    code_hint varchar(255) NULL, -- Hint không nhạy cảm để nhận diện code
    used_at timestamptz NULL, -- Thời điểm code đã được dùng
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo recovery code
);

COMMENT ON TABLE mfa_recovery_codes IS 'Stores hashed MFA recovery codes. Each code may be used only once and raw codes must only be shown once at generation time.';
COMMENT ON COLUMN mfa_recovery_codes.id IS 'Primary key of the recovery code record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN mfa_recovery_codes.user_id IS 'User who owns this MFA recovery code.';
COMMENT ON COLUMN mfa_recovery_codes.code_hash IS 'Hash of the MFA recovery code. Raw recovery code must never be stored.';
COMMENT ON COLUMN mfa_recovery_codes.code_hint IS 'Non-sensitive hint used to identify the recovery code, for example the last few characters.';
COMMENT ON COLUMN mfa_recovery_codes.used_at IS 'Timestamp when the recovery code was used. Null means unused.';
COMMENT ON COLUMN mfa_recovery_codes.created_at IS 'Timestamp when the recovery code was created.';

CREATE TABLE IF NOT EXISTS permissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID permission
    module varchar(100) NOT NULL, -- Tên module (ví dụ: iam, compute)
    object varchar(100) NOT NULL, -- Đối tượng tác động (ví dụ: users, vps)
    behavior varchar(100) NOT NULL, -- Hành vi tác động (ví dụ: read, manage)
    description text NULL, -- Mô tả permission
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo permission
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật permission
    CONSTRAINT unique_module_object_behavior UNIQUE (module, object, behavior)
);

COMMENT ON TABLE permissions IS 'Stores granular static 3-level permission definitions (<module>:<object>:<behavior>).';
COMMENT ON COLUMN permissions.id IS 'Primary key of the permission record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN permissions.module IS 'Module category, e.g., hypervisor, billing, iam.';
COMMENT ON COLUMN permissions.object IS 'Target object inside the module, e.g., vps, invoice, user.';
COMMENT ON COLUMN permissions.behavior IS 'Action performed on the object, e.g., create, read, update, delete.';
COMMENT ON COLUMN permissions.description IS 'Optional human-readable description of the permission.';
COMMENT ON COLUMN permissions.created_at IS 'Timestamp when the permission was created.';
COMMENT ON COLUMN permissions.updated_at IS 'Timestamp when the permission was last updated.';

CREATE TABLE IF NOT EXISTS roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID role
    code varchar(255) NOT NULL UNIQUE, -- Role code unique (ví dụ: workspace_admin)
    name varchar(255) NOT NULL, -- Tên hiển thị role
    description text NULL, -- Mô tả role
    role_level integer NOT NULL DEFAULT 100, -- Hierarchy level của role, càng nhỏ càng cao
    scope varchar(32) NOT NULL DEFAULT 'platform', -- Phạm vi áp dụng: platform hoặc tenant
    created_by uuid NOT NULL REFERENCES users(id), -- ID người tạo vai trò
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo role
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật role
    CONSTRAINT roles_role_level_check CHECK (role_level >= 0)
);

COMMENT ON TABLE roles IS 'Stores system and custom roles with hierarchy levels.';
COMMENT ON COLUMN roles.id IS 'Primary key of the role record.';
COMMENT ON COLUMN roles.code IS 'Unique string identifier for the role.';
COMMENT ON COLUMN roles.name IS 'Display name of the role.';
COMMENT ON COLUMN roles.description IS 'Optional description of the role.';
COMMENT ON COLUMN roles.role_level IS 'Hierarchy ranking of the role. Lower is more powerful.';
COMMENT ON COLUMN roles.created_at IS 'Timestamp when the role was created.';
COMMENT ON COLUMN roles.updated_at IS 'Timestamp when the role was last updated.';

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE, -- ID của Role
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE, -- ID của Permission 3 cấp
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm gán permission vào role
    PRIMARY KEY (role_id, permission_id)
);

COMMENT ON TABLE role_permissions IS 'Maps roles to their granular 3-level static permissions.';
COMMENT ON COLUMN role_permissions.role_id IS 'Foreign key referencing the role.';
COMMENT ON COLUMN role_permissions.permission_id IS 'Foreign key referencing the 3-level permission.';
COMMENT ON COLUMN role_permissions.created_at IS 'Timestamp when mapped.';

CREATE TABLE IF NOT EXISTS user_role (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID user_role mapping
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- ID người dùng được gán role
    username varchar(64) NOT NULL, -- Cache username tĩnh bất biến để check nhanh
    workspace_id uuid NOT NULL, -- ID workspace áp dụng role (nil UUID đại diện platform scope)
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE, -- ID role được gán (liên kết với bảng roles)
    role_name varchar(255) NOT NULL, -- Cache tên hiển thị role
    role_level integer NOT NULL, -- Cache role level để check phân cấp nhanh
    list_perm bytea NOT NULL, -- Danh sách key 5 cấp tĩnh dạng binary (Protobuf serialized RoleEntry)
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm gán role
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật role
    CONSTRAINT unique_user_workspace_role UNIQUE (user_id, workspace_id, role_id)
);

COMMENT ON TABLE user_role IS 'Stores denormalized user roles and binary computed 5-level static permissions per workspace.';
COMMENT ON COLUMN user_role.id IS 'Primary key of the assignment.';
COMMENT ON COLUMN user_role.user_id IS 'User receiving the role mapping.';
COMMENT ON COLUMN user_role.username IS 'Cached canonical username of the user.';
COMMENT ON COLUMN user_role.workspace_id IS 'Workspace UUID scope (nil UUID for platform level).';
COMMENT ON COLUMN user_role.role_id IS 'Static Role ID from system taxonomy.';
COMMENT ON COLUMN user_role.role_name IS 'Cached name of the role.';
COMMENT ON COLUMN user_role.role_level IS 'Cached numeric hierarchy rank.';
COMMENT ON COLUMN user_role.list_perm IS 'Protobuf serialized binary RoleEntry containing pre-built 5-level static keys.';
COMMENT ON COLUMN user_role.created_at IS 'Timestamp of assignment.';
COMMENT ON COLUMN user_role.updated_at IS 'Timestamp of update.';

CREATE TABLE IF NOT EXISTS tenant_role (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID tenant_role mapping
    tenant_id uuid NOT NULL, -- ID tenant được gán role
    workspace_id uuid NOT NULL, -- ID workspace áp dụng role (nil UUID đại diện platform scope)
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE, -- ID role được gán (liên kết với bảng roles)
    role_name varchar(255) NOT NULL, -- Cache tên hiển thị role
    role_level integer NOT NULL, -- Cache role level để check phân cấp nhanh
    list_perm bytea NOT NULL, -- Danh sách key 5 cấp tĩnh dạng binary (Protobuf serialized RoleEntry)
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm gán role
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật role
    CONSTRAINT unique_tenant_workspace_role UNIQUE (tenant_id, workspace_id, role_id)
);

COMMENT ON TABLE tenant_role IS 'Stores denormalized tenant roles and binary computed 5-level static permissions per workspace.';
COMMENT ON COLUMN tenant_role.id IS 'Primary key of the assignment.';
COMMENT ON COLUMN tenant_role.tenant_id IS 'Tenant receiving the role mapping.';
COMMENT ON COLUMN tenant_role.workspace_id IS 'Workspace UUID scope (nil UUID for platform level).';
COMMENT ON COLUMN tenant_role.role_id IS 'Static Role ID from system taxonomy.';
COMMENT ON COLUMN tenant_role.role_name IS 'Cached name of the role.';
COMMENT ON COLUMN tenant_role.role_level IS 'Cached numeric hierarchy rank.';
COMMENT ON COLUMN tenant_role.list_perm IS 'Protobuf serialized binary RoleEntry containing pre-built 5-level static keys.';
COMMENT ON COLUMN tenant_role.created_at IS 'Timestamp of assignment.';
COMMENT ON COLUMN tenant_role.updated_at IS 'Timestamp of update.';

-- Index phục vụ query cực nhanh ở read-path không cần JOIN
CREATE INDEX IF NOT EXISTS idx_user_role_lookup ON user_role (user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_tenant_role_lookup ON tenant_role (tenant_id, workspace_id, role_id);
-- [COMMENT]: Platform assignment là single-role; unique partial index chặn hai transaction AssignUserRole cùng chèn hai role.
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_role_platform
    ON user_role (user_id)
    WHERE workspace_id = '00000000-0000-0000-0000-000000000000';


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
