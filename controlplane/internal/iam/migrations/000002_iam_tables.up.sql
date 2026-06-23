-- IAM migration layer 000002
-- Source of truth tables for auth, device-bound auth, MFA, RBAC, admin API keys, OAuth, and audit.

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
    public_key_alg varchar(64) NOT NULL DEFAULT 'Ed25519', -- Thuật toán public key
    public_key_fingerprint varchar(255) NOT NULL, -- Fingerprint public key, unique theo user
    status device_status NOT NULL DEFAULT 'new', -- Trạng thái device
    trusted_at timestamptz NULL, -- Thời điểm trust device
    quarantined_at timestamptz NULL, -- Thời điểm quarantine device
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
COMMENT ON COLUMN devices.public_key_alg IS 'Algorithm used by the stored public key. Default is Ed25519.';
COMMENT ON COLUMN devices.public_key_fingerprint IS 'Fingerprint of the stored public key. Unique per user.';
COMMENT ON COLUMN devices.status IS 'Current lifecycle/security status of the device. Allowed values: new, recognized, trusted, suspicious, revoked.';
COMMENT ON COLUMN devices.trusted_at IS 'Timestamp when the device was marked trusted.';
COMMENT ON COLUMN devices.quarantined_at IS 'Timestamp when the device was marked quarantined.';
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


CREATE TABLE IF NOT EXISTS device_challenges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID challenge
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User liên quan challenge
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE, -- Device cần verify
    nonce text NOT NULL, -- Nonce/challenge unique
    purpose varchar(120) NOT NULL, -- Mục đích challenge, enforce ở service layer
    status challenge_status NOT NULL DEFAULT 'pending', -- Trạng thái challenge
    request_method varchar(16) NULL, -- HTTP method bind vào challenge nếu cần
    request_path text NULL, -- Path bind vào challenge nếu cần
    payload_hash text NULL, -- Hash payload cần verify nếu dùng proof-of-possession
    expires_at timestamptz NOT NULL, -- Thời điểm challenge hết hạn
    verified_at timestamptz NULL, -- Thời điểm verify thành công
    consumed_at timestamptz NULL, -- Thời điểm consume challenge
    created_ip inet NULL, -- IP tạo challenge
    created_user_agent text NULL, -- User-agent tạo challenge
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo challenge
    CONSTRAINT device_challenges_nonce_key UNIQUE (nonce),
    CONSTRAINT device_challenges_expires_after_created_chk CHECK (expires_at > created_at)
);

COMMENT ON TABLE device_challenges IS 'Stores device verification challenges and nonces for device-bound proof flows such as refresh_token, device_trust, or sensitive actions.';
COMMENT ON COLUMN device_challenges.id IS 'Primary key of the device challenge record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN device_challenges.user_id IS 'User related to the device challenge.';
COMMENT ON COLUMN device_challenges.device_id IS 'Device that must prove possession or trust state.';
COMMENT ON COLUMN device_challenges.nonce IS 'Unique nonce or challenge value issued by the server.';
COMMENT ON COLUMN device_challenges.purpose IS 'Challenge purpose such as login_device_verify, refresh_token, sensitive_action, or device_trust. Enforced at the service layer.';
COMMENT ON COLUMN device_challenges.status IS 'Current lifecycle status of the challenge.';
COMMENT ON COLUMN device_challenges.request_method IS 'Optional HTTP method bound to the challenge.';
COMMENT ON COLUMN device_challenges.request_path IS 'Optional HTTP path bound to the challenge.';
COMMENT ON COLUMN device_challenges.payload_hash IS 'Optional hash of the signed payload for proof-of-possession.';
COMMENT ON COLUMN device_challenges.expires_at IS 'Timestamp when the challenge expires.';
COMMENT ON COLUMN device_challenges.verified_at IS 'Timestamp when the challenge was successfully verified.';
COMMENT ON COLUMN device_challenges.consumed_at IS 'Timestamp when the challenge was consumed and may no longer be reused.';
COMMENT ON COLUMN device_challenges.created_ip IS 'IP address that requested or created the challenge.';
COMMENT ON COLUMN device_challenges.created_user_agent IS 'User agent that requested or created the challenge.';
COMMENT ON COLUMN device_challenges.created_at IS 'Timestamp when the challenge was created.';

