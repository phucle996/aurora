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
-- NOTE: password_hash below must match the default plain password above.
INSERT INTO users (id, username, email, password_hash, status)
VALUES
    (gen_random_uuid(), 'root', 'root', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    (gen_random_uuid(), 'sys_admin', 'sys_admin', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    (gen_random_uuid(), 'support_operator', 'support_operator', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    (gen_random_uuid(), 'audit_viewer', 'audit_viewer', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active')
ON CONFLICT DO NOTHING;

INSERT INTO user_profiles (user_id, fullname, locale, timezone)
SELECT u.id, v.fullname, 'vi-VN', 'Asia/Ho_Chi_Minh'
FROM users u
JOIN (
    VALUES
        ('root', 'Root User'),
        ('sys_admin', 'System Administrator'),
        ('support_operator', 'Support Operator'),
        ('audit_viewer', 'Audit Viewer')
) AS v(email, fullname) ON v.email = u.email
ON CONFLICT (user_id) DO UPDATE
SET
    fullname = EXCLUDED.fullname,
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
    (gen_random_uuid(), 'iam', 'roles', 'read', 'Read RBAC roles'),
    (gen_random_uuid(), 'iam', 'roles', 'manage', 'Create/update RBAC roles'),
    (gen_random_uuid(), 'iam', 'permissions', 'read', 'Read permission catalog'),
    (gen_random_uuid(), 'iam', 'assignments', 'manage', 'Assign/revoke roles for users'),
    -- [COMMENT]: Các permission mới từ các file route.go của các phân hệ (IAM và Storage)
    -- [COMMENT]: Cac permission da duoc don dep ve dung chuan CRUD (read thay the cho list)
    (gen_random_uuid(), 'iam', 'users', 'delete', 'Delete user accounts'),
    (gen_random_uuid(), 'iam', 'role', 'read', 'Read RBAC roles'),
    (gen_random_uuid(), 'iam', 'role', 'create', 'Create RBAC roles'),
    (gen_random_uuid(), 'storage', 'bucket', 'create', 'Create storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'read', 'Read storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'update', 'Update storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'delete', 'Delete storage bucket'),
    (gen_random_uuid(), 'storage', 'credential', 'create', 'Create storage credentials'),
    (gen_random_uuid(), 'storage', 'credential', 'read', 'Read storage credentials'),
    (gen_random_uuid(), 'storage', 'credential', 'delete', 'Delete storage credentials'),
    -- [COMMENT]: Thêm bộ 4 permissions CRUD cho workspace phục vụ phân quyền dòng chảy Tenant và Personal
    (gen_random_uuid(), 'hierarchy', 'workspace', 'create', 'Create workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'read', 'Read workspace details'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'update', 'Update workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'delete', 'Delete workspace')
ON CONFLICT (module, object, behavior) DO UPDATE
SET
    description = EXCLUDED.description,
    updated_at = now();

-- ----------------------------------------------------------------------------
-- 3) Seed system roles (with real random UUIDs)
-- ----------------------------------------------------------------------------
INSERT INTO roles (id, code, name, description, role_level, scope)
VALUES
    (gen_random_uuid(), 'platform_root', 'Root', 'Highest platform-level system authority', 0, 'platform'),
    (gen_random_uuid(), 'platform_user', 'Platform User', 'Default global role for registered user not joined to any tenant', 8, 'platform'),
    (gen_random_uuid(), 'platform_admin', 'System Admin', 'Platform administration role', 1, 'platform'),
    (gen_random_uuid(), 'platform_support_operator', 'Support Operator', 'Platform support operation role', 2, 'platform'),
    (gen_random_uuid(), 'tenant_owner', 'Owner', 'Tenant owner role', 3, 'tenant'),
    (gen_random_uuid(), 'tenant_admin', 'Admin', 'Tenant administrator role', 4, 'tenant'),
    (gen_random_uuid(), 'tenant_manager', 'Manager', 'Tenant manager role', 5, 'tenant'),
    (gen_random_uuid(), 'tenant_member', 'Member', 'Tenant member role', 6, 'tenant'),
    (gen_random_uuid(), 'tenant_viewer', 'Viewer', 'Tenant read-only role', 7, 'tenant')
ON CONFLICT (code) DO UPDATE
SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    role_level = EXCLUDED.role_level,
    scope = EXCLUDED.scope,
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
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- platform_support_operator gets read-only permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'platform_support_operator'
  AND p.behavior = 'read'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 5) Assign default system users -> user_role (pre-compiled binary keys, mapped dynamic role_id)
-- ----------------------------------------------------------------------------
-- root user role
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
    -- [COMMENT]: Giá trị list_perm được sinh tự động từ protobuf RoleEntry tương thích với các quyền mới
    decode('0a15726f6f743a2a3a69616d3a75736572733a726561640a17726f6f743a2a3a69616d3a75736572733a6d616e6167650a17726f6f743a2a3a69616d3a75736572733a64656c6574650a15726f6f743a2a3a69616d3a726f6c65733a726561640a17726f6f743a2a3a69616d3a726f6c65733a6d616e6167650a1b726f6f743a2a3a69616d3a7065726d697373696f6e733a726561640a1d726f6f743a2a3a69616d3a61737369676e6d656e74733a6d616e6167650a1c726f6f743a2a3a73746f726167653a6275636b65743a6372656174650a1a726f6f743a2a3a73746f726167653a6275636b65743a726561640a1c726f6f743a2a3a73746f726167653a6275636b65743a7570646174650a1c726f6f743a2a3a73746f726167653a6275636b65743a64656c6574650a20726f6f743a2a3a73746f726167653a63726564656e7469616c3a6372656174650a1e726f6f743a2a3a73746f726167653a63726564656e7469616c3a726561640a20726f6f743a2a3a73746f726167653a63726564656e7469616c3a64656c6574650a21726f6f743a2a3a6869657261726368793a776f726b73706163653a6372656174650a1f726f6f743a2a3a6869657261726368793a776f726b73706163653a726561640a21726f6f743a2a3a6869657261726368793a776f726b73706163653a7570646174650a21726f6f743a2a3a6869657261726368793a776f726b73706163653a64656c6574650a14726f6f743a2a3a69616d3a726f6c653a726561640a16726f6f743a2a3a69616d3a726f6c653a637265617465', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'root' AND r.code = 'platform_root'
ON CONFLICT DO NOTHING;

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
    -- [COMMENT]: Giá trị list_perm được sinh tự động từ protobuf RoleEntry tương thích với các quyền mới
    decode('0a1a7379735f61646d696e3a2a3a69616d3a75736572733a726561640a1c7379735f61646d696e3a2a3a69616d3a75736572733a6d616e6167650a1c7379735f61646d696e3a2a3a69616d3a75736572733a64656c6574650a1a7379735f61646d696e3a2a3a69616d3a726f6c65733a726561640a1c7379735f61646d696e3a2a3a69616d3a726f6c65733a6d616e6167650a207379735f61646d696e3a2a3a69616d3a7065726d697373696f6e733a726561640a227379735f61646d696e3a2a3a69616d3a61737369676e6d656e74733a6d616e6167650a217379735f61646d696e3a2a3a73746f726167653a6275636b65743a6372656174650a1f7379735f61646d696e3a2a3a73746f726167653a6275636b65743a726561640a217379735f61646d696e3a2a3a73746f726167653a6275636b65743a7570646174650a217379735f61646d696e3a2a3a73746f726167653a6275636b65743a64656c6574650a257379735f61646d696e3a2a3a73746f726167653a63726564656e7469616c3a6372656174650a237379735f61646d696e3a2a3a73746f726167653a63726564656e7469616c3a726561640a257379735f61646d696e3a2a3a73746f726167653a63726564656e7469616c3a64656c6574650a267379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a6372656174650a247379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a726561640a267379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a7570646174650a267379735f61646d696e3a2a3a6869657261726368793a776f726b73706163653a64656c6574650a197379735f61646d696e3a2a3a69616d3a726f6c653a726561640a1b7379735f61646d696e3a2a3a69616d3a726f6c653a637265617465', 'hex')
FROM users u  
CROSS JOIN roles r
WHERE u.username = 'sys_admin' AND r.code = 'platform_admin'
ON CONFLICT DO NOTHING;

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
    -- [COMMENT]: Giá trị list_perm được sinh tự động từ protobuf RoleEntry tương thích với các quyền mới
    decode('0a21737570706f72745f6f70657261746f723a2a3a69616d3a75736572733a726561640a21737570706f72745f6f70657261746f723a2a3a69616d3a726f6c65733a726561640a27737570706f72745f6f70657261746f723a2a3a69616d3a7065726d697373696f6e733a726561640a26737570706f72745f6f70657261746f723a2a3a73746f726167653a6275636b65743a726561640a2a737570706f72745f6f70657261746f723a2a3a73746f726167653a63726564656e7469616c3a726561640a2b737570706f72745f6f70657261746f723a2a3a6869657261726368793a776f726b73706163653a726561640a20737570706f72745f6f70657261746f723a2a3a69616d3a726f6c653a72656164', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'support_operator' AND r.code = 'platform_support_operator'
ON CONFLICT DO NOTHING;

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
    -- [COMMENT]: Giá trị list_perm được sinh tự động từ protobuf RoleEntry tương thích với các quyền mới
    decode('0a1d61756469745f7669657765723a2a3a69616d3a75736572733a726561640a1d61756469745f7669657765723a2a3a69616d3a726f6c65733a726561640a2361756469745f7669657765723a2a3a69616d3a7065726d697373696f6e733a726561640a2261756469745f7669657765723a2a3a73746f726167653a6275636b65743a726561640a2661756469745f7669657765723a2a3a73746f726167653a63726564656e7469616c3a726561640a2761756469745f7669657765723a2a3a6869657261726368793a776f726b73706163653a6372656174650a1c61756469745f7669657765723a2a3a69616d3a726f6c653a72656164', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'audit_viewer' AND r.code = 'platform_support_operator'
ON CONFLICT DO NOTHING;
