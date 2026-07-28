-- [COMMENT]: Bỏ drop index admin do bảng đã xóa
DROP INDEX IF EXISTS role_permissions_permission_id_idx;

DROP INDEX IF EXISTS roles_scope_idx;
DROP INDEX IF EXISTS roles_scope_name_uidx;
DROP INDEX IF EXISTS roles_code_uidx;

DROP INDEX IF EXISTS permissions_behavior_idx;
DROP INDEX IF EXISTS permissions_object_idx;
DROP INDEX IF EXISTS permissions_module_idx;

DROP INDEX IF EXISTS mfa_recovery_codes_used_at_idx;
DROP INDEX IF EXISTS mfa_recovery_codes_user_id_idx;
DROP INDEX IF EXISTS mfa_recovery_codes_user_hash_uidx;

DROP INDEX IF EXISTS mfa_challenges_expires_at_idx;
DROP INDEX IF EXISTS mfa_challenges_status_idx;
DROP INDEX IF EXISTS mfa_challenges_user_id_idx;

-- DROP INDEX IF EXISTS mfa_settings_status_idx;
DROP INDEX IF EXISTS mfa_settings_user_id_idx;

-- [COMMENT]: Bỏ drop index device_challenges do bảng đã xóa

DROP INDEX IF EXISTS devices_last_seen_at_idx;
DROP INDEX IF EXISTS devices_user_id_idx;
DROP INDEX IF EXISTS devices_user_fingerprint_uidx;
DROP INDEX IF EXISTS devices_user_fingerprint_idx;
DROP INDEX IF EXISTS devices_user_client_device_uidx;
DROP INDEX IF EXISTS devices_user_active_seen_idx;

DROP INDEX IF EXISTS refresh_tokens_expires_at_idx;
DROP INDEX IF EXISTS refresh_tokens_tenant_id_idx;
DROP INDEX IF EXISTS refresh_tokens_device_id_idx;
DROP INDEX IF EXISTS refresh_tokens_user_id_idx;
DROP INDEX IF EXISTS refresh_tokens_token_hash_uidx;

DROP INDEX IF EXISTS user_profiles_fullname_idx;

DROP INDEX IF EXISTS password_history_user_created_at_idx;
DROP INDEX IF EXISTS password_history_user_id_idx;

DROP INDEX IF EXISTS users_created_at_idx;
DROP INDEX IF EXISTS users_status_idx;
DROP INDEX IF EXISTS admin_devices_last_seen_at_idx;
DROP INDEX IF EXISTS admin_devices_fingerprint_uidx;
DROP INDEX IF EXISTS users_phone_idx;
DROP INDEX IF EXISTS users_username_lower_uidx;
DROP INDEX IF EXISTS users_email_lower_uidx;