CREATE TABLE IF NOT EXISTS mfa_settings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID MFA setting
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User sở hữu MFA setting
    type mfa_type NOT NULL, -- Loại MFA: totp hoặc recovery_code
    status mfa_status NOT NULL DEFAULT 'pending', -- Trạng thái MFA setting
    secret_ciphertext text NULL, -- Secret đã mã hóa, dùng cho TOTP
    secret_key_id varchar(255) NULL, -- ID key dùng để encrypt/decrypt secret
    label varchar(255) NULL, -- Nhãn hiển thị
    confirmed_at timestamptz NULL, -- Thời điểm confirm MFA thành công
    disabled_at timestamptz NULL, -- Thời điểm disable MFA
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo setting
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật setting
    CONSTRAINT mfa_settings_user_type_key UNIQUE (user_id, type)
);

COMMENT ON TABLE mfa_settings IS 'Stores MFA settings per user such as TOTP configuration or recovery_code status. Plain secrets must never be stored.';
COMMENT ON COLUMN mfa_settings.id IS 'Primary key of the MFA setting record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN mfa_settings.user_id IS 'User who owns this MFA setting.';
COMMENT ON COLUMN mfa_settings.type IS 'Type of MFA setting. Allowed values are totp and recovery_code.';
COMMENT ON COLUMN mfa_settings.status IS 'Current lifecycle status of the MFA setting.';
COMMENT ON COLUMN mfa_settings.secret_ciphertext IS 'Encrypted MFA secret, typically used for TOTP. Plain secret must never be stored.';
COMMENT ON COLUMN mfa_settings.secret_key_id IS 'Identifier of the key used to encrypt or decrypt the MFA secret.';
COMMENT ON COLUMN mfa_settings.label IS 'User-visible label for the MFA setting, for example Authenticator App.';
COMMENT ON COLUMN mfa_settings.confirmed_at IS 'Timestamp when the user confirmed this MFA setting successfully.';
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
    code varchar(255) NOT NULL, -- Permission code unique
    name varchar(255) NOT NULL, -- Tên hiển thị của permission
    description text NULL, -- Mô tả permission
    resource varchar(255) NOT NULL, -- Resource mà permission tác động
    action varchar(120) NOT NULL, -- Hành động của permission
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo permission
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật permission
);

COMMENT ON TABLE permissions IS 'Stores system and custom permission definitions used by RBAC.';
COMMENT ON COLUMN permissions.id IS 'Primary key of the permission record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN permissions.code IS 'Unique permission code, for example compute.vps.create or workspace.members.invite.';
COMMENT ON COLUMN permissions.name IS 'Display name of the permission.';
COMMENT ON COLUMN permissions.description IS 'Optional human-readable description of the permission.';
COMMENT ON COLUMN permissions.resource IS 'Resource targeted by the permission, for example compute.vps or workspace.members.';
COMMENT ON COLUMN permissions.action IS 'Action represented by the permission, for example read, create, update, delete, invite, or manage.';
COMMENT ON COLUMN permissions.created_at IS 'Timestamp when the permission was created.';
COMMENT ON COLUMN permissions.updated_at IS 'Timestamp when the permission was last updated.';

CREATE TABLE IF NOT EXISTS roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID role
    code varchar(255) NOT NULL, -- Role code unique
    name varchar(255) NOT NULL, -- Tên hiển thị role
    description text NULL, -- Mô tả role
    scope_type role_scope_type NOT NULL, -- Scope role: platform, tenant, workspace
    is_system boolean NOT NULL DEFAULT true, -- Role hệ thống hay custom
    role_level integer NOT NULL DEFAULT 100, -- Hierarchy level của role, càng nhỏ càng cao
    is_protected boolean NOT NULL DEFAULT false, -- Không cho mutate/delete nếu true
    is_assignable boolean NOT NULL DEFAULT true, -- Có cho phép assign trực tiếp cho user không
    owner_tenant_id uuid NULL, -- Tenant sở hữu custom role, null với system/shared role
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo role
    updated_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cập nhật role
    CONSTRAINT roles_role_level_check CHECK (role_level >= 0)
);

