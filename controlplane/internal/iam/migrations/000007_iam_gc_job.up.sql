-- IAM migration layer 000007
-- Stored procedure dọn dẹp khóa cũ đã hết hạn và recovery codes đã sử dụng quá 30 ngày.
-- Đồng thời thực hiện dọn dẹp các user refresh tokens và oauth tokens đã quá hạn hơn 7 ngày.

CREATE OR REPLACE PROCEDURE cleanup_expired_refresh_tokens()
LANGUAGE plpgsql
AS $$
BEGIN
    -- 1. Thực hiện xóa các user refresh tokens đã quá hạn hơn 7 ngày
    -- Phân loại: Xóa triệt để các token cũ của user để tránh phình dung lượng bảng refresh_tokens
    DELETE FROM refresh_tokens 
    WHERE expires_at < NOW() - INTERVAL '7 days';

    -- 2. Thực hiện xóa các oauth tokens đã quá hạn hơn 7 ngày
    -- Mục tiêu: Dọn dẹp các session OAuth2 đã hết hạn quá thời gian ân hạn
    DELETE FROM oauth_tokens 
    WHERE expires_at < NOW() - INTERVAL '7 days';
END;
$$;

CREATE OR REPLACE PROCEDURE cleanup_expired_admin_keys()
LANGUAGE plpgsql
AS $$
BEGIN
    -- 2. Xóa các recovery codes khẩn cấp đã được sử dụng quá 30 ngày
    DELETE FROM admin_recovery_codes 
    WHERE used_at IS NOT NULL AND used_at < NOW() - INTERVAL '30 days';

    -- 3. Gọi thủ tục dọn dẹp user/oauth refresh tokens (quá hạn > 7 ngày)
    CALL cleanup_expired_refresh_tokens();
END;
$$;
