-- IAM migration layer 000005 down
-- Drop all triggers.

DROP TRIGGER IF EXISTS trg_roles_updated_at ON roles;
DROP TRIGGER IF EXISTS trg_permissions_updated_at ON permissions;
DROP TRIGGER IF EXISTS trg_mfa_settings_updated_at ON mfa_settings;
DROP TRIGGER IF EXISTS trg_admin_devices_updated_at ON admin_devices;
DROP TRIGGER IF EXISTS trg_devices_updated_at ON devices;
DROP TRIGGER IF EXISTS trg_user_profiles_updated_at ON user_profiles;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
