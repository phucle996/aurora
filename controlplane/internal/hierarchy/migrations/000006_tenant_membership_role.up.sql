-- Remove the legacy string role projection. IAM owns normalized tenant role
-- definitions and the compiled membership_role binding created atomically by
-- CreateTenant/JoinTenantInvitation.
DROP TRIGGER IF EXISTS trg_auto_assign_tenant_role ON tenant_memberships;
DROP FUNCTION IF EXISTS auto_assign_tenant_role();
ALTER TABLE tenant_memberships DROP CONSTRAINT IF EXISTS ck_tenant_membership_role;
ALTER TABLE tenant_memberships DROP COLUMN IF EXISTS role;
