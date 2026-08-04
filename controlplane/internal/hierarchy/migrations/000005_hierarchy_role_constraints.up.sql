-- Membership creation is an explicit workflow. A database trigger cannot infer
-- tenant role, hierarchy or compiled five-level permissions safely.
DROP TRIGGER IF EXISTS trg_auto_assign_tenant_role ON tenant_memberships;
DROP FUNCTION IF EXISTS auto_assign_tenant_role();
