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

-- [COMMENT]: Xóa unique constraint để cho phép nhiều user đăng nhập chung browser/cặp khóa Ed25519
CREATE INDEX IF NOT EXISTS devices_user_fingerprint_idx ON devices(user_id, public_key_fingerprint);
CREATE INDEX IF NOT EXISTS devices_user_id_idx ON devices(user_id);
CREATE INDEX IF NOT EXISTS devices_last_seen_at_idx ON devices(last_seen_at);

CREATE UNIQUE INDEX IF NOT EXISTS devices_user_client_device_uidx
    ON devices(user_id, client_device_id)
    WHERE client_device_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS devices_user_active_seen_idx
    ON devices (user_id, last_seen_at DESC, created_at DESC)
    WHERE revoked_at IS NULL;

-- [COMMENT]: Bỏ index device_challenges do bảng đã được xóa

CREATE INDEX IF NOT EXISTS mfa_settings_user_id_idx ON mfa_settings(user_id);
-- [COMMENT]: Bỏ index mfa_settings_status_idx do cột status đã được xóa
-- CREATE INDEX IF NOT EXISTS mfa_settings_status_idx ON mfa_settings(status);

CREATE INDEX IF NOT EXISTS mfa_challenges_user_id_idx ON mfa_challenges(user_id);
CREATE INDEX IF NOT EXISTS mfa_challenges_status_idx ON mfa_challenges(status);
CREATE INDEX IF NOT EXISTS mfa_challenges_expires_at_idx ON mfa_challenges(expires_at);

CREATE UNIQUE INDEX IF NOT EXISTS mfa_recovery_codes_user_hash_uidx ON mfa_recovery_codes(user_id, code_hash);
CREATE INDEX IF NOT EXISTS mfa_recovery_codes_user_id_idx ON mfa_recovery_codes(user_id);
CREATE INDEX IF NOT EXISTS mfa_recovery_codes_used_at_idx ON mfa_recovery_codes(used_at);

CREATE INDEX IF NOT EXISTS permissions_module_idx ON permissions(module);
CREATE INDEX IF NOT EXISTS permissions_object_idx ON permissions(object);
CREATE INDEX IF NOT EXISTS permissions_behavior_idx ON permissions(behavior);

CREATE UNIQUE INDEX IF NOT EXISTS roles_code_uidx ON roles(code);
CREATE UNIQUE INDEX IF NOT EXISTS roles_scope_name_uidx ON roles(scope, name);
CREATE INDEX IF NOT EXISTS roles_scope_idx ON roles(scope);

CREATE INDEX IF NOT EXISTS role_permissions_permission_id_idx ON role_permissions(permission_id);



-- [COMMENT]: Cho phép đăng ký lại thiết bị khi thiết bị cũ bị revoked bằng cách chỉ áp dụng unique cho active devices
CREATE UNIQUE INDEX IF NOT EXISTS admin_devices_fingerprint_uidx ON admin_devices(public_key_fingerprint) WHERE revoked_at IS NULL;
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
CREATE INDEX IF NOT EXISTS oauth_tokens_expires_at_idx ON oauth_tokens(expires_at);

-- Outbox indexes
CREATE INDEX IF NOT EXISTS idx_iam_outbox_pending
ON iam_outbox_records (available_at, id)
WHERE status IN ('PENDING', 'PUBLISHING');
CREATE INDEX IF NOT EXISTS idx_iam_outbox_terminal_cleanup
ON iam_outbox_records (completed_at, id)
WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;
