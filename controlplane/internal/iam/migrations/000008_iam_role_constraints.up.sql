-- IAM migration layer 000008
-- Automatically assign platform_user role to new users if they don't have one,
-- enforcing the constraint that every active platform user must have at least one role.

CREATE OR REPLACE FUNCTION auto_assign_platform_role()
RETURNS TRIGGER AS $$
BEGIN
    -- [COMMENT]: Tự động gán platform_user role cho user mới hoạt động nếu họ chưa có platform role nào
    IF NEW.status IN ('active', 'pending-active') AND NOT EXISTS (
        SELECT 1 
        FROM user_role_assignments 
        WHERE user_id = NEW.id AND scope_type = 'platform'
    ) THEN
        INSERT INTO user_role_assignments (id, user_id, role_id, scope_type, assigned_at)
        SELECT gen_random_uuid(), NEW.id, id, 'platform', NOW()
        FROM roles
        WHERE code = 'platform_user'
        LIMIT 1;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_auto_assign_platform_role
AFTER INSERT OR UPDATE OF status ON users
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION auto_assign_platform_role();
