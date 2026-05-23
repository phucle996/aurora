# Tenant DB Contract

## Source of Truth
- Identity SoT: `iam.users` (owned by IAM).
- Tenant SoT: `tenant.tenants`.
- Tenant Domain SoT: `tenant.tenant_domains`.
- Membership SoT: `tenant.tenant_memberships`.
- Tenant Role SoT: `tenant.tenant_roles` and `tenant.tenant_membership_roles`.

## Contract Item: DB-001 Tenants
- Owner: Tenant Module
- Rules:
  - Table `tenants(id, code, name, status, created_at, updated_at, deleted_at)`.
  - `code` unique, immutable after creation.
- Invariants:
  - `status` in (`active`, `suspended`, `deleted`).
- Failure Semantics:
  - Duplicate code -> `TENANT_CONFLICT`.
- Verification Evidence:
  - Migration integration test for unique constraint and status check.

## Contract Item: DB-002 Tenant Domains
- Owner: Tenant Module
- Rules:
  - Table `tenant_domains(id, tenant_id, domain, is_primary, verified_status, created_at, updated_at, deleted_at)`.
  - `domain` unique globally.
  - Each tenant has exactly one active `is_primary=true` domain.
- Invariants:
  - Domain resolves to exactly one tenant.
- Failure Semantics:
  - Duplicate domain -> `TENANT_DOMAIN_CONFLICT`.
- Verification Evidence:
  - Migration integration test for unique index and primary-domain guard.

## Contract Item: DB-003 Membership
- Owner: Tenant Module
- Rules:
  - Table `tenant_memberships(id, tenant_id, user_id, status, joined_at, created_at, updated_at, deleted_at)`.
  - Unique `(tenant_id, user_id)` active membership.
- Invariants:
  - A membership belongs to exactly one tenant and one global user.
- Failure Semantics:
  - Duplicate membership -> `TENANT_MEMBERSHIP_CONFLICT`.
- Verification Evidence:
  - Integration test for uniqueness and soft-delete semantics.

## Contract Item: DB-004 Tenant Roles
- Owner: Tenant Module
- Rules:
  - Table `tenant_roles(id, tenant_id, code, name, is_system_default, created_at, updated_at, deleted_at)`.
  - Unique `(tenant_id, code)`.
  - Default role set must be seeded when tenant is created.
- Invariants:
  - Role scope is tenant-local only.
- Failure Semantics:
  - Duplicate role code in same tenant -> `TENANT_ROLE_CONFLICT`.
- Verification Evidence:
  - Service + repo tests for idempotent seed behavior.

## Contract Item: DB-005 Membership Role Binding
- Owner: Tenant Module
- Rules:
  - Table `tenant_membership_roles(id, membership_id, role_id, created_at)`.
  - Unique `(membership_id, role_id)`.
- Invariants:
  - `membership_id` and `role_id` must belong to same tenant.
- Failure Semantics:
  - Cross-tenant binding attempt -> `TENANT_ROLE_SCOPE_VIOLATION`.
- Verification Evidence:
  - Service validation test + FK/constraint test.
