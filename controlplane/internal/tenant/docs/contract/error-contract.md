# Tenant Error Contract

## Redaction Rule
- Public errors must be generic for auth-related failures.
- Internal diagnostic details remain in server logs only.

## Contract Item: ERR-001 Taxonomy
- Owner: Tenant Module
- Public Codes:
  - `TENANT_INVALID_ARGUMENT`
  - `TENANT_NOT_FOUND`
  - `TENANT_CONFLICT`
  - `TENANT_DOMAIN_CONFLICT`
  - `TENANT_MEMBERSHIP_CONFLICT`
  - `TENANT_FORBIDDEN`
  - `TENANT_SUSPENDED`
  - `TENANT_INTERNAL`
- Invariants:
  - Stable code semantics across versions.
- Verification Evidence:
  - Handler error mapping tests.

## Contract Item: ERR-002 Auth Enumeration Safety
- Owner: IAM + Tenant integration
- Rules:
  - `username@domain` failures return same generic auth message for user-not-found/domain-not-found/membership-not-found.
- Failure Semantics:
  - Forbidden to expose discriminator in response body.
- Verification Evidence:
  - Transport tests comparing response envelope equality.
