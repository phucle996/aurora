# Tenant Module V1 Flow (Main, Exception, Edge)

## Main Flow A: Create Tenant
1. Actor submits create tenant request.
2. Service validates payload and policy.
3. Begin transaction.
4. Insert tenant + primary domain.
5. Seed default tenant roles.
6. Create creator membership + owner role binding.
7. Commit and emit `tenant.created.v1`.

## Main Flow B: Login with `username@domain`
1. IAM parses identifier.
2. Tenant resolves domain -> tenant.
3. IAM verifies credential.
4. Tenant validates active membership.
5. IAM returns tenant-context auth response.

## Exception Flows
- EX-001 Duplicate domain: return conflict, rollback.
- EX-002 Membership missing: generic auth failure.
- EX-003 Tenant suspended: deny auth with policy code.
- EX-004 Role seed fail: rollback full create tenant.

## Edge Cases
- EG-001 One user in multiple tenants: domain is mandatory selector.
- EG-002 Username contains `@`: parse by last `@` and validate domain segment.
- EG-003 Primary domain rotation: old domain grace policy (future controlled rollout).
