# Tenant Module V1 Behavior Spec

## Scope
Deliver tenant module with domain-based tenant context, tenant-scoped membership and roles, and transactional tenant bootstrap.

## Actors / Roles
- Global User
- Tenant Owner
- Tenant Admin
- Tenant Member
- System

## Permission Boundary
- Tenant operations require tenant-scoped authorization.
- No cross-tenant mutation allowed.

## Main Flow
- Create tenant and bootstrap default role/membership records in one transaction.
- Login with `username@domain` routes auth context into resolved tenant.

## Exception Flow
- Domain/user/membership mismatch -> generic auth failure.
- Duplicate tenant/domain -> deterministic conflict.
- Seed/link failure -> rollback create tenant.

## Edge Cases
- Multi-tenant user disambiguation by domain.
- Parser behavior for complex identifiers.

## Business Rule References
- `BR-001` Tenant membership is SoT for tenant access.
- `BR-002` Domain unique globally.
- `BR-003` Username-domain login must resolve tenant before auth complete.
- `BR-004` Create tenant auto-seeds default roles and links creator owner membership.
- `BR-005` Deny-by-default cross-tenant action.

## State Transitions
- Tenant: `active -> suspended -> deleted`.
- Membership: `invited -> active -> revoked`.

## Error/Response Semantics
- Standard error envelope with stable tenant error codes.
- Auth mismatch responses are intentionally indistinguishable.

## Non-Goals
- Billing and invoicing.
- External IdP federation.
