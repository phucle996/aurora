-- IAM migration layer 000006
-- Seed bootstrap user + RBAC permissions/roles + initial role assignment.

-- ----------------------------------------------------------------------------
-- 1) Seed system users
-- ----------------------------------------------------------------------------
-- Default password (plain, for local/dev seed only): ChangeMe123!
-- root           -> ChangeMe123!
-- sys_admin      -> ChangeMe123!
-- support_operator -> ChangeMe123!
-- audit_viewer   -> ChangeMe123!
-- billing_admin -> ChangeMe123!
-- NOTE: password_hash below must match the default plain password above.
INSERT INTO users (id, username, email, password_hash, status)
VALUES
    ('00000000-0000-0000-0000-000000000001', 'root', 'root', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000002', 'sys_admin', 'sys_admin', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000003', 'support_operator', 'support_operator', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000004', 'audit_viewer', 'audit_viewer', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    -- [COMMENT]: Billing admin là IAM user thường; Cost Manager không còn giữ credential hay user riêng.
    ('00000000-0000-0000-0000-000000000005', 'billing_admin', 'billing_admin', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO user_profiles (user_id, fullname, bio, locale, timezone)
SELECT u.id, v.fullname, v.bio, 'vi-VN', 'Asia/Ho_Chi_Minh'
FROM users u
JOIN (
    VALUES
        ('root', 'Root User', 'Highest authority root account.'),
        ('sys_admin', 'System Administrator', 'Platform administrator. Desired state monitor.'),
        ('support_operator', 'Support Operator', 'Support operator for customer issues.'),
        ('audit_viewer', 'Audit Viewer', 'Auditor for system logs and activities.'),
        ('billing_admin', 'Billing Administrator', 'Level 1 operator for billing catalog and financial operations.')
) AS v(email, fullname, bio) ON v.email = u.email
ON CONFLICT (user_id) DO UPDATE
SET
    fullname = EXCLUDED.fullname,
    bio = EXCLUDED.bio,
    locale = EXCLUDED.locale,
    timezone = EXCLUDED.timezone,
    updated_at = now();

-- ----------------------------------------------------------------------------
-- 2) Seed permissions (3-level catalog with real random UUIDs)
-- ----------------------------------------------------------------------------
INSERT INTO permissions (id, module, object, behavior, description)
VALUES
    (gen_random_uuid(), 'iam', 'users', 'read', 'Read user accounts'),
    (gen_random_uuid(), 'iam', 'users', 'manage', 'Create/update/disable user accounts'),
    (gen_random_uuid(), 'iam', 'role', 'read', 'Read RBAC roles'),
    (gen_random_uuid(), 'iam', 'role', 'write', 'Create/update RBAC roles'),
    (gen_random_uuid(), 'iam', 'role', 'assign', 'Assign/revoke roles for users'),
    (gen_random_uuid(), 'iam', 'role', 'delete', 'Delete RBAC roles'),
    (gen_random_uuid(), 'iam', 'permissions', 'read', 'Read permission catalog'),
    (gen_random_uuid(), 'storage', 'bucket', 'read', 'Read storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'write', 'Write storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'delete', 'Delete storage bucket'),
    (gen_random_uuid(), 'storage', 'credential', 'read', 'Read storage credentials'),
    (gen_random_uuid(), 'storage', 'credential', 'write', 'Write storage credentials'),
    (gen_random_uuid(), 'storage', 'credential', 'delete', 'Delete storage credentials'),
    -- [COMMENT]: Thêm bộ 4 permissions CRUD cho workspace phục vụ phân quyền dòng chảy Tenant và Personal
    (gen_random_uuid(), 'hierarchy', 'workspace', 'create', 'Create workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'read', 'Read workspace details'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'update', 'Update workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'delete', 'Delete workspace'),
    -- [COMMENT]: Thêm permission iam:device:read cho phép platform audit/read user devices
    (gen_random_uuid(), 'iam', 'device', 'read', 'Read user devices'),
    -- [COMMENT]: Thêm permission iam:mfa:view cho phép platform audit user MFA settings
    (gen_random_uuid(), 'iam', 'mfa', 'view', 'View user MFA settings'),
    -- [COMMENT]: `email` là business/RBAC namespace; wire topic và zone service nội bộ vẫn dùng `mail`.
    (gen_random_uuid(), 'email', 'consumer', 'create', 'Create email broker consumers'),
    (gen_random_uuid(), 'email', 'consumer', 'read', 'Read email broker consumers'),
    (gen_random_uuid(), 'email', 'consumer', 'update', 'Update, pause, or resume email broker consumers'),
    (gen_random_uuid(), 'email', 'consumer', 'delete', 'Delete email broker consumers'),
    (gen_random_uuid(), 'email', 'template', 'create', 'Create email templates'),
    (gen_random_uuid(), 'email', 'template', 'read', 'Read email templates and immutable versions'),
    (gen_random_uuid(), 'email', 'template', 'publish', 'Publish immutable email template versions'),
    (gen_random_uuid(), 'email', 'template', 'delete', 'Delete email templates'),
    -- [COMMENT]: Billing dùng permission ba bậc được IAM render sang domain Trinity.
    (gen_random_uuid(), 'billing', 'plan', 'read', 'Read billing plans'),
    (gen_random_uuid(), 'billing', 'tier', 'read', 'Read billing tiers and immutable versions'),
    (gen_random_uuid(), 'billing', 'tier', 'publish', 'Publish immutable billing tier versions'),
    (gen_random_uuid(), 'billing', 'wallet', 'read', 'Read owner wallets'),
    (gen_random_uuid(), 'billing', 'wallet', 'top_up', 'Fund a tenant wallet'),
    (gen_random_uuid(), 'billing', 'ledger', 'read', 'Read immutable billing ledger entries'),
    (gen_random_uuid(), 'billing', 'subscription', 'write', 'Activate or change an owner subscription'),
    (gen_random_uuid(), 'billing', 'credit', 'adjust', 'Post an audited wallet credit adjustment')
ON CONFLICT (module, object, behavior) DO UPDATE
SET
    description = EXCLUDED.description,
    updated_at = now();

-- ----------------------------------------------------------------------------
-- 3) Seed system roles (with real random UUIDs)
-- ----------------------------------------------------------------------------
INSERT INTO roles (id, code, name, description, role_level, scope, created_by)
VALUES
    (gen_random_uuid(), 'platform_root', 'Root', 'Highest platform-level system authority', 0, 'platform', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'platform_user', 'Platform User', 'Default global role for registered user not joined to any tenant', 8, 'platform', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'platform_admin', 'System Admin', 'Platform administration role', 1, 'platform', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'billing_admin', 'Billing Admin', 'Billing administration role', 1, 'platform', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'platform_support_operator', 'Support Operator', 'Platform support operation role', 2, 'platform', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'tenant_owner', 'Owner', 'Tenant owner role', 3, 'tenant', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'tenant_admin', 'Admin', 'Tenant administrator role', 4, 'tenant', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'tenant_manager', 'Manager', 'Tenant manager role', 5, 'tenant', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'tenant_member', 'Member', 'Tenant member role', 6, 'tenant', '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'tenant_viewer', 'Viewer', 'Tenant read-only role', 7, 'tenant', '00000000-0000-0000-0000-000000000001')
ON CONFLICT (code) DO UPDATE
SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    role_level = EXCLUDED.role_level,
    scope = EXCLUDED.scope,
    created_by = EXCLUDED.created_by,
    updated_at = now();

-- ----------------------------------------------------------------------------
-- 4) Seed role-permission mapping (dynamic SELECT by code)
-- ----------------------------------------------------------------------------
-- platform_root / platform_admin get all permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code IN ('platform_root', 'platform_admin')
  AND NOT (p.module='billing' AND p.object='wallet' AND p.behavior='top_up')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- [COMMENT]: Billing admin chỉ mang quyền billing; level 1 không mặc nhiên mở các module platform khác.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'billing_admin'
  AND p.module = 'billing'
  AND NOT (p.object='wallet' AND p.behavior='top_up')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- [COMMENT]: User thường chỉ được mutate subscription của chính mình qua owner context đã xác minh.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'platform_user'
  AND p.module = 'billing'
  AND p.object = 'subscription'
  AND p.behavior = 'write'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- [COMMENT]: Platform user và tenant operator chỉ nhận customer-facing Email Delivery permissions;