COMMENT ON TABLE roles IS 'Stores RBAC roles. Scope type is limited to platform, tenant, or workspace. There is intentionally no project scope.';
COMMENT ON COLUMN roles.id IS 'Primary key of the role record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN roles.code IS 'Unique role code, for example platform_admin or workspace_member.';
COMMENT ON COLUMN roles.name IS 'Display name of the role.';
COMMENT ON COLUMN roles.description IS 'Optional human-readable description of the role.';
COMMENT ON COLUMN roles.scope_type IS 'Scope type of the role. Allowed values are platform, tenant, and workspace.';
COMMENT ON COLUMN roles.is_system IS 'Whether this role is a built-in system role or a custom role.';
COMMENT ON COLUMN roles.role_level IS 'Role hierarchy level for hidden authority checks. Lower value means higher authority.';
COMMENT ON COLUMN roles.is_protected IS 'When true, role is protected from regular mutation or deletion paths.';
COMMENT ON COLUMN roles.is_assignable IS 'When true, role can be assigned to users by assignment flows.';
COMMENT ON COLUMN roles.owner_tenant_id IS 'Optional owner tenant id for tenant-owned custom roles. Null for platform/shared roles.';
COMMENT ON COLUMN roles.created_at IS 'Timestamp when the role was created.';
COMMENT ON COLUMN roles.updated_at IS 'Timestamp when the role was last updated.';

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE, -- Role được gán permission
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE, -- Permission được gán vào role
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm gán permission vào role
    PRIMARY KEY (role_id, permission_id)
);

COMMENT ON TABLE role_permissions IS 'Join table that maps roles to the permissions they grant.';
COMMENT ON COLUMN role_permissions.role_id IS 'Role that receives the permission.';
COMMENT ON COLUMN role_permissions.permission_id IS 'Permission attached to the role.';
COMMENT ON COLUMN role_permissions.created_at IS 'Timestamp when the permission was attached to the role.';

CREATE TABLE IF NOT EXISTS user_role_assignments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID user-role assignment
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User được gán role
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE, -- Role được gán
    scope_type role_scope_type NOT NULL, -- Scope role: platform, tenant, workspace
    tenant_id uuid NULL, -- Tenant scope, nullable
    workspace_id uuid NULL, -- Workspace scope, nullable
    assigned_by uuid NULL REFERENCES users(id) ON DELETE SET NULL, -- Actor đã gán role
    assigned_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm gán role
    expires_at timestamptz NULL, -- Thời điểm role hết hạn
    revoked_at timestamptz NULL -- Thời điểm role bị revoke
);

COMMENT ON TABLE user_role_assignments IS 'Stores role assignments for users across platform, tenant, or workspace scope. There is intentionally no project_id column.';
COMMENT ON COLUMN user_role_assignments.id IS 'Primary key of the user role assignment record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN user_role_assignments.user_id IS 'User who receives the role assignment.';
COMMENT ON COLUMN user_role_assignments.role_id IS 'Role that is assigned to the user.';
COMMENT ON COLUMN user_role_assignments.scope_type IS 'Scope type of the assignment. Allowed values are platform, tenant, and workspace.';
COMMENT ON COLUMN user_role_assignments.tenant_id IS 'Tenant scope of the role assignment. Must be null for platform scope.';
COMMENT ON COLUMN user_role_assignments.workspace_id IS 'Workspace scope of the role assignment. Must be null for platform scope and for tenant-only scope.';
COMMENT ON COLUMN user_role_assignments.assigned_by IS 'User who created the role assignment. Nullable.';
COMMENT ON COLUMN user_role_assignments.assigned_at IS 'Timestamp when the role was assigned.';
COMMENT ON COLUMN user_role_assignments.expires_at IS 'Optional timestamp when the role assignment expires.';
COMMENT ON COLUMN user_role_assignments.revoked_at IS 'Optional timestamp when the role assignment was revoked.';

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

