-- IAM migration layer 000004 down
-- Drop all functions and stored procedures.

DROP PROCEDURE IF EXISTS cleanup_expired_admin_keys();
DROP PROCEDURE IF EXISTS cleanup_expired_refresh_tokens();
DROP FUNCTION IF EXISTS reject_tenant_role_revision_mutation();
DROP FUNCTION IF EXISTS set_updated_at();
