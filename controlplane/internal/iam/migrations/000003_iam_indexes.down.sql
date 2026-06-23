DROP INDEX IF EXISTS idx_iam_outbox_pending;
DROP INDEX IF EXISTS audit_events_created_at_idx;
DROP INDEX IF EXISTS audit_events_severity_idx;
DROP INDEX IF EXISTS audit_events_event_idx;
DROP INDEX IF EXISTS audit_events_tenant_workspace_idx;
DROP INDEX IF EXISTS audit_events_actor_user_id_idx;

DROP INDEX IF EXISTS admin_action_audits_status_created_at_idx;
DROP INDEX IF EXISTS admin_action_audits_resource_idx;
DROP INDEX IF EXISTS admin_action_audits_action_created_at_idx;
DROP INDEX IF EXISTS admin_action_audits_created_at_idx;

DROP INDEX IF EXISTS admin_recovery_codes_used_at_idx;
DROP INDEX IF EXISTS admin_recovery_codes_created_at_idx;
DROP INDEX IF EXISTS admin_recovery_codes_code_hash_uidx;
DROP INDEX IF EXISTS admin_2fa_settings_updated_at_idx;
DROP INDEX IF EXISTS oauth_tokens_expires_at_idx;
DROP INDEX IF EXISTS oauth_tokens_grant_id_idx;
DROP INDEX IF EXISTS oauth_tokens_user_id_idx;
DROP INDEX IF EXISTS oauth_tokens_client_id_idx;
DROP INDEX IF EXISTS oauth_tokens_refresh_hash_uidx;
DROP INDEX IF EXISTS oauth_tokens_access_hash_uidx;

DROP INDEX IF EXISTS oauth_grants_tenant_workspace_idx;
DROP INDEX IF EXISTS oauth_grants_user_id_idx;
DROP INDEX IF EXISTS oauth_grants_client_id_idx;

DROP INDEX IF EXISTS oauth_authorization_codes_expires_at_idx;
DROP INDEX IF EXISTS oauth_authorization_codes_user_id_idx;
DROP INDEX IF EXISTS oauth_authorization_codes_client_id_idx;
DROP INDEX IF EXISTS oauth_authorization_codes_hash_uidx;

DROP INDEX IF EXISTS oauth_client_secrets_client_id_idx;
DROP INDEX IF EXISTS oauth_client_secrets_hash_uidx;
DROP INDEX IF EXISTS oauth_client_secrets_prefix_uidx;

DROP INDEX IF EXISTS oauth_clients_status_idx;
DROP INDEX IF EXISTS oauth_clients_tenant_workspace_idx;
DROP INDEX IF EXISTS oauth_clients_owner_user_id_idx;

DROP INDEX IF EXISTS user_role_assignments_tenant_workspace_idx;
DROP INDEX IF EXISTS user_role_assignments_scope_type_idx;
DROP INDEX IF EXISTS user_role_assignments_role_id_idx;
DROP INDEX IF EXISTS user_role_assignments_user_id_idx;
DROP INDEX IF EXISTS user_role_assignments_platform_scope_uidx;
DROP INDEX IF EXISTS user_role_assignments_tenant_scope_uidx;
DROP INDEX IF EXISTS user_role_assignments_workspace_scope_uidx;

DROP INDEX IF EXISTS role_permissions_permission_id_idx;

DROP INDEX IF EXISTS roles_scope_type_idx;
DROP INDEX IF EXISTS roles_scope_name_uidx;
DROP INDEX IF EXISTS roles_code_uidx;

DROP INDEX IF EXISTS permissions_action_idx;
DROP INDEX IF EXISTS permissions_resource_idx;
DROP INDEX IF EXISTS permissions_resource_action_uidx;
DROP INDEX IF EXISTS permissions_code_uidx;

DROP INDEX IF EXISTS mfa_recovery_codes_used_at_idx;
DROP INDEX IF EXISTS mfa_recovery_codes_user_id_idx;
DROP INDEX IF EXISTS mfa_recovery_codes_user_hash_uidx;

DROP INDEX IF EXISTS mfa_challenges_expires_at_idx;
DROP INDEX IF EXISTS mfa_challenges_status_idx;
DROP INDEX IF EXISTS mfa_challenges_user_id_idx;

DROP INDEX IF EXISTS mfa_settings_status_idx;
DROP INDEX IF EXISTS mfa_settings_user_id_idx;

DROP INDEX IF EXISTS device_challenges_expires_at_idx;
DROP INDEX IF EXISTS device_challenges_status_idx;
DROP INDEX IF EXISTS device_challenges_device_id_idx;
DROP INDEX IF EXISTS device_challenges_user_id_idx;
DROP INDEX IF EXISTS device_challenges_nonce_uidx;
DROP INDEX IF EXISTS idx_device_challenges_user_status_expires;
DROP INDEX IF EXISTS idx_device_challenges_device_status_expires;

DROP INDEX IF EXISTS devices_last_seen_at_idx;
DROP INDEX IF EXISTS devices_status_idx;
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