CREATE TABLE IF NOT EXISTS admin_api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- Singleton row ID admin API key
    key_hash text NOT NULL, -- Hash của admin API key
    created_by text NULL, -- Actor tạo hoặc rotate key
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo key
    expires_at timestamptz NOT NULL, -- Thời điểm key hết hạn
    CONSTRAINT admin_api_keys_expires_after_created_chk CHECK (expires_at > created_at)
);

COMMENT ON TABLE admin_api_keys IS 'Stores the singleton active hashed admin API key. Raw key must never be stored.';
COMMENT ON COLUMN admin_api_keys.id IS 'Primary key of the singleton admin API key row. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN admin_api_keys.key_hash IS 'Hash of the admin API key. Raw key must never be stored.';
COMMENT ON COLUMN admin_api_keys.created_by IS 'Optional free-text actor that created or rotated this key.';
COMMENT ON COLUMN admin_api_keys.created_at IS 'Timestamp when this admin API key row was created.';
COMMENT ON COLUMN admin_api_keys.expires_at IS 'Hard expiry timestamp of the active admin API key.';

CREATE TABLE IF NOT EXISTS oauth_clients (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- Internal ID oauth client
    client_id varchar(255) NOT NULL UNIQUE, -- Public OAuth client_id
    name varchar(255) NOT NULL, -- Tên app/client
    description text NULL, -- Mô tả app/client
    client_type oauth_client_type NOT NULL, -- Loại client: public hoặc confidential
    redirect_uris text[] NOT NULL DEFAULT ARRAY[]::text[], -- Danh sách redirect URI hợp lệ
    allowed_scopes text[] NOT NULL DEFAULT ARRAY[]::text[], -- Danh sách scopes được xin
    grant_types text[] NOT NULL DEFAULT ARRAY[]::text[], -- Grant types được phép
    response_types text[] NOT NULL DEFAULT ARRAY[]::text[], -- Response types được phép
    status oauth_client_status NOT NULL DEFAULT 'active', -- Trạng thái client
    owner_user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL, -- User sở hữu client
    tenant_id uuid NULL, -- Tenant client thuộc về
    workspace_id uuid NULL, -- Workspace client thuộc về
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo client
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật client
);

COMMENT ON TABLE oauth_clients IS 'Stores OAuth2 clients or applications registered with the system.';
COMMENT ON COLUMN oauth_clients.id IS 'Internal primary key of the OAuth client record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN oauth_clients.client_id IS 'Public OAuth client identifier. Must be unique.';
COMMENT ON COLUMN oauth_clients.name IS 'Display name of the OAuth client or application.';
COMMENT ON COLUMN oauth_clients.description IS 'Optional description of the OAuth client.';
COMMENT ON COLUMN oauth_clients.client_type IS 'OAuth client type. Allowed values are public and confidential.';
COMMENT ON COLUMN oauth_clients.redirect_uris IS 'Allowed redirect URIs for the OAuth client.';
COMMENT ON COLUMN oauth_clients.allowed_scopes IS 'OAuth scopes the client may request.';
COMMENT ON COLUMN oauth_clients.grant_types IS 'OAuth grant types allowed for the client, for example authorization_code or refresh_token.';
COMMENT ON COLUMN oauth_clients.response_types IS 'OAuth response types allowed for the client, for example code.';
COMMENT ON COLUMN oauth_clients.status IS 'Current lifecycle status of the OAuth client.';
COMMENT ON COLUMN oauth_clients.owner_user_id IS 'User who owns this OAuth client. Nullable.';
COMMENT ON COLUMN oauth_clients.tenant_id IS 'Optional tenant context that owns or contains the OAuth client.';
COMMENT ON COLUMN oauth_clients.workspace_id IS 'Optional workspace context that owns or contains the OAuth client.';
COMMENT ON COLUMN oauth_clients.created_at IS 'Timestamp when the OAuth client was created.';
COMMENT ON COLUMN oauth_clients.updated_at IS 'Timestamp when the OAuth client was last updated.';