-- infrastructure read vẫn giới hạn ở platform admin/root/support qua các mapping ở trên.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code IN ('platform_user', 'tenant_owner', 'tenant_admin', 'tenant_manager')
  AND p.module = 'email'
  AND p.object IN ('consumer', 'template')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- [COMMENT]: Tenant funding is an explicit tenant-scoped capability. Personal
-- funding is self-service and never depends on this role permission.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code IN ('tenant_owner', 'tenant_admin')
  AND p.module = 'billing'
  AND p.object = 'wallet'
  AND p.behavior IN ('read', 'top_up')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- [COMMENT]: Tenant member/viewer được quan sát cấu hình nhưng không được mutate resource.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code IN ('tenant_member', 'tenant_viewer')
  AND p.module = 'email'
  AND p.object IN ('consumer', 'template')
  AND p.behavior = 'read'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- platform_support_operator gets read-only permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'platform_support_operator'
  AND p.behavior = 'read'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- [COMMENT]: Gán thêm quyền iam:mfa:view cho platform_support_operator
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'platform_support_operator'
  AND p.module = 'iam' AND p.object = 'mfa' AND p.behavior = 'view'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 5) Assign default system users -> user_role (pre-compiled binary keys, mapped dynamic role_id)
-- ----------------------------------------------- root user role
INSERT INTO user_role (
    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
)
SELECT 
    gen_random_uuid(),
    u.id,
    'root',
    '00000000-0000-0000-0000-000000000000'::uuid,
    r.id,
    r.name,
    r.role_level,
    -- [COMMENT]: Protobuf RoleEntry precompiled; ON CONFLICT rebuild quyền wildcard của root seed user.
    decode('0a15726f6f743a2a3a69616d3a75736572733a726561640a17726f6f743a2a3a69616d3a75736572733a6d616e6167650a14726f6f743a2a3a69616d3a726f6c653a726561640a15726f6f743a2a3a69616d3a726f6c653a77726974650a16726f6f743a2a3a69616d3a726f6c653a61737369676e0a16726f6f743a2a3a69616d3a726f6c653a64656c6574650a1b726f6f743a2a3a69616d3a7065726d697373696f6e733a726561640a1a726f6f743a2a3a73746f726167653a6275636b65743a726561640a1b726f6f743a2a3a73746f726167653a6275636b65743a77726974650a1c726f6f743a2a3a73746f726167653a6275636b65743a64656c6574650a1e726f6f743a2a3a73746f726167653a63726564656e7469616c3a726561640a1f726f6f743a2a3a73746f726167653a63726564656e7469616c3a77726974650a20726f6f743a2a3a73746f726167653a63726564656e7469616c3a64656c6574650a21726f6f743a2a3a6869657261726368793a776f726b73706163653a6372656174650a1f726f6f743a2a3a6869657261726368793a776f726b73706163653a726561640a21726f6f743a2a3a6869657261726368793a776f726b73706163653a7570646174650a21726f6f743a2a3a6869657261726368793a776f726b73706163653a64656c6574650a16726f6f743a2a3a69616d3a6465766963653a726561640a13726f6f743a2a3a69616d3a6d66613a766965770a1c726f6f743a2a3a656d61696c3a636f6e73756d65723a6372656174650a1a726f6f743a2a3a656d61696c3a636f6e73756d65723a726561640a1c726f6f743a2a3a656d61696c3a636f6e73756d65723a7570646174650a1c726f6f743a2a3a656d61696c3a636f6e73756d65723a64656c6574650a1c726f6f743a2a3a656d61696c3a74656d706c6174653a6372656174650a1a726f6f743a2a3a656d61696c3a74656d706c6174653a726561640a1d726f6f743a2a3a656d61696c3a74656d706c6174653a7075626c6973680a1c726f6f743a2a3a656d61696c3a74656d706c6174653a64656c6574650a18726f6f743a2a3a62696c6c696e673a706c616e3a726561640a18726f6f743a2a3a62696c6c696e673a746965723a726561640a1b726f6f743a2a3a62696c6c696e673a746965723a7075626c6973680a1a726f6f743a2a3a62696c6c696e673a77616c6c65743a726561640a1a726f6f743a2a3a62696c6c696e673a6c65646765723a726561640a21726f6f743a2a3a62696c6c696e673a737562736372697074696f6e3a77726974650a1c726f6f743a2a3a62696c6c696e673a6372656469743a61646a757374', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'root' AND r.code = 'platform_root'
ON CONFLICT (user_id, workspace_id, role_id) DO UPDATE 
SET list_perm = EXCLUDED.list_perm;

-- billing_admin user role
INSERT INTO user_role (
    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
)
SELECT
    gen_random_uuid(),
    u.id,
    'billing_admin',
    '00000000-0000-0000-0000-000000000000'::uuid,
    r.id,
    r.name,
    r.role_level,
    -- [COMMENT]: Snapshot chỉ chứa billing permissions; giá trị được test decode để chống drift với catalog.
    decode('0a2162696c6c696e675f61646d696e3a2a3a62696c6c696e673a706c616e3a726561640a2162696c6c696e675f61646d696e3a2a3a62696c6c696e673a746965723a726561640a2462696c6c696e675f61646d696e3a2a3a62696c6c696e673a746965723a7075626c6973680a2362696c6c696e675f61646d696e3a2a3a62696c6c696e673a77616c6c65743a726561640a2362696c6c696e675f61646d696e3a2a3a62696c6c696e673a6c65646765723a726561640a2a62696c6c696e675f61646d696e3a2a3a62696c6c696e673a737562736372697074696f6e3a77726974650a2562696c6c696e675f61646d696e3a2a3a62696c6c696e673a6372656469743a61646a757374', 'hex')
FROM users u
CROSS JOIN roles r
WHERE u.username = 'billing_admin' AND r.code = 'billing_admin'
ON CONFLICT (user_id, workspace_id, role_id) DO UPDATE
SET list_perm = EXCLUDED.list_perm;

-- sys_admin user role
INSERT INTO user_role (
    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
)
SELECT 
    gen_random_uuid(),
    u.id,
    'sys_admin',
    '00000000-0000-0000-0000-000000000000'::uuid,
    r.id,
    r.name,
    r.role_level,
    -- [COMMENT]: Protobuf RoleEntry precompiled; ON CONFLICT rebuild quyền wildcard của sys_admin.
    decode('0a1a7379735f61646d696e3a2a3a69616d3a75736572733a726561640a1c7379735f61646d696e3a2a3a69616d3a75736572733a6d616e6167650a197379735f61646d696e3a2a3a69616d3a726f6c653a726561640a1a7379735f61646d696e3a2a3a69616d3a726f6c653a77726974650a1b7379735f61646d696e3a2a3a69616d3a726f6c653a61737369676e0a1b7379735f61646d696e3a2a3a69616d3a726f6c653a64656c6574650a207379735f61646d696e3a2a3a69616d3a7065726d697373696f6e733a726561640a1f7379735f61646d696e3a2a3a73746f726167653a6275636b65743a726561640a207379735f61646d696e3a2a3a73746f726167653a6275636b65743a77726974650a217379735f61646d696e3a2a3a73746f726167653a6275636b65743a64656c6574650a237379735f61646d696e3a2a3a73746f726167653a63726564656e7469616c3a726561640a247379735f61646d696e3a2a3a73746f726167653a63726564656e7469616c3a77726974650a257379735f61646d696e3a2a3a73746f726167653a63726564656e7469616c3a64656c6574650a267379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a6372656174650a247379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a726561640a267379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a7570646174650a267379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a64656c6574650a1b7379735f61646d696e3a2a3a69616d3a6465766963653a726561640a187379735f61646d696e3a2a3a69616d3a6d66613a766965770a217379735f61646d696e3a2a3a656d61696c3a636f6e73756d65723a6372656174650a1f7379735f61646d696e3a2a3a656d61696c3a636f6e73756d65723a726561640a217379735f61646d696e3a2a3a656d61696c3a636f6e73756d65723a7570646174650a217379735f61646d696e3a2a3a656d61696c3a636f6e73756d65723a64656c6574650a217379735f61646d696e3a2a3a656d61696c3a74656d706c6174653a6372656174650a1f7379735f61646d696e3a2a3a656d61696c3a74656d706c6174653a726561640a227379735f61646d696e3a2a3a656d61696c3a74656d706c6174653a7075626c6973680a217379735f61646d696e3a2a3a656d61696c3a74656d706c6174653a64656c6574650a1d7379735f61646d696e3a2a3a62696c6c696e673a706c616e3a726561640a1d7379735f61646d696e3a2a3a62696c6c696e673a746965723a726561640a207379735f61646d696e3a2a3a62696c6c696e673a746965723a7075626c6973680a1f7379735f61646d696e3a2a3a62696c6c696e673a77616c6c65743a726561640a1f7379735f61646d696e3a2a3a62696c6c696e673a6c65646765723a726561640a267379735f61646d696e3a2a3a62696c6c696e673a737562736372697074696f6e3a77726974650a217379735f61646d696e3a2a3a62696c6c696e673a6372656469743a61646a757374', 'hex')
FROM users u  
CROSS JOIN roles r
WHERE u.username = 'sys_admin' AND r.code = 'platform_admin'
ON CONFLICT (user_id, workspace_id, role_id) DO UPDATE 
SET list_perm = EXCLUDED.list_perm;

-- support_operator user role
INSERT INTO user_role (
    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
)
SELECT 
    gen_random_uuid(),
    u.id,
    'support_operator',
    '00000000-0000-0000-0000-000000000000'::uuid,
    r.id,
    r.name,
    r.role_level,
    -- [COMMENT]: Protobuf RoleEntry precompiled chỉ chứa read/view permissions của support operator.
    decode('0a21737570706f72745f6f70657261746f723a2a3a69616d3a75736572733a726561640a20737570706f72745f6f70657261746f723a2a3a69616d3a726f6c653a726561640a27737570706f72745f6f70657261746f723a2a3a69616d3a7065726d697373696f6e733a726561640a26737570706f72745f6f70657261746f723a2a3a73746f726167653a6275636b65743a726561640a2a737570706f72745f6f70657261746f723a2a3a73746f726167653a63726564656e7469616c3a726561640a2b737570706f72745f6f70657261746f723a2a3a6869657261726368793a776f726b73706163653a726561640a22737570706f72745f6f70657261746f723a2a3a69616d3a6465766963653a726561640a1f737570706f72745f6f70657261746f723a2a3a69616d3a6d66613a766965770a26737570706f72745f6f70657261746f723a2a3a656d61696c3a636f6e73756d65723a726561640a26737570706f72745f6f70657261746f723a2a3a656d61696c3a74656d706c6174653a726561640a24737570706f72745f6f70657261746f723a2a3a62696c6c696e673a706c616e3a726561640a24737570706f72745f6f70657261746f723a2a3a62696c6c696e673a746965723a726561640a26737570706f72745f6f70657261746f723a2a3a62696c6c696e673a77616c6c65743a726561640a26737570706f72745f6f70657261746f723a2a3a62696c6c696e673a6c65646765723a72656164', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'support_operator' AND r.code = 'platform_support_operator'
ON CONFLICT (user_id, workspace_id, role_id) DO UPDATE 
SET list_perm = EXCLUDED.list_perm;

-- audit_viewer user role (mapped to support_operator permissions)
INSERT INTO user_role (
    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
)
SELECT 
    gen_random_uuid(),
    u.id,
    'audit_viewer',
    '00000000-0000-0000-0000-000000000000'::uuid,
    r.id,
    r.name,
    r.role_level,
    -- [COMMENT]: Audit viewer dùng cùng read/view catalog nhưng giữ identity prefix riêng trong binary.
    decode('0a1d61756469745f7669657765723a2a3a69616d3a75736572733a726561640a1c61756469745f7669657765723a2a3a69616d3a726f6c653a726561640a2361756469745f7669657765723a2a3a69616d3a7065726d697373696f6e733a726561640a2261756469745f7669657765723a2a3a73746f726167653a6275636b65743a726561640a2661756469745f7669657765723a2a3a73746f726167653a63726564656e7469616c3a726561640a2761756469745f7669657765723a2a3a6869657261726368793a776f726b73706163653a726561640a1e61756469745f7669657765723a2a3a69616d3a6465766963653a726561640a1b61756469745f7669657765723a2a3a69616d3a6d66613a766965770a2261756469745f7669657765723a2a3a656d61696c3a636f6e73756d65723a726561640a2261756469745f7669657765723a2a3a656d61696c3a74656d706c6174653a726561640a2061756469745f7669657765723a2a3a62696c6c696e673a706c616e3a726561640a2061756469745f7669657765723a2a3a62696c6c696e673a746965723a726561640a2261756469745f7669657765723a2a3a62696c6c696e673a77616c6c65743a726561640a2261756469745f7669657765723a2a3a62696c6c696e673a6c65646765723a72656164', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'audit_viewer' AND r.code = 'platform_support_operator'
ON CONFLICT (user_id, workspace_id, role_id) DO UPDATE 
SET list_perm = EXCLUDED.list_perm;

-- 6) Seed personal workspaces for active users existing in system
DO $$
DECLARE
    u RECORD;
    v_zone_id UUID;
    v_workspace_id UUID;
    v_role_id UUID;
    v_role_name TEXT;
    v_role_level INT;
BEGIN
    SELECT id INTO v_zone_id FROM hierarchy.zones WHERE status = 'active' LIMIT 1;
    IF v_zone_id IS NULL THEN
        SELECT id INTO v_zone_id FROM hierarchy.zones LIMIT 1;
    END IF;
 
    IF v_zone_id IS NULL THEN
        v_zone_id := '019f3d3e-997d-7894-9236-c5122634cb4f'::UUID;
        INSERT INTO hierarchy.zones (id, code, name, location, status)
        VALUES (v_zone_id, 'edge-viet-nam-1', 'Edge việt nam 1', 'Hà Nội, Vietnam', 'active')
        ON CONFLICT (id) DO NOTHING;
    END IF;

    -- [COMMENT]: Seed các dịch vụ mặc định cho zone edge-viet-nam-1
    INSERT INTO hierarchy.zone_services (id, zone_id, service_type, desired_state, actual_state)
    VALUES 
        (gen_random_uuid(), v_zone_id, 'hypervisor', true, 'healthy'),
        (gen_random_uuid(), v_zone_id, 'storage', true, 'healthy'),
        (gen_random_uuid(), v_zone_id, 'mail', true, 'healthy'),
        (gen_random_uuid(), v_zone_id, 'kubernetes', false, 'unknown'),
        (gen_random_uuid(), v_zone_id, 'ai', false, 'unknown'),
        (gen_random_uuid(), v_zone_id, 'database', false, 'unknown')
    ON CONFLICT (zone_id, service_type) DO NOTHING;
 
    FOR u IN SELECT id, username FROM users WHERE status = 'active' LOOP
        IF NOT EXISTS (SELECT 1 FROM hierarchy.personal_workspaces WHERE owner_id = u.id) THEN
            v_workspace_id := gen_random_uuid();
            INSERT INTO hierarchy.personal_workspaces (id, name, code, zone_id, owner_id)
            VALUES (
                v_workspace_id,
                'Default Workspace',
                'default-' || lower(u.username),
                v_zone_id,
                u.id
            );
 
            SELECT id, name, role_level INTO v_role_id, v_role_name, v_role_level 
            FROM roles 
            WHERE code = 'platform_user';
 
            IF v_role_id IS NOT NULL THEN
                INSERT INTO user_role (
                    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
                )
                VALUES (
                    gen_random_uuid(),
                    u.id,
                    u.username,
                    v_workspace_id,
                    v_role_id,
                    v_role_name,
                    v_role_level,
                    decode('', 'hex')
                )
                ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING;
            END IF;
        END IF;
    END LOOP;
END;
$$;
