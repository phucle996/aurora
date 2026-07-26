ALTER TABLE tenant_memberships
    ADD COLUMN IF NOT EXISTS role VARCHAR(64) NOT NULL DEFAULT 'tenant_member';

UPDATE tenant_memberships
SET role='tenant_owner'
WHERE is_ownership=true;

-- [COMMENT]: Thêm ràng buộc check role format theo cơ chế idempotent tránh lỗi duplicate_object (SQLSTATE 42710)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'ck_tenant_membership_role'
    ) THEN
        ALTER TABLE tenant_memberships
            ADD CONSTRAINT ck_tenant_membership_role
            CHECK (role ~ '^tenant_[a-z0-9_]+$');
    END IF;
END $$;

-- [COMMENT]: Membership role is the canonical tenant authorization binding.
-- Keep the legacy assignment projection synchronized for existing consumers,
-- but never choose a generic tenant_member role for an owner.
CREATE OR REPLACE FUNCTION auto_assign_tenant_role()
RETURNS TRIGGER AS $$
DECLARE
    desired_role_id UUID;
BEGIN
    IF NEW.status = 'active' THEN
        SELECT id INTO desired_role_id
        FROM iam.roles
        WHERE code=NEW.role AND scope='tenant'
        LIMIT 1;

        IF desired_role_id IS NULL THEN
            RAISE EXCEPTION 'tenant membership role % is not provisioned', NEW.role;
        END IF;

        UPDATE iam.user_role_assignments
        SET role_id=desired_role_id
        WHERE user_id=NEW.user_id
          AND tenant_id=NEW.tenant_id
          AND revoked_at IS NULL
          AND (expires_at IS NULL OR expires_at > NOW());

        IF NOT FOUND THEN
            INSERT INTO iam.user_role_assignments
                (id, user_id, role_id, scope_type, tenant_id, assigned_at)
            VALUES
                (gen_random_uuid(), NEW.user_id, desired_role_id, 'tenant', NEW.tenant_id, NOW());
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_auto_assign_tenant_role ON tenant_memberships;

CREATE CONSTRAINT TRIGGER trg_auto_assign_tenant_role
AFTER INSERT OR UPDATE OF status, tenant_id, user_id, role ON tenant_memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION auto_assign_tenant_role();