CREATE TABLE IF NOT EXISTS oauth_client_secrets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID client secret
    client_id varchar(255) NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE, -- OAuth client_id sở hữu secret
    secret_prefix varchar(255) NOT NULL, -- Prefix để nhận diện secret
    secret_hash text NOT NULL, -- Hash của client secret
    secret_name varchar(255) NOT NULL, -- Tên secret
    expires_at timestamptz NULL, -- Thời điểm hết hạn secret
    revoked_at timestamptz NULL, -- Thời điểm revoke secret
    last_used_at timestamptz NULL, -- Lần dùng gần nhất
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo secret
);

COMMENT ON TABLE oauth_client_secrets IS 'Stores hashed secrets for OAuth confidential clients. Raw client secrets must never be stored.';
COMMENT ON COLUMN oauth_client_secrets.id IS 'Primary key of the OAuth client secret record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN oauth_client_secrets.client_id IS 'OAuth client_id that owns this secret.';
COMMENT ON COLUMN oauth_client_secrets.secret_prefix IS 'Prefix used to identify the OAuth client secret without storing the full raw secret.';
COMMENT ON COLUMN oauth_client_secrets.secret_hash IS 'Hash of the OAuth client secret. Raw secret must never be stored.';
COMMENT ON COLUMN oauth_client_secrets.secret_name IS 'Human-readable name of the OAuth client secret.';
COMMENT ON COLUMN oauth_client_secrets.expires_at IS 'Optional timestamp when the client secret expires.';
COMMENT ON COLUMN oauth_client_secrets.revoked_at IS 'Optional timestamp when the client secret was revoked.';
COMMENT ON COLUMN oauth_client_secrets.last_used_at IS 'Timestamp when the client secret was used most recently.';
COMMENT ON COLUMN oauth_client_secrets.created_at IS 'Timestamp when the client secret was created.';

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID authorization code record
    code_hash text NOT NULL, -- Hash authorization code
    client_id varchar(255) NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE, -- OAuth client_id
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User đã authorize
    tenant_id uuid NULL, -- Tenant context của authorization
    workspace_id uuid NULL, -- Workspace context của authorization
    redirect_uri text NOT NULL, -- Redirect URI dùng cho authorization code
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[], -- Scopes được cấp
    code_challenge text NULL, -- PKCE code challenge
    code_challenge_method varchar(32) NULL, -- PKCE method, ví dụ S256
    expires_at timestamptz NOT NULL, -- Thời điểm code hết hạn
    consumed_at timestamptz NULL, -- Thời điểm code đã được dùng đổi token
    revoked_at timestamptz NULL, -- Thời điểm code bị revoke
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo code
);

