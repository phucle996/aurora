# Tenant Module V1 Runtime Map

## Request Flow: Login with `username@domain`
1. HTTP handler (IAM) nhận identifier.
2. IAM service parse username/domain.
3. IAM gọi Tenant Domain Resolver (sync interface).
4. IAM verify credential global user.
5. IAM gọi Tenant Membership service để lấy roles trong tenant.
6. IAM issue session/token có tenant context.
7. Response trả user + tenant context + tenant roles.

## Request Flow: Create Tenant
1. Tenant handler validate request.
2. Tenant service mở transaction.
3. Tenant repo insert tenant + primary domain.
4. Tenant service seed default roles (idempotent).
5. Tenant service bind creator membership + owner role.
6. Commit transaction.
7. Publish outbox event `tenant.created.v1`.

## Failure & Retry Flow
- Transaction failure trước commit: rollback full.
- Event publish failure sau commit: outbox retry worker.
- Domain collision: deterministic conflict response.
