-- IAM baseline seed. This file describes the complete initial IAM state from
-- zero; it is not an incremental production data patch.

-- [COMMENT]: These two migration-local functions encode protobuf wire type 2
-- so every seeded assignment is compiled from the same normalized catalog.
-- Hard-coded binary blobs would silently become stale when the baseline adds a
-- permission.
CREATE FUNCTION iam_seed_varint(input_value bigint)
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    remaining bigint := input_value;
    current_byte integer;
    encoded bytea := ''::bytea;
BEGIN
    IF remaining = 0 THEN
        RETURN decode('00', 'hex');
    END IF;
    WHILE remaining > 0 LOOP
        current_byte := (remaining & 127)::integer;
        remaining := remaining >> 7;
        IF remaining > 0 THEN
            current_byte := current_byte | 128;
        END IF;
        encoded := encoded || decode(lpad(to_hex(current_byte), 2, '0'), 'hex');
    END LOOP;
    RETURN encoded;
END;
$$;

CREATE FUNCTION iam_seed_role_entry(permission_keys text[])
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    permission_key text;
    permission_bytes bytea;
    encoded bytea := ''::bytea;
BEGIN
    FOREACH permission_key IN ARRAY COALESCE(permission_keys, ARRAY[]::text[]) LOOP
        permission_bytes := convert_to(permission_key, 'UTF8');
        encoded := encoded || decode('0a', 'hex')
                  || iam_seed_varint(octet_length(permission_bytes))
                  || permission_bytes;
    END LOOP;
    RETURN encoded;
END;
$$;

-- 1. Canonical bootstrap identities. Password is ChangeMe123! for local/dev
-- only and must be replaced before a non-development deployment.
INSERT INTO users (id, username, email, password_hash, status)
VALUES
    ('00000000-0000-0000-0000-000000000001', 'root', 'root', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000002', 'sys_admin', 'sys_admin', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000003', 'support_operator', 'support_operator', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000004', 'audit_viewer', 'audit_viewer', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active'),
    ('00000000-0000-0000-0000-000000000005', 'billing_admin', 'billing_admin', 'argon2id$v=19$m=65536,t=1,p=2$vYGk/DrySMoSKrini+XPWw$43iqwaG1qll2360XKiADPE2ng7IbWhsIbMO69N/+KFY', 'active');

INSERT INTO user_profiles (user_id, fullname, bio, locale, timezone)
SELECT u.id, seed.fullname, seed.bio, 'vi-VN', 'Asia/Ho_Chi_Minh'
FROM users u
JOIN (VALUES
    ('root', 'Root User', 'Highest authority root account.'),
    ('sys_admin', 'System Administrator', 'Platform administrator.'),
    ('support_operator', 'Support Operator', 'Support operator for customer issues.'),
    ('audit_viewer', 'Audit Viewer', 'Auditor for system activity.'),
    ('billing_admin', 'Billing Administrator', 'Billing catalogue and finance operator.')
) AS seed(username, fullname, bio) ON seed.username=u.username;

