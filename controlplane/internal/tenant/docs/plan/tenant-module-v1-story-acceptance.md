# Tenant Module V1 User Stories & Acceptance

## User Stories
- US-001: As a global user, I want to sign in with `username@domain`, so that I enter the correct tenant context.
- US-002: As a tenant creator, I want default tenant roles created automatically, so that the tenant is immediately operable.
- US-003: As a tenant admin, I want membership and roles scoped to my tenant, so that no cross-tenant privilege leak occurs.
- US-004: As a security operator, I want generic auth errors on tenant login mismatch, so that user/domain enumeration is prevented.

## Acceptance Criteria
### AC-001 (Happy Path)
- Given a valid tenant domain and valid credential
- When user signs in with `username@domain`
- Then response contains tenant context and effective tenant roles.

### AC-002 (Permission Scope)
- Given user is admin of tenant A only
- When user manages member in tenant B
- Then API returns forbidden and no data mutation occurs.

### AC-003 (Negative/Error)
- Given invalid domain or non-member user
- When login with `username@domain`
- Then response is generic auth failure with same envelope.

### AC-004 (State Transition)
- Given new tenant create request valid
- When creation succeeds
- Then default roles exist and creator membership is linked with owner role.

### AC-005 (Idempotency)
- Given retried create-tenant request with same idempotent key
- When request is replayed
- Then no duplicate tenant roles/memberships are created.
