-- IAM migration layer 000003
-- Secondary indexes and uniqueness constraints for baseline schema.

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

-- External Identities Indexes
CREATE UNIQUE INDEX IF NOT EXISTS external_identities_active_user_provider_uk
    ON external_identities (user_id, provider)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS external_identities_user_idx
    ON external_identities (user_id)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS external_identities_email_idx
    ON external_identities (provider_email);

-- MFA Recovery Codes Indexes
CREATE INDEX IF NOT EXISTS mfa_recovery_codes_setting_idx
    ON mfa_recovery_codes(mfa_setting_id, created_at);

CREATE UNIQUE INDEX IF NOT EXISTS mfa_recovery_codes_setting_hash_uidx
    ON mfa_recovery_codes(mfa_setting_id, code_hash);

-- Billing Outbox Indexes
CREATE INDEX IF NOT EXISTS idx_billing_outbox_claim
    ON billing_outbox_records (available_at, id)
    WHERE status IN ('PENDING', 'PUBLISHING');

CREATE INDEX IF NOT EXISTS idx_billing_outbox_owner_audit
    ON billing_outbox_records (owner_type, owner_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_billing_outbox_published_cleanup
    ON billing_outbox_records (published_at, id)
    WHERE status = 'PUBLISHED' AND published_at IS NOT NULL;

-- Permissions & Roles Indexes
CREATE INDEX IF NOT EXISTS permissions_module_idx ON permissions(module);
CREATE INDEX IF NOT EXISTS permissions_object_idx ON permissions(object);
CREATE INDEX IF NOT EXISTS permissions_behavior_idx ON permissions(behavior);

CREATE UNIQUE INDEX IF NOT EXISTS platform_roles_code_uidx ON platform_roles(code);
CREATE UNIQUE INDEX IF NOT EXISTS platform_roles_name_uidx ON platform_roles(name);

CREATE INDEX IF NOT EXISTS platform_role_permissions_permission_id_idx ON platform_role_permissions(permission_id);
CREATE INDEX IF NOT EXISTS tenant_role_permissions_permission_id_idx ON tenant_role_permissions(permission_id);

-- Admin Devices Indexes
CREATE UNIQUE INDEX IF NOT EXISTS admin_devices_fingerprint_uidx ON admin_devices(public_key_fingerprint) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS admin_devices_last_seen_at_idx ON admin_devices(last_seen_at);
