-- IAM migration layer 000006 down
-- Rollback seed data inserted by 000006_iam_seeds.up.sql.

DELETE FROM user_role_assignments
WHERE user_id IN (
    SELECT id FROM users
    WHERE email IN (
        'root',
        'sys_admin',
        'support_operator',
        'audit_viewer'
    )
);

DELETE FROM role_permissions
WHERE role_id IN (
    SELECT id FROM roles
    WHERE code IN (
        'platform_root',
        'platform_user',
        'platform_admin',
        'platform_support_operator',
        'tenant_owner',
        'tenant_admin',
        'tenant_manager',
        'tenant_member',
        'tenant_viewer'
    )
)
OR permission_id IN (
    SELECT id FROM permissions
    WHERE code IN (
        'iam.users.read',
        'iam.users.manage',
        'iam.roles.read',
        'iam.roles.manage',
        'iam.permissions.read',
        'iam.assignments.manage'
    )
);

DELETE FROM roles
WHERE code IN (
    'platform_root',
    'platform_user',
    'platform_admin',
    'platform_support_operator',
    'tenant_owner',
    'tenant_admin',
    'tenant_manager',
    'tenant_member',
    'tenant_viewer'
);

DELETE FROM permissions
WHERE code IN (
    'iam.users.read',
    'iam.users.manage',
    'iam.roles.read',
    'iam.roles.manage',
    'iam.permissions.read',
    'iam.assignments.manage'
);

DELETE FROM user_profiles
WHERE user_id IN (
    SELECT id FROM users
    WHERE email IN (
        'root',
        'sys_admin',
        'support_operator',
        'audit_viewer'
    )
);

DELETE FROM users
WHERE email IN (
    'root',
    'sys_admin',
    'support_operator',
    'audit_viewer'
);
