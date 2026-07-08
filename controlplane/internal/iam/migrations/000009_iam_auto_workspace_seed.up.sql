-- IAM migration layer 000009
-- Fix invalid regex character ranges and auto seed a default workspace for users when they change status to 'active'.

-- Sửa các ràng buộc CHECK regex lỗi định dạng khoảng ký tự '-' (invalid regular expression: invalid character range)
ALTER TABLE hierarchy.tenants DROP CONSTRAINT IF EXISTS ck_tenants_code_format;
ALTER TABLE hierarchy.tenants ADD CONSTRAINT ck_tenants_code_format CHECK (code ~ '^[a-z0-9_\-]+$');

ALTER TABLE hierarchy.personal_workspaces DROP CONSTRAINT IF EXISTS ck_personal_workspaces_code_format;
ALTER TABLE hierarchy.personal_workspaces ADD CONSTRAINT ck_personal_workspaces_code_format CHECK (code ~ '^[a-z0-9_\-]+$');

ALTER TABLE hierarchy.tenant_workspaces DROP CONSTRAINT IF EXISTS ck_tenant_workspaces_code_format;
ALTER TABLE hierarchy.tenant_workspaces ADD CONSTRAINT ck_tenant_workspaces_code_format CHECK (code ~ '^[a-z0-9_\-]+$');

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
        -- 1. Tìm zone để gán cho workspace. Ưu tiên zone active, fallback lấy zone bất kỳ
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
 
        -- 2. Kiểm tra xem user này đã có workspace nào do họ sở hữu chưa (tránh double seed)
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
 
            -- 3. Gán vai trò mặc định 'platform_user' cho user đối với workspace mới tạo
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

-- Tạo trigger trên bảng users
DROP TRIGGER IF EXISTS trg_auto_seed_workspace_on_user_active ON users;
CREATE TRIGGER trg_auto_seed_workspace_on_user_active
AFTER INSERT OR UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION auto_seed_workspace_on_user_active();

-- Thực thi chạy seed thủ công cho các user đã ở trạng thái active hiện có trong DB
DO $$
DECLARE
    u RECORD;
    v_zone_id UUID;
    v_workspace_id UUID;
    v_role_id UUID;
    v_role_name TEXT;
    v_role_level INT;
BEGIN
    SELECT id INTO v_zone_id FROM hierarchy.zones WHERE status = 'active' LIMIT 1;
    IF v_zone_id IS NULL THEN
        SELECT id INTO v_zone_id FROM hierarchy.zones LIMIT 1;
    END IF;
 
    IF v_zone_id IS NULL THEN
        v_zone_id := '019f3d3e-997d-7894-9236-c5122634cb4f'::UUID;
        INSERT INTO hierarchy.zones (id, code, name, location, status)
        VALUES (v_zone_id, 'edge-viet-nam-1', 'Edge việt nam 1', 'Hà Nội, Vietnam', 'active')
        ON CONFLICT (id) DO NOTHING;
    END IF;
 
    FOR u IN SELECT id, username FROM users WHERE status = 'active' LOOP
        IF NOT EXISTS (SELECT 1 FROM hierarchy.personal_workspaces WHERE owner_id = u.id) THEN
            v_workspace_id := gen_random_uuid();
            INSERT INTO hierarchy.personal_workspaces (id, name, code, zone_id, owner_id)
            VALUES (
                v_workspace_id,
                'Default Workspace',
                'default-' || lower(u.username),
                v_zone_id,
                u.id
            );
 
            SELECT id, name, role_level INTO v_role_id, v_role_name, v_role_level 
            FROM roles 
            WHERE code = 'platform_user';
 
            IF v_role_id IS NOT NULL THEN
                INSERT INTO user_role (
                    id, user_id, username, workspace_id, role_id, role_name, role_level, list_perm
                )
                VALUES (
                    gen_random_uuid(),
                    u.id,
                    u.username,
                    v_workspace_id,
                    v_role_id,
                    v_role_name,
                    v_role_level,
                    decode('', 'hex')
                )
                ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING;
            END IF;
        END IF;
    END LOOP;
END;
$$;
