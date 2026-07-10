-- IAM post-migration verification queries
-- Usage:
--   psql -h 127.0.0.1 -p 15433 -U postgres -d controlplane -f internal/iam/migrations/verification_iam_post_migrate.sql
-- Optional schema override:
--   SET search_path TO iam,public;

-- ============================================================================
-- 1) Object existence (to_regclass)
-- ============================================================================
SELECT 'users' AS object_name, to_regclass('users') AS regclass;
SELECT 'refresh_tokens' AS object_name, to_regclass('refresh_tokens') AS regclass;
SELECT 'devices' AS object_name, to_regclass('devices') AS regclass;
SELECT 'device_challenges' AS object_name, to_regclass('device_challenges') AS regclass;
SELECT 'mfa_settings' AS object_name, to_regclass('mfa_settings') AS regclass;
SELECT 'mfa_challenges' AS object_name, to_regclass('mfa_challenges') AS regclass;
SELECT 'permissions' AS object_name, to_regclass('permissions') AS regclass;
SELECT 'roles' AS object_name, to_regclass('roles') AS regclass;
SELECT 'user_role_assignments' AS object_name, to_regclass('user_role_assignments') AS regclass;

-- ============================================================================
-- 2) Index existence (pg_indexes)
-- ============================================================================
SELECT tablename, indexname
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname IN (
    'users_username_lower_uidx',
    'users_email_lower_uidx',
    'refresh_tokens_token_hash_uidx',
    'devices_user_fingerprint_uidx',
    'device_challenges_nonce_uidx',
    'permissions_code_uidx',
    'permissions_resource_action_uidx',
    'roles_code_uidx',
    'user_role_assignments_platform_scope_uidx',
    'user_role_assignments_tenant_scope_uidx',
    'user_role_assignments_workspace_scope_uidx'
  )
ORDER BY tablename, indexname;

-- ============================================================================
-- 3) Constraint existence (pg_constraint)
-- ============================================================================
SELECT conrelid::regclass AS table_name, conname, contype
FROM pg_constraint
WHERE conname IN (
  'refresh_tokens_device_id_fkey',
  'device_challenges_nonce_key',
  'device_challenges_expires_after_created_chk',
  'mfa_settings_user_id_key',
  'roles_role_level_check'
)
ORDER BY table_name::text, conname;

-- ============================================================================
-- 4) Contract comments (obj_description + col_description)
-- ============================================================================
SELECT 'refresh_tokens' AS table_name, obj_description('refresh_tokens'::regclass) AS table_comment;


SELECT 'refresh_tokens.device_id' AS column_name,
       col_description('refresh_tokens'::regclass, (
         SELECT attnum FROM pg_attribute
         WHERE attrelid = 'refresh_tokens'::regclass AND attname = 'device_id'
       )) AS column_comment;

SELECT 'device_challenges.nonce' AS column_name,
       col_description('device_challenges'::regclass, (
         SELECT attnum FROM pg_attribute
         WHERE attrelid = 'device_challenges'::regclass AND attname = 'nonce'
       )) AS column_comment;

-- ============================================================================
-- 5) Seed password hash format check (Argon2id)
-- ============================================================================
SELECT
    email,
    split_part(password_hash, '$', 1) AS hash_prefix,
    (split_part(password_hash, '$', 1) = 'argon2id') AS argon2id_prefix_ok
FROM users
WHERE email IN ('root', 'sys_admin', 'support_operator', 'audit_viewer')
ORDER BY email;
