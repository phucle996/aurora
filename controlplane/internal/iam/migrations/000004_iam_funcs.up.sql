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

    -- Xóa các oauth tokens đã quá hạn hơn 7 ngày
    DELETE FROM oauth_tokens 
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

    -- Gọi thủ tục dọn dẹp user/oauth refresh tokens (quá hạn > 7 ngày)
    CALL cleanup_expired_refresh_tokens();
END;
$$;

-- 4) Trigger Function for auto seeding workspace on user activation
CREATE OR REPLACE FUNCTION auto_seed_workspace_on_user_active()
RETURNS TRIGGER AS $$
DECLARE
    v_zone_id UUID;
    v_workspace_id UUID;
    v_role_id UUID;
    v_role_name TEXT;
    v_role_level INT;
BEGIN
    -- Chỉ thực hiện khi trạng thái chuyển sang 'active' (cả khi INSERT và UPDATE)
    IF NEW.status = 'active' AND (TG_OP = 'INSERT' OR OLD.status <> 'active') THEN
        -- Tìm zone để gán cho workspace. Ưu tiên zone active, fallback lấy zone bất kỳ
        SELECT id INTO v_zone_id FROM hierarchy.zones WHERE status = 'active' LIMIT 1;
        IF v_zone_id IS NULL THEN
            SELECT id INTO v_zone_id FROM hierarchy.zones LIMIT 1;
        END IF;
 
        -- Nếu chưa có zone nào trong hệ thống, tự động seed 1 zone mặc định
        IF v_zone_id IS NULL THEN
            v_zone_id := '019f3d3e-997d-7894-9236-c5122634cb4f'::UUID;
            INSERT INTO hierarchy.zones (id, code, name, location, status)
            VALUES (v_zone_id, 'edge-viet-nam-1', 'Edge việt nam 1', 'Hà Nội, Vietnam', 'active')
            ON CONFLICT (id) DO NOTHING;
        END IF;
 
        -- Kiểm tra xem user này đã có workspace nào do họ sở hữu chưa (tránh double seed)
        IF NOT EXISTS (SELECT 1 FROM hierarchy.personal_workspaces WHERE owner_id = NEW.id) THEN
            v_workspace_id := gen_random_uuid();
            INSERT INTO hierarchy.personal_workspaces (id, name, code, zone_id, owner_id)
            VALUES (
                v_workspace_id,
                'Default Workspace',
                'default-' || lower(NEW.username),
                v_zone_id,
                NEW.id
            );
 
            -- Gán vai trò mặc định 'platform_user' cho user đối với workspace mới tạo
            SELECT id, name, role_level INTO v_role_id, v_role_name, v_role_level 
            FROM roles 
            WHERE code = 'platform_user';
 
            IF v_role_id IS NOT NULL THEN
                INSERT INTO user_role (
                     id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
                )
                VALUES (
                    gen_random_uuid(),
                    NEW.id,
                    NEW.username,
                    v_workspace_id,
                    v_role_id,
                    v_role_name,
                    v_role_level,
                    decode('', 'hex')
                )
                ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING;
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
