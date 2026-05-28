-- IAM migration layer 000007
-- Stored procedure dọn dẹp khóa cũ đã hết hạn và recovery codes đã sử dụng quá 30 ngày.

CREATE OR REPLACE PROCEDURE cleanup_expired_admin_keys()
LANGUAGE plpgsql
AS $$
BEGIN
    -- 1. Xóa các admin api keys đã hết hạn quá 30 ngày
    DELETE FROM admin_api_keys 
    WHERE expires_at < NOW() - INTERVAL '30 days';

    -- 2. Xóa các recovery codes khẩn cấp đã được sử dụng quá 30 ngày
    DELETE FROM admin_recovery_codes 
    WHERE used_at IS NOT NULL AND used_at < NOW() - INTERVAL '30 days';
END;
$$;