-- 2. Context-free three-level permission catalogue.
INSERT INTO permissions (id, module, object, behavior, description)
VALUES
    (gen_random_uuid(), 'iam', 'users', 'read', 'Read user accounts'),
    (gen_random_uuid(), 'iam', 'users', 'manage', 'Manage user accounts'),
    (gen_random_uuid(), 'iam', 'role', 'read', 'Read RBAC roles'),
    (gen_random_uuid(), 'iam', 'role', 'write', 'Create RBAC roles'),
    (gen_random_uuid(), 'iam', 'role', 'assign', 'Assign RBAC roles'),
    (gen_random_uuid(), 'iam', 'role', 'delete', 'Delete RBAC roles'),
    (gen_random_uuid(), 'iam', 'permissions', 'read', 'Read permission catalogue'),
    (gen_random_uuid(), 'iam', 'device', 'read', 'Read user devices'),
    (gen_random_uuid(), 'iam', 'mfa', 'view', 'View user MFA status'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'create', 'Create workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'read', 'Read workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'update', 'Update workspace'),
    (gen_random_uuid(), 'hierarchy', 'workspace', 'delete', 'Delete workspace'),
    (gen_random_uuid(), 'hierarchy', 'tenant-invitation', 'create', 'Create tenant invitation'),
    (gen_random_uuid(), 'hierarchy', 'tenant-invitation', 'read', 'Read tenant invitation'),
    (gen_random_uuid(), 'hierarchy', 'tenant-invitation', 'delete', 'Revoke tenant invitation'),
    (gen_random_uuid(), 'storage', 'bucket', 'read', 'Read storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'write', 'Write storage bucket'),
    (gen_random_uuid(), 'storage', 'bucket', 'delete', 'Delete storage bucket'),
    (gen_random_uuid(), 'storage', 'credential', 'read', 'Read storage credential'),
    (gen_random_uuid(), 'storage', 'credential', 'write', 'Write storage credential'),
    (gen_random_uuid(), 'storage', 'credential', 'delete', 'Delete storage credential'),
    (gen_random_uuid(), 'email', 'consumer', 'create', 'Create email consumer'),
    (gen_random_uuid(), 'email', 'consumer', 'read', 'Read email consumer'),
    (gen_random_uuid(), 'email', 'consumer', 'update', 'Update email consumer'),
    (gen_random_uuid(), 'email', 'consumer', 'delete', 'Delete email consumer'),
    (gen_random_uuid(), 'email', 'template', 'create', 'Create email template'),
    (gen_random_uuid(), 'email', 'template', 'read', 'Read email template'),
    (gen_random_uuid(), 'email', 'template', 'publish', 'Publish email template'),
    (gen_random_uuid(), 'email', 'template', 'delete', 'Delete email template'),
    (gen_random_uuid(), 'billing', 'plan', 'read', 'Read billing plan'),
    (gen_random_uuid(), 'billing', 'tier', 'read', 'Read billing tier'),
    (gen_random_uuid(), 'billing', 'tier', 'publish', 'Publish billing tier'),
    (gen_random_uuid(), 'billing', 'wallet', 'read', 'Read wallet'),
    (gen_random_uuid(), 'billing', 'wallet', 'top_up', 'Fund tenant wallet'),
    (gen_random_uuid(), 'billing', 'ledger', 'read', 'Read ledger'),
    (gen_random_uuid(), 'billing', 'subscription', 'write', 'Change subscription'),
    (gen_random_uuid(), 'billing', 'credit', 'adjust', 'Adjust wallet credit'),
    (gen_random_uuid(), 'hypervisor', 'vm', 'read', 'Read virtual machine'),
    (gen_random_uuid(), 'hypervisor', 'vm', 'create', 'Create virtual machine'),
    (gen_random_uuid(), 'hypervisor', 'image', 'read', 'Read image catalogue'),
    (gen_random_uuid(), 'hypervisor', 'image', 'create', 'Register image'),
    (gen_random_uuid(), 'hypervisor', 'image', 'publish', 'Publish image'),
    (gen_random_uuid(), 'hypervisor', 'image', 'delete', 'Delete image'),
    (gen_random_uuid(), 'managed-service', 'catalog', 'read', 'Read managed service catalogue'),
    (gen_random_uuid(), 'managed-service', 'instance', 'read', 'Read managed service instance'),
    (gen_random_uuid(), 'managed-service', 'instance', 'write', 'Operate managed service instance');

-- 3. Platform roles only. tenant_root is created per tenant by CreateTenant.
INSERT INTO platform_roles (id, code, name, description, role_level, version, created_by)
VALUES
    (gen_random_uuid(), 'platform_root', 'Root', 'Highest platform authority', 0, 1, '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'platform_admin', 'System Admin', 'Platform administrator', 1, 1, '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'billing_admin', 'Billing Admin', 'Billing administrator', 1, 1, '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'platform_support_operator', 'Support Operator', 'Read-only support operator', 2, 1, '00000000-0000-0000-0000-000000000001'),
    (gen_random_uuid(), 'platform_user', 'Platform User', 'Default personal role', 8, 1, '00000000-0000-0000-0000-000000000001');

INSERT INTO platform_role_permissions (role_id, permission_id)
SELECT role.id, permission.id
FROM platform_roles role
CROSS JOIN permissions permission
WHERE
    (role.code IN ('platform_root', 'platform_admin')
        AND NOT (permission.module='billing' AND permission.object='wallet' AND permission.behavior='top_up'))
 OR (role.code='billing_admin' AND permission.module='billing'
        AND NOT (permission.object='wallet' AND permission.behavior='top_up'))
 OR (role.code='platform_support_operator'
        AND (permission.behavior='read'
             OR (permission.module='iam' AND permission.object='mfa' AND permission.behavior='view')))
 OR (role.code='platform_user' AND (
		(permission.module='hierarchy' AND permission.object='workspace'
			AND permission.behavior IN ('create', 'read', 'delete'))
	 OR
        (permission.module='managed-service' AND permission.object IN ('catalog', 'instance')
            AND permission.behavior IN ('read', 'write'))
     OR (permission.module='billing' AND permission.object='subscription' AND permission.behavior='write')
     OR (permission.module='email' AND permission.object IN ('consumer', 'template'))
     OR (permission.module='hypervisor' AND (
            (permission.object='vm' AND permission.behavior IN ('read', 'create'))
         OR (permission.object='image' AND permission.behavior='read')
        ))
    ));

-- 4. Compile global five-level assignments from normalized mappings. The
-- partial unique index guarantees one effective platform role per user.
WITH bootstrap_assignment(username, role_code) AS (
    VALUES
        ('root', 'platform_root'),
        ('sys_admin', 'platform_admin'),
        ('support_operator', 'platform_support_operator'),
        ('audit_viewer', 'platform_support_operator'),
        ('billing_admin', 'billing_admin')
), compiled AS (
    SELECT u.id AS user_id, u.username, role.id AS role_id, role.name AS role_name,
           role.role_level, role.version,
           iam_seed_role_entry(array_agg(
               u.username || ':00000000-0000-0000-0000-000000000000:'
               || permission.module || ':' || permission.object || ':' || permission.behavior
               ORDER BY permission.module, permission.object, permission.behavior
           )) AS list_perm
    FROM bootstrap_assignment seed
    JOIN users u ON u.username=seed.username
    JOIN platform_roles role ON role.code=seed.role_code
    JOIN platform_role_permissions mapping ON mapping.role_id=role.id
    JOIN permissions permission ON permission.id=mapping.permission_id
    GROUP BY u.id, u.username, role.id, role.name, role.role_level, role.version
)
INSERT INTO user_role
    (id, user_id, username, workspace_id, role_id, role_name, role_level, role_version, list_perm)
SELECT gen_random_uuid(), user_id, username,
       '00000000-0000-0000-0000-000000000000'::uuid,
       role_id, role_name, role_level, version, list_perm
FROM compiled;

-- 5. Canonical development Zone and one personal workspace per bootstrap user.
INSERT INTO hierarchy.zones (id, code, name, location, status)
VALUES ('019f3d3e-997d-7894-9236-c5122634cb4f', 'edge-viet-nam-1', 'Edge Viet Nam 1', 'Ha Noi, Vietnam', 'active');

INSERT INTO hierarchy.zone_services (id, zone_id, service_type, desired_state, actual_state)
VALUES
    (gen_random_uuid(), '019f3d3e-997d-7894-9236-c5122634cb4f', 'hypervisor', true, 'healthy'),
    (gen_random_uuid(), '019f3d3e-997d-7894-9236-c5122634cb4f', 'storage', true, 'healthy'),
    (gen_random_uuid(), '019f3d3e-997d-7894-9236-c5122634cb4f', 'mail', true, 'healthy'),
    (gen_random_uuid(), '019f3d3e-997d-7894-9236-c5122634cb4f', 'kubernetes', false, 'unknown'),
    (gen_random_uuid(), '019f3d3e-997d-7894-9236-c5122634cb4f', 'ai', false, 'unknown'),
    (gen_random_uuid(), '019f3d3e-997d-7894-9236-c5122634cb4f', 'database', false, 'unknown');

INSERT INTO hierarchy.personal_workspaces (id, name, code, zone_id, owner_id)
SELECT gen_random_uuid(), 'Default Workspace', 'default-' || lower(username),
       '019f3d3e-997d-7894-9236-c5122634cb4f', id
FROM users
WHERE status='active';

WITH compiled AS (
    SELECT workspace.id AS workspace_id, user_account.id AS user_id, user_account.username,
           role.id AS role_id, role.name AS role_name, role.role_level, role.version,
           iam_seed_role_entry(array_agg(
               user_account.username || ':' || workspace.id::text || ':'
               || permission.module || ':' || permission.object || ':' || permission.behavior
               ORDER BY permission.module, permission.object, permission.behavior
           )) AS list_perm
    FROM hierarchy.personal_workspaces workspace
    JOIN users user_account ON user_account.id=workspace.owner_id AND user_account.status='active'
    JOIN platform_roles role ON role.code='platform_user'
    JOIN platform_role_permissions mapping ON mapping.role_id=role.id
    JOIN permissions permission ON permission.id=mapping.permission_id
    GROUP BY workspace.id, user_account.id, user_account.username,
             role.id, role.name, role.role_level, role.version
)
INSERT INTO user_role
    (id, user_id, username, workspace_id, role_id, role_name, role_level, role_version, list_perm)
SELECT gen_random_uuid(), user_id, username, workspace_id, role_id, role_name,
       role_level, version, list_perm
FROM compiled;

DROP FUNCTION iam_seed_role_entry(text[]);
DROP FUNCTION iam_seed_varint(bigint);
