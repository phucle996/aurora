-- Hierarchy migration layer 000005
-- Automatically assign tenant_member role to new active memberships if they don't have one,
-- enforcing the constraint that every active tenant membership must have at least one tenant-scoped role.

CREATE OR REPLACE FUNCTION auto_assign_tenant_role()
RETURNS TRIGGER AS $$
BEGIN
    -- [COMMENT]: Tự động gán tenant_member role cho user trong tenant mới nếu họ chưa được gán role nào
    IF NEW.status = 'active' AND NOT EXISTS (
        SELECT 1 
        FROM iam.user_role_assignments 
        WHERE user_id = NEW.user_id 
          AND tenant_id = NEW.tenant_id
          AND (expires_at IS NULL OR expires_at > NOW())
          AND revoked_at IS NULL
    ) THEN
        INSERT INTO iam.user_role_assignments (id, user_id, role_id, scope_type, tenant_id, assigned_at)
        SELECT gen_random_uuid(), NEW.user_id, id, 'tenant', NEW.tenant_id, NOW()
        FROM iam.roles
        WHERE code = 'tenant_member'
        LIMIT 1;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_auto_assign_tenant_role
AFTER INSERT OR UPDATE OF status, tenant_id, user_id ON tenant_memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION auto_assign_tenant_role();
