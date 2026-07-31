-- IAM migration layer 000006 down
-- Rollback only records owned by the bootstrap seed.

-- [COMMENT]: Workspace rows do not carry a cross-schema FK to IAM users, so remove seeded owners explicitly.
DELETE FROM hierarchy.personal_workspaces
WHERE owner_id IN (
    '00000000-0000-0000-0000-000000000001'::uuid,
    '00000000-0000-0000-0000-000000000002'::uuid,
    '00000000-0000-0000-0000-000000000003'::uuid,
    '00000000-0000-0000-0000-000000000004'::uuid,
    '00000000-0000-0000-0000-000000000005'::uuid
);

-- [COMMENT]: Role deletion cascades role_permissions, user_role, and tenant_role before root user is removed.
DELETE FROM roles
WHERE code IN (
    'platform_root',
    'platform_user',
    'platform_admin',
    'billing_admin',
    'platform_support_operator',
    'tenant_owner',
    'tenant_admin',
    'tenant_manager',
    'tenant_member',
    'tenant_viewer'
);

DELETE FROM permissions
WHERE (module, object, behavior) IN (
    VALUES
        ('iam', 'users', 'read'),
        ('iam', 'users', 'manage'),
        ('iam', 'role', 'read'),
        ('iam', 'role', 'write'),
        ('iam', 'role', 'assign'),
        ('iam', 'role', 'delete'),
        ('iam', 'permissions', 'read'),
        ('iam', 'device', 'read'),
        ('iam', 'mfa', 'view'),
        ('storage', 'bucket', 'read'),
        ('storage', 'bucket', 'write'),
        ('storage', 'bucket', 'delete'),
        ('storage', 'credential', 'read'),
        ('storage', 'credential', 'write'),
        ('storage', 'credential', 'delete'),
        ('hierarchy', 'workspace', 'create'),
        ('hierarchy', 'workspace', 'read'),
        ('hierarchy', 'workspace', 'update'),
        ('hierarchy', 'workspace', 'delete'),
        ('email', 'consumer', 'create'),
        ('email', 'consumer', 'read'),
        ('email', 'consumer', 'update'),
        ('email', 'consumer', 'delete'),
        ('email', 'template', 'create'),
        ('email', 'template', 'read'),
        ('email', 'template', 'publish'),
        ('email', 'template', 'delete'),
        ('billing', 'plan', 'read'),
        ('billing', 'tier', 'read'),
        ('billing', 'tier', 'publish'),
        ('billing', 'wallet', 'read'),
        ('billing', 'wallet', 'top_up'),
        ('billing', 'ledger', 'read'),
        ('billing', 'subscription', 'write'),
        ('billing', 'credit', 'adjust'),
        ('hypervisor', 'vm', 'read'),
        ('hypervisor', 'vm', 'create'),
        ('hypervisor', 'image', 'read'),
        ('hypervisor', 'image', 'create'),
        ('hypervisor', 'image', 'publish'),
        ('hypervisor', 'image', 'delete'),
        ('managed-service', 'catalog', 'read'),
        ('managed-service', 'instance', 'read')
);

DELETE FROM user_profiles
WHERE user_id IN (
    '00000000-0000-0000-0000-000000000001'::uuid,
    '00000000-0000-0000-0000-000000000002'::uuid,
    '00000000-0000-0000-0000-000000000003'::uuid,
    '00000000-0000-0000-0000-000000000004'::uuid,
    '00000000-0000-0000-0000-000000000005'::uuid
);

DELETE FROM users
WHERE id IN (
    '00000000-0000-0000-0000-000000000001'::uuid,
    '00000000-0000-0000-0000-000000000002'::uuid,
    '00000000-0000-0000-0000-000000000003'::uuid,
    '00000000-0000-0000-0000-000000000004'::uuid,
    '00000000-0000-0000-0000-000000000005'::uuid
);
