# Tenant Permission Contract

## Contract Item: PERM-001 Role Scope
- Owner: Tenant Module
- Rules:
  - Roles are tenant-scoped; no global role can grant tenant-local admin rights implicitly.
- Invariants:
  - Deny-by-default.
- Verification Evidence:
  - Permission matrix tests.

## Contract Item: PERM-002 Default Roles on Tenant Creation
- Owner: Tenant Module
- Default Roles:
  - `tenant_owner`
  - `tenant_admin`
  - `tenant_member`
- Rules:
  - Seeded automatically and idempotently on tenant create.
  - Creator gets `tenant_owner` membership binding by default.
- Failure Semantics:
  - Any seed/link failure rolls back whole tenant create transaction.
- Verification Evidence:
  - Transaction integration tests.

## Contract Item: PERM-003 Membership Operations
- Owner: Tenant Module
- Rules:
  - Only tenant admin/owner can add members or grant roles within same tenant.
  - Cross-tenant access is always denied.
- Verification Evidence:
  - Handler authorization tests.
