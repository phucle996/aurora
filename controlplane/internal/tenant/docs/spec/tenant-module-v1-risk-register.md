# Tenant Module V1 Risk Register

## Risks
1. Role seed drift giữa migration seed và service seed.
2. Login parser ambiguity cho username chứa ký tự `@`.
3. Cross-tenant data leakage do thiếu tenant filter ở repo queries.
4. Domain takeover nếu thiếu verification policy (future phase).

## Mitigations
1. Define canonical default role codes trong permission contract và kiểm thử idempotency.
2. Standardize parser: split by last `@`, validate domain format strict.
3. Mandatory tenant_id predicates + tests negative scope.
4. Add `verified_status` field và policy gate cho production domain login.

## Assumptions
- IAM users table đã là global identity SoT.
- Migration framework hiện tại support transactional up/down.
