-- IAM clean-baseline verification.
-- Run after setting: SET search_path TO iam, hierarchy, public;

-- Durable object inventory.
SELECT object_name, to_regclass(object_name) IS NOT NULL AS present
FROM unnest(ARRAY[
    'users', 'user_profiles', 'devices', 'refresh_tokens',
    'external_identities', 'mfa_settings', 'mfa_recovery_codes',
    'permissions', 'platform_roles', 'tenant_roles', 'tenant_role_revisions',
    'tenant_role_revision_permissions', 'user_role',
    'membership_role', 'tenant_invitations', 'lifecycle_fact_outbox_records'
]) AS object_name
ORDER BY object_name;

-- Refresh credential is user/device-only. These legacy runtime-context and
-- rotation columns must not reappear in a zero-state schema.
SELECT expected.column_name,
       EXISTS (
           SELECT 1
           FROM information_schema.columns actual
           WHERE actual.table_schema=current_schema()
             AND actual.table_name='refresh_tokens'
             AND actual.column_name=expected.column_name
       ) AS present
FROM (VALUES
    ('id'), ('user_id'), ('device_id'), ('token_hash'), ('issued_at'), ('expires_at')
) AS expected(column_name)
ORDER BY expected.column_name;

SELECT forbidden.column_name,
       NOT EXISTS (
           SELECT 1
           FROM information_schema.columns actual
           WHERE actual.table_schema=current_schema()
             AND actual.table_name='refresh_tokens'
             AND actual.column_name=forbidden.column_name
       ) AS absent
FROM (VALUES ('tenant_id'), ('used_at'), ('revoked_at')) AS forbidden(column_name)
ORDER BY forbidden.column_name;

-- Required indexes and constraints for lookup, single-device replacement and
-- device cascade revocation.
SELECT expected.index_name,
       to_regclass(expected.index_name) IS NOT NULL AS present
FROM (VALUES
    ('refresh_tokens_token_hash_uidx'),
    ('refresh_tokens_user_id_idx'),
    ('refresh_tokens_device_id_idx'),
    ('refresh_tokens_expires_at_idx'),
    ('devices_user_client_device_uidx'),
    ('platform_roles_code_uidx')
) AS expected(index_name)
ORDER BY expected.index_name;

SELECT expected.constraint_name,
       EXISTS (
           SELECT 1 FROM pg_constraint actual
           WHERE actual.conname=expected.constraint_name
       ) AS present
FROM (VALUES
    ('refresh_tokens_user_device_uk'),
    ('refresh_tokens_device_id_fkey'),
    ('external_identities_provider_subject_uk'),
    ('membership_role_scope_uk')
) AS expected(constraint_name)
ORDER BY expected.constraint_name;

SELECT obj_description('refresh_tokens'::regclass) AS refresh_tokens_contract,
       col_description(
           'refresh_tokens'::regclass,
           (SELECT attnum FROM pg_attribute
            WHERE attrelid='refresh_tokens'::regclass AND attname='device_id')
       ) AS device_binding_contract;

-- No baseline data may violate the one-credential-per-device invariant.
SELECT user_id, device_id, count(*) AS duplicate_count
FROM refresh_tokens
GROUP BY user_id, device_id
HAVING count(*) > 1;

-- Baseline roles are platform-owned only. Tenant roles are created atomically
-- by tenant workflows, never globally seeded.
SELECT code, role_level, version
FROM platform_roles
ORDER BY role_level, code;

SELECT count(*)=0 AS no_globally_seeded_tenant_roles
FROM tenant_roles;

SELECT username, password_hash LIKE 'argon2id$%' AS argon2id_format
FROM users
WHERE username IN ('root', 'sys_admin', 'support_operator', 'audit_viewer', 'billing_admin')
ORDER BY username;