COMMENT ON TABLE oauth_authorization_codes IS 'Stores hashed OAuth authorization codes for Authorization Code Flow with optional PKCE support.';
COMMENT ON COLUMN oauth_authorization_codes.id IS 'Primary key of the OAuth authorization code record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN oauth_authorization_codes.code_hash IS 'Hash of the authorization code. Raw authorization code must never be stored.';
COMMENT ON COLUMN oauth_authorization_codes.client_id IS 'OAuth client_id that requested or owns the authorization code.';
COMMENT ON COLUMN oauth_authorization_codes.user_id IS 'User who granted authorization for this code.';
COMMENT ON COLUMN oauth_authorization_codes.tenant_id IS 'Optional tenant context for the authorization flow.';
COMMENT ON COLUMN oauth_authorization_codes.workspace_id IS 'Optional workspace context for the authorization flow.';
COMMENT ON COLUMN oauth_authorization_codes.redirect_uri IS 'Redirect URI used when issuing this authorization code.';
COMMENT ON COLUMN oauth_authorization_codes.scopes IS 'OAuth scopes granted to this authorization code.';
COMMENT ON COLUMN oauth_authorization_codes.code_challenge IS 'Optional PKCE code challenge sent by the client.';
COMMENT ON COLUMN oauth_authorization_codes.code_challenge_method IS 'Optional PKCE challenge method, for example S256.';
COMMENT ON COLUMN oauth_authorization_codes.expires_at IS 'Timestamp when the authorization code expires.';
COMMENT ON COLUMN oauth_authorization_codes.consumed_at IS 'Timestamp when the authorization code was successfully exchanged for tokens.';
COMMENT ON COLUMN oauth_authorization_codes.revoked_at IS 'Timestamp when the authorization code was revoked.';
COMMENT ON COLUMN oauth_authorization_codes.created_at IS 'Timestamp when the authorization code record was created.';

CREATE TABLE IF NOT EXISTS oauth_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID grant
    client_id varchar(255) NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE, -- OAuth client_id
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- User cấp quyền
    tenant_id uuid NULL, -- Tenant context của grant
    workspace_id uuid NULL, -- Workspace context của grant
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[], -- Danh sách scopes được cấp
    granted_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm cấp quyền
    revoked_at timestamptz NULL, -- Thời điểm revoke grant
    expires_at timestamptz NULL, -- Thời điểm grant hết hạn
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo grant
);

COMMENT ON TABLE oauth_grants IS 'Stores OAuth grants or user consent records for third-party clients.';
COMMENT ON COLUMN oauth_grants.id IS 'Primary key of the OAuth grant record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN oauth_grants.client_id IS 'OAuth client_id that received the grant.';
COMMENT ON COLUMN oauth_grants.user_id IS 'User who granted the OAuth scopes.';
COMMENT ON COLUMN oauth_grants.tenant_id IS 'Optional tenant context for the grant.';
COMMENT ON COLUMN oauth_grants.workspace_id IS 'Optional workspace context for the grant.';
COMMENT ON COLUMN oauth_grants.scopes IS 'Scopes granted by the user to the OAuth client.';
COMMENT ON COLUMN oauth_grants.granted_at IS 'Timestamp when the grant was issued.';
COMMENT ON COLUMN oauth_grants.revoked_at IS 'Timestamp when the grant was revoked.';
COMMENT ON COLUMN oauth_grants.expires_at IS 'Optional timestamp when the grant expires.';
COMMENT ON COLUMN oauth_grants.created_at IS 'Timestamp when the grant record was created.';

CREATE TABLE IF NOT EXISTS oauth_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID OAuth token record
    client_id varchar(255) NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE, -- OAuth client_id
    user_id uuid NULL REFERENCES users(id) ON DELETE CASCADE, -- User liên quan
    grant_id uuid NULL REFERENCES oauth_grants(id) ON DELETE SET NULL, -- Grant liên quan
    access_token_hash text NOT NULL, -- Hash access token
    refresh_token_hash text NULL, -- Hash refresh token
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[], -- Scopes token được cấp
    issued_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm phát hành token
    expires_at timestamptz NOT NULL, -- Thời điểm access token hết hạn
    rotated_at timestamptz NULL, -- Thời điểm token được rotate
    revoked_at timestamptz NULL, -- Thời điểm token bị revoke
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo record
);

