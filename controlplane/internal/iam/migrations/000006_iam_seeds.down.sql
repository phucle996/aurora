-- IAM migration layer 000006 down
-- Rollback only records owned by the bootstrap seed.

-- [COMMENT]: Rollback personal wallet provision outbox records for canonical bootstrap users
DELETE FROM lifecycle_fact_outbox_records
WHERE event_id IN (
    '59002562-bee4-5f1a-aa66-6a0a9e0efcfa'::uuid,
    'cb70c09c-dfee-5a92-b224-85eaf583117e'::uuid,
    '5d112cc7-128d-5598-a6c6-376e9c73642f'::uuid,
    '66191a81-8a5a-590d-a961-5fa0716c7fbc'::uuid,
    '81b33b0c-65a1-538f-9b36-62eeb548b471'::uuid
);

-- [COMMENT]: Workspace rows do not carry a cross-schema FK to IAM users, so remove seeded owners explicitly.
DELETE FROM hierarchy.personal_workspaces
WHERE owner_id IN (
    '00000000-0000-0000-0000-000000000001'::uuid,
    '00000000-0000-0000-0000-000000000002'::uuid,
    '00000000-0000-0000-0000-000000000003'::uuid,
    '00000000-0000-0000-0000-000000000004'::uuid,
    '00000000-0000-0000-0000-000000000005'::uuid
);

-- [COMMENT]: Role deletion cascades platform_role_permissions and user_role before root user is removed.
DELETE FROM platform_roles
WHERE code IN (
    'platform_root',
    'platform_user',
    'platform_admin',
    'billing_admin',
    'platform_support_operator'
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
        ('hierarchy', 'tenant', 'create'),
        ('hierarchy', 'tenant-invitation', 'create'),
        ('hierarchy', 'tenant-invitation', 'read'),
        ('hierarchy', 'tenant-invitation', 'delete'),
        ('email', 'consumer', 'create'),
        ('email', 'consumer', 'read'),
        ('email', 'consumer', 'update'),
        ('email', 'consumer', 'delete'),
        ('email', 'template', 'create'),
        ('email', 'template', 'read'),
        ('email', 'template', 'publish'),
        ('email', 'template', 'delete'),
        ('billing', 'pricing_schedule', 'read'),
        ('billing', 'pricing_schedule', 'publish'),
        ('billing', 'wallet', 'read'),
        ('billing', 'wallet', 'top_up'),
        ('billing', 'ledger', 'read'),
        ('billing', 'subscription', 'write'),
        ('billing', 'credit', 'adjust'),
        ('hypervisor', 'vm', 'read'),
        ('hypervisor', 'vm', 'create'),
        ('hypervisor', 'vm', 'delete'),
        ('hypervisor', 'image', 'read'),
        ('hypervisor', 'image', 'create'),
        ('hypervisor', 'image', 'publish'),
        ('hypervisor', 'image', 'delete'),
        ('managed-service', 'catalog', 'read'),
        ('managed-service', 'instance', 'read'),
        ('managed-service', 'instance', 'write')
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
