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
-- 2) Seed permissions (system RBAC baseline)
-- ----------------------------------------------------------------------------
INSERT INTO permissions (id, code, name, description, resource, action)
VALUES
    (gen_random_uuid(), 'iam.users.read', 'Read Users', 'Read user accounts', 'iam.users', 'read'),
    (gen_random_uuid(), 'iam.users.manage', 'Manage Users', 'Create/update/disable user accounts', 'iam.users', 'manage'),
    (gen_random_uuid(), 'iam.roles.read', 'Read Roles', 'Read RBAC roles', 'iam.roles', 'read'),
    (gen_random_uuid(), 'iam.roles.manage', 'Manage Roles', 'Create/update RBAC roles', 'iam.roles', 'manage'),
    (gen_random_uuid(), 'iam.permissions.read', 'Read Permissions', 'Read permission catalog', 'iam.permissions', 'read'),
    (gen_random_uuid(), 'iam.assignments.manage', 'Manage Assignments', 'Assign/revoke roles for users', 'iam.assignments', 'manage')
ON CONFLICT (code) DO UPDATE
SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    resource = EXCLUDED.resource,
    action = EXCLUDED.action,
    updated_at = now();

-- ----------------------------------------------------------------------------
-- 3) Seed system roles (must satisfy roles_system_flags_consistency_check)
-- ----------------------------------------------------------------------------
INSERT INTO roles (
    id, code, name, description, scope_type,
    is_system, role_level, is_protected, is_assignable, owner_tenant_id
)
VALUES
    (gen_random_uuid(), 'platform_root', 'Root', 'Highest platform-level system authority', 'platform', true, 0, true, true, NULL),
    (gen_random_uuid(), 'platform_user', 'Platform User', 'Default global role for registered user not joined to any tenant', 'platform', true, 8, true, true, NULL),
    (gen_random_uuid(), 'platform_admin', 'System Admin', 'Platform administration role', 'platform', true, 1, true, true, NULL),
    (gen_random_uuid(), 'platform_support_operator', 'Support Operator', 'Platform support operation role', 'platform', true, 2, true, true, NULL),
    (gen_random_uuid(), 'tenant_owner', 'Owner', 'Tenant owner role', 'tenant', true, 3, true, true, NULL),
    (gen_random_uuid(), 'tenant_admin', 'Admin', 'Tenant administrator role', 'tenant', true, 4, true, true, NULL),
    (gen_random_uuid(), 'tenant_manager', 'Manager', 'Tenant manager role', 'tenant', true, 5, true, true, NULL),
    (gen_random_uuid(), 'tenant_member', 'Member', 'Tenant member role', 'tenant', true, 6, true, true, NULL),
    (gen_random_uuid(), 'tenant_viewer', 'Viewer', 'Tenant read-only role', 'tenant', true, 7, true, true, NULL)
ON CONFLICT (code) DO UPDATE
SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    scope_type = EXCLUDED.scope_type,
    role_level = EXCLUDED.role_level,
    is_assignable = EXCLUDED.is_assignable,
    updated_at = now();

-- ----------------------------------------------------------------------------
-- 4) Seed role-permission mapping
-- ----------------------------------------------------------------------------
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
    'iam.users.read',
    'iam.users.manage',
    'iam.roles.read',
    'iam.roles.manage',
    'iam.permissions.read',
    'iam.assignments.manage'
)
WHERE r.code IN ('platform_root', 'platform_admin')
ON CONFLICT (role_id, permission_id) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
    'iam.users.read',
    'iam.roles.read',
    'iam.permissions.read'
)
WHERE r.code IN ('platform_support_operator', 'tenant_viewer')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 5) Assign system users -> platform roles
-- ----------------------------------------------------------------------------
INSERT INTO user_role_assignments (
    id, user_id, role_id, scope_type, tenant_id, workspace_id, assigned_by
)
SELECT
    gen_random_uuid(),
    u.id,
    r.id,
    'platform',
    NULL,
    NULL,
    u.id
FROM users u
JOIN (
    VALUES
        ('root', 'platform_root'),
        ('sys_admin', 'platform_admin'),
        ('support_operator', 'platform_support_operator'),
        ('audit_viewer', 'platform_support_operator')
) AS map(email, role_code) ON map.email = u.email
JOIN roles r ON r.code = map.role_code
ON CONFLICT DO NOTHING;
