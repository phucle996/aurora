# Tenant API Contract

## Contract Item: API-001 Create Tenant
- Owner: Tenant Module
- Endpoint: `POST /api/v1/tenants`
- Auth: Global authenticated user (future policy: platform admin or self-serve policy flag).
- Request:
  - `name` (required)
  - `code` (required)
  - `primary_domain` (required)
- Response:
  - `201` with tenant summary + creator membership role.
- Rules:
  - Transaction includes tenant + primary domain + default roles + creator membership admin link.
- Failure Semantics:
  - `409` on duplicate code/domain.
  - `400` on invalid payload.
- Verification Evidence:
  - Handler transport tests + integration transaction rollback test.

## Contract Item: API-002 Resolve Tenant by Domain
- Owner: Tenant Module
- Endpoint: internal service contract (sync call from IAM): `ResolveDomain(domain)`.
- Rules:
  - Returns exactly one active tenant or typed not-found.
- Failure Semantics:
  - Not found -> typed domain not found error.
- Verification Evidence:
  - Service unit test for hit/miss/suspended tenant.

## Contract Item: API-003 Tenant Login Context
- Owner: IAM module + Tenant integration contract
- External Behavior:
  - Login identifier may be `username` or `username@domain`.
- Rules:
  - `username@domain` path resolves domain via Tenant contract before credential completion.
  - Generic auth error on user/domain mismatch.
- Failure Semantics:
  - Never leak whether username or domain failed.
- Verification Evidence:
  - End-to-end workflow testcases.

## Contract Item: API-004 Membership Management
- Owner: Tenant Module
- Endpoints:
  - `GET /api/v1/tenants/:tenant_id/members`
  - `POST /api/v1/tenants/:tenant_id/members`
  - `POST /api/v1/tenants/:tenant_id/members/:membership_id/roles`
- Auth:
  - Tenant admin within same tenant.
- Failure Semantics:
  - Cross-tenant action -> `403`.
- Verification Evidence:
  - Permission tests and negative scope tests.
