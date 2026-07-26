CREATE OR REPLACE FUNCTION auto_assign_tenant_role()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'active' AND NOT EXISTS (
        SELECT 1
        FROM iam.user_role_assignments
        WHERE user_id=NEW.user_id
          AND tenant_id=NEW.tenant_id
          AND (expires_at IS NULL OR expires_at > NOW())
          AND revoked_at IS NULL
    ) THEN
        INSERT INTO iam.user_role_assignments
            (id, user_id, role_id, scope_type, tenant_id, assigned_at)
        SELECT gen_random_uuid(), NEW.user_id, id, 'tenant', NEW.tenant_id, NOW()
        FROM iam.roles
        WHERE code='tenant_member'
        LIMIT 1;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_auto_assign_tenant_role ON tenant_memberships;

CREATE CONSTRAINT TRIGGER trg_auto_assign_tenant_role
AFTER INSERT OR UPDATE OF status, tenant_id, user_id ON tenant_memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION auto_assign_tenant_role();

ALTER TABLE tenant_memberships
    DROP CONSTRAINT IF EXISTS ck_tenant_membership_role,
    DROP COLUMN IF EXISTS role;
