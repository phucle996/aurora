-- IAM migration layer 000004 down
-- Drop all functions and stored procedures.

DROP FUNCTION IF EXISTS auto_seed_workspace_on_user_active();
DROP PROCEDURE IF EXISTS cleanup_expired_admin_keys();
DROP PROCEDURE IF EXISTS cleanup_expired_refresh_tokens();
DROP FUNCTION IF EXISTS set_updated_at();