COMMENT ON TABLE oauth_tokens IS 'Stores hashed OAuth access and refresh tokens issued to third-party clients.';
COMMENT ON COLUMN oauth_tokens.id IS 'Primary key of the OAuth token record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN oauth_tokens.client_id IS 'OAuth client_id that owns or received this token.';
COMMENT ON COLUMN oauth_tokens.user_id IS 'User related to this token, if the token is user-bound.';
COMMENT ON COLUMN oauth_tokens.grant_id IS 'Related OAuth grant, if the token was issued from a prior consent record.';
COMMENT ON COLUMN oauth_tokens.access_token_hash IS 'Hash of the OAuth access token. Raw access token must never be stored.';
COMMENT ON COLUMN oauth_tokens.refresh_token_hash IS 'Optional hash of the OAuth refresh token. Raw refresh token must never be stored.';
COMMENT ON COLUMN oauth_tokens.scopes IS 'Scopes granted to this OAuth token.';
COMMENT ON COLUMN oauth_tokens.issued_at IS 'Timestamp when the token was issued.';
COMMENT ON COLUMN oauth_tokens.expires_at IS 'Timestamp when the access token expires.';
COMMENT ON COLUMN oauth_tokens.rotated_at IS 'Timestamp when the token was rotated.';
COMMENT ON COLUMN oauth_tokens.revoked_at IS 'Timestamp when the token was revoked.';
COMMENT ON COLUMN oauth_tokens.created_at IS 'Timestamp when the OAuth token record was created.';

CREATE TABLE IF NOT EXISTS audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID audit event
    actor_user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL, -- User thực hiện action
    tenant_id uuid NULL, -- Tenant context
    workspace_id uuid NULL, -- Workspace context
    event varchar(255) NOT NULL, -- Event user/RBAC
    severity audit_severity NOT NULL DEFAULT 'info', -- Mức độ event
    ip_address inet NULL, -- IP request
    user_agent text NULL, -- User-agent request
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm xảy ra event
);

COMMENT ON TABLE audit_events IS 'Stores user-facing IAM/RBAC activity events such as login, password change, MFA updates, role assignment effects, and device/session actions. This table is not for admin API key audits.';
COMMENT ON COLUMN audit_events.id IS 'Primary key of the audit event record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN audit_events.actor_user_id IS 'User who performed the audited action. Nullable.';
COMMENT ON COLUMN audit_events.tenant_id IS 'Optional tenant context for the event.';
COMMENT ON COLUMN audit_events.workspace_id IS 'Optional workspace context for the event.';
COMMENT ON COLUMN audit_events.event IS 'Machine-readable user/RBAC event, for example auth.login.success, mfa.challenge.verified, or rbac.role.assigned.';
COMMENT ON COLUMN audit_events.severity IS 'Severity of the event. Allowed values are info, warning, and critical.';
COMMENT ON COLUMN audit_events.ip_address IS 'IP address related to the audited event.';
COMMENT ON COLUMN audit_events.user_agent IS 'User agent related to the audited event.';
COMMENT ON COLUMN audit_events.created_at IS 'Timestamp when the audited event occurred.';

CREATE TABLE IF NOT EXISTS admin_action_audits (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID audit record
    action text NOT NULL, -- Hành động admin (rotate_key, upsert_zone, ...)
    resource_type text NOT NULL, -- Loại resource (admin_api_key, zone, ...)
    resource_id text NULL, -- ID resource nếu có
    status text NOT NULL, -- Kết quả: success hoặc failed
    request_ip text NULL, -- IP request
    request_path text NOT NULL, -- Path request
    request_method text NOT NULL, -- HTTP method
    error_code text NULL, -- Mã lỗi logic (nếu failed)
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb, -- Metadata bổ sung để debug/audit
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm ghi log audit
);

COMMENT ON TABLE admin_action_audits IS 'Dedicated audit log for critical admin actions executed via admin-only flow.';
COMMENT ON COLUMN admin_action_audits.id IS 'Primary key of admin action audit record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN admin_action_audits.action IS 'Admin action name, for example admin.api_key.rotate or core.zone.update.';
COMMENT ON COLUMN admin_action_audits.resource_type IS 'Logical target type of the action, for example admin_api_key, zone, or zone_service.';
COMMENT ON COLUMN admin_action_audits.resource_id IS 'Optional target resource identifier, when available.';
COMMENT ON COLUMN admin_action_audits.status IS 'Result status of the action, typically success or failed.';
COMMENT ON COLUMN admin_action_audits.request_ip IS 'Source IP captured from the request path to support incident investigation.';
COMMENT ON COLUMN admin_action_audits.request_path IS 'HTTP request path of the audited admin action.';
COMMENT ON COLUMN admin_action_audits.request_method IS 'HTTP request method of the audited admin action.';
COMMENT ON COLUMN admin_action_audits.error_code IS 'Optional business error code when action fails.';
COMMENT ON COLUMN admin_action_audits.metadata IS 'JSONB metadata for contextual details while avoiding secret leakage.';
COMMENT ON COLUMN admin_action_audits.created_at IS 'Timestamp when the admin action audit record was created.';

