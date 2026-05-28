-- IAM migration layer 000003
-- Secondary indexes and uniqueness constraints.

CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_uidx ON users (lower(username));
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uidx ON users (lower(email));
CREATE INDEX IF NOT EXISTS users_phone_idx ON users(phone);
CREATE INDEX IF NOT EXISTS users_status_idx ON users(status);
CREATE INDEX IF NOT EXISTS users_created_at_idx ON users(created_at);

CREATE INDEX IF NOT EXISTS password_history_user_id_idx ON password_history(user_id);
CREATE INDEX IF NOT EXISTS password_history_user_created_at_idx ON password_history(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS user_profiles_fullname_idx ON user_profiles(fullname);

CREATE UNIQUE INDEX IF NOT EXISTS refresh_tokens_token_hash_uidx ON refresh_tokens(token_hash);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_id_idx ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_device_id_idx ON refresh_tokens(device_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_tenant_id_idx ON refresh_tokens(tenant_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_expires_at_idx ON refresh_tokens(expires_at);
CREATE INDEX IF NOT EXISTS refresh_tokens_family_idx ON refresh_tokens(token_family_id);

CREATE UNIQUE INDEX IF NOT EXISTS devices_user_fingerprint_uidx ON devices(user_id, public_key_fingerprint);
CREATE INDEX IF NOT EXISTS devices_user_id_idx ON devices(user_id);
CREATE INDEX IF NOT EXISTS devices_status_idx ON devices(status);
CREATE INDEX IF NOT EXISTS devices_last_seen_at_idx ON devices(last_seen_at);

CREATE UNIQUE INDEX IF NOT EXISTS devices_user_client_device_uidx
    ON devices(user_id, client_device_id)
    WHERE client_device_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS devices_user_active_seen_idx
    ON devices (user_id, last_seen_at DESC, created_at DESC)
    WHERE status != 'revoked';

CREATE UNIQUE INDEX IF NOT EXISTS device_challenges_nonce_uidx ON device_challenges(nonce);
CREATE INDEX IF NOT EXISTS device_challenges_user_id_idx ON device_challenges(user_id);
CREATE INDEX IF NOT EXISTS device_challenges_device_id_idx ON device_challenges(device_id);
CREATE INDEX IF NOT EXISTS device_challenges_status_idx ON device_challenges(status);
CREATE INDEX IF NOT EXISTS device_challenges_expires_at_idx ON device_challenges(expires_at);
CREATE INDEX IF NOT EXISTS idx_device_challenges_device_status_expires ON device_challenges(device_id, status, expires_at);
CREATE INDEX IF NOT EXISTS idx_device_challenges_user_status_expires ON device_challenges(user_id, status, expires_at);

CREATE INDEX IF NOT EXISTS mfa_settings_user_id_idx ON mfa_settings(user_id);
CREATE INDEX IF NOT EXISTS mfa_settings_status_idx ON mfa_settings(status);

CREATE INDEX IF NOT EXISTS mfa_challenges_user_id_idx ON mfa_challenges(user_id);
CREATE INDEX IF NOT EXISTS mfa_challenges_status_idx ON mfa_challenges(status);
CREATE INDEX IF NOT EXISTS mfa_challenges_expires_at_idx ON mfa_challenges(expires_at);

CREATE UNIQUE INDEX IF NOT EXISTS mfa_recovery_codes_user_hash_uidx ON mfa_recovery_codes(user_id, code_hash);
CREATE INDEX IF NOT EXISTS mfa_recovery_codes_user_id_idx ON mfa_recovery_codes(user_id);
CREATE INDEX IF NOT EXISTS mfa_recovery_codes_used_at_idx ON mfa_recovery_codes(used_at);

CREATE UNIQUE INDEX IF NOT EXISTS permissions_code_uidx ON permissions(code);
CREATE UNIQUE INDEX IF NOT EXISTS permissions_resource_action_uidx ON permissions(resource, action);
CREATE INDEX IF NOT EXISTS permissions_resource_idx ON permissions(resource);
CREATE INDEX IF NOT EXISTS permissions_action_idx ON permissions(action);

CREATE UNIQUE INDEX IF NOT EXISTS roles_code_uidx ON roles(code);
CREATE UNIQUE INDEX IF NOT EXISTS roles_scope_name_uidx ON roles(scope_type, name);
CREATE INDEX IF NOT EXISTS roles_scope_type_idx ON roles(scope_type);

CREATE INDEX IF NOT EXISTS role_permissions_permission_id_idx ON role_permissions(permission_id);

CREATE INDEX IF NOT EXISTS user_role_assignments_user_id_idx ON user_role_assignments(user_id);
CREATE INDEX IF NOT EXISTS user_role_assignments_role_id_idx ON user_role_assignments(role_id);
CREATE INDEX IF NOT EXISTS user_role_assignments_scope_type_idx ON user_role_assignments(scope_type);
CREATE INDEX IF NOT EXISTS user_role_assignments_tenant_workspace_idx ON user_role_assignments(tenant_id, workspace_id);
CREATE UNIQUE INDEX IF NOT EXISTS user_role_assignments_platform_scope_uidx ON user_role_assignments(user_id, role_id, scope_type)
    WHERE tenant_id IS NULL AND workspace_id IS NULL AND revoked_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS user_role_assignments_tenant_scope_uidx ON user_role_assignments(user_id, role_id, scope_type, tenant_id)
    WHERE tenant_id IS NOT NULL AND workspace_id IS NULL AND revoked_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS user_role_assignments_workspace_scope_uidx ON user_role_assignments(user_id, role_id, scope_type, tenant_id, workspace_id)
    WHERE tenant_id IS NOT NULL AND workspace_id IS NOT NULL AND revoked_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS admin_devices_fingerprint_uidx ON admin_devices(public_key_fingerprint);
CREATE INDEX IF NOT EXISTS admin_devices_last_seen_at_idx ON admin_devices(last_seen_at);


CREATE INDEX IF NOT EXISTS oauth_clients_owner_user_id_idx ON oauth_clients(owner_user_id);
CREATE INDEX IF NOT EXISTS oauth_clients_tenant_workspace_idx ON oauth_clients(tenant_id, workspace_id);
CREATE INDEX IF NOT EXISTS oauth_clients_status_idx ON oauth_clients(status);

CREATE UNIQUE INDEX IF NOT EXISTS oauth_client_secrets_prefix_uidx ON oauth_client_secrets(secret_prefix);
CREATE UNIQUE INDEX IF NOT EXISTS oauth_client_secrets_hash_uidx ON oauth_client_secrets(secret_hash);
CREATE INDEX IF NOT EXISTS oauth_client_secrets_client_id_idx ON oauth_client_secrets(client_id);

CREATE UNIQUE INDEX IF NOT EXISTS oauth_authorization_codes_hash_uidx ON oauth_authorization_codes(code_hash);
CREATE INDEX IF NOT EXISTS oauth_authorization_codes_client_id_idx ON oauth_authorization_codes(client_id);
CREATE INDEX IF NOT EXISTS oauth_authorization_codes_user_id_idx ON oauth_authorization_codes(user_id);
CREATE INDEX IF NOT EXISTS oauth_authorization_codes_expires_at_idx ON oauth_authorization_codes(expires_at);

CREATE INDEX IF NOT EXISTS oauth_grants_client_id_idx ON oauth_grants(client_id);
CREATE INDEX IF NOT EXISTS oauth_grants_user_id_idx ON oauth_grants(user_id);
CREATE INDEX IF NOT EXISTS oauth_grants_tenant_workspace_idx ON oauth_grants(tenant_id, workspace_id);

CREATE UNIQUE INDEX IF NOT EXISTS oauth_tokens_access_hash_uidx ON oauth_tokens(access_token_hash);
CREATE UNIQUE INDEX IF NOT EXISTS oauth_tokens_refresh_hash_uidx ON oauth_tokens(refresh_token_hash) WHERE refresh_token_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS oauth_tokens_client_id_idx ON oauth_tokens(client_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_user_id_idx ON oauth_tokens(user_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_grant_id_idx ON oauth_tokens(grant_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_family_idx ON oauth_tokens(token_family_id);
CREATE INDEX IF NOT EXISTS oauth_tokens_expires_at_idx ON oauth_tokens(expires_at);

CREATE INDEX IF NOT EXISTS audit_events_actor_user_id_idx ON audit_events(actor_user_id);
CREATE INDEX IF NOT EXISTS audit_events_tenant_workspace_idx ON audit_events(tenant_id, workspace_id);
CREATE INDEX IF NOT EXISTS audit_events_event_idx ON audit_events(event);
CREATE INDEX IF NOT EXISTS audit_events_severity_idx ON audit_events(severity);
CREATE INDEX IF NOT EXISTS audit_events_created_at_idx ON audit_events(created_at);

-- Admin API key v1 indexes
CREATE UNIQUE INDEX IF NOT EXISTS admin_api_keys_key_hash_uidx ON admin_api_keys(key_hash);
CREATE INDEX IF NOT EXISTS admin_api_keys_expires_at_idx_v1 ON admin_api_keys(expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS admin_api_keys_singleton_uidx ON admin_api_keys((true));

-- Admin action audit v1 indexes
CREATE INDEX IF NOT EXISTS admin_action_audits_created_at_idx ON admin_action_audits(created_at);
CREATE INDEX IF NOT EXISTS admin_action_audits_action_created_at_idx ON admin_action_audits(action, created_at);
CREATE INDEX IF NOT EXISTS admin_action_audits_resource_idx ON admin_action_audits(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS admin_action_audits_status_created_at_idx ON admin_action_audits(status, created_at);

-- Admin 2FA/recovery v1 indexes
CREATE INDEX IF NOT EXISTS admin_2fa_settings_updated_at_idx ON admin_2fa_settings(updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS admin_recovery_codes_code_hash_uidx ON admin_recovery_codes(code_hash);
CREATE INDEX IF NOT EXISTS admin_recovery_codes_created_at_idx ON admin_recovery_codes(created_at);
CREATE INDEX IF NOT EXISTS admin_recovery_codes_used_at_idx ON admin_recovery_codes(used_at);
