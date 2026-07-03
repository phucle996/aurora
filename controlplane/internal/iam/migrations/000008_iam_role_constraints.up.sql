-- IAM migration layer 000008
-- Automatically assign platform_user role to new users if they don't have one,
-- enforcing the constraint that every active platform user must have at least one role.

-- [COMMENT]: Triggers for auto assignment of platform role are deprecated.
-- The metadata for roles and permissions is now managed in Go memory, so auto-assignment is handled in the Go application layer.
DROP TRIGGER IF EXISTS trg_auto_assign_platform_role ON users;
DROP FUNCTION IF EXISTS auto_assign_platform_role();