CREATE TABLE IF NOT EXISTS admin_2fa_settings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID cấu hình 2FA admin
    secret_ciphertext text NULL, -- Secret 2FA dạng đã bảo vệ
    created_at timestamptz NOT NULL DEFAULT now(), -- Thời điểm tạo cấu hình
    updated_at timestamptz NOT NULL DEFAULT now() -- Thời điểm cập nhật cấu hình
);

COMMENT ON TABLE admin_2fa_settings IS 'Stores dedicated 2FA settings for admin auth flow.';
COMMENT ON COLUMN admin_2fa_settings.id IS 'Primary key of admin 2FA settings row. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN admin_2fa_settings.secret_ciphertext IS 'Protected secret material for admin 2FA factor. Never plaintext.';
COMMENT ON COLUMN admin_2fa_settings.created_at IS 'Timestamp when admin 2FA settings record was created.';
COMMENT ON COLUMN admin_2fa_settings.updated_at IS 'Timestamp when admin 2FA settings record was last updated.';

CREATE TABLE IF NOT EXISTS admin_recovery_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), -- ID recovery code
    code_hash text NOT NULL, -- Hash recovery code one-time
    used_at timestamptz NULL, -- Thời điểm code đã dùng
    created_at timestamptz NOT NULL DEFAULT now() -- Thời điểm tạo code
);

COMMENT ON TABLE admin_recovery_codes IS 'Stores one-time hashed recovery codes for admin auth recovery flow.';
COMMENT ON COLUMN admin_recovery_codes.id IS 'Primary key of admin recovery code record. Generated automatically with gen_random_uuid().';
COMMENT ON COLUMN admin_recovery_codes.code_hash IS 'Hash of recovery code. Raw recovery code must never be stored.';
COMMENT ON COLUMN admin_recovery_codes.used_at IS 'Timestamp when this recovery code was consumed.';
COMMENT ON COLUMN admin_recovery_codes.created_at IS 'Timestamp when this recovery code record was created.';

-- -------------------------------------------------------------
-- Bảng outbox lưu trữ các sự kiện/tác vụ bất đồng bộ của module IAM để CDC đồng bộ sang Redis/Kafka
-- -------------------------------------------------------------
CREATE TABLE IF NOT EXISTS iam_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL, -- UUID định danh duy nhất của sự kiện (Idempotency Key)
    routing_scope VARCHAR(100) NOT NULL, -- Phạm vi định tuyến và thực thi (e.g. platform, zone:vn)
    job_topic VARCHAR(100) NOT NULL, -- Tên topic/tác vụ (e.g. mail.system.verify_account)
    payload BYTEA NOT NULL, -- Dữ liệu nhị phân serialized dạng Protobuf
    user_id VARCHAR(64) NOT NULL, -- ID người dùng kích hoạt hành động này
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHED', 'PROCESSING', 'COMPLETED', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMP WITH TIME ZONE, -- Thời gian hoàn tất tác vụ

    -- CÁC CỘT ĐỒNG BỘ CONTRACT:
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA, -- Trích xuất OpenTelemetry trace parent để liên kết vết
    idle INT, -- Hạn mức timeout cho tác vụ tính bằng giây

    -- CÁC CỘT PHẢN HỒI KẾT QUẢ:
    error_code VARCHAR(100),
    error_message TEXT
);
