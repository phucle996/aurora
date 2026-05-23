# Tenant Module V1 Ops Runbook

## Operational Checks
- Check migration status before enabling tenant endpoints.
- Verify default role seed integrity per tenant.
- Verify domain uniqueness and primary-domain invariant.

## Incident Quick Triage
1. Tenant create failed
   - Check transaction rollback logs and DB constraint violations.
2. `username@domain` login failed unexpectedly
   - Validate domain mapping, membership active state, tenant status.
3. Permission denied anomalies
   - Validate tenant scope in membership role bindings.

## Observability Queries
- Error rate by code: `TENANT_*`
- Domain resolve latency p95
- Cross-tenant deny count
