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
    (gen_random_uuid(), 'iam', 'assignments', 'manage', 'Assign/revoke roles for users')
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
    decode('0a3a726f6f743a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a75736572733a6d616e6167650a3a726f6f743a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a726f6c65733a6d616e616765', 'hex')
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
    decode('0a3d7379735f61646d696e3a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a75736572733a726561640a3f7379735f61646d696e3a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a75736572733a6d616e6167650a3d7379735f61646d696e3a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a726f6c65733a726561640a3f7379735f61646d696e3a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a726f6c65733a6d616e6167650a437379735f61646d696e3a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a7065726d697373696f6e733a726561640a457379735f61646d696e3a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a61737369676e6d656e74733a6d616e616765', 'hex')
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
    decode('0a44737570706f72745f6f70657261746f723a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a75736572733a726561640a44737570706f72745f6f70657261746f723a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a726f6c65733a726561640a4a737570706f72745f6f70657261746f723a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a7065726d697373696f6e733a72656164', 'hex')
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
    decode('0a4061756469745f7669657765723a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a75736572733a726561640a4061756469745f7669657765723a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a726f6c65733a726561640a4661756469745f7669657765723a30303030303030302d303030302d303030302d303030302d3030303030303030303030303a69616d3a7065726d697373696f6e733a72656164', 'hex')
FROM users u 
CROSS JOIN roles r
WHERE u.username = 'audit_viewer' AND r.code = 'platform_support_operator'
ON CONFLICT DO NOTHING;
