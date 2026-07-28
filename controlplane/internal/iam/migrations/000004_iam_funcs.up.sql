-- IAM migration layer 000004
-- SQL Functions and Stored Procedures

-- 1) Function set_updated_at
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

-- 2) Stored procedure cleanup_expired_refresh_tokens
CREATE OR REPLACE PROCEDURE cleanup_expired_refresh_tokens()
LANGUAGE plpgsql
AS $$
BEGIN
    -- Xóa các user refresh tokens đã quá hạn hơn 7 ngày
    DELETE FROM refresh_tokens 
    WHERE expires_at < NOW() - INTERVAL '7 days';
END;
$$;

-- 3) Stored procedure cleanup_expired_admin_keys
CREATE OR REPLACE PROCEDURE cleanup_expired_admin_keys()
LANGUAGE plpgsql
AS $$
BEGIN
    -- Xóa các recovery codes khẩn cấp đã được sử dụng quá 30 ngày
    DELETE FROM admin_recovery_codes 
    WHERE used_at IS NOT NULL AND used_at < NOW() - INTERVAL '30 days';

    -- Gọi thủ tục dọn dẹp user refresh tokens (quá hạn > 7 ngày)
    CALL cleanup_expired_refresh_tokens();
END;
$$;
