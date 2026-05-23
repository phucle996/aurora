# Tenant Event/Job Contract

## Contract Item: EV-001 Tenant Created Event
- Owner: Tenant Module
- Event Name: `tenant.created.v1`
- Producer: Tenant service
- Consumer: Audit/analytics/notifications (future)
- Payload:
  - `tenant_id`, `tenant_code`, `creator_user_id`, `created_at`
- Ordering:
  - Best-effort within same transaction outbox order.
- Idempotency:
  - Event key: `tenant_id:event_type`.
- Failure Semantics:
  - Publish failure triggers retry via outbox worker; no duplicate tenant writes.
- Verification Evidence:
  - Outbox integration test.

## Contract Item: EV-002 Membership Granted Event
- Owner: Tenant Module
- Event Name: `tenant.membership.granted.v1`
- Payload:
  - `tenant_id`, `membership_id`, `user_id`, `role_codes`, `granted_by`, `created_at`
- Idempotency:
  - Event key: `membership_id:grant_version`.
- Failure Semantics:
  - Retryable publish with backoff; DLQ after max retry.
- Verification Evidence:
  - Worker retry tests.
