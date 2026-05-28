-- IAM migration layer 000007 down
-- Xóa stored procedure cleanup_expired_admin_keys.

DROP PROCEDURE IF EXISTS cleanup_expired_admin_keys();
